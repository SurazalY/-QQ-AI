package acp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func containsArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

func hasLogFileDevNull(args []string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--log-file" && args[i+1] == os.DevNull {
			return true
		}
	}
	return false
}

const sampleAgyUsageJSON = `{"conversation_id":"","status":"SUCCESS","response":"...","duration_seconds":0,"num_turns":0,"usage":{"input_tokens":0,"output_tokens":0,"thinking_tokens":0,"cache_read_tokens":0,"total_tokens":0},"command":{"name":"usage","data":{"description":"Within each group, models share a weekly limit and a 5-hour limit.","groups":[{"name":"Gemini Models","description":"Models within this group: Gemini Flash, Gemini Pro","buckets":[{"id":"gemini-weekly","name":"Weekly Limit Remaining","description":"You have used some of your weekly limit.","window":"weekly","remaining_fraction":0.894691526889801,"reset_time":"2026-11-17T16:05:23Z"},{"id":"gemini-5h","name":"Five Hour Limit Remaining","description":"You have used some of your 5-hour limit.","window":"5h","remaining_fraction":0.8900241851806641,"reset_time":"2026-11-13T06:46:40Z"}]},{"name":"Claude and GPT models","description":"Models within this group: Claude Opus, Claude Sonnet, GPT-OSS","buckets":[{"id":"3p-weekly","name":"Weekly Limit Remaining","window":"weekly","remaining_fraction":1,"reset_time":"2026-11-20T04:04:53Z"},{"id":"3p-5h","name":"Five Hour Limit Remaining","window":"5h","remaining_fraction":1,"reset_time":"2026-11-13T09:04:53Z"}]},{"name":"Gemini 4 Argon","description":"For paid Pro plans, Argon quota is separate.","buckets":[{"id":"gemini-argon-wtu-weekly","name":"Gemini 4 Argon Weekly Limit","description":"You have used some of your weekly limit.","window":"weekly","remaining_fraction":0.9779761433601379,"reset_time":"2026-11-16T15:46:15Z"}]}]}}}`

const sampleAgyCreditsJSON = `{"conversation_id":"","status":"SUCCESS","response":"Remaining credits\t0\n","duration_seconds":0,"num_turns":0,"usage":{"input_tokens":0,"output_tokens":0,"thinking_tokens":0,"cache_read_tokens":0,"total_tokens":0},"command":{"name":"credits","data":{"remaining_credits":250,"upgrade_uri":"https://antigravity.google/g1-upgrade"}}}`

func TestProbeMonitor_AgyUsageCreditsAndModelMatching(t *testing.T) {
	cases := []struct {
		model         string
		wantGroupName string
	}{
		{"gemini-3.8-flash-medium", "Gemini Models"},
		{"claude-opus-4-6", "Claude and GPT models"},
		{"gpt-oss-120b", "Claude and GPT models"},
		{"gemini-4-argon-medium", "Gemini 4 Argon"},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			a := &Agent{
				cmd:          "/usr/local/bin/agy",
				currentModel: tc.model,
				probeRunner: func(_ context.Context, _ string, args []string, _ string, _ []string) ([]byte, error) {
					if len(args) == 1 && args[0] == "--version" {
						return []byte("1.5.7\n"), nil
					}
					if containsArg(args, "/usage") {
						if !hasLogFileDevNull(args) {
							return nil, errors.New("missing --log-file devnull for /usage")
						}
						return []byte(sampleAgyUsageJSON), nil
					}
					if containsArg(args, "/credits") {
						if !hasLogFileDevNull(args) {
							return nil, errors.New("missing --log-file devnull for /credits")
						}
						return []byte(sampleAgyCreditsJSON), nil
					}
					return nil, errors.New("unexpected args")
				},
			}

			rep, err := a.ProbeMonitor(context.Background())
			if err != nil {
				t.Fatalf("ProbeMonitor failed: %v", err)
			}
			if rep.CLIBinary != "agy" || rep.CLIVersion != "1.5.7" || rep.ActiveModel != tc.model {
				t.Fatalf("unexpected report metadata: %+v", rep)
			}
			if rep.PartialError != "" {
				t.Fatalf("unexpected PartialError: %q", rep.PartialError)
			}
			if rep.Quota == nil || len(rep.Quota.Groups) != 3 {
				t.Fatalf("unexpected quota groups: %+v", rep.Quota)
			}
			if rep.Quota.Credits == nil || rep.Quota.Credits.RemainingCredits != 250 {
				t.Fatalf("unexpected credits: %+v", rep.Quota.Credits)
			}

			var matched []string
			for _, g := range rep.Quota.Groups {
				if g.IsCurrentModelGroup {
					matched = append(matched, g.Name)
				}
			}
			if len(matched) != 1 || matched[0] != tc.wantGroupName {
				t.Fatalf("model %q matched groups %v, want [%s]", tc.model, matched, tc.wantGroupName)
			}

			// Verify percentage calculation (0.89469... -> 89.5)
			if got := rep.Quota.Groups[0].Buckets[0].RemainingPercent; got != 89.5 {
				t.Fatalf("gemini-weekly RemainingPercent = %v, want 89.5", got)
			}
		})
	}
}

