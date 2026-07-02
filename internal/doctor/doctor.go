package doctor

import (
	"context"
	jsonPkg "encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/config"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
)

// CheckResult represents the result of a single diagnostic check.
type CheckResult struct {
	Name    string
	Status  Status
	Message string
	Detail  string
	Latency time.Duration
}

// Status represents the health status of a check.
type Status int

const (
	StatusPass Status = iota
	StatusWarn
	StatusFail
	StatusSkip
)

// String returns the display string for a status.
func (s Status) String() string {
	switch s {
	case StatusPass:
		return "PASS"
	case StatusWarn:
		return "WARN"
	case StatusFail:
		return "FAIL"
	case StatusSkip:
		return "SKIP"
	default:
		return "UNKNOWN"
	}
}

// Symbol returns the emoji symbol for a status.
func (s Status) Symbol() string {
	switch s {
	case StatusPass:
		return "✅"
	case StatusWarn:
		return "⚠️"
	case StatusFail:
		return "❌"
	case StatusSkip:
		return "⏭️"
	default:
		return "❓"
	}
}

// Report holds all diagnostic results.
type Report struct {
	Checks    []CheckResult
	StartTime time.Time
	Duration  time.Duration
	Version   string
	OS        string
	Arch      string
}

// PassCount returns the number of passing checks.
func (r *Report) PassCount() int {
	count := 0
	for _, c := range r.Checks {
		if c.Status == StatusPass {
			count++
		}
	}
	return count
}

// FailCount returns the number of failing checks.
func (r *Report) FailCount() int {
	count := 0
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			count++
		}
	}
	return count
}

// WarnCount returns the number of warning checks.
func (r *Report) WarnCount() int {
	count := 0
	for _, c := range r.Checks {
		if c.Status == StatusWarn {
			count++
		}
	}
	return count
}

// Run executes all diagnostic checks and returns a report.
func Run(ctx context.Context, version string, verbose bool) *Report {
	start := time.Now()
	report := &Report{
		StartTime: start,
		Version:   version,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}

	// System checks
	report.Checks = append(report.Checks, checkConfigFile())
	report.Checks = append(report.Checks, checkConfigParseable())

	// Instance checks
	cfg, err := config.Load()
	if err == nil && len(cfg.Instances) > 0 {
		for key, inst := range cfg.Instances {
			report.Checks = append(report.Checks, checkInstanceURL(key, inst))
			report.Checks = append(report.Checks, checkInstanceDNS(ctx, key, inst))
			report.Checks = append(report.Checks, checkInstanceHealth(ctx, key, inst))
			report.Checks = append(report.Checks, checkInstanceServices(ctx, key, inst))
		}
	} else if err != nil {
		report.Checks = append(report.Checks, CheckResult{
			Name:    "Instance connectivity",
			Status:  StatusSkip,
			Message: "No valid config to test instances",
		})
	} else {
		report.Checks = append(report.Checks, CheckResult{
			Name:    "Instance connectivity",
			Status:  StatusWarn,
			Message: "No instances configured — run 'argus config add'",
		})
	}

	// AI checks
	report.Checks = append(report.Checks, checkAnthropicKey(cfg))
	report.Checks = append(report.Checks, checkAnthropicConnectivity(ctx, cfg))

	// Network checks
	report.Checks = append(report.Checks, checkDNSResolution(ctx))
	report.Checks = append(report.Checks, checkInternetConnectivity(ctx))

	report.Duration = time.Since(start)
	return report
}

func checkConfigFile() CheckResult {
	path := config.Path()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return CheckResult{
			Name:    "Config file exists",
			Status:  StatusFail,
			Message: "Config file not found",
			Detail:  fmt.Sprintf("Expected at: %s\nRun 'argus config add' to create one", path),
		}
	}
	if err != nil {
		return CheckResult{
			Name:    "Config file exists",
			Status:  StatusFail,
			Message: fmt.Sprintf("Cannot access config: %v", err),
		}
	}

	// Check permissions
	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		return CheckResult{
			Name:    "Config file exists",
			Status:  StatusWarn,
			Message: fmt.Sprintf("Config file found but permissions too open (%s)", perm),
			Detail:  fmt.Sprintf("Path: %s\nRecommended: chmod 600 %s", path, path),
		}
	}

	return CheckResult{
		Name:    "Config file exists",
		Status:  StatusPass,
		Message: fmt.Sprintf("Found at %s", path),
	}
}

