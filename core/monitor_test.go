package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMonitorStore_PersistenceRingLimitAndClear(t *testing.T) {
	dir := t.TempDir()
	ms := NewMonitorStore(dir)

	// Record 620 errors to exercise both ring limit (500) and file compaction (>=600).
	for i := 1; i <= 620; i++ {
		ms.RecordError(MonitorErrorEntry{
			Source:     "agy_stream",
			Level:      "error",
			Project:    "whale",
			SessionKey: "qq:100:200",
			Code:       "503",
			Summary:    fmt.Sprintf("error #%d", i),
			Detail:     fmt.Sprintf("detail #%d", i),
		})
	}

	snap := ms.QueryErrors("", "", 500)
	if snap.TotalRetained != defaultMaxMonitorErrors {
		t.Fatalf("TotalRetained = %d, want %d", snap.TotalRetained, defaultMaxMonitorErrors)
	}
	if len(snap.Items) != defaultMaxMonitorErrors {
		t.Fatalf("len(Items) = %d, want %d", len(snap.Items), defaultMaxMonitorErrors)
	}
	// Newest-first ordering: first item is #620, last retained is #121.
	if snap.Items[0].Summary != "error #620" {
		t.Fatalf("Items[0].Summary = %q, want %q", snap.Items[0].Summary, "error #620")
	}
	if snap.Items[len(snap.Items)-1].Summary != "error #121" {
		t.Fatalf("Items[last].Summary = %q, want %q", snap.Items[len(snap.Items)-1].Summary, "error #121")
	}

	// Reload from errors.jsonl and verify identical retained set.
	reloaded := NewMonitorStore(dir)
	rSnap := reloaded.QueryErrors("", "", 500)
	if rSnap.TotalRetained != defaultMaxMonitorErrors {
		t.Fatalf("reloaded TotalRetained = %d, want %d", rSnap.TotalRetained, defaultMaxMonitorErrors)
	}
	if rSnap.Items[0].Summary != "error #620" {
		t.Fatalf("reloaded Items[0].Summary = %q, want %q", rSnap.Items[0].Summary, "error #620")
	}
	if rSnap.Items[len(rSnap.Items)-1].Summary != "error #121" {
		t.Fatalf("reloaded Items[last].Summary = %q, want %q", rSnap.Items[len(rSnap.Items)-1].Summary, "error #121")
	}

	// Clear errors and verify file is truncated and reload stays empty.
	if err := reloaded.ClearErrors(); err != nil {
		t.Fatalf("ClearErrors: %v", err)
	}
	if got := reloaded.QueryErrors("", "", 100).TotalRetained; got != 0 {
		t.Fatalf("after ClearErrors TotalRetained = %d, want 0", got)
	}
	raw, err := os.ReadFile(filepath.Join(dir, monitorErrorsFileName))
	if err != nil {
		t.Fatalf("read errors.jsonl: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("expected empty errors.jsonl after clear, got %d bytes", len(raw))
	}
	reloaded2 := NewMonitorStore(dir)
	if got := reloaded2.QueryErrors("", "", 100).TotalRetained; got != 0 {
		t.Fatalf("reloaded2 TotalRetained = %d, want 0", got)
	}
}

func TestMonitorStore_QueryFilterAndTimeCounts(t *testing.T) {
	ms := NewMonitorStore("")
	now := time.Now().UTC()

	ms.errMu.Lock()
	ms.errors = []MonitorError{
		{
			ID:        "1",
			Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339),
			Source:    "agy_stream",
			Level:     "error",
			Code:      "503",
			Summary:   "old 503",
		},
		{
			ID:        "2",
			Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339),
			Source:    "acp_process",
			Level:     "warn",
			Code:      "RPC_ERROR",
			Summary:   "mid warn",
		},
		{
			ID:        "3",
			Timestamp: now.Add(-5 * time.Minute).Format(time.RFC3339),
			Source:    "platform",
			Level:     "error",
			Code:      "WS_DISCONNECT",
			Summary:   "recent disconnect",
		},
		{
			ID:        "4",
			Timestamp: now.Add(-1 * time.Minute).Format(time.RFC3339),
			Source:    "agy_stream",
			Level:     "error",
			Code:      "EOF",
			Summary:   "recent eof",
		},
	}
	ms.errMu.Unlock()

	all := ms.QueryErrors("", "", 10)
	if all.TotalRetained != 4 {
		t.Fatalf("TotalRetained = %d, want 4", all.TotalRetained)
	}
	if all.Recent15mCount != 2 {
		t.Fatalf("Recent15mCount = %d, want 2", all.Recent15mCount)
	}
	if all.Recent1hCount != 3 {
		t.Fatalf("Recent1hCount = %d, want 3", all.Recent1hCount)
	}

	filtered := ms.QueryErrors("agy_stream", "error", 1)
	if filtered.FilteredCount != 2 {
		t.Fatalf("FilteredCount = %d, want 2", filtered.FilteredCount)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].ID != "4" {
		t.Fatalf("filtered.Items = %+v, want single item ID=4", filtered.Items)
	}
}