func TestProbeMonitor_AgyPartialAndFatalErrors(t *testing.T) {
	// Partial failure: /usage succeeds, /credits fails.
	a := &Agent{
		cmd:          "/usr/local/bin/agy",
		currentModel: "gemini-3.8-flash-medium",
		probeRunner: func(_ context.Context, _ string, args []string, _ string, _ []string) ([]byte, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("1.5.7"), nil
			}
			if containsArg(args, "/usage") {
				return []byte(sampleAgyUsageJSON), nil
			}
			return nil, errors.New("credits timeout")
		},
	}
	rep, err := a.ProbeMonitor(context.Background())
	if err != nil {
		t.Fatalf("expected partial success, got err: %v", err)
	}
	if rep.Quota == nil || rep.PartialError == "" {
		t.Fatalf("expected Quota + PartialError, got %+v", rep)
	}

	// Fatal failure: /usage fails, and /credits MUST NOT be invoked (short-circuit).
	creditsCalled := false
	a.probeRunner = func(_ context.Context, _ string, args []string, _ string, _ []string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("1.5.7"), nil
		}
		if containsArg(args, "/credits") {
			creditsCalled = true
		}
		return nil, errors.New("503 unavailable")
	}
	if _, err := a.ProbeMonitor(context.Background()); err == nil {
		t.Fatal("expected error when /usage fails")
	}
	if creditsCalled {
		t.Fatal("expected /credits not to be called after /usage failure")
	}
}

func TestHeadlessProbeEnv(t *testing.T) {
	got := headlessProbeEnv([]string{"PATH=/usr/bin"})
	hasBrowserFalse := false
	for _, entry := range got {
		if entry == "BROWSER=false" {
			hasBrowserFalse = true
		}
		if strings.HasPrefix(entry, "SSH_CONNECTION=") {
			t.Fatalf("must not inject SSH_CONNECTION (breaks agy keyring auth): %v", got)
		}
	}
	if !hasBrowserFalse {
		t.Fatalf("expected BROWSER=false in headlessProbeEnv, got %v", got)
	}

	// Respect existing BROWSER value when explicitly provided.
	explicit := headlessProbeEnv([]string{"BROWSER=/custom/noop"})
	for _, entry := range explicit {
		if entry == "BROWSER=false" {
			t.Fatalf("overwrote explicit BROWSER: %v", explicit)
		}
	}
}

func TestIsAgyCommand(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/usr/local/bin/agy", true},
		{"agy", true},
		{"AGY.EXE", true},
		{"/usr/local/bin/agent", false},
		{"/usr/local/bin/kimi", false},
	}
	for _, tc := range cases {
		if got := isAgyCommand(tc.path); got != tc.want {
			t.Errorf("isAgyCommand(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestProbeMonitor_NonAgyFallback(t *testing.T) {
	a := &Agent{
		cmd:          "/usr/local/bin/kimi",
		currentModel: "kimi-k2",
		probeRunner: func(_ context.Context, _ string, args []string, _ string, _ []string) ([]byte, error) {
			if len(args) == 1 && args[0] == "--version" {
				return []byte("kimi 0.9.1\n"), nil
			}
			return nil, errors.New("should not run /usage")
		},
	}
	rep, err := a.ProbeMonitor(context.Background())
	if err != nil {
		t.Fatalf("ProbeMonitor fallback failed: %v", err)
	}
	if rep.CLIBinary != "kimi" || rep.CLIVersion != "kimi 0.9.1" || rep.Quota != nil {
		t.Fatalf("unexpected non-agy report: %+v", rep)
	}
}

type recordingMonitorSink struct {
	mu      sync.Mutex
	entries []core.MonitorErrorEntry
}

func (s *recordingMonitorSink) RecordMonitorError(e core.MonitorErrorEntry) {
	s.mu.Lock()
	s.entries = append(s.entries, e)
	s.mu.Unlock()
}

func (s *recordingMonitorSink) snapshot() []core.MonitorErrorEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.MonitorErrorEntry, len(s.entries))
	copy(out, s.entries)
	return out
}