func checkConfigParseable() CheckResult {
	cfg, err := config.Load()
	if err != nil {
		return CheckResult{
			Name:    "Config file parseable",
			Status:  StatusFail,
			Message: fmt.Sprintf("Failed to parse config: %v", err),
		}
	}

	instanceCount := len(cfg.Instances)
	defaultSet := cfg.DefaultInstance != ""
	details := fmt.Sprintf("Instances: %d", instanceCount)
	if defaultSet {
		details += fmt.Sprintf(", Default: %s", cfg.DefaultInstance)
	}

	if instanceCount == 0 {
		return CheckResult{
			Name:    "Config file parseable",
			Status:  StatusWarn,
			Message: "Config parsed but no instances defined",
			Detail:  details,
		}
	}

	// Verify default instance exists
	if defaultSet {
		if _, ok := cfg.Instances[cfg.DefaultInstance]; !ok {
			return CheckResult{
				Name:    "Config file parseable",
				Status:  StatusWarn,
				Message: fmt.Sprintf("Default instance '%s' not found in instances", cfg.DefaultInstance),
				Detail:  details,
			}
		}
	}

	return CheckResult{
		Name:    "Config file parseable",
		Status:  StatusPass,
		Message: details,
	}
}

func checkInstanceURL(key string, inst types.Instance) CheckResult {
	name := fmt.Sprintf("Instance '%s' URL valid", key)
	if inst.URL == "" {
		return CheckResult{
			Name:    name,
			Status:  StatusFail,
			Message: "URL is empty",
		}
	}

	if !strings.HasPrefix(inst.URL, "http://") && !strings.HasPrefix(inst.URL, "https://") {
		return CheckResult{
			Name:    name,
			Status:  StatusFail,
			Message: "URL must start with http:// or https://",
			Detail:  fmt.Sprintf("Current: %s", inst.URL),
		}
	}

	if strings.HasPrefix(inst.URL, "http://") && !strings.Contains(inst.URL, "localhost") && !strings.Contains(inst.URL, "127.0.0.1") {
		return CheckResult{
			Name:    name,
			Status:  StatusWarn,
			Message: "Using HTTP (not HTTPS) for non-local instance",
			Detail:  fmt.Sprintf("URL: %s — Consider using HTTPS for security", inst.URL),
		}
	}

	return CheckResult{
		Name:    name,
		Status:  StatusPass,
		Message: inst.URL,
	}
}

func checkInstanceDNS(ctx context.Context, key string, inst types.Instance) CheckResult {
	name := fmt.Sprintf("Instance '%s' DNS resolves", key)
	if inst.URL == "" {
		return CheckResult{Name: name, Status: StatusSkip, Message: "No URL"}
	}

	// Extract hostname from URL
	host := inst.URL
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}
	if idx := strings.Index(host, "/"); idx != -1 {
		host = host[:idx]
	}

	start := time.Now()
	resolver := &net.Resolver{}
	dnsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	addrs, err := resolver.LookupHost(dnsCtx, host)
	latency := time.Since(start)

	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  StatusFail,
			Message: fmt.Sprintf("DNS lookup failed: %v", err),
			Latency: latency,
		}
	}

	return CheckResult{
		Name:    name,
		Status:  StatusPass,
		Message: fmt.Sprintf("Resolved to %s", strings.Join(addrs, ", ")),
		Latency: latency,
	}
}

func checkInstanceHealth(ctx context.Context, key string, inst types.Instance) CheckResult {
	name := fmt.Sprintf("Instance '%s' health check", key)
	if inst.URL == "" {
		return CheckResult{Name: name, Status: StatusSkip, Message: "No URL"}
	}

	client := signoz.New(inst)
	healthy, latency, err := client.Health(ctx)

	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  StatusFail,
			Message: fmt.Sprintf("Health check failed: %v", err),
			Latency: latency,
		}
	}

	if !healthy {
		return CheckResult{
			Name:    name,
			Status:  StatusFail,
			Message: "Instance reported unhealthy",
			Latency: latency,
		}
	}

	statusMsg := fmt.Sprintf("Healthy (API %s)", inst.GetAPIVersion())
	if latency > 2*time.Second {
		return CheckResult{
			Name:    name,
			Status:  StatusWarn,
			Message: fmt.Sprintf("%s — slow response", statusMsg),
			Latency: latency,
		}
	}

	return CheckResult{
		Name:    name,
		Status:  StatusPass,
		Message: statusMsg,
		Latency: latency,
	}
}