type stubMonitorProberAgent struct {
	stubAgent
	mu          sync.Mutex
	model       string
	cliBin      string
	displayName string
	probeCalls  atomic.Int32
	probeFn     func(ctx context.Context) (*MonitorProbeReport, error)
}

func (a *stubMonitorProberAgent) CLIBinaryName() string  { return a.cliBin }
func (a *stubMonitorProberAgent) CLIDisplayName() string { return a.displayName }
func (a *stubMonitorProberAgent) SetModel(m string) {
	a.mu.Lock()
	a.model = m
	a.mu.Unlock()
}
func (a *stubMonitorProberAgent) GetModel() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.model
}
func (a *stubMonitorProberAgent) AvailableModels(context.Context) []ModelOption { return nil }
func (a *stubMonitorProberAgent) ProbeMonitor(ctx context.Context) (*MonitorProbeReport, error) {
	a.probeCalls.Add(1)
	if a.probeFn != nil {
		return a.probeFn(ctx)
	}
	return &MonitorProbeReport{
		CLIBinary:   a.cliBin,
		CLIVersion:  "1.5.7",
		ActiveModel: a.GetModel(),
	}, nil
}

func TestMonitorStore_SingleFlightProbe(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	ag := &stubMonitorProberAgent{
		cliBin:      "agy",
		displayName: "Antigravity CLI",
		model:       "gemini-3.8-flash-medium",
		probeFn: func(ctx context.Context) (*MonitorProbeReport, error) {
			once.Do(func() { close(started) })
			<-release
			return &MonitorProbeReport{
				CLIBinary:   "agy",
				CLIVersion:  "1.5.7",
				ActiveModel: "gemini-3.8-flash-medium",
				Quota: &MonitorQuota{
					FetchedAt:   time.Now().UTC().Format(time.RFC3339),
					ActiveModel: "gemini-3.8-flash-medium",
					Groups: []MonitorQuotaGroup{
						{
							Name:                "Gemini Models",
							IsCurrentModelGroup: true,
							Buckets: []MonitorQuotaBucket{
								{ID: "gemini-5h", Window: "5h", RemainingFraction: 0.91, RemainingPercent: 91.0},
							},
						},
					},
				},
			}, nil
		},
	}

	e := NewEngine("whale", ag, nil, "", LangEnglish)
	ms := NewMonitorStore("")
	ms.RegisterEngine("whale", e)

	const callers = 5
	results := make([]*MonitorSnapshot, callers)
	var wg sync.WaitGroup
	wg.Add(callers)

	go func() {
		defer wg.Done()
		results[0] = ms.ProbeNow(context.Background())
	}()

	<-started
	for i := 1; i < callers; i++ {
		idx := i
		go func() {
			defer wg.Done()
			results[idx] = ms.ProbeNow(context.Background())
		}()
	}

	// Give concurrent callers time to enter single-flight wait before releasing.
	time.Sleep(40 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := ag.probeCalls.Load(); got != 1 {
		t.Fatalf("ProbeMonitor called %d times, want 1 (single-flight deduplication)", got)
	}
	for i, snap := range results {
		if snap == nil || snap.Probe.InProgress || snap.Probe.CLIVersion != "1.5.7" || snap.Quota == nil || len(snap.Quota.Groups) != 1 {
			t.Fatalf("caller %d got unexpected snapshot: %+v", i, snap)
		}
	}
}

func TestMonitorStore_ProbeFailureAndRecovery(t *testing.T) {
	var mode atomic.Int32 // 0=fail, 1=ok, 2=partial, 3=fail
	ag := &stubMonitorProberAgent{
		cliBin:      "agy",
		displayName: "Antigravity CLI",
		model:       "gemini-3.8-flash-medium",
		probeFn: func(ctx context.Context) (*MonitorProbeReport, error) {
			switch mode.Load() {
			case 0, 3:
				return nil, errors.New("agy /usage failed: 503 unavailable")
			case 2:
				return &MonitorProbeReport{
					CLIBinary:    "agy",
					CLIVersion:   "1.5.7",
					ActiveModel:  "gemini-3.8-flash-medium",
					PartialError: "agy /credits failed: timeout",
					Quota:        &MonitorQuota{FetchedAt: time.Now().UTC().Format(time.RFC3339)},
				}, nil
			default:
				return &MonitorProbeReport{
					CLIBinary:   "agy",
					CLIVersion:  "1.5.7",
					ActiveModel: "gemini-3.8-flash-medium",
					Quota:       &MonitorQuota{FetchedAt: time.Now().UTC().Format(time.RFC3339)},
				}, nil
			}
		},
	}

	e := NewEngine("whale", ag, nil, "", LangEnglish)
	ms := NewMonitorStore("")
	ms.RegisterEngine("whale", e)

	// Initial failure -> unhealthy, error recorded.
	mode.Store(0)
	s1 := ms.ProbeNow(context.Background())
	if s1.Probe.Status != "unhealthy" || s1.OverallStatus != "unhealthy" || s1.Probe.ConsecutiveFailures != 1 {
		t.Fatalf("initial fail snapshot = %+v, overall=%q", s1.Probe, s1.OverallStatus)
	}
	if s1.Errors.TotalRetained != 1 || s1.Errors.Items[0].Source != "probe" {
		t.Fatalf("expected 1 probe error recorded, got %+v", s1.Errors)
	}

	// Success -> healthy, consecutive failures reset.
	mode.Store(1)
	s2 := ms.ProbeNow(context.Background())
	if s2.Probe.Status != "healthy" || s2.OverallStatus != "healthy" || s2.Probe.ConsecutiveFailures != 0 || s2.Probe.LastError != "" {
		t.Fatalf("recovery snapshot = %+v, overall=%q", s2.Probe, s2.OverallStatus)
	}

	// Partial warning -> degraded.
	mode.Store(2)
	s3 := ms.ProbeNow(context.Background())
	if s3.Probe.Status != "degraded" || s3.OverallStatus != "degraded" {
		t.Fatalf("partial snapshot = %+v, overall=%q", s3.Probe, s3.OverallStatus)
	}

	// First transient failure after prior success -> degraded; second -> unhealthy.
	mode.Store(3)
	s4 := ms.ProbeNow(context.Background())
	if s4.Probe.Status != "degraded" || s4.Probe.ConsecutiveFailures != 1 {
		t.Fatalf("1st post-success fail = %+v", s4.Probe)
	}
	s5 := ms.ProbeNow(context.Background())
	if s5.Probe.Status != "unhealthy" || s5.Probe.ConsecutiveFailures != 2 {
		t.Fatalf("2nd consecutive fail = %+v", s5.Probe)
	}
}

type livenessStubSession struct {
	stubAgentSession
	id    string
	alive bool
}

func (s *livenessStubSession) CurrentSessionID() string { return s.id }
func (s *livenessStubSession) Alive() bool              { return s.alive }

type failingSendPlatform struct {
	stubPlatformEngine
	health PlatformHealthInfo
}

func (p *failingSendPlatform) Send(_ context.Context, _ any, _ string) error {
	return errors.New("send failed: connection reset")
}
func (p *failingSendPlatform) PlatformHealth() PlatformHealthInfo {
	return p.health
}

func TestMonitor_EngineErrorCaptureAndSessionLiveness(t *testing.T) {
	ms := NewMonitorStore("")
	p := &failingSendPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "stub-plat"},
		health: PlatformHealthInfo{
			Name:           "stub-plat",
			Connected:      false,
			Degraded:       true,
			DegradedReason: "ws disconnected",
			DegradedSince:  time.Now(),
		},
	}
	ag := &stubMonitorProberAgent{cliBin: "agy", displayName: "Antigravity CLI", model: "gemini-3.8-flash-medium"}
	e := NewEngine("whale", ag, []Platform{p}, "", LangEnglish)
	e.SetMonitorStore(ms)
	ms.RegisterEngine("whale", e)

	e.interactiveMu.Lock()
	e.interactiveStates["stub-plat:1"] = &interactiveState{
		agentSession: &livenessStubSession{id: "sess-alive", alive: true},
	}
	e.interactiveStates["stub-plat:2"] = &interactiveState{
		agentSession: &livenessStubSession{id: "sess-dead", alive: false},
	}
	e.interactiveMu.Unlock()

	active, alive, details := e.MonitorAgentSessionLiveness()
	if active != 2 || alive != 1 || len(details) != 2 {
		t.Fatalf("liveness = (%d, %d, %+v), want (2, 1, len=2)", active, alive, details)
	}

	// Trigger platform ready -> unavailable + sendAlreadyRenderedWithError + engine error.
	e.OnPlatformReady(p)
	e.OnPlatformUnavailable(p, errors.New("socket closed"))
	_ = e.sendAlreadyRenderedWithError(p, nil, "hello")
	e.recordMonitorError("engine", "error", "stub-plat:1", "503", "turn failed", "503 unavailable")

	snap := ms.Snapshot(42)
	if snap.OverallStatus != "degraded" {
		t.Fatalf("OverallStatus = %q, want degraded", snap.OverallStatus)
	}
	if len(snap.Services.Agents) != 1 || snap.Services.Agents[0].ActiveSessions != 2 || snap.Services.Agents[0].AliveSessions != 1 {
		t.Fatalf("Services.Agents = %+v", snap.Services.Agents)
	}
	if snap.Errors.TotalRetained != 3 {
		t.Fatalf("Errors.TotalRetained = %d, want 3 (got %+v)", snap.Errors.TotalRetained, snap.Errors.Items)
	}
}

