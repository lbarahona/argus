package deploy

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
)

// ChangeType classifies what kind of behavioral change was detected.
const (
	ChangeErrorSpike    = "error_spike"
	ChangeErrorDrop     = "error_drop"
	ChangeLatencySpike  = "latency_spike"
	ChangeLatencyDrop   = "latency_drop"
	ChangeTrafficSurge  = "traffic_surge"
	ChangeTrafficDrop   = "traffic_drop"
	ChangeNewErrors     = "new_errors"
	ChangeServiceAppear = "service_appear"
)

// Impact classifies the overall deployment impact.
const (
	ImpactPositive = "positive"
	ImpactNegative = "negative"
	ImpactNeutral  = "neutral"
	ImpactMixed    = "mixed"
)

// Sensitivity presets for change detection thresholds.
const (
	SensitivityLow    = "low"
	SensitivityMedium = "medium"
	SensitivityHigh   = "high"
)

// Options configures deployment detection.
type Options struct {
	Duration     int    // minutes of data to analyze (default: 360 = 6h)
	Buckets      int    // number of time buckets (default: 12)
	Service      string // optional: focus on one service
	Sensitivity  string // low, medium, high (default: medium)
	Format       string // terminal or markdown
	WithAI       bool   // include AI analysis
	AIProvider   ai.Provider
}

// ChangePoint represents a detected behavioral change in a service.
type ChangePoint struct {
	Service     string    `json:"service"`
	Type        string    `json:"type"`         // ChangeType constant
	DetectedAt  time.Time `json:"detected_at"`  // estimated time of change
	Metric      string    `json:"metric"`       // what metric changed
	BeforeValue float64   `json:"before_value"` // average before change
	AfterValue  float64   `json:"after_value"`  // average after change
	Magnitude   float64   `json:"magnitude"`    // percentage change
	Confidence  float64   `json:"confidence"`   // 0-1, how confident we are this is real
	Description string    `json:"description"`
}

// ServiceImpact summarizes the deployment impact on a single service.
type ServiceImpact struct {
	Name         string        `json:"name"`
	Impact       string        `json:"impact"` // positive, negative, neutral, mixed
	ImpactScore  float64       `json:"impact_score"` // -100 to +100
	Changes      []ChangePoint `json:"changes"`
	ErrorsBefore int           `json:"errors_before"`
	ErrorsAfter  int           `json:"errors_after"`
	CallsBefore  int           `json:"calls_before"`
	CallsAfter   int           `json:"calls_after"`
	P99Before    float64       `json:"p99_before_ms"`
	P99After     float64       `json:"p99_after_ms"`
}

// Result holds the complete deployment detection analysis.
type Result struct {
	Instance       string          `json:"instance"`
	Duration       int             `json:"duration_minutes"`
	Buckets        int             `json:"buckets"`
	Sensitivity    string          `json:"sensitivity"`
	Services       []ServiceImpact `json:"services"`
	ChangePoints   []ChangePoint   `json:"change_points"`
	Summary        Summary         `json:"summary"`
	AISummary      string          `json:"ai_summary,omitempty"`
	GeneratedAt    time.Time       `json:"generated_at"`
}

// Summary provides a high-level overview.
type Summary struct {
	TotalChanges   int     `json:"total_changes"`
	ServicesAffected int   `json:"services_affected"`
	MostImpacted   string  `json:"most_impacted"`
	OverallImpact  string  `json:"overall_impact"`
	OverallScore   float64 `json:"overall_score"` // -100 to +100
	Positive       int     `json:"positive"`
	Negative       int     `json:"negative"`
	Neutral        int     `json:"neutral"`
	Mixed          int     `json:"mixed"`
}

// thresholds returns sensitivity-dependent thresholds.
type thresholds struct {
	errorRateChange  float64 // % change to trigger
	latencyChange    float64 // % change to trigger
	trafficChange    float64 // % change to trigger
	minConfidence    float64 // minimum confidence to report
	newErrorMinCount int     // min new error count to flag
}

func getThresholds(sensitivity string) thresholds {
	switch sensitivity {
	case SensitivityHigh:
		return thresholds{
			errorRateChange:  15,
			latencyChange:    20,
			trafficChange:    25,
			minConfidence:    0.3,
			newErrorMinCount: 1,
		}
	case SensitivityLow:
		return thresholds{
			errorRateChange:  50,
			latencyChange:    75,
			trafficChange:    100,
			minConfidence:    0.7,
			newErrorMinCount: 5,
		}
	default: // medium
		return thresholds{
			errorRateChange:  30,
			latencyChange:    40,
			trafficChange:    50,
			minConfidence:    0.5,
			newErrorMinCount: 3,
		}
	}
}

