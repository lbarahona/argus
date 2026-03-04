package scorecard

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
)

// Grade represents a reliability grade.
type Grade string

const (
	GradeA Grade = "A"
	GradeB Grade = "B"
	GradeC Grade = "C"
	GradeD Grade = "D"
	GradeF Grade = "F"
)

// ServiceScore holds reliability metrics and grade for a single service.
type ServiceScore struct {
	Name         string
	Grade        Grade
	Score        float64 // 0-100
	ErrorRate    float64 // percentage
	TotalCalls   int
	TotalErrors  int
	P50Latency   float64 // ms
	P99Latency   float64 // ms
	ErrorTrend   Trend   // compared to previous period
	LatencyTrend Trend
	TopErrors    []ErrorGroup
}

// Trend represents a metric direction.
type Trend int

const (
	TrendStable   Trend = 0
	TrendBetter   Trend = 1
	TrendWorse    Trend = -1
	TrendNoData   Trend = 2
)

// ErrorGroup groups similar errors with count.
type ErrorGroup struct {
	Message string
	Count   int
}

// Scorecard holds the full reliability scorecard.
type Scorecard struct {
	GeneratedAt  time.Time
	Duration     int // minutes
	Instance     string
	Services     []ServiceScore
	OverallGrade Grade
	OverallScore float64
	AISummary    string
}

// Options configures scorecard generation.
type Options struct {
	Duration     int
	Service      string // filter to single service
	WithAI       bool
	Format       string // "terminal" or "markdown"
	AnthropicKey string
}

// Generate creates a reliability scorecard from Signoz data.
func Generate(ctx context.Context, client signoz.SignozQuerier, instKey string, opts Options) (*Scorecard, error) {
	sc := &Scorecard{
		GeneratedAt: time.Now(),
		Duration:    opts.Duration,
		Instance:    instKey,
	}

	// Get current services
	services, err := client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	if len(services) == 0 {
		return sc, nil
	}

	// Filter if requested
	if opts.Service != "" {
		filtered := make([]types.Service, 0)
		for _, s := range services {
			if strings.EqualFold(s.Name, opts.Service) {
				filtered = append(filtered, s)
			}
		}
		services = filtered
	}

	// Get error logs for pattern detection
	errorLogs := make(map[string][]types.LogEntry)
	for _, svc := range services {
		result, err := client.QueryLogs(ctx, svc.Name, opts.Duration, 100, "ERROR")
		if err == nil && result != nil {
			errorLogs[svc.Name] = result.Logs
		}
	}

	// Get traces for latency data
	traceData := make(map[string][]types.TraceEntry)
	for _, svc := range services {
		result, err := client.QueryTraces(ctx, svc.Name, opts.Duration, 200)
		if err == nil && result != nil {
			traceData[svc.Name] = result.Traces
		}
	}

	// Get previous period data for trends
	prevServices, _ := client.ListServices(ctx)
	prevMap := make(map[string]types.Service)
	for _, s := range prevServices {
		prevMap[s.Name] = s
	}

	// Score each service
	for _, svc := range services {
		score := scoreService(svc, errorLogs[svc.Name], traceData[svc.Name], prevMap)
		sc.Services = append(sc.Services, score)
	}

	// Sort by score ascending (worst first)
	sort.Slice(sc.Services, func(i, j int) bool {
		return sc.Services[i].Score < sc.Services[j].Score
	})

	// Overall score = weighted average by call volume
	sc.OverallScore, sc.OverallGrade = computeOverall(sc.Services)

	// AI summary
	if opts.WithAI && opts.AnthropicKey != "" {
		summary, err := generateAISummary(ctx, sc, opts.AnthropicKey)
		if err == nil {
			sc.AISummary = summary
		}
	}

	return sc, nil
}

