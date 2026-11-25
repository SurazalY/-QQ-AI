package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSanitizeSilentReply_SuppressesLeakedCoT(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantCleaned string
		wantSilent  bool
		wantLeaked  bool
	}{
		{
			name:        "pure NO_REPLY",
			input:       "NO_REPLY",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  false,
		},
		{
			name:        "pure [SILENT]",
			input:       "[SILENT]",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  false,
		},
		{
			name:        "repeated [SILENT] lines",
			input:       "[SILENT]\n[SILENT]\n",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  false,
		},
		{
			name:        "real sample: [SILENT]tmp Chinese CoT",
			input:       "[SILENT]tmp\n请只输出静默标记。[SILENT]",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "real sample: [SILENT]tmp English CoT",
			input:       "[SILENT]tmp\nWait why did my previous output have `[SILENT]`? Let me check: right now I am inside thought, so my final response must be literally `[SILENT]`.[SILENT]",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "real sample: truncated [SILENT]tmp chunk",
			input:       "[SILENT]tmp\n请只输出静默标记。[",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "real sample: stray closing fragment SILENT]",
			input:       "SILENT]",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "real sample: repeated [SILENT] followed by [SILENT]tmp",
			input:       "[SILENT]\n[SILENT]tmp\n",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "real sample: bracketed [SILENT] with drafted reply and CoT self-correction",
			input:       "[SILENT]\n[SILENT]\n好家伙 然后呢！\nWait no, I should just output `[SILENT]` once! Let me output ONLY `[SILENT]`.[SILENT]",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "NO_REPLYtmp spillover",
			input:       "NO_REPLYtmp\n请只输出静默标记。NO_REPLY",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "real sample: NO_REPLY glued with random CJK glitch token",
			input:       "NO_REPLY旅\n",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "NO_REPLY followed by same-line glitch token",
			input:       "NO_REPLY 旅",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "NO_REPLY glued with punctuation",
			input:       "NO_REPLY。",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "repeated NO_REPLY then glued glitch token",
			input:       "NO_REPLY\nNO_REPLY旅\n",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "NO_REPLY bracketed CoT",
			input:       "NO_REPLY\nWait, output only NO_REPLY.\nNO_REPLY",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "unbracketed CoT ending with NO_REPLY",
			input:       "Wait — I am inside thought, output only `NO_REPLY`.\nNO_REPLY",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  true,
		},
		{
			name:        "warning banner wrapped in [SILENT] stays silent without CoT leak count",
			input:       "[SILENT]\n\n⚠ Your previous response contained an improperly formatted function call.\n\n[SILENT]",
			wantCleaned: "",
			wantSilent:  true,
			wantLeaked:  false,
		},
		{
			name:        "real sample: leading repeated [SILENT] before genuine reply",
			input:       "[SILENT]\n[SILENT]\n呜 听着鸡皮疙瘩都起来了\n大晚上的有点吓人呢",
			wantCleaned: "呜 听着鸡皮疙瘩都起来了\n大晚上的有点吓人呢",
			wantSilent:  false,
			wantLeaked:  false,
		},
		{
			name:        "real sample: genuine reply with trailing [SILENT]",
			input:       "AG现在是什么配置呀\n哇 那确实挺猛的 难怪可惜\n[SILENT]",
			wantCleaned: "AG现在是什么配置呀\n哇 那确实挺猛的 难怪可惜",
			wantSilent:  false,
			wantLeaked:  false,
		},
		{
			name:        "normal reply with trailing NO_REPLY preserves body",
			input:       "Hello\nNO_REPLY",
			wantCleaned: "Hello",
			wantSilent:  false,
			wantLeaked:  false,
		},
		{
			name:        "normal chat mentioning [SILENT] is untouched",
			input:       "呜 我以后一定小心不乱漏 [SILENT] 啦",
			wantCleaned: "呜 我以后一定小心不乱漏 [SILENT] 啦",
			wantSilent:  false,
			wantLeaked:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cleaned, silent, leaked := SanitizeSilentReply(tc.input)
			if silent != tc.wantSilent || leaked != tc.wantLeaked || cleaned != tc.wantCleaned {
				t.Fatalf("SanitizeSilentReply(%q) = (cleaned=%q, silent=%v, leaked=%v), want (%q, %v, %v)",
					tc.input, cleaned, silent, leaked, tc.wantCleaned, tc.wantSilent, tc.wantLeaked)
			}
		})
	}
}

type silentLeakTestPlatform struct {
	stubPlatform
	mu     sync.Mutex
	sent   []string
	errors []string
}

func (p *silentLeakTestPlatform) Reply(_ context.Context, _ any, content string) error {
	return p.Send(context.Background(), nil, content)
}

func (p *silentLeakTestPlatform) Send(_ context.Context, _ any, content string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, content)
	return nil
}

func (p *silentLeakTestPlatform) SendError(_ context.Context, _ any, content string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errors = append(p.errors, content)
	return nil
}

func (p *silentLeakTestPlatform) getSent() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.sent))
	copy(out, p.sent)
	return out
}