func makeAgentChunkParams(text string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"sessionId": "acp-1",
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content": map[string]any{
				"type": "text",
				"text": text,
			},
		},
	})
	return b
}

func TestACPSession_StreamWarningDetectionAndDeduplication(t *testing.T) {
	sink := &recordingMonitorSink{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &acpSession{
		events:        make(chan core.Event, 32),
		ctx:           ctx,
		cancel:        cancel,
		acpSessID:     "acp-1",
		sessionKey:    "qq:100001:200002",
		errorSink:     sink,
		turnWarnCodes: make(map[string]bool),
	}

	// Simulate an in-flight turn where agy streams repeated 503 retries across split chunks
	// followed by the high-traffic banner and generic termination banner.
	s.resetStreamTurnState()
	s.promptInFlight.Store(1)

	chunks := []string{
		"Normal intro text.\n",
		"⚠ API error (attempt 1/8): UNAVAILABLE (code 503): ",
		"No capacity available for model gemini-3.8-flash-medium on the server\n",
		"⚠ API error (attempt 2/8): UNAVAILABLE (code 503): No capacity available\n",
		"⚠ Our servers are experiencing high traffic right now, please try again in a minute.\n",
		"⚠ Agent execution terminated due to error.",
	}
	for _, c := range chunks {
		s.onNotification("session/update", makeAgentChunkParams(c))
	}
	s.flushStreamText()
	s.promptInFlight.Store(0)

	// Verify normal text streaming was NOT altered or dropped.
	var streamedCount int
	for len(s.events) > 0 {
		ev := <-s.events
		if ev.Type == core.EventText {
			streamedCount++
		}
	}
	if streamedCount != len(chunks) {
		t.Fatalf("streamed %d EventText events, want %d (streaming must not be altered)", streamedCount, len(chunks))
	}

	// Verify 503 retries + trailing EXEC_TERMINATED in the same turn were deduplicated to 1 entry.
	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d recorded stream errors, want 1: %+v", len(got), got)
	}
	if got[0].Source != "agy_stream" || got[0].Code != "503" || got[0].SessionKey != "qq:100001:200002" {
		t.Fatalf("unexpected recorded error: %+v", got[0])
	}

	// Turn 2: standalone EOF error in split chunks, then Turn 3: standalone EXEC_TERMINATED.
	s.resetStreamTurnState()
	s.promptInFlight.Store(1)
	s.onNotification("session/update", makeAgentChunkParams(`⚠ API error (attempt 1/8): Post "https://daily-cloudcode-pa.googleapis.com": unexpected EOF`+"\n"))
	s.flushStreamText()
	s.promptInFlight.Store(0)

	s.resetStreamTurnState()
	s.promptInFlight.Store(1)
	s.onNotification("session/update", makeAgentChunkParams("⚠ Agent execution terminated due to error."))
	s.flushStreamText()
	s.promptInFlight.Store(0)

	got = sink.snapshot()
	if len(got) != 3 {
		t.Fatalf("got %d total recorded errors, want 3: %+v", len(got), got)
	}
	if got[1].Code != "EOF" || got[2].Code != "EXEC_TERMINATED" {
		t.Fatalf("unexpected codes in turn 2 & 3: %+v", got[1:])
	}
}

