package qq

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"

	"github.com/gorilla/websocket"
)

func TestSnowLumaOptions(t *testing.T) {
	platform, err := New(map[string]any{
		"token": "ws-token", "http_token": "http-token",
		"allow_groups": "706636822", "allow_private": "839107207", "split_lines": true, "error_user_id": "839107207",
	})
	if err != nil {
		t.Fatal(err)
	}
	p := platform.(*Platform)
	if p.httpToken != "http-token" || p.token != "ws-token" || !p.splitLines || p.errorUserID != 839107207 {
		t.Fatal("SnowLuma options were not applied")
	}
	// Rejected conversations must return before parsing attachments or invoking the engine.
	p.handleMessage(map[string]any{"message_type": "group", "group_id": float64(123), "user_id": float64(839107207)})
	p.handleMessage(map[string]any{"message_type": "private", "user_id": float64(123)})
	for _, content := range []string{"", " \n ", "[SILENT]", " \n[SILENT]\n "} {
		if err := p.Send(context.Background(), nil, content); err != nil {
			t.Fatal(err)
		}
	}
	platform, err = New(map[string]any{"token": "shared-token"})
	if err != nil {
		t.Fatal(err)
	}
	if platform.(*Platform).httpToken != "shared-token" {
		t.Fatal("http_token must retain the documented shared-token behavior")
	}
}

func TestParseMessage_PreservesMentionTarget(t *testing.T) {
	p := &Platform{}
	p.selfID.Store(3905380281)
	for _, tc := range []struct{ target, want string }{
		{"3905380281", "[QQ：此消息直接 @ 了你] 你好"},
		{"839107207", "@839107207 你好"},
	} {
		payload := map[string]any{"message": []any{
			map[string]any{"type": "at", "data": map[string]any{"qq": tc.target}},
			map[string]any{"type": "text", "data": map[string]any{"text": "你好"}},
		}}
		text, _, _, _ := p.parseMessage(payload, "group", 706636822)
		if text != tc.want {
			t.Fatalf("mention %s: got %q, want %q", tc.target, text, tc.want)
		}
	}
}

func TestPlatform_Name(t *testing.T) {
	p := &Platform{}
	if got := p.Name(); got != "qq" {
		t.Errorf("Name() = %q, want %q", got, "qq")
	}
}