func TestManagementAPI_MonitorEndpoints(t *testing.T) {
	dir := t.TempDir()
	ms := NewMonitorStore(dir)
	ms.RecordError(MonitorErrorEntry{
		Source:  "agy_stream",
		Level:   "error",
		Project: "whale",
		Code:    "503",
		Summary: "No capacity available",
	})
	ms.RecordError(MonitorErrorEntry{
		Source:  "platform",
		Level:   "warn",
		Project: "whale",
		Code:    "PLATFORM_ERROR",
		Summary: "timeout warning",
	})

	ag := &stubMonitorProberAgent{
		cliBin:      "agy",
		displayName: "Antigravity CLI",
		model:       "gemini-4-argon-medium",
		probeFn: func(ctx context.Context) (*MonitorProbeReport, error) {
			return &MonitorProbeReport{
				CLIBinary:   "agy",
				CLIVersion:  "1.5.7",
				ActiveModel: "gemini-4-argon-medium",
				Quota: &MonitorQuota{
					FetchedAt:   time.Now().UTC().Format(time.RFC3339),
					ActiveModel: "gemini-4-argon-medium",
					Groups: []MonitorQuotaGroup{
						{
							Name:                "Gemini 4 Argon",
							IsCurrentModelGroup: true,
							Buckets: []MonitorQuotaBucket{
								{ID: "gemini-argon-wtu-weekly", Window: "weekly", RemainingFraction: 0.978, RemainingPercent: 97.8},
							},
						},
					},
					Credits: &MonitorQuotaCredits{RemainingCredits: 0, UpgradeURI: "https://antigravity.google/g1-upgrade"},
				},
			}, nil
		},
	}

	e := NewEngine("whale", ag, nil, "", LangEnglish)
	e.SetMonitorStore(ms)
	ms.RegisterEngine("whale", e)

	mgmt := NewManagementServer(0, "tok", nil)
	mgmt.RegisterEngine("whale", e)
	mgmt.SetMonitorStore(ms)

	mux := http.NewServeMux()
	handler := mgmt.buildHandler(mux)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// 0. Initial GET /api/v1/monitor before first probe should report probe status "unknown".
	initialResp := mgmtGet(t, ts.URL+"/api/v1/monitor", "tok")
	if !initialResp.OK {
		t.Fatalf("initial GET /monitor failed: %s", initialResp.Error)
	}
	var snap0 MonitorSnapshot
	if err := json.Unmarshal(initialResp.Data, &snap0); err != nil {
		t.Fatalf("unmarshal initial monitor snapshot: %v", err)
	}
	if snap0.Probe.Status != "unknown" || snap0.Probe.InProgress {
		t.Fatalf("expected initial probe status=unknown and in_progress=false, got %+v", snap0.Probe)
	}

	// 1. POST /api/v1/monitor/probe
	probeResp := mgmtPost(t, ts.URL+"/api/v1/monitor/probe", "tok", nil)
	if !probeResp.OK {
		t.Fatalf("POST /monitor/probe failed: %s", probeResp.Error)
	}
	var snap MonitorSnapshot
	if err := json.Unmarshal(probeResp.Data, &snap); err != nil {
		t.Fatalf("unmarshal probe snapshot: %v", err)
	}
	if snap.OverallStatus != "healthy" || snap.Probe.InProgress || snap.Probe.Status != "healthy" || snap.Probe.CLIVersion != "1.5.7" || snap.Quota == nil || len(snap.Quota.Groups) != 1 {
		t.Fatalf("unexpected probe snapshot: %+v", snap)
	}
	if !snap.Quota.Groups[0].IsCurrentModelGroup {
		t.Fatalf("expected Argon group marked as current model group")
	}

	// 2. GET /api/v1/monitor
	monResp := mgmtGet(t, ts.URL+"/api/v1/monitor", "tok")
	if !monResp.OK {
		t.Fatalf("GET /monitor failed: %s", monResp.Error)
	}
	var snap2 MonitorSnapshot
	if err := json.Unmarshal(monResp.Data, &snap2); err != nil {
		t.Fatalf("unmarshal monitor snapshot: %v", err)
	}
	if snap2.Errors.TotalRetained != 2 || len(snap2.Errors.Items) != 2 {
		t.Fatalf("expected 2 errors in monitor snapshot, got %+v", snap2.Errors)
	}

	// 3. GET /api/v1/monitor/errors?source=agy_stream&level=error&limit=5
	errResp := mgmtGet(t, ts.URL+"/api/v1/monitor/errors?source=agy_stream&level=error&limit=5", "tok")
	if !errResp.OK {
		t.Fatalf("GET /monitor/errors failed: %s", errResp.Error)
	}
	var filtered MonitorErrorsSnapshot
	if err := json.Unmarshal(errResp.Data, &filtered); err != nil {
		t.Fatalf("unmarshal filtered errors: %v", err)
	}
	if filtered.TotalRetained != 2 || filtered.FilteredCount != 1 || len(filtered.Items) != 1 {
		t.Fatalf("unexpected filtered errors: %+v", filtered)
	}

	// 4. DELETE /api/v1/monitor/errors
	delResp := mgmtDelete(t, ts.URL+"/api/v1/monitor/errors", "tok")
	if !delResp.OK {
		t.Fatalf("DELETE /monitor/errors failed: %s", delResp.Error)
	}
	monAfterDel := mgmtGet(t, ts.URL+"/api/v1/monitor", "tok")
	var snap3 MonitorSnapshot
	_ = json.Unmarshal(monAfterDel.Data, &snap3)
	if snap3.Errors.TotalRetained != 0 || len(snap3.Errors.Items) != 0 {
		t.Fatalf("expected 0 errors after DELETE, got %+v", snap3.Errors)
	}
}