func checkInstanceServices(ctx context.Context, key string, inst types.Instance) CheckResult {
	name := fmt.Sprintf("Instance '%s' service discovery", key)
	if inst.URL == "" {
		return CheckResult{Name: name, Status: StatusSkip, Message: "No URL"}
	}

	client := signoz.New(inst)
	start := time.Now()
	services, err := client.ListServices(ctx)
	latency := time.Since(start)

	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  StatusWarn,
			Message: fmt.Sprintf("Could not list services: %v", err),
			Detail:  "This might indicate missing data or permissions",
			Latency: latency,
		}
	}

	if len(services) == 0 {
		return CheckResult{
			Name:    name,
			Status:  StatusWarn,
			Message: "No services found — is data being ingested?",
			Latency: latency,
		}
	}

	svcNames := make([]string, 0, min(5, len(services)))
	for i, s := range services {
		if i >= 5 {
			break
		}
		svcNames = append(svcNames, s.Name)
	}
	detail := strings.Join(svcNames, ", ")
	if len(services) > 5 {
		detail += fmt.Sprintf(" (+%d more)", len(services)-5)
	}

	return CheckResult{
		Name:    name,
		Status:  StatusPass,
		Message: fmt.Sprintf("%d services discovered", len(services)),
		Detail:  detail,
		Latency: latency,
	}
}

func checkAnthropicKey(cfg *types.Config) CheckResult {
	name := "AI provider configured"

	if cfg == nil {
		return CheckResult{
			Name:    name,
			Status:  StatusWarn,
			Message: "No config loaded — AI features disabled",
			Detail:  "Run 'argus config init' to set up an AI provider",
		}
	}

	aiCfg := cfg.GetAIConfig()
	provider := aiCfg.Provider

	switch provider {
	case "anthropic", "":
		key := aiCfg.AnthropicKey
		if key == "" {
			key = os.Getenv("ANTHROPIC_API_KEY")
		}
		if key == "" {
			return CheckResult{
				Name:    name,
				Status:  StatusWarn,
				Message: "Not configured — AI features disabled",
				Detail:  "Set ANTHROPIC_API_KEY env var or configure ai.anthropic_key in config",
			}
		}
		if !strings.HasPrefix(key, "sk-ant-") {
			return CheckResult{
				Name:    name,
				Status:  StatusWarn,
				Message: "Anthropic key found but format looks unusual",
			}
		}
		return CheckResult{
			Name:    name,
			Status:  StatusPass,
			Message: fmt.Sprintf("Anthropic (sk-ant-...%s)", keySuffix(key)),
		}

	case "openai":
		key := aiCfg.OpenAIKey
		if key == "" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		if key == "" {
			return CheckResult{
				Name:    name,
				Status:  StatusWarn,
				Message: "OpenAI selected but no key configured",
				Detail:  "Set OPENAI_API_KEY env var or configure ai.openai_key in config",
			}
		}
		return CheckResult{
			Name:    name,
			Status:  StatusPass,
			Message: fmt.Sprintf("OpenAI (key: ...%s)", keySuffix(key)),
		}

	case "bedrock":
		endpoint := aiCfg.Bedrock.Endpoint
		token := aiCfg.Bedrock.Token
		if token == "" {
			token = os.Getenv("AWS_BEARER_TOKEN_BEDROCK")
		}
		if endpoint == "" || token == "" {
			return CheckResult{
				Name:    name,
				Status:  StatusWarn,
				Message: "Bedrock selected but endpoint or token missing",
				Detail:  "Configure ai.bedrock.endpoint and ai.bedrock.token (or AWS_BEARER_TOKEN_BEDROCK env var)",
			}
		}
		return CheckResult{
			Name:    name,
			Status:  StatusPass,
			Message: fmt.Sprintf("Bedrock (%s)", endpoint),
		}

	default:
		return CheckResult{
			Name:    name,
			Status:  StatusFail,
			Message: fmt.Sprintf("Unknown provider: %s", provider),
			Detail:  "Supported providers: anthropic, openai, bedrock",
		}
	}
}

