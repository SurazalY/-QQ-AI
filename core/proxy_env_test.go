package core

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestParseScutilProxyOutput(t *testing.T) {
	sample := `<dictionary> {
  ExceptionsList : <array> {
    0 : 127.0.0.1
    1 : localhost
  }
  FTPPassive : 1
  HTTPEnable : 1
  HTTPPort : 7897
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7897
  HTTPSProxy : 127.0.0.1
  ProxyAutoConfigEnable : 0
  SOCKSEnable : 1
  SOCKSPort : 7897
  SOCKSProxy : 127.0.0.1
}`
	httpProxy, httpsProxy, allProxy := parseScutilProxyOutput(sample)
	if httpProxy != "http://127.0.0.1:7897" {
		t.Fatalf("httpProxy = %q, want http://127.0.0.1:7897", httpProxy)
	}
	if httpsProxy != "http://127.0.0.1:7897" {
		t.Fatalf("httpsProxy = %q, want http://127.0.0.1:7897", httpsProxy)
	}
	if allProxy != "socks5://127.0.0.1:7897" {
		t.Fatalf("allProxy = %q, want socks5://127.0.0.1:7897", allProxy)
	}

	disabled := `<dictionary> {
  HTTPEnable : 0
  HTTPPort : 7897
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 0
  SOCKSEnable : 0
}`
	h1, h2, h3 := parseScutilProxyOutput(disabled)
	if h1 != "" || h2 != "" || h3 != "" {
		t.Fatalf("expected empty proxies when disabled, got (%q, %q, %q)", h1, h2, h3)
	}

	ipv6 := `<dictionary> {
  HTTPSEnable : 1
  HTTPSPort : 8080
  HTTPSProxy : ::1
}`
	_, httpsIPv6, _ := parseScutilProxyOutput(ipv6)
	if httpsIPv6 != "http://[::1]:8080" {
		t.Fatalf("ipv6 httpsProxy = %q, want http://[::1]:8080", httpsIPv6)
	}
}

func TestMergeSystemProxyEnv_PreservesExistingAndDetectsDarwin(t *testing.T) {
	// Existing proxy variable must never be overwritten.
	base := []string{"PATH=/usr/bin", "HTTPS_PROXY=http://custom:9999"}
	got := MergeSystemProxyEnv(base)
	if !envHasValue(got, "HTTPS_PROXY", "http://custom:9999") {
		t.Fatalf("existing HTTPS_PROXY overwritten: %v", got)
	}

	if runtime.GOOS != "darwin" {
		return
	}

	prevRunner := scutilProxyRunner
	defer func() {
		scutilProxyRunner = prevRunner
		resetDarwinProxyCacheForTest()
	}()
	resetDarwinProxyCacheForTest()

	scutilProxyRunner = func() (string, error) {
		return `<dictionary> {
  HTTPEnable : 1
  HTTPPort : 7897
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7897
  HTTPSProxy : 127.0.0.1
}`, nil
	}

	merged := MergeSystemProxyEnv([]string{"PATH=/usr/bin"})
	if !envHasValue(merged, "HTTP_PROXY", "http://127.0.0.1:7897") {
		t.Fatalf("missing injected HTTP_PROXY: %v", merged)
	}
	if !envHasValue(merged, "HTTPS_PROXY", "http://127.0.0.1:7897") {
		t.Fatalf("missing injected HTTPS_PROXY: %v", merged)
	}
	if !envHasValue(merged, "NO_PROXY", defaultNoProxyValue) {
		t.Fatalf("missing injected NO_PROXY: %v", merged)
	}

	// SOCKS-only fallback when HTTP/HTTPS are disabled.
	resetDarwinProxyCacheForTest()
	scutilProxyRunner = func() (string, error) {
		return `<dictionary> {
  HTTPEnable : 0
  HTTPSEnable : 0
  SOCKSEnable : 1
  SOCKSProxy : 127.0.0.1
  SOCKSPort : 1080
  NO_PROXY_IGNORE : 1
}`, nil
	}
	mergedSocks := MergeSystemProxyEnv([]string{"PATH=/usr/bin", "NO_PROXY=10.0.0.1"})
	if !envHasValue(mergedSocks, "ALL_PROXY", "socks5://127.0.0.1:1080") {
		t.Fatalf("missing injected ALL_PROXY: %v", mergedSocks)
	}
	// Existing NO_PROXY must be preserved.
	if !envHasValue(mergedSocks, "NO_PROXY", "10.0.0.1") {
		t.Fatalf("existing NO_PROXY overwritten: %v", mergedSocks)
	}
}

func TestMergeSystemProxyEnv_ScutilErrorDoesNotPoisonCache(t *testing.T) {
	if runtime.GOOS != "darwin" {
		return
	}

	prevRunner := scutilProxyRunner
	defer func() {
		scutilProxyRunner = prevRunner
		resetDarwinProxyCacheForTest()
	}()
	resetDarwinProxyCacheForTest()

	scutilProxyRunner = func() (string, error) {
		return "", errors.New("scutil failed")
	}
	gotErr := MergeSystemProxyEnv([]string{"PATH=/usr/bin"})
	if len(gotErr) != 1 || gotErr[0] != "PATH=/usr/bin" {
		t.Fatalf("expected original env on scutil error, got %v", gotErr)
	}

	// Subsequent recovery must succeed without waiting 60s for a poisoned cache.
	scutilProxyRunner = func() (string, error) {
		return `<dictionary> {
  HTTPSEnable : 1
  HTTPSPort : 7897
  HTTPSProxy : 127.0.0.1
}`, nil
	}
	gotRecovered := MergeSystemProxyEnv([]string{"PATH=/usr/bin"})
	if !envHasValue(gotRecovered, "HTTPS_PROXY", "http://127.0.0.1:7897") {
		t.Fatalf("expected recovery after transient scutil error, got %v", gotRecovered)
	}
}

func envHasValue(env []string, key, want string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) && entry[len(prefix):] == want {
			return true
		}
	}
	return false
}
