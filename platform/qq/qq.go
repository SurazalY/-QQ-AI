package qq

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chenhg5/cc-connect/core"
	"github.com/gorilla/websocket"
)

func init() {
	core.RegisterPlatform("qq", New)
}

// Platform connects to a OneBot v11 implementation (NapCat, LLOneBot, etc.)
// via forward WebSocket. It receives message events and sends messages back
// through the same WS connection.
type Platform struct {
	wsURL                 string // e.g. "ws://127.0.0.1:3001"
	token                 string // optional access_token
	httpToken             string
	allowFrom             string // comma-separated user IDs or "*"
	allowGroups           string
	errorUserID           int64
	allowPrivate          string
	splitLines            bool
	requireMention        bool // group chats: only react when the bot is @-mentioned (default true)
	shareSessionInChannel bool
	handler               core.MessageHandler
	conn                  *websocket.Conn
	mu                    sync.Mutex
	echoSeq               atomic.Int64
	echoCh                sync.Map // echo -> chan json.RawMessage
	cancel                context.CancelFunc
	stopped               bool
	selfID                atomic.Int64
	dedup                 core.MessageDedup
	groupNameCache        sync.Map // groupID -> group name
	httpURL               string   // OneBot HTTP API URL, e.g. "http://127.0.0.1:3000"
	reconnectDelay        func(attempt int) time.Duration

	healthMu       sync.RWMutex
	connected      bool
	degraded       bool
	degradedReason string
	degradedSince  time.Time
	errorSink      core.MonitorErrorSink
}

const (
	qqMaxFastReconnectAttempts  = 30
	qqDefaultSlowReconnectDelay = 60 * time.Second
)

var urlQuerySecretRe = regexp.MustCompile(`(?i)([?&](?:access_token|token|secret|api_key|apikey|auth)=)([^&\s"'<>` + "`" + `]+)`)

func extractURLQuerySecrets(rawURL string) []string {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	sensitiveKeys := map[string]bool{
		"access_token": true,
		"token":        true,
		"secret":       true,
		"api_key":      true,
		"apikey":       true,
		"auth":         true,
	}
	var secrets []string
	for k, vals := range u.Query() {
		if !sensitiveKeys[strings.ToLower(k)] {
			continue
		}
		for _, v := range vals {
			if v != "" {
				secrets = append(secrets, v)
			}
		}
	}
	for _, part := range strings.Split(u.RawQuery, "&") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || v == "" {
			continue
		}
		if sensitiveKeys[strings.ToLower(k)] {
			secrets = append(secrets, v)
		}
	}
	return secrets
}

func (p *Platform) redactSecrets(text string) string {
	if text == "" {
		return text
	}
	var secrets []string
	for _, s := range []string{p.token, p.httpToken} {
		if s != "" {
			secrets = append(secrets, s)
		}
	}
	secrets = append(secrets, extractURLQuerySecrets(p.wsURL)...)
	secrets = append(secrets, extractURLQuerySecrets(p.httpURL)...)

	// Redact longest secrets first so shorter substrings do not leave partial fragments.
	sort.Slice(secrets, func(i, j int) bool {
		return len(secrets[i]) > len(secrets[j])
	})
	for _, s := range secrets {
		text = core.RedactToken(text, s)
	}
	return urlQuerySecretRe.ReplaceAllString(text, "${1}[REDACTED]")
}

func New(opts map[string]any) (core.Platform, error) {
	wsURL, _ := opts["ws_url"].(string)
	if wsURL == "" {
		wsURL = "ws://127.0.0.1:3001"
	}
	token, _ := opts["token"].(string)
	httpToken, _ := opts["http_token"].(string)
	if httpToken == "" {
		httpToken = token
	}
	allowFrom, _ := opts["allow_from"].(string)
	allowGroups, _ := opts["allow_groups"].(string)
	allowPrivate, _ := opts["allow_private"].(string)
	errorUser, _ := opts["error_user_id"].(string)
	var errorUserID int64
	if errorUser != "" {
		var err error
		errorUserID, err = strconv.ParseInt(errorUser, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("qq: error_user_id: %w", err)
		}
	}
	splitLines, _ := opts["split_lines"].(bool)
	shareSessionInChannel, _ := opts["share_session_in_channel"].(bool)

	// require_mention: in group chats only react when the bot is @-mentioned.
	// Defaults to true (consistent with the feishu platform); set false to
	// process every group message like earlier versions did.
	requireMention := true
	if v, ok := opts["require_mention"].(bool); ok {
		requireMention = v
	}

	core.CheckAllowFrom("qq", allowFrom)

	httpURL, _ := opts["http_url"].(string)
	httpURL = strings.TrimRight(httpURL, "/")

	return &Platform{
		wsURL:                 wsURL,
		token:                 token,
		httpToken:             httpToken,
		allowFrom:             allowFrom,
		allowGroups:           allowGroups,
		allowPrivate:          allowPrivate,
		errorUserID:           errorUserID,
		splitLines:            splitLines,
		requireMention:        requireMention,
		shareSessionInChannel: shareSessionInChannel,
		httpURL:               httpURL,
	}, nil
}