func (p *silentLeakTestPlatform) getErrors() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.errors))
	copy(out, p.errors)
	return out
}

func TestEngine_SilentLeakSuppressesAndAlertsOwner(t *testing.T) {
	p := &silentLeakTestPlatform{stubPlatform: stubPlatform{n: "stub-err"}}
	agent := &stubAgent{}
	e := NewEngine("test-leak", agent, []Platform{p}, "", LangChinese)
	e.display.ThinkingMessages = false
	e.display.ToolMessages = false

	sessionKey := "stub:group:1"
	session := e.sessions.GetOrCreateActive(sessionKey)
	agentSession := newControllableSession("s-leak-1")
	state := &interactiveState{
		agentSession: agentSession,
		platform:     p,
		replyCtx:     "ctx-1",
		agent:        agent,
	}

	runTurn := func(text string) {
		agentSession.events <- Event{Type: EventText, Content: text}
		agentSession.events <- Event{Type: EventResult, Content: text, Done: true}
		e.processInteractiveEvents(state, session, e.sessions, sessionKey, "m1", time.Now(), nil, nil, state.replyCtx, 0)
	}

	// Turn 1: leaked silent CoT -> suppressed from chat, below alert threshold (1 < 2)
	runTurn("[SILENT]tmp\n请只输出静默标记。[SILENT]")
	if sent := p.getSent(); len(sent) != 0 {
		t.Fatalf("turn 1 should not send to chat, got %v", sent)
	}
	if errs := p.getErrors(); len(errs) != 0 {
		t.Fatalf("turn 1 should not alert yet, got %v", errs)
	}

	// Turn 2: 2nd consecutive leak -> suppressed from chat AND alerts via SendError
	runTurn("[SILENT]tmp\nWait—I am inside thought, output ONLY `[SILENT]`.[SILENT]")
	if sent := p.getSent(); len(sent) != 0 {
		t.Fatalf("turn 2 should not send to chat, got %v", sent)
	}
	errs := p.getErrors()
	if len(errs) != 1 || !strings.Contains(errs[0], "/new") {
		t.Fatalf("expected 1 owner alert mentioning /new, got %v", errs)
	}

	// Turn 3: 3rd consecutive leak within cooldown -> still suppressed, no duplicate alert spam
	runTurn("NO_REPLYtmp\n请只输出静默标记。NO_REPLY")
	if sent := p.getSent(); len(sent) != 0 {
		t.Fatalf("turn 3 should not send to chat, got %v", sent)
	}
	if errs := p.getErrors(); len(errs) != 1 {
		t.Fatalf("cooldown should prevent duplicate alert, got %d: %v", len(errs), errs)
	}

	// Turn 4: clean reply delivers normally and resets streak
	runTurn("主人好呀")
	sent := p.getSent()
	if len(sent) != 1 || sent[0] != "主人好呀" {
		t.Fatalf("turn 4 expected normal reply, got %v", sent)
	}
	state.mu.Lock()
	streak := state.consecutiveSilentLeaks
	state.mu.Unlock()
	if streak != 0 {
		t.Fatalf("expected consecutiveSilentLeaks reset to 0, got %d", streak)
	}
}

func TestEngine_UnsolicitedReader_SuppressesSilentAndLeakedCoT(t *testing.T) {
	p := &silentLeakTestPlatform{stubPlatform: stubPlatform{n: "stub-unsolicited"}}
	agent := &stubAgent{}
	e := NewEngine("test-unsolicited", agent, []Platform{p}, "", LangChinese)

	sessionKey := "stub:group:2"
	session := e.sessions.GetOrCreateActive(sessionKey)
	agentSession := newControllableSession("s-unsolicited-1")
	state := &interactiveState{
		agentSession: agentSession,
		platform:     p,
		replyCtx:     "ctx-2",
		agent:        agent,
	}

	e.startUnsolicitedReader(state, session, e.sessions, sessionKey, "")
	defer e.stopUnsolicitedReader(state)

	// Send silent marker, broken fragment, and leaked CoT out-of-turn
	agentSession.events <- Event{Type: EventText, Content: "SILENT]"}
	agentSession.events <- Event{Type: EventResult, Done: true}
	agentSession.events <- Event{Type: EventText, Content: "[SILENT]tmp\n请只输出静默标记。[SILENT]"}
	agentSession.events <- Event{Type: EventResult, Done: true}
	agentSession.events <- Event{Type: EventText, Content: "NO_REPLY"}
	agentSession.events <- Event{Type: EventResult, Done: true}
	// Follow with a normal unsolicited message to synchronize completion
	agentSession.events <- Event{Type: EventText, Content: "后台任务完成"}
	agentSession.events <- Event{Type: EventResult, Done: true}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(p.getSent()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	sent := p.getSent()
	if len(sent) != 1 || sent[0] != "后台任务完成" {
		t.Fatalf("unsolicited reader should only deliver normal text, got %v", sent)
	}
	errs := p.getErrors()
	if len(errs) != 1 || !strings.Contains(errs[0], "/new") {
		t.Fatalf("expected 1 owner alert after 2 consecutive unsolicited CoT leaks, got %v", errs)
	}
}