func scoreService(svc types.Service, logs []types.LogEntry, traces []types.TraceEntry, prev map[string]types.Service) ServiceScore {
	ss := ServiceScore{
		Name:        svc.Name,
		TotalCalls:  svc.NumCalls,
		TotalErrors: svc.NumErrors,
	}

	// Error rate
	if svc.NumCalls > 0 {
		ss.ErrorRate = float64(svc.NumErrors) / float64(svc.NumCalls) * 100
	}

	// Latency from traces
	if len(traces) > 0 {
		durations := make([]float64, len(traces))
		for i, t := range traces {
			durations[i] = t.DurationMs()
		}
		sort.Float64s(durations)
		ss.P50Latency = percentile(durations, 50)
		ss.P99Latency = percentile(durations, 99)
	}

	// Error trend
	if prevSvc, ok := prev[svc.Name]; ok && prevSvc.NumCalls > 0 {
		prevRate := float64(prevSvc.NumErrors) / float64(prevSvc.NumCalls) * 100
		if ss.ErrorRate < prevRate*0.8 {
			ss.ErrorTrend = TrendBetter
		} else if ss.ErrorRate > prevRate*1.2 {
			ss.ErrorTrend = TrendWorse
		} else {
			ss.ErrorTrend = TrendStable
		}
	} else {
		ss.ErrorTrend = TrendNoData
	}

	// Top errors
	ss.TopErrors = groupErrors(logs, 3)

	// Compute score (0-100)
	ss.Score = computeScore(ss)
	ss.Grade = scoreToGrade(ss.Score)

	return ss
}

func computeScore(ss ServiceScore) float64 {
	score := 100.0

	// Error rate penalty (0-50 points)
	// 0% = 0 penalty, 1% = -10, 5% = -30, 10%+ = -50
	errPenalty := math.Min(ss.ErrorRate*6, 50)
	score -= errPenalty

	// Latency penalty (0-30 points)
	// P99 > 500ms starts penalizing, >5s = max penalty
	if ss.P99Latency > 500 {
		latPenalty := math.Min((ss.P99Latency-500)/150, 30)
		score -= latPenalty
	}

	// Low traffic bonus/penalty
	if ss.TotalCalls == 0 {
		score = 50 // no data = neutral
	}

	// Trend penalty
	if ss.ErrorTrend == TrendWorse {
		score -= 10
	} else if ss.ErrorTrend == TrendBetter {
		score += 5
	}

	return math.Max(0, math.Min(100, score))
}

func scoreToGrade(score float64) Grade {
	switch {
	case score >= 90:
		return GradeA
	case score >= 75:
		return GradeB
	case score >= 60:
		return GradeC
	case score >= 40:
		return GradeD
	default:
		return GradeF
	}
}

func computeOverall(services []ServiceScore) (float64, Grade) {
	if len(services) == 0 {
		return 0, GradeF
	}
	totalCalls := 0
	weightedSum := 0.0
	for _, s := range services {
		weight := s.TotalCalls
		if weight == 0 {
			weight = 1
		}
		totalCalls += weight
		weightedSum += s.Score * float64(weight)
	}
	avg := weightedSum / float64(totalCalls)
	return avg, scoreToGrade(avg)
}

func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(p)/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func groupErrors(logs []types.LogEntry, limit int) []ErrorGroup {
	counts := make(map[string]int)
	for _, l := range logs {
		body := l.Body
		if len(body) > 100 {
			body = body[:100]
		}
		counts[body]++
	}

	groups := make([]ErrorGroup, 0, len(counts))
	for msg, count := range counts {
		groups = append(groups, ErrorGroup{Message: msg, Count: count})
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Count > groups[j].Count
	})

	if len(groups) > limit {
		groups = groups[:limit]
	}
	return groups
}

func generateAISummary(ctx context.Context, sc *Scorecard, key string) (string, error) {
	prompt := fmt.Sprintf(`Analyze this service reliability scorecard and provide a brief (3-5 sentence) executive summary with actionable recommendations.

Instance: %s
Overall: %s (%.1f/100)
Duration: %d minutes

Services:
`, sc.Instance, sc.OverallGrade, sc.OverallScore, sc.Duration)

	for _, s := range sc.Services {
		prompt += fmt.Sprintf("- %s: %s (%.1f) | Errors: %.2f%% (%d/%d) | P50: %.1fms P99: %.1fms\n",
			s.Name, s.Grade, s.Score, s.ErrorRate, s.TotalErrors, s.TotalCalls, s.P50Latency, s.P99Latency)
		for _, e := range s.TopErrors {
			prompt += fmt.Sprintf("  Error: %s (x%d)\n", e.Message, e.Count)
		}
	}

	analyzer := ai.New(key)
	return analyzer.AnalyzeSync(prompt)
}