func (p *Platform) Name() string { return "qq" }

// SetMonitorErrorSink implements core.MonitorErrorReporter.
func (p *Platform) SetMonitorErrorSink(sink core.MonitorErrorSink) {
	p.healthMu.Lock()
	p.errorSink = sink
	p.healthMu.Unlock()
}

var _ core.MonitorErrorReporter = (*Platform)(nil)

// PlatformHealth implements core.PlatformHealth.
func (p *Platform) PlatformHealth() core.PlatformHealthInfo {
	p.healthMu.RLock()
	defer p.healthMu.RUnlock()
	return core.PlatformHealthInfo{
		Name:           p.Name(),
		Connected:      p.connected,
		Degraded:       p.degraded,
		DegradedReason: p.degradedReason,
		DegradedSince:  p.degradedSince,
	}
}

var _ core.PlatformHealth = (*Platform)(nil)

func (p *Platform) setConnected() {
	p.healthMu.Lock()
	p.connected = true
	p.degraded = false
	p.degradedReason = ""
	p.degradedSince = time.Time{}
	p.healthMu.Unlock()
}

func (p *Platform) setDegraded(reason string) {
	p.healthMu.Lock()
	p.connected = false
	p.degraded = true
	p.degradedReason = reason
	if p.degradedSince.IsZero() {
		p.degradedSince = time.Now()
	}
	p.healthMu.Unlock()
}

func (p *Platform) setDisconnected() {
	p.healthMu.Lock()
	p.connected = false
	p.healthMu.Unlock()
}

func (p *Platform) recordMonitorError(level, code, summary, detail string) {
	p.healthMu.RLock()
	sink := p.errorSink
	p.healthMu.RUnlock()
	if sink == nil {
		return
	}
	sink.RecordMonitorError(core.MonitorErrorEntry{
		Source:  "platform",
		Level:   level,
		Code:    code,
		Summary: summary,
		Detail:  detail,
	})
}

func (p *Platform) Start(handler core.MessageHandler) error {
	p.handler = handler

	header := http.Header{}
	if p.token != "" {
		header.Set("Authorization", "Bearer "+p.token)
	}

	conn, _, err := websocket.DefaultDialer.Dial(p.wsURL, header)
	if err != nil {
		safeErr := p.redactSecrets(err.Error())
		safeURL := p.redactSecrets(p.wsURL)
		p.setDegraded("ws connect failed: " + safeErr)
		p.recordMonitorError("error", "WS_CONNECT_FAILED", "qq websocket connect failed", safeErr)
		if safeErr != err.Error() {
			return fmt.Errorf("qq: ws connect failed (%s): %s", safeURL, safeErr)
		}
		return fmt.Errorf("qq: ws connect failed (%s): %w", safeURL, err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	p.mu.Lock()
	p.stopped = false
	p.conn = conn
	p.cancel = cancel
	p.setConnected()
	p.mu.Unlock()

	slog.Info("qq: connected to OneBot", "url", p.redactSecrets(p.wsURL))

	// Start readLoop BEFORE callAPI: callAPI's response is routed by readLoop,
	// so calling it first would always time out after 15s and leave selfID=0,
	// which disables the self-message filter in handleMessage and lets the bot
	// respond to its own messages.
	go p.readLoop(ctx)

	// Get bot self info
	if info, err := p.callAPI("get_login_info", nil); err == nil {
		if uid := jsonInt64(info, "user_id"); uid != 0 {
			p.selfID.Store(uid)
		}
		nick, _ := info["nickname"].(string)
		slog.Info("qq: logged in", "qq", p.selfID.Load(), "nickname", nick)
	} else {
		slog.Warn("qq: get_login_info failed; self-message filter disabled until next reconnect", "error", p.redactSecrets(err.Error()))
	}

	return nil
}

func (p *Platform) readLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			return
		}
		conn := p.conn
		p.mu.Unlock()
		if conn == nil {
			if !p.reconnect(ctx) {
				return
			}
			continue
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			p.mu.Lock()
			stopped := p.stopped
			if p.conn == conn {
				p.conn = nil
			}
			p.mu.Unlock()
			_ = conn.Close()
			if stopped || ctx.Err() != nil {
				return
			}

			safeErr := p.redactSecrets(err.Error())
			slog.Error("qq: ws read error, reconnecting...", "error", safeErr)
			p.setDegraded("websocket disconnected: " + safeErr)
			p.recordMonitorError("error", "WS_DISCONNECTED", "qq websocket disconnected", safeErr)
			if !p.reconnect(ctx) {
				return
			}
			continue
		}

		var payload map[string]any
		if json.Unmarshal(raw, &payload) != nil {
			continue
		}

		// If this is an API response (has "echo" field), route to caller
		if echo, ok := payload["echo"].(string); ok {
			if ch, loaded := p.echoCh.LoadAndDelete(echo); loaded {
				if dataCh, ok := ch.(chan json.RawMessage); ok {
					dataCh <- raw
				}
			}
			continue
		}

		// Otherwise it's an event
		postType, _ := payload["post_type"].(string)
		if postType == "message" {
			// Message handling can call OneBot APIs. Keep reading their replies.
			go p.handleMessage(payload)
		}
	}
}