func checkAnthropicConnectivity(ctx context.Context, cfg *types.Config) CheckResult {
	name := "AI API reachable"

	if cfg == nil {
		return CheckResult{
			Name:   name,
			Status: StatusSkip,
			Message: "No config — skipping connectivity test",
		}
	}

	aiCfg := cfg.GetAIConfig()
	provider := aiCfg.Provider

	// For non-Anthropic providers, do a basic connectivity check to their endpoint
	switch provider {
	case "openai":
		key := aiCfg.OpenAIKey
		if key == "" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		if key == "" {
			return CheckResult{Name: name, Status: StatusSkip, Message: "No OpenAI key — skipping"}
		}
		start := time.Now()
		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://api.openai.com/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		latency := time.Since(start)
		if err != nil {
			return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("OpenAI unreachable: %v", err)}
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
			return CheckResult{Name: name, Status: StatusPass, Message: fmt.Sprintf("OpenAI reachable (%dms)", latency.Milliseconds())}
		}
		return CheckResult{Name: name, Status: StatusWarn, Message: fmt.Sprintf("OpenAI responded with %d", resp.StatusCode)}

	case "bedrock":
		endpoint := aiCfg.Bedrock.Endpoint
		if endpoint == "" {
			return CheckResult{Name: name, Status: StatusSkip, Message: "No Bedrock endpoint — skipping"}
		}
		start := time.Now()
		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
		resp, err := http.DefaultClient.Do(req)
		latency := time.Since(start)
		if err != nil {
			return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("Bedrock endpoint unreachable: %v", err)}
		}
		defer resp.Body.Close()
		return CheckResult{Name: name, Status: StatusPass, Message: fmt.Sprintf("Bedrock endpoint reachable (%dms)", latency.Milliseconds())}
	}

	// Default: Anthropic
	key := aiCfg.AnthropicKey
	if key == "" {
		key = os.Getenv("ANTHROPIC_API_KEY")
	}
	if key == "" {
		return CheckResult{
			Name:   name,
			Status: StatusSkip,
			Message: "No API key — skipping connectivity test",
		}
	}

	start := time.Now()
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	latency := time.Since(start)

	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  StatusFail,
			Message: fmt.Sprintf("Connection failed: %v", err),
			Latency: latency,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return CheckResult{
			Name:    name,
			Status:  StatusFail,
			Message: "Authentication failed — invalid API key",
			Latency: latency,
		}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return CheckResult{
			Name:    name,
			Status:  StatusPass,
			Message: "Connected successfully",
			Latency: latency,
		}
	}

	return CheckResult{
		Name:    name,
		Status:  StatusWarn,
		Message: fmt.Sprintf("Unexpected status %d", resp.StatusCode),
		Latency: latency,
	}
}

func checkDNSResolution(ctx context.Context) CheckResult {
	start := time.Now()
	resolver := &net.Resolver{}
	dnsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := resolver.LookupHost(dnsCtx, "api.anthropic.com")
	latency := time.Since(start)

	if err != nil {
		return CheckResult{
			Name:    "DNS resolution",
			Status:  StatusFail,
			Message: fmt.Sprintf("Cannot resolve api.anthropic.com: %v", err),
			Latency: latency,
		}
	}

	return CheckResult{
		Name:    "DNS resolution",
		Status:  StatusPass,
		Message: "Working",
		Latency: latency,
	}
}

func checkInternetConnectivity(ctx context.Context) CheckResult {
	start := time.Now()
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(reqCtx, http.MethodHead, "https://www.google.com", nil)
	resp, err := http.DefaultClient.Do(req)
	latency := time.Since(start)

	if err != nil {
		return CheckResult{
			Name:    "Internet connectivity",
			Status:  StatusFail,
			Message: fmt.Sprintf("Cannot reach internet: %v", err),
			Latency: latency,
		}
	}
	defer resp.Body.Close()

	return CheckResult{
		Name:    "Internet connectivity",
		Status:  StatusPass,
		Message: "Connected",
		Latency: latency,
	}
}

