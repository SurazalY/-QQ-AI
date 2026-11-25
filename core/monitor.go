package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxMonitorErrors    = 500
	defaultMonitorPreviewItems = 100
	defaultMonitorProbeEvery   = 3 * time.Minute
	defaultMonitorProbeTimeout = 30 * time.Second
	monitorErrorsFileName      = "errors.jsonl"
	monitorCompactionThreshold = 600
	maxMonitorSummaryRunes     = 500
	maxMonitorDetailRunes      = 4000
)

// MonitorError is a persisted error or warning event exposed by the monitoring API.
type MonitorError struct {
	ID         string `json:"id"`
	Timestamp  string `json:"timestamp"`
	Source     string `json:"source"`
	Level      string `json:"level"`
	Project    string `json:"project,omitempty"`
	SessionKey string `json:"session_key,omitempty"`
	Code       string `json:"code,omitempty"`
	Summary    string `json:"summary"`
	Detail     string `json:"detail,omitempty"`
}

// MonitorErrorsSnapshot holds summary counters and filtered error items.
type MonitorErrorsSnapshot struct {
	TotalRetained  int            `json:"total_retained"`
	Recent15mCount int            `json:"recent_15m_count"`
	Recent1hCount  int            `json:"recent_1h_count"`
	FilteredCount  int            `json:"filtered_count,omitempty"`
	Items          []MonitorError `json:"items"`
}

// MonitorCCConnectService summarizes the cc-connect daemon health.
type MonitorCCConnectService struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	UptimeSeconds int    `json:"uptime_seconds"`
	ProjectsCount int    `json:"projects_count"`
}

// MonitorPlatformService summarizes one platform adapter's health.
type MonitorPlatformService struct {
	Project        string `json:"project"`
	Name           string `json:"name"`
	Connected      bool   `json:"connected"`
	Degraded       bool   `json:"degraded"`
	DegradedReason string `json:"degraded_reason,omitempty"`
	DegradedSince  string `json:"degraded_since,omitempty"`
	Status         string `json:"status"`
}

// MonitorAgentSessionStatus describes a single active session's process liveness.
type MonitorAgentSessionStatus struct {
	SessionKey     string `json:"session_key"`
	AgentSessionID string `json:"agent_session_id,omitempty"`
	Alive          bool   `json:"alive"`
}

// MonitorAgentService summarizes an agent backend and its active session liveness.
type MonitorAgentService struct {
	Project        string                      `json:"project"`
	Name           string                      `json:"name"`
	DisplayName    string                      `json:"display_name"`
	CLIBinary      string                      `json:"cli_binary,omitempty"`
	CLIVersion     string                      `json:"cli_version,omitempty"`
	ActiveModel    string                      `json:"active_model,omitempty"`
	ActiveSessions int                         `json:"active_sessions"`
	AliveSessions  int                         `json:"alive_sessions"`
	LiveSessions   int                         `json:"live_sessions"`
	Sessions       []MonitorAgentSessionStatus `json:"sessions"`
	Status         string                      `json:"status"`
}

// MonitorServices groups service-level health info in MonitorSnapshot.
type MonitorServices struct {
	CCConnect MonitorCCConnectService  `json:"cc_connect"`
	Platforms []MonitorPlatformService `json:"platforms"`
	Agents    []MonitorAgentService    `json:"agents"`
}