func (p *Platform) nextReconnectDelay(attempt int) time.Duration {
	if p.reconnectDelay != nil {
		return p.reconnectDelay(attempt)
	}
	if attempt <= qqMaxFastReconnectAttempts {
		return time.Duration(attempt) * 2 * time.Second
	}
	return qqDefaultSlowReconnectDelay
}

func (p *Platform) reconnect(ctx context.Context) bool {
	reportedMax := false
	for i := 1; ; i++ {
		delay := p.nextReconnectDelay(i)
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return false
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			return false
		}
		p.mu.Lock()
		stopped := p.stopped
		p.mu.Unlock()
		if stopped {
			return false
		}

		header := http.Header{}
		if p.token != "" {
			header.Set("Authorization", "Bearer "+p.token)
		}
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, p.wsURL, header)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			safeErr := p.redactSecrets(err.Error())
			if i <= qqMaxFastReconnectAttempts {
				slog.Warn("qq: reconnect attempt failed", "attempt", i, "error", safeErr)
				if i == qqMaxFastReconnectAttempts && !reportedMax {
					reportedMax = true
					detail := safeErr
					if detail == "" {
						detail = "max reconnect attempts reached"
					}
					p.setDegraded("websocket reconnect failed after 30 attempts: " + detail)
					p.recordMonitorError("error", "WS_RECONNECT_FAILED", "qq websocket reconnect failed after 30 attempts", detail)
					slog.Error("qq: failed to reconnect after 30 attempts, continuing slow retry", "error", detail)
				}
			} else {
				slog.Debug("qq: slow reconnect attempt failed", "attempt", i, "error", safeErr)
			}
			continue
		}

		p.mu.Lock()
		if p.stopped || ctx.Err() != nil {
			p.mu.Unlock()
			_ = conn.Close()
			return false
		}
		oldConn := p.conn
		p.conn = conn
		p.setConnected()
		p.mu.Unlock()
		if oldConn != nil && oldConn != conn {
			_ = oldConn.Close()
		}
		slog.Info("qq: reconnected", "attempt", i)
		return true
	}
}