func TestProbeMonitor_NonAgyCLIExtraArgsAndExcludesSessionEnv(t *testing.T) {
	t.Run("non_agy_wrapper_includes_cliExtraArgs_and_excludes_sessionEnv", func(t *testing.T) {
		var gotArgs []string
		var gotEnv []string
		a := &Agent{
			cmd:          "npx",
			cliExtraArgs: []string{"-y", "@example/acp-cli"},
			args:         []string{"--acp", "--stdio"},
			extraEnv:     []string{"HTTP_PROXY=http://127.0.0.1:7897", "STATIC_CFG=1"},
			sessionEnv:   []string{"CC_SESSION_KEY=qq:100001:200002", "SESSION_ONLY=leak"},
			currentModel: "example-model",
			probeRunner: func(_ context.Context, cmd string, args []string, _ string, env []string) ([]byte, error) {
				if cmd != "npx" {
					return nil, errors.New("unexpected command")
				}
				gotArgs = append([]string(nil), args...)
				gotEnv = append([]string(nil), env...)
				return []byte("acp-cli 2.3.4\n"), nil
			},
		}

		rep, err := a.ProbeMonitor(context.Background())
		if err != nil {
			t.Fatalf("ProbeMonitor failed: %v", err)
		}
		wantArgs := []string{"-y", "@example/acp-cli", "--version"}
		if !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Fatalf("probe args = %v, want %v", gotArgs, wantArgs)
		}
		if rep.CLIBinary != "npx" || rep.CLIVersion != "acp-cli 2.3.4" {
			t.Fatalf("unexpected report: %+v", rep)
		}
		for _, e := range gotEnv {
			if strings.HasPrefix(e, "CC_SESSION_KEY=") || strings.HasPrefix(e, "SESSION_ONLY=") {
				t.Fatalf("ProbeMonitor leaked sessionEnv entry %q into project probe: %v", e, gotEnv)
			}
		}
		if !containsArg(gotEnv, "HTTP_PROXY=http://127.0.0.1:7897") || !containsArg(gotEnv, "STATIC_CFG=1") {
			t.Fatalf("ProbeMonitor missing static extraEnv: %v", gotEnv)
		}
	})

	t.Run("agy_excludes_sessionEnv", func(t *testing.T) {
		var envs [][]string
		a := &Agent{
			cmd:          "/usr/local/bin/agy",
			extraEnv:     []string{"HTTPS_PROXY=http://127.0.0.1:7897"},
			sessionEnv:   []string{"CC_SESSION_KEY=qq:100001:200002"},
			currentModel: "gemini-3.8-flash-medium",
			probeRunner: func(_ context.Context, _ string, args []string, _ string, env []string) ([]byte, error) {
				envs = append(envs, append([]string(nil), env...))
				if containsArg(args, "--version") {
					return []byte("1.5.9\n"), nil
				}
				if containsArg(args, "/usage") {
					return []byte(sampleAgyUsageJSON), nil
				}
				if containsArg(args, "/credits") {
					return []byte(sampleAgyCreditsJSON), nil
				}
				return nil, errors.New("unexpected args")
			},
		}
		if _, err := a.ProbeMonitor(context.Background()); err != nil {
			t.Fatalf("ProbeMonitor failed: %v", err)
		}
		for _, env := range envs {
			for _, e := range env {
				if strings.HasPrefix(e, "CC_SESSION_KEY=") {
					t.Fatalf("agy ProbeMonitor leaked sessionEnv %q: %v", e, env)
				}
			}
		}
	})
}

func TestACPSession_CloseFlushesTrailingStreamTextAfterWait(t *testing.T) {
	sink := &recordingMonitorSink{}
	ctx, cancel := context.WithCancel(context.Background())

	s := &acpSession{
		events:        make(chan core.Event, 32),
		ctx:           ctx,
		cancel:        cancel,
		acpSessID:     "acp-1",
		sessionKey:    "qq:100001:200002",
		errorSink:     sink,
		turnWarnCodes: make(map[string]bool),
	}
	s.alive.Store(true)
	// Keep promptInFlight=1 so observeStreamText buffers the partial trailing line
	// until flushStreamText runs.
	s.promptInFlight.Store(1)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		<-ctx.Done()
		// Simulate a final warning chunk arriving during shutdown before I/O loop exits
		// (without a trailing newline).
		s.onNotification("session/update", makeAgentChunkParams("⚠ API error (attempt 1/8): unexpected EOF"))
	}()

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case _, ok := <-s.events:
		// Drain buffered EventText first if present, then ensure channel is closed.
		if ok {
			select {
			case _, ok2 := <-s.events:
				if ok2 {
					t.Fatal("expected events channel to be closed after Close()")
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for events channel close")
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for events channel close")
	}

	got := sink.snapshot()
	if len(got) != 1 || got[0].Code != "EOF" || got[0].Source != "agy_stream" {
		t.Fatalf("expected trailing EOF stream warning flushed after wg.Wait, got: %+v", got)
	}
}