// MonitorProbeStatus summarizes the latest active probe result for the primary agent.
type MonitorProbeStatus struct {
	Project             string `json:"project,omitempty"`
	Agent               string `json:"agent,omitempty"`
	CLIBinary           string `json:"cli_binary,omitempty"`
	CLIVersion          string `json:"cli_version,omitempty"`
	ActiveModel         string `json:"active_model,omitempty"`
	Status              string `json:"status"`
	LatencyMS           int64  `json:"latency_ms"`
	LastCheckedAt       string `json:"last_checked_at,omitempty"`
	NextProbeAt         string `json:"next_probe_at,omitempty"`
	IntervalSeconds     int    `json:"interval_seconds"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastError           string `json:"last_error,omitempty"`
	InProgress          bool   `json:"in_progress"`
}

// MonitorSnapshot is the unified response payload for GET /api/v1/monitor and POST /api/v1/monitor/probe.
type MonitorSnapshot struct {
	UpdatedAt     string                `json:"updated_at"`
	OverallStatus string                `json:"overall_status"`
	Services      MonitorServices       `json:"services"`
	Probe         MonitorProbeStatus    `json:"probe"`
	Quota         *MonitorQuota         `json:"quota,omitempty"`
	Errors        MonitorErrorsSnapshot `json:"errors"`
}

type probeRecord struct {
	project             string
	agent               string
	cliBinary           string
	cliVersion          string
	activeModel         string
	status              string
	latencyMS           int64
	lastCheckedAt       time.Time
	nextProbeAt         time.Time
	consecutiveFailures int
	hasSucceeded        bool
	lastError           string
	quota               *MonitorQuota
}

type probeFlight struct {
	done     chan struct{}
	snapshot *MonitorSnapshot
}

// MonitorStore manages persistent recent errors and periodic/on-demand agent health probes.
type MonitorStore struct {
	filePath      string
	maxErrors     int
	probeInterval time.Duration
	probeTimeout  time.Duration
	startedAt     time.Time

	errMu          sync.Mutex
	errors         []MonitorError
	persistedLines int
	seq            atomic.Uint64

	engMu   sync.RWMutex
	engines map[string]*Engine

	probeStateMu sync.RWMutex
	probeRecords map[string]*probeRecord

	probeMu      sync.Mutex
	activeFlight *probeFlight

	lifeMu   sync.Mutex
	started  bool
	stopped  bool
	stopCh   chan struct{}
	bgCtx    context.Context
	bgCancel context.CancelFunc
	wg       sync.WaitGroup
}

// NewMonitorStore creates a MonitorStore backed by <dataDir>/errors.jsonl when dataDir is non-empty.
func NewMonitorStore(dataDir string) *MonitorStore {
	bgCtx, bgCancel := context.WithCancel(context.Background())
	ms := &MonitorStore{
		maxErrors:     defaultMaxMonitorErrors,
		probeInterval: defaultMonitorProbeEvery,
		probeTimeout:  defaultMonitorProbeTimeout,
		startedAt:     time.Now(),
		engines:       make(map[string]*Engine),
		probeRecords:  make(map[string]*probeRecord),
		stopCh:        make(chan struct{}),
		bgCtx:         bgCtx,
		bgCancel:      bgCancel,
	}
	if dataDir != "" {
		ms.filePath = filepath.Join(dataDir, monitorErrorsFileName)
		ms.loadFromDisk()
	}
	return ms
}

// RegisterEngine registers a project engine with the monitor store for probing and status snapshots.
func (s *MonitorStore) RegisterEngine(project string, e *Engine) {
	if s == nil || e == nil || project == "" {
		return
	}
	s.engMu.Lock()
	s.engines[project] = e
	s.engMu.Unlock()
}

// Start launches the background periodic prober (initial async probe + 3m ticker).
func (s *MonitorStore) Start() {
	if s == nil {
		return
	}
	s.lifeMu.Lock()
	if s.started || s.stopped {
		s.lifeMu.Unlock()
		return
	}
	s.started = true
	s.wg.Add(1)
	s.lifeMu.Unlock()

	go s.runProbeLoop()
}

// Stop halts the background prober and waits for active probe/flush work to finish.
func (s *MonitorStore) Stop() {
	if s == nil {
		return
	}
	s.lifeMu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.stopCh)
	}
	s.lifeMu.Unlock()
	s.bgCancel()
	s.wg.Wait()
}

func (s *MonitorStore) runProbeLoop() {
	defer s.wg.Done()
	// Run an initial probe asynchronously so startup is never blocked.
	s.ProbeNow(s.bgCtx)

	interval := s.probeInterval
	if interval <= 0 {
		interval = defaultMonitorProbeEvery
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.ProbeNow(s.bgCtx)
		}
	}
}

// RecordMonitorError implements MonitorErrorSink.
func (s *MonitorStore) RecordMonitorError(entry MonitorErrorEntry) {
	s.RecordError(entry)
}

// RecordError appends a bounded error entry to the in-memory ring buffer and JSONL file.
func (s *MonitorStore) RecordError(entry MonitorErrorEntry) {
	if s == nil {
		return
	}
	now := time.Now().UTC()
	source := strings.TrimSpace(entry.Source)
	if source == "" {
		source = "engine"
	}
	level := strings.ToLower(strings.TrimSpace(entry.Level))
	if level != "warn" && level != "error" {
		level = "error"
	}
	summary := truncateMonitorText(strings.TrimSpace(redactMonitorSecrets(entry.Summary)), maxMonitorSummaryRunes)
	detail := truncateMonitorText(strings.TrimSpace(redactMonitorSecrets(entry.Detail)), maxMonitorDetailRunes)
	if summary == "" && detail != "" {
		summary = truncateMonitorText(detail, maxMonitorSummaryRunes)
	}
	if summary == "" {
		summary = "unknown error"
	}
	code := strings.TrimSpace(entry.Code)
	if code == "" {
		code = classifyMonitorErrorCode(summary + "\n" + detail)
	}

	seq := s.seq.Add(1)
	rec := MonitorError{
		ID:         fmt.Sprintf("err-%d-%d", now.UnixMilli(), seq),
		Timestamp:  now.Format(time.RFC3339),
		Source:     source,
		Level:      level,
		Project:    strings.TrimSpace(entry.Project),
		SessionKey: strings.TrimSpace(entry.SessionKey),
		Code:       code,
		Summary:    summary,
		Detail:     detail,
	}

	s.errMu.Lock()
	defer s.errMu.Unlock()

	s.errors = append(s.errors, rec)
	trimmed := false
	if len(s.errors) > s.maxErrors {
		s.errors = append([]MonitorError(nil), s.errors[len(s.errors)-s.maxErrors:]...)
		trimmed = true
	}

	if s.filePath == "" {
		return
	}
	if trimmed && s.persistedLines >= monitorCompactionThreshold {
		if err := s.writeErrorsFileLocked(); err != nil {
			slog.Warn("monitor: compact errors.jsonl failed", "error", err)
		}
		return
	}
	if err := s.appendErrorLineLocked(rec); err != nil {
		slog.Warn("monitor: append errors.jsonl failed", "error", err)
	}
}

// ClearErrors clears all retained errors from memory and truncates errors.jsonl.
func (s *MonitorStore) ClearErrors() error {
	if s == nil {
		return nil
	}
	s.errMu.Lock()
	defer s.errMu.Unlock()

	s.errors = nil
	s.persistedLines = 0
	if s.filePath == "" {
		return nil
	}
	return AtomicWriteFile(s.filePath, []byte{}, 0o644)
}

// QueryErrors returns newest-first errors filtered by optional source, level, and limit.
func (s *MonitorStore) QueryErrors(source, level string, limit int) MonitorErrorsSnapshot {
	if s == nil {
		return MonitorErrorsSnapshot{Items: []MonitorError{}}
	}
	if limit <= 0 {
		limit = defaultMonitorPreviewItems
	}
	if limit > s.maxErrors {
		limit = s.maxErrors
	}

	srcFilter := strings.TrimSpace(source)
	lvlFilter := strings.TrimSpace(level)
	now := time.Now().UTC()
	cutoff15m := now.Add(-15 * time.Minute)
	cutoff1h := now.Add(-1 * time.Hour)

	s.errMu.Lock()
	snapshot := make([]MonitorError, len(s.errors))
	copy(snapshot, s.errors)
	s.errMu.Unlock()

	var recent15m, recent1h int
	filtered := make([]MonitorError, 0, minInt(limit, len(snapshot)))
	filteredTotal := 0

	// Iterate newest-first.
	for i := len(snapshot) - 1; i >= 0; i-- {
		item := snapshot[i]
		if ts, err := time.Parse(time.RFC3339, item.Timestamp); err == nil {
			if !ts.Before(cutoff15m) {
				recent15m++
			}
			if !ts.Before(cutoff1h) {
				recent1h++
			}
		}
		if srcFilter != "" && item.Source != srcFilter {
			continue
		}
		if lvlFilter != "" && item.Level != lvlFilter {
			continue
		}
		filteredTotal++
		if len(filtered) < limit {
			filtered = append(filtered, item)
		}
	}

	return MonitorErrorsSnapshot{
		TotalRetained:  len(snapshot),
		Recent15mCount: recent15m,
		Recent1hCount:  recent1h,
		FilteredCount:  filteredTotal,
		Items:          filtered,
	}
}

// ProbeNow triggers an on-demand health & quota probe with single-flight deduplication.
func (s *MonitorStore) ProbeNow(ctx context.Context) *MonitorSnapshot {
	return s.ProbeNowWithUptime(ctx, int(time.Since(s.startedAt).Seconds()))
}

// ProbeNowWithUptime runs a single-flight probe and returns the updated MonitorSnapshot.
// The shared probe executes under s.bgCtx so an individual caller disconnecting does
// not cancel an in-flight probe for other callers or record spurious cancellation errors.
func (s *MonitorStore) ProbeNowWithUptime(ctx context.Context, uptimeSeconds int) *MonitorSnapshot {
	if s == nil {
		return &MonitorSnapshot{}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.probeMu.Lock()
	if s.activeFlight != nil {
		flight := s.activeFlight
		s.probeMu.Unlock()
		select {
		case <-flight.done:
			if flight.snapshot != nil {
				return flight.snapshot
			}
		case <-ctx.Done():
			return s.Snapshot(uptimeSeconds)
		}
		return s.Snapshot(uptimeSeconds)
	}
	if ctx.Err() != nil {
		s.probeMu.Unlock()
		return s.Snapshot(uptimeSeconds)
	}
	s.lifeMu.Lock()
	if s.stopped {
		s.lifeMu.Unlock()
		s.probeMu.Unlock()
		return s.Snapshot(uptimeSeconds)
	}
	s.wg.Add(1)
	s.lifeMu.Unlock()

	flight := &probeFlight{done: make(chan struct{})}
	s.activeFlight = flight
	s.probeMu.Unlock()

	var finishOnce sync.Once
	finishFlight := func() *MonitorSnapshot {
		finishOnce.Do(func() {
			s.probeMu.Lock()
			if s.activeFlight == flight {
				s.activeFlight = nil
			}
			s.probeMu.Unlock()
			flight.snapshot = s.Snapshot(uptimeSeconds)
			close(flight.done)
		})
		return flight.snapshot
	}

	baseCtx := s.bgCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	timeout := s.probeTimeout
	if timeout <= 0 {
		timeout = defaultMonitorProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(baseCtx, timeout)

	go func() {
		defer s.wg.Done()
		defer cancel()
		defer finishFlight()
		s.executeProbes(probeCtx)
	}()

	select {
	case <-flight.done:
		if flight.snapshot != nil {
			return flight.snapshot
		}
		return s.Snapshot(uptimeSeconds)
	case <-ctx.Done():
		return s.Snapshot(uptimeSeconds)
	}
}

func (s *MonitorStore) executeProbes(ctx context.Context) {
	s.engMu.RLock()
	projects := make([]string, 0, len(s.engines))
	enginesCopy := make(map[string]*Engine, len(s.engines))
	for name, e := range s.engines {
		projects = append(projects, name)
		enginesCopy[name] = e
	}
	s.engMu.RUnlock()
	sort.Strings(projects)

	for _, proj := range projects {
		e := enginesCopy[proj]
		if e == nil || e.agent == nil {
			continue
		}
		prober, ok := e.agent.(AgentMonitorProber)
		if !ok {
			continue
		}

		cliBin, displayName := agentCLIInfo(e.agent)
		_ = displayName
		activeModel := ""
		if ms, ok := e.agent.(ModelSwitcher); ok {
			activeModel = ms.GetModel()
		}

		start := time.Now()
		report, err := prober.ProbeMonitor(ctx)
		latencyMS := time.Since(start).Milliseconds()
		checkedAt := time.Now().UTC()
		nextProbeAt := checkedAt.Add(s.probeInterval)

		s.probeStateMu.Lock()
		rec, exists := s.probeRecords[proj]
		if !exists {
			rec = &probeRecord{project: proj}
			s.probeRecords[proj] = rec
		}
		rec.agent = e.agent.Name()
		rec.cliBinary = cliBin
		if activeModel != "" {
			rec.activeModel = activeModel
		}
		rec.latencyMS = latencyMS
		rec.lastCheckedAt = checkedAt
		rec.nextProbeAt = nextProbeAt

		if report != nil {
			if report.CLIBinary != "" {
				rec.cliBinary = report.CLIBinary
			}
			if report.CLIVersion != "" {
				rec.cliVersion = report.CLIVersion
			}
			if report.ActiveModel != "" {
				rec.activeModel = report.ActiveModel
			}
			if report.Quota != nil {
				rec.quota = report.Quota
			}
		}

		var recordedErr error
		if err != nil {
			rec.consecutiveFailures++
			rec.lastError = redactMonitorSecrets(err.Error())
			if rec.consecutiveFailures == 1 && rec.hasSucceeded {
				rec.status = "degraded"
			} else {
				rec.status = "unhealthy"
			}
			recordedErr = err
		} else {
			rec.consecutiveFailures = 0
			rec.hasSucceeded = true
			if report != nil && report.PartialError != "" {
				rec.status = "degraded"
				rec.lastError = redactMonitorSecrets(report.PartialError)
			} else {
				rec.status = "healthy"
				rec.lastError = ""
			}
		}
		s.probeStateMu.Unlock()

		if recordedErr != nil {
			s.RecordError(MonitorErrorEntry{
				Source:  "probe",
				Level:   "error",
				Project: proj,
				Code:    "PROBE_ERROR",
				Summary: fmt.Sprintf("agent monitor probe failed (%s)", proj),
				Detail:  recordedErr.Error(),
			})
		} else if report != nil && report.PartialError != "" {
			s.RecordError(MonitorErrorEntry{
				Source:  "probe",
				Level:   "warn",
				Project: proj,
				Code:    "PROBE_ERROR",
				Summary: fmt.Sprintf("agent monitor probe partial warning (%s)", proj),
				Detail:  report.PartialError,
			})
		}
	}
}

// Snapshot returns the current MonitorSnapshot combining service health, probe, quota, and errors.
func (s *MonitorStore) Snapshot(uptimeSeconds int) *MonitorSnapshot {
	if s == nil {
		return &MonitorSnapshot{
			UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
			OverallStatus: "healthy",
			Errors:        MonitorErrorsSnapshot{Items: []MonitorError{}},
		}
	}
	if uptimeSeconds <= 0 {
		uptimeSeconds = int(time.Since(s.startedAt).Seconds())
	}

	s.engMu.RLock()
	projects := make([]string, 0, len(s.engines))
	enginesCopy := make(map[string]*Engine, len(s.engines))
	for name, e := range s.engines {
		projects = append(projects, name)
		enginesCopy[name] = e
	}
	s.engMu.RUnlock()
	sort.Strings(projects)

	s.probeMu.Lock()
	inProgress := s.activeFlight != nil
	s.probeMu.Unlock()

	s.probeStateMu.RLock()
	recordsCopy := make(map[string]probeRecord, len(s.probeRecords))
	for k, v := range s.probeRecords {
		if v != nil {
			cp := *v
			recordsCopy[k] = cp
		}
	}
	s.probeStateMu.RUnlock()

	platforms := make([]MonitorPlatformService, 0)
	agents := make([]MonitorAgentService, 0, len(projects))

	overall := "healthy"
	bumpStatus := func(st string) {
		switch st {
		case "unhealthy":
			overall = "unhealthy"
		case "degraded":
			if overall != "unhealthy" {
				overall = "degraded"
			}
		}
	}

	var primaryProbe *probeRecord
	var fallbackQuota *MonitorQuota
	for _, proj := range projects {
		e := enginesCopy[proj]
		if e == nil {
			continue
		}

		for _, p := range e.platforms {
			ps := MonitorPlatformService{
				Project:   proj,
				Name:      p.Name(),
				Connected: true,
				Status:    "healthy",
			}
			if ph, ok := p.(PlatformHealth); ok {
				info := ph.PlatformHealth()
				ps.Connected = info.Connected
				ps.Degraded = info.Degraded
				ps.DegradedReason = info.DegradedReason
				if !info.DegradedSince.IsZero() {
					ps.DegradedSince = info.DegradedSince.UTC().Format(time.RFC3339)
				}
				if info.Degraded {
					ps.Status = "degraded"
				} else if !info.Connected {
					ps.Status = "unhealthy"
				}
			}
			bumpStatus(ps.Status)
			platforms = append(platforms, ps)
		}

		if e.agent != nil {
			activeCount, aliveCount, sessDetails := e.MonitorAgentSessionLiveness()
			cliBin, displayName := agentCLIInfo(e.agent)
			activeModel := ""
			if ms, ok := e.agent.(ModelSwitcher); ok {
				activeModel = ms.GetModel()
			}
			agentStatus := "healthy"
			cliVersion := ""
			if rec, ok := recordsCopy[proj]; ok {
				if rec.cliVersion != "" {
					cliVersion = rec.cliVersion
				}
				if activeModel == "" && rec.activeModel != "" {
					activeModel = rec.activeModel
				}
				if rec.status != "" {
					agentStatus = rec.status
				}
				if fallbackQuota == nil && rec.quota != nil {
					fallbackQuota = rec.quota
				}
				if shouldReplacePrimaryProbe(primaryProbe, &rec) {
					cp := rec
					primaryProbe = &cp
				}
			}
			if agentStatus == "healthy" && activeCount > aliveCount {
				agentStatus = "degraded"
			}
			bumpStatus(agentStatus)

			sessionsOut := make([]MonitorAgentSessionStatus, len(sessDetails))
			for i, d := range sessDetails {
				sessionsOut[i] = MonitorAgentSessionStatus(d)
			}

			agents = append(agents, MonitorAgentService{
				Project:        proj,
				Name:           e.agent.Name(),
				DisplayName:    displayName,
				CLIBinary:      cliBin,
				CLIVersion:     cliVersion,
				ActiveModel:    activeModel,
				ActiveSessions: activeCount,
				AliveSessions:  aliveCount,
				LiveSessions:   aliveCount,
				Sessions:       sessionsOut,
				Status:         agentStatus,
			})
		}
	}

	probeStatus := MonitorProbeStatus{
		Status:          "unknown",
		IntervalSeconds: int(s.probeInterval.Seconds()),
		InProgress:      inProgress,
	}
	var quota *MonitorQuota
	if primaryProbe != nil {
		probeStatus.Project = primaryProbe.project
		probeStatus.Agent = primaryProbe.agent
		probeStatus.CLIBinary = primaryProbe.cliBinary
		probeStatus.CLIVersion = primaryProbe.cliVersion
		probeStatus.ActiveModel = primaryProbe.activeModel
		if primaryProbe.status != "" {
			probeStatus.Status = primaryProbe.status
		}
		probeStatus.LatencyMS = primaryProbe.latencyMS
		if !primaryProbe.lastCheckedAt.IsZero() {
			probeStatus.LastCheckedAt = primaryProbe.lastCheckedAt.UTC().Format(time.RFC3339)
		}
		if !primaryProbe.nextProbeAt.IsZero() {
			probeStatus.NextProbeAt = primaryProbe.nextProbeAt.UTC().Format(time.RFC3339)
		}
		probeStatus.ConsecutiveFailures = primaryProbe.consecutiveFailures
		probeStatus.LastError = primaryProbe.lastError
		quota = primaryProbe.quota
		if quota == nil {
			quota = fallbackQuota
		}
	} else if len(agents) > 0 {
		probeStatus.Project = agents[0].Project
		probeStatus.Agent = agents[0].Name
		probeStatus.CLIBinary = agents[0].CLIBinary
		probeStatus.CLIVersion = agents[0].CLIVersion
		probeStatus.ActiveModel = agents[0].ActiveModel
	}

	// Ensure active_model on probe and quota reflects live GetModel() if changed after last probe.
	if probeStatus.Project != "" {
		if e := enginesCopy[probeStatus.Project]; e != nil && e.agent != nil {
			if ms, ok := e.agent.(ModelSwitcher); ok {
				if liveModel := ms.GetModel(); liveModel != "" {
					probeStatus.ActiveModel = liveModel
				}
			}
		}
	}

	errorsSnap := s.QueryErrors("", "", defaultMonitorPreviewItems)

	return &MonitorSnapshot{
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		OverallStatus: overall,
		Services: MonitorServices{
			CCConnect: MonitorCCConnectService{
				Status:        "healthy",
				Version:       CurrentVersion,
				UptimeSeconds: uptimeSeconds,
				ProjectsCount: len(projects),
			},
			Platforms: platforms,
			Agents:    agents,
		},
		Probe:  probeStatus,
		Quota:  quota,
		Errors: errorsSnap,
	}
}

func probeSeverity(status string) int {
	switch status {
	case "unhealthy":
		return 3
	case "degraded":
		return 2
	case "healthy":
		return 1
	default:
		return 0
	}
}

// shouldReplacePrimaryProbe selects the highest-severity probe record for the
// top-level MonitorSnapshot.Probe summary so a healthy project never masks a
// failing project, breaking ties in favor of a record carrying quota details.
func shouldReplacePrimaryProbe(cur, cand *probeRecord) bool {
	if cand == nil {
		return false
	}
	if cur == nil {
		return true
	}
	curSev := probeSeverity(cur.status)
	candSev := probeSeverity(cand.status)
	if candSev != curSev {
		return candSev > curSev
	}
	return cur.quota == nil && cand.quota != nil
}

func (s *MonitorStore) loadFromDisk() {
	f, err := os.Open(s.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("monitor: failed to open errors.jsonl", "path", s.filePath, "error", err)
		}
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var loaded []MonitorError
	totalLines := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		totalLines++
		var item MonitorError
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			slog.Warn("monitor: skipping malformed errors.jsonl line", "error", err)
			continue
		}
		loaded = append(loaded, item)
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("monitor: error reading errors.jsonl", "error", err)
	}

	if len(loaded) > s.maxErrors {
		loaded = append([]MonitorError(nil), loaded[len(loaded)-s.maxErrors:]...)
	}

	s.errMu.Lock()
	s.errors = loaded
	s.persistedLines = totalLines
	if totalLines > s.maxErrors {
		if err := s.writeErrorsFileLocked(); err != nil {
			slog.Warn("monitor: failed to compact errors.jsonl on load", "error", err)
		}
	}
	s.errMu.Unlock()
}

func (s *MonitorStore) appendErrorLineLocked(rec MonitorError) error {
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	s.persistedLines++
	return nil
}

func (s *MonitorStore) writeErrorsFileLocked() error {
	var buf bytes.Buffer
	for _, item := range s.errors {
		b, err := json.Marshal(item)
		if err != nil {
			continue
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := AtomicWriteFile(s.filePath, buf.Bytes(), 0o644); err != nil {
		return err
	}
	s.persistedLines = len(s.errors)
	return nil
}

func classifyMonitorErrorCode(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "503") || strings.Contains(lower, "unavailable") || strings.Contains(lower, "no capacity available") || strings.Contains(lower, "high traffic"):
		return "503"
	case strings.Contains(lower, "429") || strings.Contains(lower, "rate limit"):
		return "429"
	case strings.Contains(lower, "eof") || strings.Contains(lower, "connection reset") || strings.Contains(lower, "network issue"):
		return "EOF"
	case strings.Contains(lower, "agent execution terminated due to error"):
		return "EXEC_TERMINATED"
	case strings.Contains(lower, "idle timeout") || strings.Contains(lower, "timed out"):
		return "IDLE_TIMEOUT"
	case strings.Contains(lower, "process exited"):
		return "PROCESS_EXIT"
	case strings.Contains(lower, "rpc"):
		return "RPC_ERROR"
	default:
		return "ERROR"
	}
}

func truncateMonitorText(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "..."
}

var (
	reBearerSecret = regexp.MustCompile(`(?i)(Bearer\s+)[^\s"']+`)
	reQuerySecret  = regexp.MustCompile(`(?i)\b(access_token|token|api_key|apikey|secret|password)=([^\s&"']+)`)
)

func redactMonitorSecrets(text string) string {
	if text == "" {
		return text
	}
	text = reBearerSecret.ReplaceAllString(text, "${1}[REDACTED]")
	text = reQuerySecret.ReplaceAllString(text, "${1}=[REDACTED]")
	return text
}