func (p *Platform) handleMessage(payload map[string]any) {
	msgType, _ := payload["message_type"].(string)
	userID := jsonInt64(payload, "user_id")
	groupID := jsonInt64(payload, "group_id")
	messageID := jsonInt64(payload, "message_id")
	if msgType == "group" && !core.AllowList(p.allowGroups, strconv.FormatInt(groupID, 10)) {
		return
	}
	if msgType == "private" && !core.AllowList(p.allowPrivate, strconv.FormatInt(userID, 10)) {
		return
	}

	if selfID := p.selfID.Load(); selfID != 0 && userID == selfID {
		return
	}

	if ts, ok := payload["time"].(float64); ok && ts > 0 {
		if core.IsOldMessage(time.Unix(int64(ts), 0)) {
			slog.Debug("qq: ignoring old message after restart", "time", int64(ts))
			return
		}
	}

	msgIDStr := strconv.FormatInt(messageID, 10)
	if p.dedup.IsDuplicate(msgIDStr) {
		slog.Debug("qq: duplicate message ignored", "message_id", messageID)
		return
	}

	if !p.isAllowed(userID) {
		return
	}

	if msgType == "group" && p.requireMention && !p.messageMentionsSelf(payload) {
		slog.Debug("qq: ignoring group message without bot mention", "group_id", groupID, "user_id", userID)
		return
	}

	// Extract sender info
	var userName string
	if sender, ok := payload["sender"].(map[string]any); ok {
		card, _ := sender["card"].(string)
		nick, _ := sender["nickname"].(string)
		if card != "" {
			userName = card
		} else {
			userName = nick
		}
	}

	// Parse message content from CQ message array or raw_message
	text, images, files, audio := p.parseMessage(payload, msgType, groupID)
	if text == "" && len(images) == 0 && len(files) == 0 && audio == nil {
		return
	}

	var sessionKey string
	if msgType == "group" {
		if p.shareSessionInChannel {
			sessionKey = fmt.Sprintf("qq:g:%d", groupID)
		} else {
			sessionKey = fmt.Sprintf("qq:%d:%d", groupID, userID)
		}
	} else {
		sessionKey = fmt.Sprintf("qq:%d", userID)
	}

	rctx := &replyContext{
		messageType: msgType,
		userID:      userID,
		groupID:     groupID,
		messageID:   int32(messageID),
	}

	var chatName string
	if msgType == "group" {
		chatName = p.resolveGroupName(groupID)
	}

	msg := &core.Message{
		SessionKey:  sessionKey,
		Platform:    "qq",
		MessageID:   strconv.FormatInt(messageID, 10),
		UserID:      strconv.FormatInt(userID, 10),
		UserName:    userName,
		ChatName:    chatName,
		Content:     text,
		Images:      images,
		Files:       files,
		Audio:       audio,
		ReplyCtx:    rctx,
		MergeQueued: msgType == "group" && p.shareSessionInChannel,
	}

	slog.Debug("qq: message received", "type", msgType, "user", userID, "text_len", len(text))
	p.handler(p, msg)
}