// DataPoint represents a single time-bucketed measurement.
type DataPoint struct {
	Timestamp time.Time
	Value     float64
}

// Detect analyzes service behavior to find deployment-like changes.
func Detect(ctx context.Context, client signoz.SignozQuerier, instKey string, opts Options) (*Result, error) {
	if opts.Duration <= 0 {
		opts.Duration = 360
	}
	if opts.Buckets <= 0 {
		opts.Buckets = 12
	}
	if opts.Sensitivity == "" {
		opts.Sensitivity = SensitivityMedium
	}

	thresh := getThresholds(opts.Sensitivity)
	now := time.Now()

	// Fetch services
	services, err := client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	// Fetch error logs for the full duration
	allLogs, err := client.QueryLogs(ctx, opts.Service, opts.Duration, 1000, "ERROR")
	if err != nil {
		return nil, fmt.Errorf("querying logs: %w", err)
	}

	// Fetch traces for latency analysis
	allTraces, err := client.QueryTraces(ctx, opts.Service, opts.Duration, 500)
	if err != nil {
		// Traces are optional — don't fail
		allTraces = &types.QueryResult{}
	}

	result := &Result{
		Instance:    instKey,
		Duration:    opts.Duration,
		Buckets:     opts.Buckets,
		Sensitivity: opts.Sensitivity,
		GeneratedAt: now,
	}

	bucketSize := time.Duration(opts.Duration/opts.Buckets) * time.Minute
	windowStart := now.Add(-time.Duration(opts.Duration) * time.Minute)

	// Filter services if specified
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

	// Analyze each service
	for _, svc := range services {
		impact := analyzeService(svc, allLogs, allTraces, windowStart, now, bucketSize, opts.Buckets, thresh)
		if impact != nil {
			result.Services = append(result.Services, *impact)
			result.ChangePoints = append(result.ChangePoints, impact.Changes...)
		}
	}

	// Sort: most impacted first (by absolute impact score)
	sort.Slice(result.Services, func(i, j int) bool {
		return math.Abs(result.Services[i].ImpactScore) > math.Abs(result.Services[j].ImpactScore)
	})

	// Sort change points by time
	sort.Slice(result.ChangePoints, func(i, j int) bool {
		return result.ChangePoints[i].DetectedAt.Before(result.ChangePoints[j].DetectedAt)
	})

	// Build summary
	result.Summary = buildSummary(result.Services)

	// AI analysis
	if opts.WithAI && opts.AIProvider != nil {
		summary, err := generateAISummary(ctx, result, opts.AIProvider)
		if err == nil {
			result.AISummary = summary
		}
	}

	return result, nil
}

// analyzeService examines one service for behavioral changes.
func analyzeService(svc types.Service, logs *types.QueryResult, traces *types.QueryResult, windowStart, windowEnd time.Time, bucketSize time.Duration, numBuckets int, thresh thresholds) *ServiceImpact {
	// Bucket error logs by time
	errorBuckets := bucketLogs(logs.Logs, svc.Name, windowStart, bucketSize, numBuckets)
	// Bucket traces by latency
	latencyBuckets := bucketTraceLatency(traces.Traces, svc.Name, windowStart, bucketSize, numBuckets)

	// Detect error pattern changes
	errorNewPatterns := detectNewErrorPatterns(logs.Logs, svc.Name, windowStart, windowEnd)

	var changes []ChangePoint

	// 1. Error rate change point detection
	if cp := detectChangePoint(errorBuckets, svc.Name, "error_count", thresh.errorRateChange, thresh.minConfidence); cp != nil {
		if cp.Magnitude > 0 {
			cp.Type = ChangeErrorSpike
			cp.Description = fmt.Sprintf("Error count jumped %.0f%% (%.0f → %.0f avg/bucket)", cp.Magnitude, cp.BeforeValue, cp.AfterValue)
		} else {
			cp.Type = ChangeErrorDrop
			cp.Description = fmt.Sprintf("Error count dropped %.0f%% (%.0f → %.0f avg/bucket)", math.Abs(cp.Magnitude), cp.BeforeValue, cp.AfterValue)
		}
		changes = append(changes, *cp)
	}

	// 2. Latency change point detection
	if cp := detectChangePoint(latencyBuckets, svc.Name, "p99_latency_ms", thresh.latencyChange, thresh.minConfidence); cp != nil {
		if cp.Magnitude > 0 {
			cp.Type = ChangeLatencySpike
			cp.Description = fmt.Sprintf("P99 latency increased %.0f%% (%.1fms → %.1fms)", cp.Magnitude, cp.BeforeValue, cp.AfterValue)
		} else {
			cp.Type = ChangeLatencyDrop
			cp.Description = fmt.Sprintf("P99 latency decreased %.0f%% (%.1fms → %.1fms)", math.Abs(cp.Magnitude), cp.BeforeValue, cp.AfterValue)
		}
		changes = append(changes, *cp)
	}

	// 3. New error patterns appearing
	if len(errorNewPatterns) >= thresh.newErrorMinCount {
		midpoint := windowStart.Add(time.Duration(numBuckets/2) * bucketSize)
		changes = append(changes, ChangePoint{
			Service:     svc.Name,
			Type:        ChangeNewErrors,
			DetectedAt:  midpoint,
			Metric:      "error_patterns",
			AfterValue:  float64(len(errorNewPatterns)),
			Magnitude:   100,
			Confidence:  0.7,
			Description: fmt.Sprintf("%d new error patterns detected in recent window", len(errorNewPatterns)),
		})
	}

	if len(changes) == 0 {
		return nil
	}

	// Calculate before/after stats
	midBucket := numBuckets / 2
	errorsBefore, errorsAfter := sumBuckets(errorBuckets, midBucket)
	p99Before, p99After := avgBuckets(latencyBuckets, midBucket)

	impact := &ServiceImpact{
		Name:         svc.Name,
		Changes:      changes,
		ErrorsBefore: int(errorsBefore),
		ErrorsAfter:  int(errorsAfter),
		CallsBefore:  svc.NumCalls,
		CallsAfter:   svc.NumCalls,
		P99Before:    p99Before,
		P99After:     p99After,
	}

	// Calculate impact score (-100 to +100)
	impact.ImpactScore = calculateImpactScore(changes)
	impact.Impact = classifyImpact(impact.ImpactScore)

	return impact
}