func TestMonitorStore_StartStopLifecycle(t *testing.T) {
	dir := t.TempDir()
	ms := NewMonitorStore(dir)
	ms.probeInterval = 10 * time.Millisecond

	probeStarted := make(chan struct{})
	var startOnce sync.Once
	var probeExited atomic.Bool

	ag := &stubMonitorProberAgent{
		cliBin:      "agy",
		displayName: "Antigravity CLI",
		model:       "gemini-3.8-flash",
		probeFn: func(ctx context.Context) (*MonitorProbeReport, error) {
			startOnce.Do(func() { close(probeStarted) })
			<-ctx.Done()
			time.Sleep(25 * time.Millisecond)
			probeExited.Store(true)
			return nil, ctx.Err()
		},
	}

	e := NewEngine("whale", ag, nil, "", LangEnglish)
	ms.RegisterEngine("whale", e)

	ms.Start()
	ms.Start() // idempotent second start

	select {
	case <-probeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for background probe to start")
	}

	ms.Stop()
	if !probeExited.Load() {
		t.Fatal("Stop() returned before background probe loop exited")
	}
	if _, err := os.Stat(filepath.Join(dir, monitorErrorsFileName)); err != nil {
		t.Fatalf("expected errors.jsonl written before Stop() returned: %v", err)
	}

	// Subsequent Stop and ProbeNow after Stop must be safe no-ops without spawning.
	ms.Stop()
	callsBefore := ag.probeCalls.Load()
	_ = ms.ProbeNow(context.Background())
	if got := ag.probeCalls.Load(); got != callsBefore {
		t.Fatalf("ProbeNow after Stop invoked ProbeMonitor (%d -> %d)", callsBefore, got)
	}
}