func (p *Platform) parseMessage(payload map[string]any, msgType string, groupID int64) (string, []core.ImageAttachment, []core.FileAttachment, *core.AudioAttachment) {
	var textParts []string
	var images []core.ImageAttachment
	var files []core.FileAttachment
	var audio *core.AudioAttachment

	// OneBot message can be array of segments or a string
	switch msg := payload["message"].(type) {
	case []any:
		for _, seg := range msg {
			s, ok := seg.(map[string]any)
			if !ok {
				continue
			}
			segType, _ := s["type"].(string)
			data, _ := s["data"].(map[string]any)
			if data == nil {
				continue
			}

			switch segType {
			case "text":
				if text, ok := data["text"].(string); ok {
					textParts = append(textParts, text)
				}
			case "at":
				target := extractQQTarget(data)
				if target == "" {
					continue
				}
				if selfID := p.selfID.Load(); selfID != 0 && target == strconv.FormatInt(selfID, 10) {
					textParts = append(textParts, "[QQ：此消息直接 @ 了你] ")
				} else {
					textParts = append(textParts, "@"+target+" ")
				}
			case "image":
				if url, ok := data["url"].(string); ok && url != "" {
					imgData, mime, err := downloadFile(url)
					if err != nil {
						slog.Warn("qq: download image failed", "error", err)
						continue
					}
					images = append(images, core.ImageAttachment{
						MimeType: mime,
						Data:     imgData,
					})
				}
			case "record":
				if url, ok := data["url"].(string); ok && url != "" {
					audioData, _, err := downloadFile(url)
					if err != nil {
						slog.Warn("qq: download audio failed", "error", err)
						continue
					}
					format := "silk"
					if f, ok := data["file"].(string); ok {
						if strings.HasSuffix(f, ".amr") {
							format = "amr"
						} else if strings.HasSuffix(f, ".mp3") {
							format = "mp3"
						}
					}
					audio = &core.AudioAttachment{
						Data:   audioData,
						Format: format,
					}
				}
			case "file":
				name, _ := data["name"].(string)
				if name == "" {
					name, _ = data["file"].(string)
				}
				fileID, _ := data["file_id"].(string)
				if fileID == "" {
					fileID, _ = data["file"].(string)
				}

				slog.Info("qq: file segment received", "name", name, "file_id", fileID,
					"has_url", data["url"] != nil, "msg_type", msgType, "group_id", groupID)

				var downloaded bool

				// Step 1: Try direct URL from message segment (with longer timeout for large files)
				if url, ok := data["url"].(string); ok && url != "" {
					fileData, mime, err := downloadLargeFile(url)
					if err != nil {
						slog.Warn("qq: [step1] download file via segment URL failed", "error", err)
					} else {
						files = append(files, core.FileAttachment{
							MimeType: mime,
							Data:     fileData,
							FileName: name,
						})
						downloaded = true
						slog.Info("qq: [step1] file downloaded via segment URL", "name", name, "size", len(fileData))
					}
				}

				// Step 2: Get fresh direct link via NapCat API (CDN URLs expire / have download limits)
				// NapCat docs: params are STRINGS — group (not group_id), file_id
				if !downloaded && p.httpURL != "" && fileID != "" {
					var freshURL string
					if msgType == "group" && groupID != 0 {
						groupStr := strconv.FormatInt(groupID, 10)
						slog.Info("qq: [step2] trying get_group_file_url", "file_id", fileID, "group", groupStr)
						result, err := p.callHTTPAPI("get_group_file_url", map[string]any{
							"file_id": fileID,
							"group":   groupStr,
						})
						if err == nil {
							freshURL, _ = result["url"].(string)
							slog.Info("qq: [step2] get_group_file_url returned", "has_url", freshURL != "")
						} else {
							slog.Warn("qq: [step2] get_group_file_url failed", "file_id", fileID, "error", err)
						}
					} else {
						slog.Info("qq: [step2] trying get_private_file_url", "file_id", fileID)
						result, err := p.callHTTPAPI("get_private_file_url", map[string]any{
							"file_id": fileID,
						})
						if err == nil {
							freshURL, _ = result["url"].(string)
						} else {
							slog.Warn("qq: [step2] get_private_file_url failed", "file_id", fileID, "error", err)
						}
					}
					if freshURL != "" {
						fileData, mime, err := downloadLargeFile(freshURL)
						if err == nil {
							files = append(files, core.FileAttachment{
								MimeType: mime,
								Data:     fileData,
								FileName: name,
							})
							downloaded = true
							slog.Info("qq: [step2] file downloaded via fresh URL", "name", name, "size", len(fileData))
						} else {
							slog.Warn("qq: [step2] download file via fresh URL failed", "error", err)
						}
					}
				}

				// Step 3: Last resort — get_file (downloads to NapCat local or returns base64)
				if !downloaded && p.httpURL != "" && fileID != "" {
					slog.Info("qq: [step3] trying get_file", "file_id", fileID)
					result, err := p.callHTTPAPI("get_file", map[string]any{"file_id": fileID})
					if err == nil {
						if fileURL, ok := result["url"].(string); ok && fileURL != "" {
							fileData, mime, err := downloadLargeFile(fileURL)
							if err == nil {
								files = append(files, core.FileAttachment{
									MimeType: mime,
									Data:     fileData,
									FileName: name,
								})
								downloaded = true
							}
						}
						if !downloaded {
							if b64Str, ok := result["base64"].(string); ok && b64Str != "" {
								if decoded, err := base64.StdEncoding.DecodeString(b64Str); err == nil {
									files = append(files, core.FileAttachment{
										MimeType: http.DetectContentType(decoded),
										Data:     decoded,
										FileName: name,
									})
									downloaded = true
								}
							}
						}
					} else {
						slog.Warn("qq: get_file API failed", "file_id", fileID, "error", err)
					}
				}

				if !downloaded {
					slog.Warn("qq: file segment could not be downloaded", "name", name)
				}
			}
		}
	default:
		// raw_message fallback (string with CQ codes)
		if raw, ok := payload["raw_message"].(string); ok {
			textParts = append(textParts, stripCQCodes(raw))
		}
	}

	return strings.TrimSpace(strings.Join(textParts, "")), images, files, audio
}

// Reply sends a message as a reply to an incoming message.
func (p *Platform) Reply(ctx context.Context, replyCtx any, content string) error {
	return p.Send(ctx, replyCtx, content)
}