// FormatTerminal renders the report for terminal output.
func FormatTerminal(r *Report, verbose bool) string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString("  🩺 Argus Doctor\n")
	b.WriteString(fmt.Sprintf("  Version: %s | OS: %s/%s\n", r.Version, r.OS, r.Arch))
	b.WriteString("  ──────────────────────────────────────────────\n\n")

	for _, c := range r.Checks {
		latencyStr := ""
		if c.Latency > 0 {
			latencyStr = fmt.Sprintf(" (%s)", c.Latency.Round(time.Millisecond))
		}
		b.WriteString(fmt.Sprintf("  %s  %-40s %s%s\n", c.Status.Symbol(), c.Name, c.Message, latencyStr))

		if verbose && c.Detail != "" {
			for _, line := range strings.Split(c.Detail, "\n") {
				b.WriteString(fmt.Sprintf("      %s\n", line))
			}
		}
	}

	b.WriteString("\n  ──────────────────────────────────────────────\n")
	b.WriteString(fmt.Sprintf("  Summary: %d passed, %d warnings, %d failed (%s)\n\n",
		r.PassCount(), r.WarnCount(), r.FailCount(), r.Duration.Round(time.Millisecond)))

	if r.FailCount() > 0 {
		b.WriteString("  💡 Suggested fixes:\n")
		for _, c := range r.Checks {
			if c.Status == StatusFail {
				b.WriteString(fmt.Sprintf("     • %s: %s\n", c.Name, c.Message))
				if c.Detail != "" {
					for _, line := range strings.Split(c.Detail, "\n") {
						b.WriteString(fmt.Sprintf("       %s\n", line))
					}
				}
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

// FormatJSON renders the report as JSON.
func FormatJSON(r *Report) ([]byte, error) {
	type jsonCheck struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Message string `json:"message"`
		Detail  string `json:"detail,omitempty"`
		Latency string `json:"latency,omitempty"`
	}
	type jsonReport struct {
		Version  string      `json:"version"`
		OS       string      `json:"os"`
		Arch     string      `json:"arch"`
		Duration string      `json:"duration"`
		Pass     int         `json:"pass"`
		Warn     int         `json:"warn"`
		Fail     int         `json:"fail"`
		Checks   []jsonCheck `json:"checks"`
	}

	jr := jsonReport{
		Version:  r.Version,
		OS:       r.OS,
		Arch:     r.Arch,
		Duration: r.Duration.Round(time.Millisecond).String(),
		Pass:     r.PassCount(),
		Warn:     r.WarnCount(),
		Fail:     r.FailCount(),
	}

	for _, c := range r.Checks {
		jc := jsonCheck{
			Name:    c.Name,
			Status:  c.Status.String(),
			Message: c.Message,
			Detail:  c.Detail,
		}
		if c.Latency > 0 {
			jc.Latency = c.Latency.Round(time.Millisecond).String()
		}
		jr.Checks = append(jr.Checks, jc)
	}

	return jsonPkg.MarshalIndent(jr, "", "  ")
}

// FormatMarkdown renders the report as markdown.
func FormatMarkdown(r *Report) string {
	var b strings.Builder

	b.WriteString("# 🩺 Argus Doctor Report\n\n")
	b.WriteString(fmt.Sprintf("**Version:** %s | **OS:** %s/%s | **Duration:** %s\n\n",
		r.Version, r.OS, r.Arch, r.Duration.Round(time.Millisecond)))

	b.WriteString("| Status | Check | Result | Latency |\n")
	b.WriteString("|--------|-------|--------|---------|\n")

	for _, c := range r.Checks {
		latency := ""
		if c.Latency > 0 {
			latency = c.Latency.Round(time.Millisecond).String()
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
			c.Status.Symbol(), c.Name, c.Message, latency))
	}

	b.WriteString(fmt.Sprintf("\n**Summary:** %d passed, %d warnings, %d failed\n",
		r.PassCount(), r.WarnCount(), r.FailCount()))

	return b.String()
}

// keySuffix returns the last 4 characters for display, or the whole key if shorter.
func keySuffix(key string) string {
	if len(key) <= 4 {
		return key
	}
	return key[len(key)-4:]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ConfigPath returns the expected config file path (for display).
func ConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".argus", "config.yaml")
}
