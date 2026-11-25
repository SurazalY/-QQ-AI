package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

const agyProbeTimeout = 25 * time.Second

// Ensure *Agent satisfies core.AgentMonitorProber at compile time.
var _ core.AgentMonitorProber = (*Agent)(nil)

type agyUsageCommandOutput struct {
	Status  string `json:"status"`
	Error   string `json:"error"`
	Command struct {
		Name string `json:"name"`
		Data struct {
			Description string `json:"description"`
			Groups      []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Buckets     []struct {
					ID                string  `json:"id"`
					Name              string  `json:"name"`
					Description       string  `json:"description"`
					Window            string  `json:"window"`
					RemainingFraction float64 `json:"remaining_fraction"`
					ResetTime         string  `json:"reset_time"`
				} `json:"buckets"`
			} `json:"groups"`
		} `json:"data"`
	} `json:"command"`
}

type agyCreditsCommandOutput struct {
	Status  string `json:"status"`
	Error   string `json:"error"`
	Command struct {
		Name string `json:"name"`
		Data struct {
			RemainingCredits float64 `json:"remaining_credits"`
			UpgradeURI       string  `json:"upgrade_uri"`
		} `json:"data"`
	} `json:"command"`
}

// ProbeMonitor runs a non-interactive health and quota check for this ACP agent.
// For `agy`, it runs `--version`, `/usage`, and (only if `/usage` succeeds) `/credits`
// sequentially with `--log-file <devnull>` and a 25s timeout to avoid concurrent token-refresh races.
// For other ACP binaries, it falls back to `<cmd> --version`.
func (a *Agent) ProbeMonitor(ctx context.Context) (*core.MonitorProbeReport, error) {
	a.mu.RLock()
	cmdPath := a.cmd
	cliExtraArgs := append([]string(nil), a.cliExtraArgs...)
	workDir := a.workDir
	// Project-level monitor probes must only inherit static config env (a.extraEnv),
	// never per-session temporary env (a.sessionEnv).
	extra := append([]string(nil), a.extraEnv...)
	runner := a.probeRunner
	a.mu.RUnlock()

	if runner == nil {
		runner = defaultProbeCmdRunner
	}

	probeCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, agyProbeTimeout)
		defer cancel()
	}

	cliBin := filepath.Base(cmdPath)
	activeModel := a.GetModel()

	if !isAgyCommand(cmdPath) {
		verArgs := append(append([]string(nil), cliExtraArgs...), "--version")
		verOut, err := runner(probeCtx, cmdPath, verArgs, workDir, extra)
		if err != nil {
			return &core.MonitorProbeReport{
				CLIBinary:   cliBin,
				ActiveModel: activeModel,
			}, fmt.Errorf("acp probe (%s --version): %w", cliBin, err)
		}
		return &core.MonitorProbeReport{
			CLIBinary:   cliBin,
			CLIVersion:  parseCLIVersion(verOut),
			ActiveModel: activeModel,
		}, nil
	}

	verOut, verErr := runner(probeCtx, cmdPath, []string{"--version"}, workDir, extra)

	report := &core.MonitorProbeReport{
		CLIBinary:   cliBin,
		ActiveModel: activeModel,
	}
	if verErr == nil {
		report.CLIVersion = parseCLIVersion(verOut)
	}

	usageArgs := []string{"--log-file", os.DevNull, "--output-format", "json", "-p", "/usage"}
	usageOut, usageErr := runner(probeCtx, cmdPath, usageArgs, workDir, extra)
	if usageErr != nil {
		if verErr != nil {
			return report, fmt.Errorf("agy probe failed: version: %v; usage: %w", verErr, usageErr)
		}
		return report, fmt.Errorf("agy /usage probe failed: %w", usageErr)
	}

	quota, err := parseAgyUsageJSON(usageOut, activeModel)
	if err != nil {
		return report, fmt.Errorf("parse agy /usage: %w", err)
	}

	creditsArgs := []string{"--log-file", os.DevNull, "--output-format", "json", "-p", "/credits"}
	creditsOut, creditsErr := runner(probeCtx, cmdPath, creditsArgs, workDir, extra)
	if creditsErr != nil {
		report.PartialError = fmt.Sprintf("agy /credits failed: %v", creditsErr)
	} else {
		credits, cErr := parseAgyCreditsJSON(creditsOut)
		if cErr != nil {
			report.PartialError = fmt.Sprintf("parse agy /credits: %v", cErr)
		} else {
			quota.Credits = credits
		}
	}

	if verErr != nil && report.PartialError == "" {
		report.PartialError = fmt.Sprintf("agy --version failed: %v", verErr)
	}

	report.Quota = quota
	return report, nil
}