// detectChangePoint uses CUSUM-inspired change detection on time-bucketed data.
// Returns nil if no significant change is detected.
func detectChangePoint(buckets []DataPoint, service, metric string, thresholdPct, minConfidence float64) *ChangePoint {
	if len(buckets) < 4 {
		return nil
	}

	values := make([]float64, len(buckets))
	for i, b := range buckets {
		values[i] = b.Value
	}

	// Calculate overall mean and std dev
	mean := calcMean(values)
	stddev := calcStdDev(values, mean)

	if mean == 0 && stddev == 0 {
		return nil
	}

	// Find the split point that maximizes the difference in means (binary segmentation)
	bestSplit := -1
	bestDiff := 0.0

	for split := 2; split < len(values)-1; split++ {
		leftMean := calcMean(values[:split])
		rightMean := calcMean(values[split:])
		diff := math.Abs(rightMean - leftMean)
		if diff > bestDiff {
			bestDiff = diff
			bestSplit = split
		}
	}

	if bestSplit < 0 {
		return nil
	}

	leftMean := calcMean(values[:bestSplit])
	rightMean := calcMean(values[bestSplit:])

	// Calculate percentage change
	var pctChange float64
	if leftMean > 0 {
		pctChange = ((rightMean - leftMean) / leftMean) * 100
	} else if rightMean > 0 {
		pctChange = 100
	} else {
		return nil
	}

	// Check if change exceeds threshold
	if math.Abs(pctChange) < thresholdPct {
		return nil
	}

	// Calculate confidence based on:
	// 1. How much the change exceeds the threshold
	// 2. How consistent the before/after segments are (low variance = high confidence)
	threshExceed := math.Abs(pctChange) / thresholdPct // >1 means exceeds threshold
	confidence := math.Min(1.0, 0.3+(threshExceed-1)*0.3)

	// Boost confidence if variance is low in both segments
	if stddev > 0 {
		leftStd := calcStdDev(values[:bestSplit], leftMean)
		rightStd := calcStdDev(values[bestSplit:], rightMean)
		avgSegStd := (leftStd + rightStd) / 2
		if avgSegStd < stddev {
			confidence = math.Min(1.0, confidence+0.2)
		}
	}

	if confidence < minConfidence {
		return nil
	}

	return &ChangePoint{
		Service:     service,
		Metric:      metric,
		DetectedAt:  buckets[bestSplit].Timestamp,
		BeforeValue: leftMean,
		AfterValue:  rightMean,
		Magnitude:   pctChange,
		Confidence:  confidence,
	}
}