func TestMonitorStore_ProbeNowClientDisconnectDoesNotCancelSharedProbe(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	ag := &stubMonitorProberAgent{
		cliBin:      "agy",
		displayName: "Antigravity CLI",
		model:       "gemini-3.8-flash",
		probeFn: func(ctx context.Context) (*MonitorProbeReport, error) {
			once.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &MonitorProbeReport{
				CLIBinary:   "agy",
				CLIVersion:  "1.5.9",
				ActiveModel: "gemini-3.8-flash",
				Quota: &MonitorQuota{
					FetchedAt:   time.Now().UTC().Format(time.RFC3339),
					ActiveModel: "gemini-3.8-flash",
				},
			}, nil
		},
	}

	e := NewEngine("whale", ag, nil, "", LangEnglish)
	ms := NewMonitorStore("")
	ms.RegisterEngine("whale", e)
	defer ms.Stop()

	ctx1, cancel1 := context.WithCancel(context.Background())
	caller1Done := make(chan *MonitorSnapshot, 1)
	go func() {
		caller1Done <- ms.ProbeNowWithUptime(ctx1, 10)
	}()

	<-started

	caller2Done := make(chan *MonitorSnapshot, 1)
	go func() {
		caller2Done <- ms.ProbeNowWithUptime(context.Background(), 10)
	}()

	// Give caller2 time to attach to activeFlight, then cancel caller1 (leader HTTP disconnect).
	time.Sleep(30 * time.Millisecond)
	cancel1()

	select {
	case <-caller1Done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled caller1 did not return promptly")
	}

	close(release)

	var snap2 *MonitorSnapshot
	select {
	case snap2 = <-caller2Done:
	case <-time.After(2 * time.Second):
		t.Fatal("caller2 did not receive completed probe snapshot")
	}

	if got := ag.probeCalls.Load(); got != 1 {
		t.Fatalf("ProbeMonitor called %d times, want 1", got)
	}
	if snap2.Probe.Status != "healthy" || snap2.Probe.LastError != "" || snap2.Quota == nil {
		t.Fatalf("caller2 got unexpected snapshot after caller1 disconnect: %+v", snap2)
	}
	if errs := ms.QueryErrors("", "", 10); errs.TotalRetained != 0 {
		t.Fatalf("unexpected probe errors recorded on client disconnect: %+v", errs)
	}
}

