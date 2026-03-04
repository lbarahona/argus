package forecast

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

// Options configures forecast generation.
type Options struct {
	Duration     int    // minutes of historical data to analyze
	Horizon      int    // minutes to forecast into the future
	Service      string // optional: filter to specific service
	Format       string // "terminal" or "markdown"
	WithAI       bool   // include AI narrative
	AnthropicKey string
}

// ServiceForecast holds prediction data for a single service.
type ServiceForecast struct {
	Name string

	// Current metrics
	CurrentErrors   int
	CurrentCalls    int
	CurrentRate     float64
	CurrentP99      float64 // ms, from traces

	// Trend data points (time-bucketed)
	ErrorBuckets []DataPoint
	CallBuckets  []DataPoint

	// Linear regression results
	ErrorTrend   Trend
	RateTrend    Trend

	// Predictions
	PredictedRate   float64 // error rate at horizon
	PredictedErrors int     // total errors at horizon
	RiskLevel       string  // "stable", "degrading", "critical"
	RiskScore       float64 // 0-100
	Warnings        []string
}

// DataPoint represents a time-bucketed metric value.
type DataPoint struct {
	Timestamp time.Time
	Value     float64
}

// Trend holds linear regression results.
type Trend struct {
	Slope     float64 // change per minute
	Intercept float64
	R2        float64 // goodness of fit (0-1)
	Direction string  // "rising", "falling", "stable"
}

// Report holds all forecast data.
type Report struct {
	GeneratedAt time.Time
	Instance    string
	Duration    int // historical window (minutes)
	Horizon     int // forecast window (minutes)
	Services    []ServiceForecast
	AISummary   string

	// Summary
	TotalServices  int
	StableCount    int
	DegradingCount int
	CriticalCount  int
}