// detectNewErrorPatterns finds error messages that only appear in the second half of the window.
func detectNewErrorPatterns(logs []types.LogEntry, service string, windowStart, windowEnd time.Time) []string {
	midpoint := windowStart.Add(windowEnd.Sub(windowStart) / 2)

	beforePatterns := make(map[string]bool)
	afterPatterns := make(map[string]int)

	for _, l := range logs {
		if service != "" && !strings.EqualFold(l.ServiceName, service) {
			continue
		}
		// Normalize pattern: take first 80 chars
		pattern := normalizePattern(l.Body)
		if pattern == "" {
			continue
		}
		if l.Timestamp.Before(midpoint) {
			beforePatterns[pattern] = true
		} else {
			afterPatterns[pattern]++
		}
	}

	var newPatterns []string
	for pattern, count := range afterPatterns {
		if !beforePatterns[pattern] && count >= 1 {
			newPatterns = append(newPatterns, pattern)
		}
	}

	return newPatterns
}

// normalizePattern extracts a stable pattern from a log message.
func normalizePattern(body string) string {
	if len(body) == 0 {
		return ""
	}
	// Truncate for grouping
	if len(body) > 80 {
		body = body[:80]
	}
	return strings.TrimSpace(body)
}

// bucketLogs counts error logs per time bucket for a given service.
func bucketLogs(logs []types.LogEntry, service string, windowStart time.Time, bucketSize time.Duration, numBuckets int) []DataPoint {
	buckets := make([]DataPoint, numBuckets)
	for i := range buckets {
		buckets[i].Timestamp = windowStart.Add(time.Duration(i) * bucketSize)
	}

	for _, l := range logs {
		if service != "" && !strings.EqualFold(l.ServiceName, service) {
			continue
		}
		idx := int(l.Timestamp.Sub(windowStart) / bucketSize)
		if idx >= 0 && idx < numBuckets {
			buckets[idx].Value++
		}
	}

	return buckets
}

// bucketTraceLatency computes P99 latency per bucket for a service.
func bucketTraceLatency(traces []types.TraceEntry, service string, windowStart time.Time, bucketSize time.Duration, numBuckets int) []DataPoint {
	// Collect latencies per bucket
	bucketLatencies := make([][]float64, numBuckets)
	for i := range bucketLatencies {
		bucketLatencies[i] = make([]float64, 0)
	}

	for _, t := range traces {
		if service != "" && !strings.EqualFold(t.ServiceName, service) {
			continue
		}
		idx := int(t.Timestamp.Sub(windowStart) / bucketSize)
		if idx >= 0 && idx < numBuckets {
			bucketLatencies[idx] = append(bucketLatencies[idx], t.DurationMs())
		}
	}

	buckets := make([]DataPoint, numBuckets)
	for i := range buckets {
		buckets[i].Timestamp = windowStart.Add(time.Duration(i) * bucketSize)
		if len(bucketLatencies[i]) > 0 {
			buckets[i].Value = percentile(bucketLatencies[i], 99)
		}
	}

	return buckets
}