func TestManagementAPI_MonitorProbeDisconnectDoesNotCancelProbe(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once

	ag := &stubMonitorProberAgent{
		cliBin:      "agy",
		displayName: "Antigravity CLI",
		model:       "gemini-3.8-flash",
		probeFn: func(ctx context.Context) (*MonitorProbeReport, error) {
			once.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &MonitorProbeReport{
				CLIBinary:   "agy",
				CLIVersion:  "1.5.9",
				ActiveModel: "gemini-3.8-flash",
			}, nil
		},
	}

	e := NewEngine("whale", ag, nil, "", LangEnglish)
	ms := NewMonitorStore("")
	ms.RegisterEngine("whale", e)
	defer ms.Stop()

	mgmt := NewManagementServer(0, "tok", nil)
	mgmt.RegisterEngine("whale", e)
	mgmt.SetMonitorStore(ms)

	ts := httptest.NewServer(mgmt.buildHandler(http.NewServeMux()))
	defer ts.Close()

	reqCtx, cancelReq := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, ts.URL+"/api/v1/monitor/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")

	reqDone := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		reqDone <- err
	}()

	<-started
	cancelReq()
	<-reqDone

	close(release)

	// Wait for the background single-flight probe to finish cleanly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := ms.Snapshot(1)
		if !snap.Probe.InProgress && snap.Probe.Status == "healthy" {
			if snap.Errors.TotalRetained != 0 {
				t.Fatalf("unexpected error recorded after HTTP disconnect: %+v", snap.Errors)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("shared probe did not complete healthy after HTTP disconnect: %+v", ms.Snapshot(1))
}

func TestMonitorStore_MultiProjectSnapshotPreservesFailure(t *testing.T) {
	quota := &MonitorQuota{
		FetchedAt:   time.Now().UTC().Format(time.RFC3339),
		ActiveModel: "gemini-3.8-flash",
	}

	t.Run("failing earlier project not masked by later healthy project with quota", func(t *testing.T) {
		ms := NewMonitorStore("")
		agFail := &stubMonitorProberAgent{
			cliBin:      "agy",
			displayName: "Agy",
			model:       "m1",
			probeFn: func(context.Context) (*MonitorProbeReport, error) {
				return nil, errors.New("503 unavailable")
			},
		}
		agOK := &stubMonitorProberAgent{
			cliBin:      "agy",
			displayName: "Agy",
			model:       "m2",
			probeFn: func(context.Context) (*MonitorProbeReport, error) {
				return &MonitorProbeReport{CLIBinary: "agy", CLIVersion: "1.5.9", Quota: quota}, nil
			},
		}
		ms.RegisterEngine("proj-a", NewEngine("proj-a", agFail, nil, "", LangEnglish))
		ms.RegisterEngine("proj-b", NewEngine("proj-b", agOK, nil, "", LangEnglish))

		snap := ms.ProbeNow(context.Background())
		if snap.OverallStatus != "unhealthy" {
			t.Fatalf("OverallStatus = %q, want unhealthy", snap.OverallStatus)
		}
		if snap.Probe.Project != "proj-a" || snap.Probe.Status != "unhealthy" || snap.Probe.LastError == "" {
			t.Fatalf("Probe = %+v, want proj-a unhealthy with LastError", snap.Probe)
		}
		if snap.Quota == nil {
			t.Fatal("expected Quota preserved from proj-b when proj-a failed without quota")
		}
	})

	t.Run("healthy earlier project without quota yields to failing later project", func(t *testing.T) {
		ms := NewMonitorStore("")
		agOK := &stubMonitorProberAgent{
			cliBin:      "claude",
			displayName: "Claude",
			model:       "sonnet",
			probeFn: func(context.Context) (*MonitorProbeReport, error) {
				return &MonitorProbeReport{CLIBinary: "claude", CLIVersion: "1.0"}, nil
			},
		}
		agFail := &stubMonitorProberAgent{
			cliBin:      "agy",
			displayName: "Agy",
			model:       "m2",
			probeFn: func(context.Context) (*MonitorProbeReport, error) {
				return nil, errors.New("eof")
			},
		}
		ms.RegisterEngine("proj-a", NewEngine("proj-a", agOK, nil, "", LangEnglish))
		ms.RegisterEngine("proj-b", NewEngine("proj-b", agFail, nil, "", LangEnglish))

		snap := ms.ProbeNow(context.Background())
		if snap.OverallStatus != "unhealthy" {
			t.Fatalf("OverallStatus = %q, want unhealthy", snap.OverallStatus)
		}
		if snap.Probe.Project != "proj-b" || snap.Probe.Status != "unhealthy" || snap.Probe.LastError == "" {
			t.Fatalf("Probe = %+v, want proj-b unhealthy with LastError", snap.Probe)
		}
	})

	t.Run("equal healthy severity prefers project carrying quota", func(t *testing.T) {
		ms := NewMonitorStore("")
		agNoQuota := &stubMonitorProberAgent{
			cliBin: "claude",
			probeFn: func(context.Context) (*MonitorProbeReport, error) {
				return &MonitorProbeReport{CLIBinary: "claude", CLIVersion: "1.0"}, nil
			},
		}
		agWithQuota := &stubMonitorProberAgent{
			cliBin: "agy",
			probeFn: func(context.Context) (*MonitorProbeReport, error) {
				return &MonitorProbeReport{CLIBinary: "agy", CLIVersion: "1.5.9", Quota: quota}, nil
			},
		}
		ms.RegisterEngine("proj-a", NewEngine("proj-a", agNoQuota, nil, "", LangEnglish))
		ms.RegisterEngine("proj-b", NewEngine("proj-b", agWithQuota, nil, "", LangEnglish))

		snap := ms.ProbeNow(context.Background())
		if snap.OverallStatus != "healthy" || snap.Probe.Project != "proj-b" || snap.Quota == nil {
			t.Fatalf("snap = %+v, want proj-b healthy with quota", snap)
		}
	})
}