// Generate produces a forecast report from Signoz data.
func Generate(ctx context.Context, client signoz.SignozQuerier, instance string, opts Options) (*Report, error) {
	if opts.Duration == 0 {
		opts.Duration = 120
	}
	if opts.Horizon == 0 {
		opts.Horizon = 60
	}

	services, err := client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	if opts.Service != "" {
		filtered := make([]types.Service, 0)
		for _, s := range services {
			if strings.EqualFold(s.Name, opts.Service) {
				filtered = append(filtered, s)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("service %q not found", opts.Service)
		}
		services = filtered
	}

	// Get error logs bucketed over the duration for trend analysis
	allLogs, err := client.QueryLogs(ctx, "", opts.Duration, 1000, "ERROR")
	if err != nil {
		return nil, fmt.Errorf("querying error logs: %w", err)
	}

	// Get all logs for call volume estimation
	allCallLogs, err := client.QueryLogs(ctx, "", opts.Duration, 1000, "")
	if err != nil {
		// Non-fatal, we can work without call volume trends
		allCallLogs = &types.QueryResult{}
	}

	now := time.Now()
	bucketSize := time.Duration(opts.Duration/10) * time.Minute
	if bucketSize < time.Minute {
		bucketSize = time.Minute
	}

	forecasts := make([]ServiceForecast, 0, len(services))
	for _, svc := range services {
		sf := buildServiceForecast(svc, allLogs.Logs, allCallLogs.Logs, now, opts.Duration, opts.Horizon, bucketSize)
		forecasts = append(forecasts, sf)
	}

	// Sort by risk score descending
	sort.Slice(forecasts, func(i, j int) bool {
		return forecasts[i].RiskScore > forecasts[j].RiskScore
	})

	report := &Report{
		GeneratedAt:   now,
		Instance:      instance,
		Duration:      opts.Duration,
		Horizon:       opts.Horizon,
		Services:      forecasts,
		TotalServices: len(forecasts),
	}

	for _, f := range forecasts {
		switch f.RiskLevel {
		case "critical":
			report.CriticalCount++
		case "degrading":
			report.DegradingCount++
		default:
			report.StableCount++
		}
	}

	if opts.WithAI && opts.AnthropicKey != "" {
		summary, err := generateAISummary(report, opts.AnthropicKey)
		if err == nil {
			report.AISummary = summary
		}
	}

	return report, nil
}

func buildServiceForecast(svc types.Service, errorLogs, allLogs []types.LogEntry, now time.Time, duration, horizon int, bucketSize time.Duration) ServiceForecast {
	sf := ServiceForecast{
		Name:          svc.Name,
		CurrentErrors: svc.NumErrors,
		CurrentCalls:  svc.NumCalls,
		CurrentRate:   svc.ErrorRate,
	}

	// Bucket error logs by time for this service
	start := now.Add(-time.Duration(duration) * time.Minute)
	sf.ErrorBuckets = bucketLogs(errorLogs, svc.Name, start, now, bucketSize)
	sf.CallBuckets = bucketLogs(allLogs, svc.Name, start, now, bucketSize)

	// Compute error trend via linear regression
	if len(sf.ErrorBuckets) >= 3 {
		sf.ErrorTrend = linearRegression(sf.ErrorBuckets, start)
	}

	// Compute error rate trend
	if len(sf.ErrorBuckets) >= 3 && len(sf.CallBuckets) >= 3 {
		rateBuckets := computeRateBuckets(sf.ErrorBuckets, sf.CallBuckets)
		if len(rateBuckets) >= 3 {
			sf.RateTrend = linearRegression(rateBuckets, start)
		}
	}

	// Predict future state
	horizonMinutes := float64(duration + horizon)
	if sf.ErrorTrend.Slope != 0 {
		predicted := sf.ErrorTrend.Intercept + sf.ErrorTrend.Slope*horizonMinutes
		if predicted < 0 {
			predicted = 0
		}
		sf.PredictedErrors = int(predicted)
	} else {
		sf.PredictedErrors = sf.CurrentErrors
	}

	if sf.RateTrend.Slope != 0 {
		predicted := sf.RateTrend.Intercept + sf.RateTrend.Slope*horizonMinutes
		if predicted < 0 {
			predicted = 0
		}
		if predicted > 100 {
			predicted = 100
		}
		sf.PredictedRate = predicted
	} else {
		sf.PredictedRate = sf.CurrentRate
	}

	// Assess risk
	sf.RiskScore, sf.RiskLevel, sf.Warnings = assessRisk(sf)

	return sf
}

func bucketLogs(logs []types.LogEntry, service string, start, end time.Time, bucketSize time.Duration) []DataPoint {
	numBuckets := int(end.Sub(start) / bucketSize)
	if numBuckets <= 0 {
		numBuckets = 1
	}

	counts := make([]float64, numBuckets)
	for _, log := range logs {
		if log.ServiceName != service {
			continue
		}
		if log.Timestamp.Before(start) || log.Timestamp.After(end) {
			continue
		}
		idx := int(log.Timestamp.Sub(start) / bucketSize)
		if idx >= numBuckets {
			idx = numBuckets - 1
		}
		counts[idx]++
	}

	points := make([]DataPoint, numBuckets)
	for i := range counts {
		points[i] = DataPoint{
			Timestamp: start.Add(time.Duration(i) * bucketSize),
			Value:     counts[i],
		}
	}
	return points
}

func computeRateBuckets(errorBuckets, callBuckets []DataPoint) []DataPoint {
	n := len(errorBuckets)
	if len(callBuckets) < n {
		n = len(callBuckets)
	}
	result := make([]DataPoint, 0, n)
	for i := 0; i < n; i++ {
		if callBuckets[i].Value > 0 {
			result = append(result, DataPoint{
				Timestamp: errorBuckets[i].Timestamp,
				Value:     (errorBuckets[i].Value / callBuckets[i].Value) * 100,
			})
		}
	}
	return result
}

// linearRegression performs ordinary least squares on data points.
// x-axis is minutes from start, y-axis is the value.
func linearRegression(points []DataPoint, start time.Time) Trend {
	n := float64(len(points))
	if n < 2 {
		return Trend{Direction: "stable"}
	}

	var sumX, sumY, sumXY, sumX2 float64
	for _, p := range points {
		x := p.Timestamp.Sub(start).Minutes()
		y := p.Value
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	denom := n*sumX2 - sumX*sumX
	if denom == 0 {
		return Trend{Intercept: sumY / n, Direction: "stable"}
	}

	slope := (n*sumXY - sumX*sumY) / denom
	intercept := (sumY - slope*sumX) / n

	// R² calculation
	meanY := sumY / n
	var ssTot, ssRes float64
	for _, p := range points {
		x := p.Timestamp.Sub(start).Minutes()
		predicted := intercept + slope*x
		ssRes += (p.Value - predicted) * (p.Value - predicted)
		ssTot += (p.Value - meanY) * (p.Value - meanY)
	}
	r2 := 0.0
	if ssTot > 0 {
		r2 = 1 - ssRes/ssTot
	}

	direction := "stable"
	if math.Abs(slope) > 0.01 {
		if slope > 0 {
			direction = "rising"
		} else {
			direction = "falling"
		}
	}

	return Trend{
		Slope:     slope,
		Intercept: intercept,
		R2:        r2,
		Direction: direction,
	}
}

func assessRisk(sf ServiceForecast) (float64, string, []string) {
	score := 0.0
	var warnings []string

	// Current error rate contribution (0-30 points)
	if sf.CurrentRate > 0 {
		score += math.Min(sf.CurrentRate*3, 30)
	}

	// Error trend contribution (0-30 points)
	if sf.ErrorTrend.Direction == "rising" && sf.ErrorTrend.R2 > 0.3 {
		// Strong upward trend with good fit
		score += math.Min(sf.ErrorTrend.Slope*10, 30)
		warnings = append(warnings, fmt.Sprintf("Error count rising (%.2f/min, R²=%.2f)", sf.ErrorTrend.Slope, sf.ErrorTrend.R2))
	}

	// Rate trend contribution (0-25 points)
	if sf.RateTrend.Direction == "rising" && sf.RateTrend.R2 > 0.3 {
		score += math.Min(sf.RateTrend.Slope*5, 25)
		warnings = append(warnings, fmt.Sprintf("Error rate trending up (%.3f%%/min)", sf.RateTrend.Slope))
	}

	// Predicted rate exceeding thresholds (0-15 points)
	if sf.PredictedRate > 10 {
		score += 15
		warnings = append(warnings, fmt.Sprintf("Predicted error rate: %.1f%%", sf.PredictedRate))
	} else if sf.PredictedRate > 5 {
		score += 8
		warnings = append(warnings, fmt.Sprintf("Predicted error rate: %.1f%%", sf.PredictedRate))
	}

	if score > 100 {
		score = 100
	}

	level := "stable"
	if score >= 60 {
		level = "critical"
	} else if score >= 30 {
		level = "degrading"
	}

	return score, level, warnings
}

func generateAISummary(r *Report, apiKey string) (string, error) {
	var sb strings.Builder
	sb.WriteString("You are an SRE analyst. Analyze this service forecast report and provide:\n")
	sb.WriteString("1. A brief executive summary (2-3 sentences)\n")
	sb.WriteString("2. Top risks and recommended actions\n")
	sb.WriteString("3. Services that need immediate attention\n\n")

	sb.WriteString(fmt.Sprintf("Historical window: %d minutes | Forecast horizon: %d minutes\n", r.Duration, r.Horizon))
	sb.WriteString(fmt.Sprintf("Services: %d total (%d stable, %d degrading, %d critical)\n\n", r.TotalServices, r.StableCount, r.DegradingCount, r.CriticalCount))

	for _, f := range r.Services {
		if f.RiskLevel == "stable" && f.RiskScore < 10 {
			continue
		}
		sb.WriteString(fmt.Sprintf("Service: %s [%s, risk=%.0f]\n", f.Name, f.RiskLevel, f.RiskScore))
		sb.WriteString(fmt.Sprintf("  Current: %d errors, %.1f%% rate, %d calls\n", f.CurrentErrors, f.CurrentRate, f.CurrentCalls))
		sb.WriteString(fmt.Sprintf("  Error trend: %s (slope=%.3f/min, R²=%.2f)\n", f.ErrorTrend.Direction, f.ErrorTrend.Slope, f.ErrorTrend.R2))
		sb.WriteString(fmt.Sprintf("  Predicted: %d errors, %.1f%% rate\n", f.PredictedErrors, f.PredictedRate))
		for _, w := range f.Warnings {
			sb.WriteString(fmt.Sprintf("  ⚠️  %s\n", w))
		}
		sb.WriteString("\n")
	}

	analyzer := ai.New(apiKey)
	return analyzer.AnalyzeSync(sb.String())
}

// RenderTerminal outputs the forecast to the terminal.
func (r *Report) RenderTerminal(w io.Writer) {
	fmt.Fprintf(w, "\n%s\n\n", output.TitleStyle.Render("🔮 Service Forecast"))
	fmt.Fprintf(w, "  Instance: %s\n", output.AccentStyle.Render(r.Instance))
	fmt.Fprintf(w, "  Historical: %s | Forecast: %s\n",
		output.AccentStyle.Render(fmt.Sprintf("%dm", r.Duration)),
		output.AccentStyle.Render(fmt.Sprintf("%dm", r.Horizon)))
	fmt.Fprintf(w, "  Generated: %s\n\n", r.GeneratedAt.Format("2006-01-02 15:04:05"))

	// Summary bar
	fmt.Fprintf(w, "  Services: %d total  ", r.TotalServices)
	fmt.Fprintf(w, "✅ %d stable  ", r.StableCount)
	if r.DegradingCount > 0 {
		fmt.Fprintf(w, "⚠️  %d degrading  ", r.DegradingCount)
	}
	if r.CriticalCount > 0 {
		fmt.Fprintf(w, "🔴 %d critical  ", r.CriticalCount)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)

	// Table header
	fmt.Fprintf(w, "  %-30s %6s %8s %8s %8s %8s %5s\n",
		"SERVICE", "RISK", "ERR NOW", "ERR/MIN", "RATE", "PRED %", "FIT")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 85))

	for _, f := range r.Services {
		riskIcon := "✅"
		switch f.RiskLevel {
		case "degrading":
			riskIcon = "⚠️ "
		case "critical":
			riskIcon = "🔴"
		}

		trendArrow := "→"
		switch f.ErrorTrend.Direction {
		case "rising":
			trendArrow = "↑"
		case "falling":
			trendArrow = "↓"
		}

		name := f.Name
		if len(name) > 28 {
			name = name[:28] + "…"
		}

		fmt.Fprintf(w, "  %-30s %s%3.0f %7d %7s %7.1f%% %7.1f%% %5.2f\n",
			name,
			riskIcon,
			f.RiskScore,
			f.CurrentErrors,
			fmt.Sprintf("%.2f%s", math.Abs(f.ErrorTrend.Slope), trendArrow),
			f.CurrentRate,
			f.PredictedRate,
			f.ErrorTrend.R2)

		// Show warnings for non-stable services
		for _, warn := range f.Warnings {
			fmt.Fprintf(w, "  %32s %s\n", "", output.WarningStyle.Render("⚠ "+warn))
		}
	}
	fmt.Fprintln(w)

	if r.AISummary != "" {
		fmt.Fprintf(w, "  %s\n\n", output.TitleStyle.Render("🤖 AI Analysis"))
		for _, line := range strings.Split(r.AISummary, "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
		fmt.Fprintln(w)
	}
}

// RenderMarkdown outputs the forecast in markdown format.
func (r *Report) RenderMarkdown(w io.Writer) {
	fmt.Fprintf(w, "# 🔮 Service Forecast\n\n")
	fmt.Fprintf(w, "**Instance:** %s  \n", r.Instance)
	fmt.Fprintf(w, "**Historical window:** %d minutes | **Forecast horizon:** %d minutes  \n", r.Duration, r.Horizon)
	fmt.Fprintf(w, "**Generated:** %s  \n\n", r.GeneratedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "**Summary:** %d services — ✅ %d stable, ⚠️ %d degrading, 🔴 %d critical\n\n",
		r.TotalServices, r.StableCount, r.DegradingCount, r.CriticalCount)

	fmt.Fprintln(w, "| Service | Risk | Errors | Trend | Rate | Predicted | R² |")
	fmt.Fprintln(w, "|---------|------|--------|-------|------|-----------|-----|")

	for _, f := range r.Services {
		riskIcon := "✅"
		switch f.RiskLevel {
		case "degrading":
			riskIcon = "⚠️"
		case "critical":
			riskIcon = "🔴"
		}

		trendArrow := "→"
		switch f.ErrorTrend.Direction {
		case "rising":
			trendArrow = "↑"
		case "falling":
			trendArrow = "↓"
		}

		fmt.Fprintf(w, "| %s | %s %.0f | %d | %.2f/min %s | %.1f%% | %.1f%% | %.2f |\n",
			f.Name, riskIcon, f.RiskScore, f.CurrentErrors,
			math.Abs(f.ErrorTrend.Slope), trendArrow,
			f.CurrentRate, f.PredictedRate, f.ErrorTrend.R2)
	}

	if len(r.Services) > 0 {
		fmt.Fprintln(w)
		hasWarnings := false
		for _, f := range r.Services {
			if len(f.Warnings) > 0 {
				if !hasWarnings {
					fmt.Fprintln(w, "### ⚠️ Warnings")
					hasWarnings = true
				}
				for _, warn := range f.Warnings {
					fmt.Fprintf(w, "- **%s:** %s\n", f.Name, warn)
				}
			}
		}
	}

	if r.AISummary != "" {
		fmt.Fprintf(w, "\n### 🤖 AI Analysis\n\n%s\n", r.AISummary)
	}
}