// Send sends a message to the conversation identified by replyCtx.
func (p *Platform) Send(ctx context.Context, replyCtx any, content string) error {
	cleaned, isSilent, leakedCoT := core.SanitizeSilentReply(content)
	if isSilent {
		if leakedCoT {
			slog.Warn("qq: suppressed leaked chain-of-thought in silent reply", "raw_len", len(content))
		}
		return nil
	}
	content = strings.TrimSpace(cleaned)
	if content == "" {
		return nil
	}
	rctx, ok := replyCtx.(*replyContext)
	if !ok {
		return fmt.Errorf("qq: invalid reply context")
	}

	// agy also emits service warnings as ordinary ACP text, outside EventError.
	if rctx.messageType == "group" && p.errorUserID != 0 {
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "⚠") || strings.HasPrefix(line, "❌") || strings.HasPrefix(line, "⏰ ⚠") || strings.HasPrefix(line, "⏰ ❌") {
				return p.SendError(ctx, replyCtx, content)
			}
		}
	}
	parts := []string{content}
	if p.splitLines {
		parts = nil
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.EqualFold(line, "[SILENT]") && !strings.EqualFold(line, "NO_REPLY") {
				parts = append(parts, line)
			}
		}
		if len(parts) == 0 {
			return nil
		}
		if len(parts) > 3 {
			parts = parts[:3]
		}
	}
	action := "send_private_msg"
	params := map[string]any{"user_id": rctx.userID}
	if rctx.messageType == "group" {
		action = "send_group_msg"
		params = map[string]any{"group_id": rctx.groupID}
	}
	for i, part := range parts {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(800 * time.Millisecond):
			}
		}
		params["message"] = part
		if _, err := p.callAPI(action, params); err != nil {
			return err
		}
	}
	return nil
}

// SendError sends group failures to the configured owner, without splitting
// or truncating diagnostic details. Private conversation errors stay private.
func (p *Platform) SendError(ctx context.Context, replyCtx any, content string) error {
	rctx, ok := replyCtx.(*replyContext)
	if !ok {
		return fmt.Errorf("qq: SendError: invalid reply context")
	}
	if rctx.messageType != "group" || p.errorUserID == 0 {
		return p.Send(ctx, replyCtx, content)
	}
	_, err := p.callAPI("send_private_msg", map[string]any{
		"user_id": p.errorUserID,
		"message": fmt.Sprintf("群 %d 报错：\n%s", rctx.groupID, content),
	})
	return err
}

var _ core.ErrorSender = (*Platform)(nil)

// SendImage sends an image to the conversation.
// Implements core.ImageSender.
func (p *Platform) SendImage(ctx context.Context, replyCtx any, img core.ImageAttachment) error {
	rctx, ok := replyCtx.(*replyContext)
	if !ok {
		return fmt.Errorf("qq: SendImage: invalid reply context type %T", replyCtx)
	}

	b64 := base64.StdEncoding.EncodeToString(img.Data)
	segments := []map[string]any{
		{"type": "image", "data": map[string]any{"file": "base64://" + b64}},
	}

	params := map[string]any{
		"message": segments,
	}

	if rctx.messageType == "group" {
		params["group_id"] = rctx.groupID
		_, err := p.callAPI("send_group_msg", params)
		if err != nil {
			return fmt.Errorf("qq: send image: %w", err)
		}
		return nil
	}

	params["user_id"] = rctx.userID
	_, err := p.callAPI("send_private_msg", params)
	if err != nil {
		return fmt.Errorf("qq: send image: %w", err)
	}
	return nil
}

var _ core.ImageSender = (*Platform)(nil)

