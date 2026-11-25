package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func envValueFromSlice(env []string, key string) (string, int) {
	prefix := key + "="
	count := 0
	val := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			count++
			val = strings.TrimPrefix(entry, prefix)
		}
	}
	return val, count
}

func TestProbeSpawnEnv_MergesSessionAndProxyWithoutSSHConnection(t *testing.T) {
	a := &Agent{
		extraEnv: []string{
			"STATIC_ONLY=1",
			"SHARED_KEY=from-static",
		},
		sessionEnv: []string{
			"HTTPS_PROXY=http://127.0.0.1:7897",
			"HTTP_PROXY=http://127.0.0.1:7897",
			"NO_PROXY=localhost,127.0.0.1,::1",
			"SHARED_KEY=from-session",
			"SESSION_ONLY=yes",
		},
	}

	base := []string{"PATH=/usr/bin", "HOME=/tmp/home"}
	env := a.probeSpawnEnv(base)

	if v, cnt := envValueFromSlice(env, "HTTPS_PROXY"); cnt != 1 || v != "http://127.0.0.1:7897" {
		t.Fatalf("HTTPS_PROXY = (%q, count=%d), want (http://127.0.0.1:7897, 1)", v, cnt)
	}
	if v, cnt := envValueFromSlice(env, "HTTP_PROXY"); cnt != 1 || v != "http://127.0.0.1:7897" {
		t.Fatalf("HTTP_PROXY = (%q, count=%d), want (http://127.0.0.1:7897, 1)", v, cnt)
	}
	if v, cnt := envValueFromSlice(env, "SESSION_ONLY"); cnt != 1 || v != "yes" {
		t.Fatalf("SESSION_ONLY = (%q, count=%d), want (yes, 1)", v, cnt)
	}
	if v, cnt := envValueFromSlice(env, "STATIC_ONLY"); cnt != 1 || v != "1" {
		t.Fatalf("STATIC_ONLY = (%q, count=%d), want (1, 1)", v, cnt)
	}
	if v, cnt := envValueFromSlice(env, "SHARED_KEY"); cnt != 1 || v != "from-static" {
		t.Fatalf("SHARED_KEY = (%q, count=%d), want static precedence (from-static, 1)", v, cnt)
	}
	if v, cnt := envValueFromSlice(env, "SSH_CONNECTION"); cnt != 0 {
		t.Fatalf("must not inject SSH_CONNECTION, found %q (count=%d)", v, cnt)
	}
}

// TestACPListSessionsHelper acts as a mock ACP stdio server when invoked with
// GO_WANT_ACP_LIST_HELPER=1, echoing proxy/env diagnostics inside session titles.
func TestACPListSessionsHelper(t *testing.T) {
	if os.Getenv("GO_WANT_ACP_LIST_HELPER") != "1" {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": 1,
					"agentCapabilities": map[string]any{
						"sessionCapabilities": map[string]any{
							"list": map[string]any{},
						},
					},
				},
			}
			b, _ := json.Marshal(resp)
			fmt.Println(string(b))
		case "session/list":
			_, hasSSH := os.LookupEnv("SSH_CONNECTION")
			title := fmt.Sprintf("proxy=%s;has_ssh=%v", os.Getenv("HTTPS_PROXY"), hasSSH)
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"sessions": []map[string]any{
						{
							"sessionId": "sess-proxy-check",
							"cwd":       "",
							"title":     title,
						},
					},
				},
			}
			b, _ := json.Marshal(resp)
			fmt.Println(string(b))
		}
	}
	os.Exit(0)
}

func TestListSessions_PropagatesSessionProxyEnvWithoutSSHConnection(t *testing.T) {
	if orig, ok := os.LookupEnv("SSH_CONNECTION"); ok {
		_ = os.Unsetenv("SSH_CONNECTION")
		t.Cleanup(func() { _ = os.Setenv("SSH_CONNECTION", orig) })
	}
	t.Setenv("HTTPS_PROXY", "")

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	a := &Agent{
		workDir: t.TempDir(),
		cmd:     exe,
		args:    []string{"-test.run=^TestACPListSessionsHelper$"},
		extraEnv: []string{
			"GO_WANT_ACP_LIST_HELPER=1",
		},
	}
	a.SetSessionEnv([]string{
		"HTTPS_PROXY=http://127.0.0.1:7897",
	})

	sessions, err := a.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1: %+v", len(sessions), sessions)
	}
	wantTitle := "proxy=http://127.0.0.1:7897;has_ssh=false"
	if sessions[0].Summary != wantTitle {
		t.Fatalf("session summary = %q, want %q", sessions[0].Summary, wantTitle)
	}
}