// percentile calculates the p-th percentile of a sorted slice.
func percentile(data []float64, p float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sort.Float64s(data)
	idx := int(math.Ceil(p/100*float64(len(data)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(data) {
		idx = len(data) - 1
	}
	return data[idx]
}

// sumBuckets sums values before and after a midpoint.
func sumBuckets(buckets []DataPoint, midBucket int) (float64, float64) {
	var before, after float64
	for i, b := range buckets {
		if i < midBucket {
			before += b.Value
		} else {
			after += b.Value
		}
	}
	return before, after
}

// avgBuckets computes average non-zero value before and after midpoint.
func avgBuckets(buckets []DataPoint, midBucket int) (float64, float64) {
	var beforeSum, afterSum float64
	var beforeCount, afterCount int
	for i, b := range buckets {
		if b.Value == 0 {
			continue
		}
		if i < midBucket {
			beforeSum += b.Value
			beforeCount++
		} else {
			afterSum += b.Value
			afterCount++
		}
	}
	var beforeAvg, afterAvg float64
	if beforeCount > 0 {
		beforeAvg = beforeSum / float64(beforeCount)
	}
	if afterCount > 0 {
		afterAvg = afterSum / float64(afterCount)
	}
	return beforeAvg, afterAvg
}

// calculateImpactScore computes -100 to +100 score from changes.
// Negative = things got worse, Positive = things got better.
func calculateImpactScore(changes []ChangePoint) float64 {
	if len(changes) == 0 {
		return 0
	}

	var score float64
	for _, c := range changes {
		weight := c.Confidence
		switch c.Type {
		case ChangeErrorSpike:
			// More errors = bad
			score -= math.Min(50, math.Abs(c.Magnitude)/2) * weight
		case ChangeErrorDrop:
			// Fewer errors = good
			score += math.Min(30, math.Abs(c.Magnitude)/3) * weight
		case ChangeLatencySpike:
			// Higher latency = bad
			score -= math.Min(40, math.Abs(c.Magnitude)/3) * weight
		case ChangeLatencyDrop:
			// Lower latency = good
			score += math.Min(25, math.Abs(c.Magnitude)/4) * weight
		case ChangeNewErrors:
			// New error patterns = bad
			score -= 20 * weight
		case ChangeTrafficSurge:
			// Neutral-ish, slight concern
			score -= 5 * weight
		case ChangeTrafficDrop:
			// Could be bad
			score -= 15 * weight
		case ChangeServiceAppear:
			// Neutral
			score -= 5 * weight
		}
	}

	// Clamp to -100..+100
	return math.Max(-100, math.Min(100, score))
}

// classifyImpact converts a score to an impact label.
func classifyImpact(score float64) string {
	switch {
	case score >= 10:
		return ImpactPositive
	case score <= -10:
		return ImpactNegative
	case score > -10 && score < 10:
		return ImpactNeutral
	default:
		return ImpactMixed
	}
}

// buildSummary aggregates service impacts into a summary.
func buildSummary(services []ServiceImpact) Summary {
	s := Summary{}
	var totalScore float64
	affected := 0

	for _, svc := range services {
		s.TotalChanges += len(svc.Changes)
		totalScore += svc.ImpactScore

		switch svc.Impact {
		case ImpactPositive:
			s.Positive++
		case ImpactNegative:
			s.Negative++
			affected++
		case ImpactMixed:
			s.Mixed++
			affected++
		default:
			s.Neutral++
		}

		if s.MostImpacted == "" || math.Abs(svc.ImpactScore) > math.Abs(totalScore/float64(len(services))) {
			s.MostImpacted = svc.Name
		}
	}

	s.ServicesAffected = affected
	if len(services) > 0 {
		s.OverallScore = totalScore / float64(len(services))
	}
	s.OverallImpact = classifyImpact(s.OverallScore)

	// Most impacted is the one with highest absolute score
	maxAbs := 0.0
	for _, svc := range services {
		if math.Abs(svc.ImpactScore) > maxAbs {
			maxAbs = math.Abs(svc.ImpactScore)
			s.MostImpacted = svc.Name
		}
	}

	return s
}

// generateAISummary produces an AI-powered deployment impact analysis.
func generateAISummary(ctx context.Context, result *Result, provider ai.Provider) (string, error) {
	analyzer := ai.NewFromProvider(provider)

	var sb strings.Builder
	sb.WriteString("Analyze this deployment change detection report for an SRE team.\n\n")
	sb.WriteString(fmt.Sprintf("Time window: %d minutes | Sensitivity: %s\n", result.Duration, result.Sensitivity))
	sb.WriteString(fmt.Sprintf("Overall impact: %s (score: %.1f)\n", result.Summary.OverallImpact, result.Summary.OverallScore))
	sb.WriteString(fmt.Sprintf("Changes detected: %d across %d services\n\n", result.Summary.TotalChanges, len(result.Services)))

	sb.WriteString("CHANGE POINTS:\n")
	for _, cp := range result.ChangePoints {
		sb.WriteString(fmt.Sprintf("- [%s] %s @ %s: %s (confidence: %.0f%%)\n",
			cp.Type, cp.Service, cp.DetectedAt.Format("15:04"), cp.Description, cp.Confidence*100))
	}

	sb.WriteString("\nSERVICE IMPACT:\n")
	for _, svc := range result.Services {
		sb.WriteString(fmt.Sprintf("- %s: impact=%s (score=%.1f), errors %d→%d, p99 %.1fms→%.1fms\n",
			svc.Name, svc.Impact, svc.ImpactScore,
			svc.ErrorsBefore, svc.ErrorsAfter,
			svc.P99Before, svc.P99After))
	}

	sb.WriteString("\nProvide:\n")
	sb.WriteString("1. Were there likely deployments? When?\n")
	sb.WriteString("2. Which services were most affected and how?\n")
	sb.WriteString("3. Are any changes concerning enough to investigate?\n")
	sb.WriteString("4. Recommended actions (if any)\n")
	sb.WriteString("5. Correlation between changes (e.g., did a deploy to service A cascade to B?)\n")

	summary, err := analyzer.AnalyzeSync(sb.String())
	if err != nil {
		return "", err
	}
	return summary, nil
}

// Helper math functions.

func calcMean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func calcStdDev(values []float64, mean float64) float64 {
	if len(values) < 2 {
		return 0
	}
	var sum float64
	for _, v := range values {
		d := v - mean
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(values)-1))
}