// RenderTerminal writes the scorecard to a terminal-formatted output.
func RenderTerminal(w io.Writer, sc *Scorecard) {
	fmt.Fprintf(w, "\n%s\n", output.TitleStyle.Render(fmt.Sprintf("  Reliability Scorecard — %s  ", sc.Instance)))
	fmt.Fprintf(w, "  Generated: %s | Window: %d minutes\n", sc.GeneratedAt.Format("2006-01-02 15:04"), sc.Duration)
	fmt.Fprintf(w, "  Overall: %s %s (%.1f/100)\n\n", gradeEmoji(sc.OverallGrade), sc.OverallGrade, sc.OverallScore)

	if len(sc.Services) == 0 {
		fmt.Fprintln(w, "  No services found.")
		return
	}

	// Table header
	fmt.Fprintf(w, "  %-30s %5s %6s %8s %8s %10s %10s %s\n",
		"SERVICE", "GRADE", "SCORE", "ERR%", "CALLS", "P50", "P99", "TREND")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 95))

	for _, s := range sc.Services {
		trend := trendSymbol(s.ErrorTrend)
		fmt.Fprintf(w, "  %-30s %s %-3s %6.1f %7.2f%% %8d %8.1fms %8.1fms %s\n",
			truncate(s.Name, 30),
			gradeEmoji(s.Grade),
			s.Grade,
			s.Score,
			s.ErrorRate,
			s.TotalCalls,
			s.P50Latency,
			s.P99Latency,
			trend,
		)

		// Show top errors for C/D/F grades
		if s.Grade >= GradeC && len(s.TopErrors) > 0 {
			for _, e := range s.TopErrors {
				fmt.Fprintf(w, "    └─ %s (x%d)\n", truncate(e.Message, 70), e.Count)
			}
		}
	}

	if sc.AISummary != "" {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s\n", output.TitleStyle.Render("  AI Analysis  "))
		fmt.Fprintf(w, "  %s\n", sc.AISummary)
	}
}

// RenderMarkdown writes the scorecard in markdown format.
func RenderMarkdown(w io.Writer, sc *Scorecard) {
	fmt.Fprintf(w, "# Reliability Scorecard — %s\n\n", sc.Instance)
	fmt.Fprintf(w, "**Generated:** %s | **Window:** %d minutes\n\n", sc.GeneratedAt.Format("2006-01-02 15:04"), sc.Duration)
	fmt.Fprintf(w, "## Overall: %s %s (%.1f/100)\n\n", gradeEmoji(sc.OverallGrade), sc.OverallGrade, sc.OverallScore)

	if len(sc.Services) == 0 {
		fmt.Fprintln(w, "No services found.")
		return
	}

	fmt.Fprintln(w, "| Service | Grade | Score | Error Rate | Calls | P50 | P99 | Trend |")
	fmt.Fprintln(w, "|---------|-------|-------|------------|-------|-----|-----|-------|")

	for _, s := range sc.Services {
		fmt.Fprintf(w, "| %s | %s %s | %.1f | %.2f%% | %d | %.1fms | %.1fms | %s |\n",
			s.Name, gradeEmoji(s.Grade), s.Grade, s.Score, s.ErrorRate, s.TotalCalls, s.P50Latency, s.P99Latency, trendSymbol(s.ErrorTrend))
	}

	// Detail sections for underperformers
	for _, s := range sc.Services {
		if s.Grade >= GradeC && len(s.TopErrors) > 0 {
			fmt.Fprintf(w, "\n### %s %s — %s\n\n", gradeEmoji(s.Grade), s.Name, s.Grade)
			for _, e := range s.TopErrors {
				fmt.Fprintf(w, "- `%s` (x%d)\n", e.Message, e.Count)
			}
		}
	}

	if sc.AISummary != "" {
		fmt.Fprintf(w, "\n## AI Analysis\n\n%s\n", sc.AISummary)
	}
}

func gradeEmoji(g Grade) string {
	switch g {
	case GradeA:
		return "🟢"
	case GradeB:
		return "🔵"
	case GradeC:
		return "🟡"
	case GradeD:
		return "🟠"
	case GradeF:
		return "🔴"
	default:
		return "⚪"
	}
}

func trendSymbol(t Trend) string {
	switch t {
	case TrendBetter:
		return "📈 improving"
	case TrendWorse:
		return "📉 degrading"
	case TrendStable:
		return "➡️  stable"
	default:
		return "—"
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