func (p *Platform) wrapRedactedError(prefix string, err error) error {
	safeErr := p.redactSecrets(err.Error())
	if safeErr != err.Error() {
		return fmt.Errorf("%s: %s", prefix, safeErr)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func (p *Platform) Stop() error {
	p.mu.Lock()
	p.stopped = true
	cancel := p.cancel
	p.cancel = nil
	conn := p.conn
	p.conn = nil
	p.setDisconnected()
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func (p *Platform) resolveGroupName(groupID int64) string {
	if groupID == 0 {
		return ""
	}
	fallback := strconv.FormatInt(groupID, 10)
	if cached, ok := p.groupNameCache.Load(fallback); ok {
		if name, ok := cached.(string); ok {
			return name
		}
	}
	result, err := p.callAPI("get_group_info", map[string]any{"group_id": groupID})
	if err != nil {
		slog.Debug("qq: resolve group name failed", "group_id", groupID, "error", p.redactSecrets(err.Error()))
		return fallback
	}
	name, _ := result["group_name"].(string)
	if name != "" {
		p.groupNameCache.Store(fallback, name)
		return name
	}
	return fallback
}

// ── OneBot API call via WebSocket ───────────────────────────────

func (p *Platform) callAPI(action string, params map[string]any) (result map[string]any, apiErr error) {
	defer func() {
		if apiErr != nil {
			safeErr := p.redactSecrets(apiErr.Error())
			p.recordMonitorError("error", "PLATFORM_ERROR", fmt.Sprintf("qq API %s failed", action), safeErr)
			if action == "send_group_msg" && p.errorUserID != 0 {
				if _, err := p.callAPI("send_private_msg", map[string]any{
					"user_id": p.errorUserID,
					"message": fmt.Sprintf("群 %v 发送失败：\n%s", params["group_id"], safeErr),
				}); err != nil {
					slog.Error("qq: error notification failed", "error", p.redactSecrets(err.Error()))
				}
			}
		}
	}()
	seq := p.echoSeq.Add(1)
	echo := strconv.FormatInt(seq, 10)

	req := map[string]any{
		"action": action,
		"echo":   echo,
	}
	if params != nil {
		req["params"] = params
	}

	ch := make(chan json.RawMessage, 1)
	p.echoCh.Store(echo, ch)
	defer p.echoCh.Delete(echo)

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	conn := p.conn
	if conn == nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("qq: ws not connected")
	}
	err = conn.WriteMessage(websocket.TextMessage, data)
	p.mu.Unlock()
	if err != nil {
		return nil, p.wrapRedactedError("qq: ws write", err)
	}

	select {
	case raw := <-ch:
		var resp struct {
			Status  string          `json:"status"`
			RetCode int             `json:"retcode"`
			Data    json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &resp) != nil {
			return nil, fmt.Errorf("qq: invalid API response")
		}
		if resp.RetCode != 0 {
			return nil, fmt.Errorf("qq: API %s failed (retcode=%d)", action, resp.RetCode)
		}
		var result map[string]any
		_ = json.Unmarshal(resp.Data, &result)
		return result, nil

	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("qq: API %s timeout", action)
	}
}

// callHTTPAPI calls a OneBot v11 HTTP endpoint (e.g. /upload_group_file).
// Used for file operations — avoids WebSocket message size limits and
// file-path issues across Windows/WSL/Docker boundaries.
// Requires http_url to be configured.
func (p *Platform) callHTTPAPI(action string, params map[string]any) (result map[string]any, apiErr error) {
	defer func() {
		if apiErr != nil {
			safeErr := p.redactSecrets(apiErr.Error())
			p.recordMonitorError("error", "PLATFORM_ERROR", fmt.Sprintf("qq HTTP %s failed", action), safeErr)
			if action == "upload_group_file" && p.errorUserID != 0 {
				if _, err := p.callAPI("send_private_msg", map[string]any{
					"user_id": p.errorUserID,
					"message": fmt.Sprintf("群 %v 文件发送失败：\n%s", params["group_id"], safeErr),
				}); err != nil {
					slog.Error("qq: error notification failed", "error", p.redactSecrets(err.Error()))
				}
			}
		}
	}()
	if p.httpURL == "" {
		return nil, fmt.Errorf("qq: http_url not configured")
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	url := p.httpURL + "/" + action
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, p.wrapRedactedError(fmt.Sprintf("qq: HTTP %s request", action), err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.httpToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.httpToken)
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, p.wrapRedactedError(fmt.Sprintf("qq: HTTP %s failed", action), err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, p.wrapRedactedError(fmt.Sprintf("qq: HTTP %s read body", action), err)
	}

	var apiResp struct {
		Status  string          `json:"status"`
		RetCode int             `json:"retcode"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(raw, &apiResp) != nil {
		return nil, fmt.Errorf("qq: HTTP %s invalid response", action)
	}
	if apiResp.RetCode != 0 {
		return nil, fmt.Errorf("qq: HTTP %s failed (retcode=%d, msg=%s)", action, apiResp.RetCode, p.redactSecrets(apiResp.Message))
	}
	_ = json.Unmarshal(apiResp.Data, &result)
	return result, nil
}

// ── Helpers ─────────────────────────────────────────────────────

type replyContext struct {
	messageType string // "private" or "group"
	userID      int64
	groupID     int64
	messageID   int32
}

func (p *Platform) ReconstructReplyCtx(sessionKey string) (any, error) {
	// qq:{userID}, qq:{groupID}:{userID} or qq:g:{groupID}
	parts := strings.SplitN(sessionKey, ":", 3)
	if len(parts) < 2 || parts[0] != "qq" {
		return nil, fmt.Errorf("qq: invalid session key %q", sessionKey)
	}
	if len(parts) == 3 {
		if parts[1] == "g" {
			gid, _ := strconv.ParseInt(parts[2], 10, 64)
			return &replyContext{messageType: "group", groupID: gid}, nil
		}
		gid, _ := strconv.ParseInt(parts[1], 10, 64)
		uid, _ := strconv.ParseInt(parts[2], 10, 64)
		return &replyContext{messageType: "group", groupID: gid, userID: uid}, nil
	}
	uid, _ := strconv.ParseInt(parts[1], 10, 64)
	return &replyContext{messageType: "private", userID: uid}, nil
}

func (p *Platform) isAllowed(userID int64) bool {
	if p.allowFrom == "" || p.allowFrom == "*" {
		return true
	}
	uid := strconv.FormatInt(userID, 10)
	for _, allowed := range strings.Split(p.allowFrom, ",") {
		if strings.TrimSpace(allowed) == uid {
			return true
		}
	}
	return false
}

// extractQQTarget safely converts a OneBot "at" segment's data["qq"] field to string.
func extractQQTarget(data map[string]any) string {
	if data == nil {
		return ""
	}
	switch v := data["qq"].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	default:
		return ""
	}
}

// messageMentionsSelf reports whether a OneBot message payload @-mentions the
// bot itself (or @all). Used to gate group messages when require_mention is on.
// It fails open (returns true) when the bot's own id is unknown.
func (p *Platform) messageMentionsSelf(payload map[string]any) bool {
	selfID := p.selfID.Load()
	if selfID == 0 {
		return true
	}
	self := strconv.FormatInt(selfID, 10)
	switch m := payload["message"].(type) {
	case []any:
		for _, seg := range m {
			segMap, ok := seg.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := segMap["type"].(string); t != "at" {
				continue
			}
			data, _ := segMap["data"].(map[string]any)
			q := extractQQTarget(data)
			if q == self || q == "all" {
				return true
			}
		}
	case string:
		// CQ-code fallback for string-format OneBot implementations.
		if strings.Contains(m, "[CQ:at,qq="+self+"]") || strings.Contains(m, "[CQ:at,qq=all]") {
			return true
		}
	}
	return false
}

func jsonInt64(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

func stripCQCodes(s string) string {
	var result strings.Builder
	for len(s) > 0 {
		idx := strings.Index(s, "[CQ:")
		if idx < 0 {
			result.WriteString(s)
			break
		}
		result.WriteString(s[:idx])
		end := strings.Index(s[idx:], "]")
		if end < 0 {
			break
		}
		s = s[idx+end+1:]
	}
	return result.String()
}

func downloadLargeFile(url string) ([]byte, string, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	mime := resp.Header.Get("Content-Type")
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	return data, mime, nil
}

func downloadFile(url string) ([]byte, string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	mime := resp.Header.Get("Content-Type")
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	return data, mime, nil
}

// SendFile sends a file to the conversation.
// Implements core.FileSender.
//
// Uses base64-encoded file data to avoid file-path issues across
// Windows/WSL/Docker. Routes through NapCat HTTP API when configured
// (better for large files), falls back to WebSocket.
func (p *Platform) SendFile(ctx context.Context, replyCtx any, file core.FileAttachment) error {
	rctx, ok := replyCtx.(*replyContext)
	if !ok {
		return fmt.Errorf("qq: SendFile: invalid reply context type %T", replyCtx)
	}

	name := file.FileName
	if name == "" {
		name = "attachment"
	}

	b64data := "base64://" + base64.StdEncoding.EncodeToString(file.Data)

	// Pick API caller: prefer HTTP for large payloads, fall back to WebSocket.
	call := p.callAPI
	if p.httpURL != "" {
		call = p.callHTTPAPI
	}

	if rctx.messageType == "group" {
		_, err := call("upload_group_file", map[string]any{
			"group_id": rctx.groupID,
			"file":     b64data,
			"name":     name,
		})
		if err != nil {
			return fmt.Errorf("qq: SendFile group: %w", err)
		}
		return nil
	}

	// Private: use send_private_msg with file segment
	_, err := call("send_private_msg", map[string]any{
		"user_id": rctx.userID,
		"message": []map[string]any{
			{"type": "file", "data": map[string]any{"file": b64data, "name": name}},
		},
	})
	if err != nil {
		return fmt.Errorf("qq: SendFile private: %w", err)
	}
	return nil
}

var _ core.FileSender = (*Platform)(nil)