func TestNew_DefaultWSURL(t *testing.T) {
	p, err := New(map[string]any{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	platform := p.(*Platform)
	if platform.wsURL != "ws://127.0.0.1:3001" {
		t.Errorf("wsURL = %q, want %q", platform.wsURL, "ws://127.0.0.1:3001")
	}
}

func TestNew_CustomWSURL(t *testing.T) {
	p, err := New(map[string]any{
		"ws_url": "ws://example.com:8080",
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	platform := p.(*Platform)
	if platform.wsURL != "ws://example.com:8080" {
		t.Errorf("wsURL = %q, want %q", platform.wsURL, "ws://example.com:8080")
	}
}

func TestNew_WithToken(t *testing.T) {
	p, err := New(map[string]any{
		"token": "my-secret-token",
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	platform := p.(*Platform)
	if platform.token != "my-secret-token" {
		t.Errorf("token = %q, want %q", platform.token, "my-secret-token")
	}
}

func TestNew_WithAllowFrom(t *testing.T) {
	p, err := New(map[string]any{
		"allow_from": "user1,user2,*",
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	platform := p.(*Platform)
	if platform.allowFrom != "user1,user2,*" {
		t.Errorf("allowFrom = %q, want %q", platform.allowFrom, "user1,user2,*")
	}
}

func TestNew_ShareSessionInChannel(t *testing.T) {
	p, err := New(map[string]any{
		"share_session_in_channel": true,
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	platform := p.(*Platform)
	if !platform.shareSessionInChannel {
		t.Error("shareSessionInChannel = false, want true")
	}
}

// verify Platform implements core.Platform
var _ core.Platform = (*Platform)(nil)

// TestStart_FetchesSelfIDWithoutTimeout verifies that Start() completes
// promptly with selfID populated from the get_login_info OneBot API call.
// Regression for a bug where Start invoked callAPI BEFORE launching readLoop,
// so the API response had no consumer and callAPI always timed out after 15s
// — leaving selfID=0 and disabling the self-message filter in handleMessage.
func TestStart_FetchesSelfIDWithoutTimeout(t *testing.T) {
	const botUserID = 999999

	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			var req map[string]any
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}
			if req["action"] == "get_login_info" {
				echo, _ := req["echo"].(string)
				resp := map[string]any{
					"status":  "ok",
					"retcode": 0,
					"echo":    echo,
					"data":    map[string]any{"user_id": botUserID, "nickname": "TestBot"},
				}
				raw, _ := json.Marshal(resp)
				_ = c.WriteMessage(websocket.TextMessage, raw)
			}
		}
	}))
	defer ts.Close()

	p := &Platform{
		wsURL: "ws" + strings.TrimPrefix(ts.URL, "http"),
	}

	done := make(chan error, 1)
	go func() {
		done <- p.Start(func(core.Platform, *core.Message) {})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = p.Stop()
		t.Fatal("Start did not complete within 5s; readLoop likely starts after callAPI, so get_login_info never gets a response")
	}
	defer p.Stop()

	if gotID := p.selfID.Load(); gotID != botUserID {
		t.Errorf("selfID = %d, want %d (self-message filter would be disabled)", gotID, botUserID)
	}
}

func TestNew_RequireMentionDefaultsTrue(t *testing.T) {
	p, err := New(map[string]any{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !p.(*Platform).requireMention {
		t.Error("requireMention should default to true")
	}
}

func TestNew_RequireMentionFalse(t *testing.T) {
	p, err := New(map[string]any{"require_mention": false})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if p.(*Platform).requireMention {
		t.Error("requireMention should be false when explicitly configured")
	}
}

func TestMessageMentionsSelf(t *testing.T) {
	p := &Platform{}
	p.selfID.Store(12345)
	cases := []struct {
		name    string
		message any
		want    bool
	}{
		{"array at self", []any{map[string]any{"type": "at", "data": map[string]any{"qq": "12345"}}}, true},
		{"array at all", []any{map[string]any{"type": "at", "data": map[string]any{"qq": "all"}}}, true},
		{"array at other", []any{map[string]any{"type": "at", "data": map[string]any{"qq": "99999"}}}, false},
		{"array no at", []any{map[string]any{"type": "text", "data": map[string]any{"text": "hello"}}}, false},
		{"cq string at self", "[CQ:at,qq=12345] hello", true},
		{"cq string at all", "[CQ:at,qq=all] hello", true},
		{"cq string no at", "hello", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{"message": tc.message}
			if got := p.messageMentionsSelf(payload); got != tc.want {
				t.Errorf("messageMentionsSelf() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMessageMentionsSelf_FailsOpenWithoutSelfID(t *testing.T) {
	p := &Platform{} // selfID unknown
	if !p.messageMentionsSelf(map[string]any{"message": []any{}}) {
		t.Error("should fail open (return true) when selfID is unknown")
	}
}

var (
	_ core.PlatformHealth       = (*Platform)(nil)
	_ core.MonitorErrorReporter = (*Platform)(nil)
)

type spyMonitorSink struct {
	mu      sync.Mutex
	entries []core.MonitorErrorEntry
}

func (s *spyMonitorSink) RecordMonitorError(entry core.MonitorErrorEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry)
}

func (s *spyMonitorSink) snapshot() []core.MonitorErrorEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.MonitorErrorEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

func TestPlatformHealthAndErrorSink(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var connMu sync.Mutex
	var serverConns []*websocket.Conn

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connMu.Lock()
		serverConns = append(serverConns, c)
		connMu.Unlock()
		defer c.Close()

		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			var req map[string]any
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}
			echo, _ := req["echo"].(string)
			action, _ := req["action"].(string)
			switch action {
			case "get_login_info":
				resp := map[string]any{
					"status":  "ok",
					"retcode": 0,
					"echo":    echo,
					"data":    map[string]any{"user_id": 123456, "nickname": "TestBot"},
				}
				raw, _ := json.Marshal(resp)
				_ = c.WriteMessage(websocket.TextMessage, raw)
			case "send_private_msg":
				resp := map[string]any{
					"status":  "failed",
					"retcode": 100,
					"echo":    echo,
				}
				raw, _ := json.Marshal(resp)
				_ = c.WriteMessage(websocket.TextMessage, raw)
			}
		}
	}))
	defer ts.Close()

	httpAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"failed","retcode":500,"message":"boom"}`))
	}))
	defer httpAPI.Close()

	p := &Platform{
		wsURL:   "ws" + strings.TrimPrefix(ts.URL, "http"),
		httpURL: httpAPI.URL,
	}
	sink := &spyMonitorSink{}
	p.SetMonitorErrorSink(sink)

	if h := p.PlatformHealth(); h.Connected || h.Degraded {
		t.Fatalf("initial PlatformHealth() = %+v, want disconnected & non-degraded", h)
	}

	if err := p.Start(func(core.Platform, *core.Message) {}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	h := p.PlatformHealth()
	if !h.Connected || h.Degraded || h.DegradedReason != "" {
		t.Fatalf("after Start PlatformHealth() = %+v, want connected & healthy", h)
	}

	// callAPI retcode != 0 should return error and record PLATFORM_ERROR.
	if _, err := p.callAPI("send_private_msg", map[string]any{"user_id": 1, "message": "hi"}); err == nil {
		t.Fatal("expected callAPI failure for retcode=100")
	}
	// callHTTPAPI retcode != 0 should return error and record PLATFORM_ERROR.
	if _, err := p.callHTTPAPI("send_private_msg", map[string]any{"user_id": 1, "message": "hi"}); err == nil {
		t.Fatal("expected callHTTPAPI failure for retcode=500")
	}

	// Force WS disconnect to verify degraded state transition and WS_DISCONNECTED error recording.
	connMu.Lock()
	for _, c := range serverConns {
		_ = c.Close()
	}
	connMu.Unlock()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h = p.PlatformHealth()
		if !h.Connected && h.Degraded && h.DegradedReason != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if h.Connected || !h.Degraded || h.DegradedReason == "" || h.DegradedSince.IsZero() {
		t.Fatalf("after WS close PlatformHealth() = %+v, want !Connected && Degraded with reason and since", h)
	}

	entries := sink.snapshot()
	var sawAPI, sawHTTP, sawWS bool
	for _, e := range entries {
		if e.Source != "platform" {
			t.Errorf("unexpected source %q", e.Source)
		}
		if e.Code == "PLATFORM_ERROR" && strings.Contains(e.Summary, "qq API send_private_msg") {
			sawAPI = true
		}
		if e.Code == "PLATFORM_ERROR" && strings.Contains(e.Summary, "qq HTTP send_private_msg") {
			sawHTTP = true
		}
		if e.Code == "WS_DISCONNECTED" {
			sawWS = true
		}
	}
	if !sawAPI || !sawHTTP || !sawWS {
		t.Fatalf("expected API (%v), HTTP (%v), and WS_DISCONNECTED (%v) monitor errors; got %+v", sawAPI, sawHTTP, sawWS, entries)
	}

	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	h = p.PlatformHealth()
	if h.Connected {
		t.Fatalf("after Stop PlatformHealth().Connected = true, want false")
	}
}

func TestParseMessage_AtNonStringDoesNotPanic(t *testing.T) {
	p := &Platform{}
	p.selfID.Store(100001)

	payload := map[string]any{
		"message": []any{
			map[string]any{"type": "at", "data": map[string]any{"qq": nil}},
			map[string]any{"type": "at", "data": map[string]any{"qq": float64(100001)}},
			map[string]any{"type": "at", "data": map[string]any{"qq": json.Number("200002")}},
			map[string]any{"type": "at", "data": map[string]any{"qq": true}},
			map[string]any{"type": "at", "data": map[string]any{}},
			map[string]any{"type": "text", "data": map[string]any{"text": "hello"}},
		},
	}

	text, _, _, _ := p.parseMessage(payload, "group", 300003)
	want := "[QQ：此消息直接 @ 了你] @200002 hello"
	if text != want {
		t.Fatalf("parseMessage() = %q, want %q", text, want)
	}
}

func TestStart_ConnectFailedAndSecretRedaction(t *testing.T) {
	closedWS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	wsBase := "ws" + strings.TrimPrefix(closedWS.URL, "http")
	closedWS.Close()

	closedHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	httpBase := closedHTTP.URL
	closedHTTP.Close()

	const (
		wsSecret        = "ws-bearer-secret-123"
		httpSecret      = "http-bearer-secret-456"
		querySecret     = "ws-query-secret-789"
		httpQuerySecret = "http-query-secret-012"
	)

	p := &Platform{
		wsURL:     wsBase + "/?access_token=" + querySecret,
		httpURL:   httpBase + "/onebot?access_token=" + httpQuerySecret,
		token:     wsSecret,
		httpToken: httpSecret,
	}
	sink := &spyMonitorSink{}
	p.SetMonitorErrorSink(sink)

	err := p.Start(func(core.Platform, *core.Message) {})
	if err == nil {
		t.Fatal("expected Start to fail against closed server")
	}
	if strings.Contains(err.Error(), querySecret) || strings.Contains(err.Error(), wsSecret) {
		t.Fatalf("Start error leaked secret: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("expected Start error to contain [REDACTED], got: %s", err.Error())
	}

	h := p.PlatformHealth()
	if h.Connected || !h.Degraded {
		t.Fatalf("PlatformHealth() = %+v, want !Connected && Degraded", h)
	}
	if strings.Contains(h.DegradedReason, querySecret) || strings.Contains(h.DegradedReason, wsSecret) {
		t.Fatalf("DegradedReason leaked secret: %s", h.DegradedReason)
	}

	entries := sink.snapshot()
	var sawConnectFailed bool
	for _, e := range entries {
		if e.Code == "WS_CONNECT_FAILED" {
			sawConnectFailed = true
			if strings.Contains(e.Detail, querySecret) || strings.Contains(e.Detail, wsSecret) {
				t.Fatalf("WS_CONNECT_FAILED detail leaked secret: %s", e.Detail)
			}
		}
	}
	if !sawConnectFailed {
		t.Fatalf("expected WS_CONNECT_FAILED in sink, got %+v", entries)
	}

	// Verify callHTTPAPI redacts URL query tokens and configured tokens on transport error.
	_, httpErr := p.callHTTPAPI("send_private_msg", map[string]any{"user_id": 200002, "message": "hi"})
	if httpErr == nil {
		t.Fatal("expected callHTTPAPI to fail against closed server")
	}
	if strings.Contains(httpErr.Error(), httpQuerySecret) || strings.Contains(httpErr.Error(), httpSecret) {
		t.Fatalf("callHTTPAPI error leaked secret: %s", httpErr.Error())
	}
	if !strings.Contains(httpErr.Error(), "[REDACTED]") {
		t.Fatalf("expected callHTTPAPI error to contain [REDACTED], got: %s", httpErr.Error())
	}

	// Verify redactSecrets handles Bearer tokens and query params directly.
	sample := "failed dial " + p.wsURL + " auth=Bearer " + wsSecret + " http=" + httpSecret
	redacted := p.redactSecrets(sample)
	for _, secret := range []string{wsSecret, httpSecret, querySecret} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redactSecrets left %q in %q", secret, redacted)
		}
	}
}

func TestReconnect_RecoveryAndMaxAttemptsFailed(t *testing.T) {
	t.Run("recovery", func(t *testing.T) {
		upgrader := websocket.Upgrader{}
		var attempts atomic.Int32
		var connMu sync.Mutex
		var conns []*websocket.Conn

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := attempts.Add(1)
			// Reject attempts 2 and 3; allow initial (1) and attempt 4.
			if n == 2 || n == 3 {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			connMu.Lock()
			conns = append(conns, c)
			connMu.Unlock()
			go func() {
				defer c.Close()
				for {
					_, msg, err := c.ReadMessage()
					if err != nil {
						return
					}
					var req map[string]any
					if json.Unmarshal(msg, &req) == nil && req["action"] == "get_login_info" {
						echo, _ := req["echo"].(string)
						raw, _ := json.Marshal(map[string]any{
							"status":  "ok",
							"retcode": 0,
							"echo":    echo,
							"data":    map[string]any{"user_id": 100001, "nickname": "Bot"},
						})
						_ = c.WriteMessage(websocket.TextMessage, raw)
					}
				}
			}()
		}))
		defer ts.Close()

		p := &Platform{
			wsURL:          "ws" + strings.TrimPrefix(ts.URL, "http"),
			reconnectDelay: func(int) time.Duration { return 5 * time.Millisecond },
		}
		if err := p.Start(func(core.Platform, *core.Message) {}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer p.Stop()

		// Drop the initial connection to trigger reconnect().
		connMu.Lock()
		first := conns[0]
		connMu.Unlock()
		_ = first.Close()

		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			h := p.PlatformHealth()
			if attempts.Load() >= 4 && h.Connected && !h.Degraded {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("expected reconnect recovery, attempts=%d health=%+v", attempts.Load(), p.PlatformHealth())
	})

	t.Run("max_attempts_WS_RECONNECT_FAILED", func(t *testing.T) {
		upgrader := websocket.Upgrader{}
		var attempts atomic.Int32
		var firstConn atomic.Pointer[websocket.Conn]
		const wsSecret = "reconnect-secret-token"

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := attempts.Add(1)
			if n > 1 {
				http.Error(w, "denied token="+wsSecret, http.StatusBadGateway)
				return
			}
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			firstConn.Store(c)
			go func() {
				defer c.Close()
				for {
					_, msg, err := c.ReadMessage()
					if err != nil {
						return
					}
					var req map[string]any
					if json.Unmarshal(msg, &req) == nil && req["action"] == "get_login_info" {
						echo, _ := req["echo"].(string)
						raw, _ := json.Marshal(map[string]any{
							"status":  "ok",
							"retcode": 0,
							"echo":    echo,
							"data":    map[string]any{"user_id": 100001, "nickname": "Bot"},
						})
						_ = c.WriteMessage(websocket.TextMessage, raw)
					}
				}
			}()
		}))
		defer ts.Close()

		p := &Platform{
			wsURL: "ws" + strings.TrimPrefix(ts.URL, "http") + "/?access_token=" + wsSecret,
			token: wsSecret,
			reconnectDelay: func(attempt int) time.Duration {
				if attempt <= qqMaxFastReconnectAttempts {
					return time.Millisecond
				}
				return 50 * time.Millisecond
			},
		}
		sink := &spyMonitorSink{}
		p.SetMonitorErrorSink(sink)

		if err := p.Start(func(core.Platform, *core.Message) {}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer p.Stop()

		if c := firstConn.Load(); c != nil {
			_ = c.Close()
		}

		deadline := time.Now().Add(5 * time.Second)
		var found core.MonitorErrorEntry
		var ok bool
		for time.Now().Before(deadline) {
			for _, e := range sink.snapshot() {
				if e.Code == "WS_RECONNECT_FAILED" {
					found = e
					ok = true
					break
				}
			}
			if ok {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !ok {
			t.Fatalf("expected WS_RECONNECT_FAILED after 30 attempts (attempts=%d, entries=%+v)", attempts.Load(), sink.snapshot())
		}
		if strings.Contains(found.Detail, wsSecret) {
			t.Fatalf("WS_RECONNECT_FAILED detail leaked secret: %s", found.Detail)
		}
		h := p.PlatformHealth()
		if h.Connected || !h.Degraded || !strings.Contains(h.DegradedReason, "30 attempts") {
			t.Fatalf("unexpected health after 30 failed attempts: %+v", h)
		}
		if strings.Contains(h.DegradedReason, wsSecret) {
			t.Fatalf("DegradedReason leaked secret: %s", h.DegradedReason)
		}
	})
}

func TestReconnect_ConcurrentWithStop_NoRace(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var attempts atomic.Int32
	reconnecting := make(chan struct{})
	releaseUpgrade := make(chan struct{})
	var releaseOnce sync.Once

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 2 {
			close(reconnecting)
			<-releaseUpgrade
		}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			for {
				_, msg, err := c.ReadMessage()
				if err != nil {
					return
				}
				var req map[string]any
				if json.Unmarshal(msg, &req) == nil && req["action"] == "get_login_info" {
					echo, _ := req["echo"].(string)
					raw, _ := json.Marshal(map[string]any{
						"status":  "ok",
						"retcode": 0,
						"echo":    echo,
						"data":    map[string]any{"user_id": 100001, "nickname": "Bot"},
					})
					_ = c.WriteMessage(websocket.TextMessage, raw)
				}
			}
		}()
	}))
	defer func() {
		releaseOnce.Do(func() { close(releaseUpgrade) })
		ts.Close()
	}()

	p := &Platform{
		wsURL:          "ws" + strings.TrimPrefix(ts.URL, "http"),
		reconnectDelay: func(int) time.Duration { return time.Millisecond },
	}
	if err := p.Start(func(core.Platform, *core.Message) {}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Force disconnect so readLoop enters reconnect() and reaches attempt 2's DialContext.
	p.mu.Lock()
	c := p.conn
	p.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}

	select {
	case <-reconnecting:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for reconnect dial")
	}

	// Stop concurrently while attempt 2 upgrade finishes.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = p.Stop()
	}()
	releaseOnce.Do(func() { close(releaseUpgrade) })
	wg.Wait()

	if h := p.PlatformHealth(); h.Connected {
		t.Fatalf("PlatformHealth().Connected = true after concurrent Stop(), want false")
	}
}

func TestStart_ConcurrentSelfIDWithIncomingMessages(t *testing.T) {
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		var writeMu sync.Mutex

		// Push inbound messages concurrently while get_login_info is being resolved.
		go func() {
			for i := 0; i < 5; i++ {
				ev, _ := json.Marshal(map[string]any{
					"post_type":    "message",
					"message_type": "group",
					"group_id":     300003,
					"user_id":      200002,
					"message_id":   i + 1,
					"message": []any{
						map[string]any{"type": "at", "data": map[string]any{"qq": "100001"}},
						map[string]any{"type": "text", "data": map[string]any{"text": "hi"}},
					},
				})
				writeMu.Lock()
				_ = c.WriteMessage(websocket.TextMessage, ev)
				writeMu.Unlock()
			}
		}()

		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			var req map[string]any
			if json.Unmarshal(msg, &req) == nil && req["action"] == "get_login_info" {
				time.Sleep(15 * time.Millisecond)
				echo, _ := req["echo"].(string)
				raw, _ := json.Marshal(map[string]any{
					"status":  "ok",
					"retcode": 0,
					"echo":    echo,
					"data":    map[string]any{"user_id": 100001, "nickname": "TestBot"},
				})
				writeMu.Lock()
				_ = c.WriteMessage(websocket.TextMessage, raw)
				writeMu.Unlock()
			}
		}
	}))
	defer ts.Close()

	p := &Platform{
		wsURL:       "ws" + strings.TrimPrefix(ts.URL, "http"),
		allowGroups: "300003",
	}
	if err := p.Start(func(core.Platform, *core.Message) {}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	if p.selfID.Load() != 100001 {
		t.Fatalf("selfID = %d, want 100001", p.selfID.Load())
	}
}

func TestSend_SuppressesSilentAndLeakedCoT(t *testing.T) {
	p := &Platform{splitLines: true}
	suppressed := []string{
		"",
		" \n ",
		"[SILENT]",
		"NO_REPLY",
		"no_reply",
		"[SILENT]\n[SILENT]",
		"NO_REPLY\nNO_REPLY",
		"[SILENT]tmp\n请只输出静默标记。[SILENT]",
		"[SILENT]tmp\nWait—I am in thought, output ONLY `[SILENT]`.[SILENT]",
		"[SILENT]tmp\n请只输出静默标记。[",
		"SILENT]",
		"[SILENT]\n[SILENT]tmp\n",
		"NO_REPLYtmp\n请只输出静默标记。NO_REPLY",
		"NO_REPLY旅\n",
		"NO_REPLY 旅",
		"NO_REPLY。",
		"[SILENT]\n好家伙 然后呢！\nWait no, I should just output `[SILENT]` once![SILENT]",
	}
	for _, content := range suppressed {
		// Passing nil replyCtx verifies Send short-circuits before attempting any API call.
		if err := p.Send(context.Background(), nil, content); err != nil {
			t.Fatalf("expected silent/leaked content %q to be suppressed cleanly, got err: %v", content, err)
		}
	}

	// Genuine reply with leading [SILENT] must still be delivered (reaches replyCtx validation).
	if err := p.Send(context.Background(), nil, "[SILENT]\n呜 听着鸡皮疙瘩都起来了"); err == nil {
		t.Fatal("expected genuine reply after leading [SILENT] to proceed to delivery")
	}
}