func defaultProbeCmdRunner(ctx context.Context, cmdPath string, args []string, dir string, extraEnv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, cmdPath, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	env := core.MergeEnv(os.Environ(), extraEnv)
	env = core.MergeSystemProxyEnv(env)
	env = headlessProbeEnv(env)
	cmd.Env = env
	if isAgyCommand(cmdPath) {
		// Provide an immediate-EOF pipe (not /dev/null, which has ModeCharDevice)
		// combined with a detached session (Setsid) so agy never enters
		// interactive browser OAuth when silent auth fails.
		cmd.Stdin = strings.NewReader("")
		detachControllingTTY(cmd)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = strings.TrimSpace(stdout.String())
		}
		if errText != "" {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, errText)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func isAgyCommand(cmdPath string) bool {
	base := filepath.Base(strings.TrimSpace(cmdPath))
	return strings.EqualFold(base, "agy") || strings.EqualFold(base, "agy.exe")
}

// headlessProbeEnv injects defensive environment variables so background probes
// never trigger interactive browser OAuth prompts without disabling keyring auth.
func headlessProbeEnv(baseEnv []string) []string {
	if envHasNonEmptyKey(baseEnv, "BROWSER") {
		return baseEnv
	}
	return core.MergeEnv(baseEnv, []string{"BROWSER=false"})
}

func envHasNonEmptyKey(env []string, keys ...string) bool {
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

func parseCLIVersion(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	return s
}

func extractJSONObject(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return trimmed
	}
	start := bytes.IndexByte(trimmed, '{')
	end := bytes.LastIndexByte(trimmed, '}')
	if start >= 0 && end > start {
		return trimmed[start : end+1]
	}
	return trimmed
}

func parseAgyUsageJSON(raw []byte, activeModel string) (*core.MonitorQuota, error) {
	payload := extractJSONObject(raw)
	var parsed agyUsageCommandOutput
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, err
	}
	if parsed.Status != "" && !strings.EqualFold(parsed.Status, "SUCCESS") {
		msg := strings.TrimSpace(parsed.Error)
		if msg == "" {
			msg = parsed.Status
		}
		return nil, fmt.Errorf("status=%s (%s)", parsed.Status, msg)
	}
	if len(parsed.Command.Data.Groups) == 0 && parsed.Command.Name == "" {
		return nil, fmt.Errorf("missing command.data.groups in /usage output")
	}

	groups := make([]core.MonitorQuotaGroup, 0, len(parsed.Command.Data.Groups))
	for _, g := range parsed.Command.Data.Groups {
		buckets := make([]core.MonitorQuotaBucket, 0, len(g.Buckets))
		for _, b := range g.Buckets {
			frac := clampFraction(b.RemainingFraction)
			pct := math.Round(frac*1000) / 10
			buckets = append(buckets, core.MonitorQuotaBucket{
				ID:                b.ID,
				Name:              b.Name,
				Description:       b.Description,
				Window:            b.Window,
				RemainingFraction: frac,
				RemainingPercent:  pct,
				ResetTime:         b.ResetTime,
			})
		}
		groups = append(groups, core.MonitorQuotaGroup{
			Name:                g.Name,
			Description:         g.Description,
			IsCurrentModelGroup: matchQuotaGroupForModel(g.Name, buckets, activeModel),
			Buckets:             buckets,
		})
	}

	return &core.MonitorQuota{
		FetchedAt:   time.Now().UTC().Format(time.RFC3339),
		ActiveModel: activeModel,
		Description: parsed.Command.Data.Description,
		Groups:      groups,
	}, nil
}

func parseAgyCreditsJSON(raw []byte) (*core.MonitorQuotaCredits, error) {
	payload := extractJSONObject(raw)
	var parsed agyCreditsCommandOutput
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, err
	}
	if parsed.Status != "" && !strings.EqualFold(parsed.Status, "SUCCESS") {
		msg := strings.TrimSpace(parsed.Error)
		if msg == "" {
			msg = parsed.Status
		}
		return nil, fmt.Errorf("status=%s (%s)", parsed.Status, msg)
	}
	return &core.MonitorQuotaCredits{
		RemainingCredits: parsed.Command.Data.RemainingCredits,
		UpgradeURI:       parsed.Command.Data.UpgradeURI,
	}, nil
}

func clampFraction(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func matchQuotaGroupForModel(groupName string, buckets []core.MonitorQuotaBucket, activeModel string) bool {
	model := strings.ToLower(strings.TrimSpace(activeModel))
	if model == "" {
		return false
	}
	gLower := strings.ToLower(groupName)

	// Check Argon before generic Gemini so "gemini-4-argon*" maps to Argon group.
	isArgonModel := strings.Contains(model, "argon")
	isGeminiModel := strings.Contains(model, "gemini") && !isArgonModel
	is3pModel := strings.Contains(model, "claude") ||
		strings.Contains(model, "gpt") ||
		strings.Contains(model, "opus") ||
		strings.Contains(model, "sonnet") ||
		strings.Contains(model, "oss")

	if strings.Contains(gLower, "argon") || hasBucketPrefix(buckets, "gemini-argon") {
		return isArgonModel
	}
	if strings.Contains(gLower, "gemini") || hasBucketPrefix(buckets, "gemini-") {
		return isGeminiModel
	}
	if strings.Contains(gLower, "claude") || strings.Contains(gLower, "gpt") || hasBucketPrefix(buckets, "3p-") {
		return is3pModel
	}
	return false
}

func hasBucketPrefix(buckets []core.MonitorQuotaBucket, prefix string) bool {
	for _, b := range buckets {
		if strings.HasPrefix(strings.ToLower(b.ID), prefix) {
			return true
		}
	}
	return false
}
