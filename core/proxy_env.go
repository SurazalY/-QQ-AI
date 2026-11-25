package core

import (
	"context"
	"log/slog"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	darwinProxyCacheTTL   = 60 * time.Second
	scutilProxyCmdTimeout = 2 * time.Second
	defaultNoProxyValue   = "localhost,127.0.0.1,::1"
)

var (
	darwinProxyMu        sync.Mutex
	darwinProxyFetchedAt time.Time
	darwinProxyCached    []string

	scutilProxyRunner = runScutilProxy
)

// MergeSystemProxyEnv ensures standard HTTP/HTTPS proxy environment variables
// are populated for spawned child processes when none are explicitly configured.
// Existing proxy variables in env are never overwritten.
func MergeSystemProxyEnv(env []string) []string {
	if hasAnyProxyEnv(env) {
		return env
	}
	if runtime.GOOS != "darwin" {
		return env
	}

	proxyPairs := detectDarwinSystemProxyEnv()
	if len(proxyPairs) == 0 {
		return env
	}

	toInject := append([]string(nil), proxyPairs...)
	if !hasNoProxyEnv(env) {
		toInject = append(toInject,
			"NO_PROXY="+defaultNoProxyValue,
			"no_proxy="+defaultNoProxyValue,
		)
	}
	return MergeEnv(env, toInject)
}

func hasAnyProxyEnv(env []string) bool {
	return hasNonEmptyEnvKey(env,
		"HTTPS_PROXY", "https_proxy",
		"HTTP_PROXY", "http_proxy",
		"ALL_PROXY", "all_proxy",
	)
}

func hasNoProxyEnv(env []string) bool {
	return hasNonEmptyEnvKey(env, "NO_PROXY", "no_proxy")
}

func hasNonEmptyEnvKey(env []string, keys ...string) bool {
	if len(env) == 0 || len(keys) == 0 {
		return false
	}
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	for _, entry := range env {
		k, v, ok := strings.Cut(entry, "=")
		if !ok || !want[k] {
			continue
		}
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

func detectDarwinSystemProxyEnv() []string {
	now := time.Now()

	darwinProxyMu.Lock()
	if !darwinProxyFetchedAt.IsZero() && now.Sub(darwinProxyFetchedAt) < darwinProxyCacheTTL {
		cached := append([]string(nil), darwinProxyCached...)
		darwinProxyMu.Unlock()
		return cached
	}
	darwinProxyMu.Unlock()

	raw, err := scutilProxyRunner()
	if err != nil {
		slog.Warn("proxy_env: scutil --proxy failed", "error", err)
		return nil
	}

	var pairs []string
	httpProxy, httpsProxy, allProxy := parseScutilProxyOutput(raw)
	if httpProxy != "" {
		pairs = append(pairs, "HTTP_PROXY="+httpProxy, "http_proxy="+httpProxy)
	}
	if httpsProxy != "" {
		pairs = append(pairs, "HTTPS_PROXY="+httpsProxy, "https_proxy="+httpsProxy)
	}
	if httpProxy == "" && httpsProxy == "" && allProxy != "" {
		pairs = append(pairs, "ALL_PROXY="+allProxy, "all_proxy="+allProxy)
	}

	darwinProxyMu.Lock()
	darwinProxyFetchedAt = time.Now()
	darwinProxyCached = append([]string(nil), pairs...)
	darwinProxyMu.Unlock()

	return pairs
}

func resetDarwinProxyCacheForTest() {
	darwinProxyMu.Lock()
	darwinProxyFetchedAt = time.Time{}
	darwinProxyCached = nil
	darwinProxyMu.Unlock()
}

func runScutilProxy() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scutilProxyCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "scutil", "--proxy").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// parseScutilProxyOutput extracts enabled HTTP, HTTPS, and SOCKS proxy URLs
// from macOS `scutil --proxy` output.
func parseScutilProxyOutput(raw string) (httpProxy, httpsProxy, allProxy string) {
	vals := make(map[string]string)
	depth := 0

	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if trimmed == "}" {
			if depth > 0 {
				depth--
			}
			continue
		}
		opensBrace := strings.HasSuffix(trimmed, "{")
		if depth <= 1 && !opensBrace {
			if k, v, ok := strings.Cut(trimmed, ":"); ok {
				key := strings.TrimSpace(k)
				val := strings.TrimSpace(v)
				if key != "" && val != "" {
					vals[key] = val
				}
			}
		}
		if opensBrace {
			depth++
		}
	}

	if vals["HTTPEnable"] == "1" {
		httpProxy = formatProxyURL("http", vals["HTTPProxy"], vals["HTTPPort"])
	}
	if vals["HTTPSEnable"] == "1" {
		httpsProxy = formatProxyURL("http", vals["HTTPSProxy"], vals["HTTPSPort"])
	}
	if vals["SOCKSEnable"] == "1" {
		allProxy = formatProxyURL("socks5", vals["SOCKSProxy"], vals["SOCKSPort"])
	}
	return httpProxy, httpsProxy, allProxy
}

func formatProxyURL(scheme, host, port string) string {
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" {
		return ""
	}
	if strings.Contains(host, "://") {
		return host
	}
	if port == "" {
		return scheme + "://" + host
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}
