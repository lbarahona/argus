package anomaly

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/internal/textutil"
	"github.com/lbarahona/argus/pkg/types"
)

// Severity levels for detected anomalies.
const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
)

// AnomalyType classifies what kind of anomaly was detected.
const (
	TypeSpike     = "spike"      // sudden increase
	TypeDrop      = "drop"       // sudden decrease
	TypeTrend     = "trend"      // gradual directional change
	TypeVariance  = "variance"   // unusual variability
	TypeErrorRate = "error_rate" // elevated error rate
	TypeLatency   = "latency"    // elevated latency
)

// Anomaly represents a single detected anomaly.
type Anomaly struct {
	Type        string    `json:"type"`
	Severity    string    `json:"severity"`
	Service     string    `json:"service"`
	Metric      string    `json:"metric"`
	Description string    `json:"description"`
	Value       float64   `json:"value"`
	Expected    float64   `json:"expected"`
	Deviation   float64   `json:"deviation"` // how many std devs or % from normal
	DetectedAt  time.Time `json:"detected_at"`
	Window      string    `json:"window"` // time window analyzed
}

// Result holds all anomalies found during a scan.
type Result struct {
	Anomalies   []Anomaly     `json:"anomalies"`
	Services    []ServiceScan `json:"services"`
	ScanTime    time.Time     `json:"scan_time"`
	Duration    int           `json:"duration_minutes"`
	Instance    string        `json:"instance"`
	AISummary   string        `json:"ai_summary,omitempty"`
	TotalScanned int          `json:"total_scanned"`
}

// ServiceScan summarizes the scan result for one service.
type ServiceScan struct {
	Name           string  `json:"name"`
	Calls          int     `json:"calls"`
	Errors         int     `json:"errors"`
	ErrorRate      float64 `json:"error_rate"`
	AnomalyCount   int     `json:"anomaly_count"`
	HighestSeverity string `json:"highest_severity"`
}

// Options configures anomaly detection.
type Options struct {
	Duration       int     // minutes to analyze
	Sensitivity    float64 // z-score threshold (default 2.0)
	Service        string  // specific service to scan (empty = all)
	WithAI         bool    // include AI analysis
	AIProvider     ai.Provider
	Quiet          bool    // only show anomalies (no OK services)
}

// DefaultSensitivity is the default z-score threshold.
const DefaultSensitivity = 2.0

// Detect runs anomaly detection across services.
func Detect(ctx context.Context, client signoz.SignozQuerier, instKey string, opts Options) (*Result, error) {
	if opts.Sensitivity <= 0 {
		opts.Sensitivity = DefaultSensitivity
	}

	result := &Result{
		ScanTime: time.Now(),
		Duration: opts.Duration,
		Instance: instKey,
	}

	// Get all services
	services, err := client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	// Filter to specific service if requested
	if opts.Service != "" {
		var filtered []types.Service
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

	result.TotalScanned = len(services)

	for _, svc := range services {
		scan := ServiceScan{
			Name:      svc.Name,
			Calls:     svc.NumCalls,
			Errors:    svc.NumErrors,
			ErrorRate: svc.ErrorRate,
		}

		// Check error rate anomaly
		if anomalies := detectErrorRateAnomalies(svc, opts); len(anomalies) > 0 {
			result.Anomalies = append(result.Anomalies, anomalies...)
			scan.AnomalyCount += len(anomalies)
		}

		// Analyze logs for error spikes
		if logs, err := client.QueryLogs(ctx, svc.Name, opts.Duration, 200, ""); err == nil && logs != nil {
			if anomalies := detectLogAnomalies(svc.Name, logs.Logs, opts); len(anomalies) > 0 {
				result.Anomalies = append(result.Anomalies, anomalies...)
				scan.AnomalyCount += len(anomalies)
			}
		}

		// Analyze traces for latency anomalies
		if traces, err := client.QueryTraces(ctx, svc.Name, opts.Duration, 200); err == nil && traces != nil {
			if anomalies := detectLatencyAnomalies(svc.Name, traces.Traces, opts); len(anomalies) > 0 {
				result.Anomalies = append(result.Anomalies, anomalies...)
				scan.AnomalyCount += len(anomalies)
			}
		}

		// Set highest severity
		scan.HighestSeverity = highestSeverity(result.Anomalies, svc.Name)
		result.Services = append(result.Services, scan)
	}

	// Sort anomalies by severity (critical first), breaking ties by service
	// name so equal-severity anomalies have a deterministic, stable order
	// across runs.
	sort.SliceStable(result.Anomalies, func(i, j int) bool {
		a, b := result.Anomalies[i], result.Anomalies[j]
		if a.Severity != b.Severity {
			return severityRank(a.Severity) > severityRank(b.Severity)
		}
		return a.Service < b.Service
	})

	// AI summary
	if opts.WithAI && opts.AIProvider != nil && len(result.Anomalies) > 0 {
		result.AISummary = generateAISummary(ctx, result, opts.AIProvider)
	}

	return result, nil
}

// detectErrorRateAnomalies checks service error rates for anomalies.
func detectErrorRateAnomalies(svc types.Service, opts Options) []Anomaly {
	var anomalies []Anomaly

	if svc.NumCalls == 0 {
		return nil
	}

	rate := svc.ErrorRate

	// Critical: >10% error rate with significant traffic
	if rate > 10 && svc.NumCalls > 10 {
		anomalies = append(anomalies, Anomaly{
			Type:        TypeErrorRate,
			Severity:    SeverityCritical,
			Service:     svc.Name,
			Metric:      "error_rate",
			Description: fmt.Sprintf("Error rate at %.1f%% (%d errors / %d calls)", rate, svc.NumErrors, svc.NumCalls),
			Value:       rate,
			Expected:    1.0, // typical healthy error rate
			Deviation:   rate / 1.0,
			DetectedAt:  time.Now(),
		})
	} else if rate > 5 && svc.NumCalls > 10 {
		// Warning: >5% error rate
		anomalies = append(anomalies, Anomaly{
			Type:        TypeErrorRate,
			Severity:    SeverityWarning,
			Service:     svc.Name,
			Metric:      "error_rate",
			Description: fmt.Sprintf("Elevated error rate at %.1f%% (%d errors / %d calls)", rate, svc.NumErrors, svc.NumCalls),
			Value:       rate,
			Expected:    1.0,
			Deviation:   rate / 1.0,
			DetectedAt:  time.Now(),
		})
	} else if rate > 2 && svc.NumCalls > 50 {
		// Info: >2% with high traffic
		anomalies = append(anomalies, Anomaly{
			Type:        TypeErrorRate,
			Severity:    SeverityInfo,
			Service:     svc.Name,
			Metric:      "error_rate",
			Description: fmt.Sprintf("Slightly elevated error rate at %.1f%%", rate),
			Value:       rate,
			Expected:    1.0,
			Deviation:   rate / 1.0,
			DetectedAt:  time.Now(),
		})
	}

	return anomalies
}

// detectLogAnomalies analyzes log patterns for anomalies.
func detectLogAnomalies(service string, logs []types.LogEntry, opts Options) []Anomaly {
	if len(logs) == 0 {
		return nil
	}

	var anomalies []Anomaly

	// Count errors by time bucket (5 min windows)
	bucketSize := 5 * time.Minute
	errorBuckets := make(map[int64]int)
	totalBuckets := make(map[int64]int)

	for _, log := range logs {
		bucket := log.Timestamp.Truncate(bucketSize).Unix()
		totalBuckets[bucket]++
		sev := strings.ToUpper(log.SeverityText)
		if sev == "ERROR" || sev == "FATAL" || sev == "CRITICAL" {
			errorBuckets[bucket]++
		}
	}

	// Need at least 3 buckets for statistical analysis
	if len(totalBuckets) < 3 {
		return anomalies
	}

	// Get error counts per bucket
	var errorCounts []float64
	for bucket := range totalBuckets {
		errorCounts = append(errorCounts, float64(errorBuckets[bucket]))
	}

	mean, stddev := meanStdDev(errorCounts)

	// Check each bucket for spikes
	if stddev > 0 {
		for bucket, count := range errorBuckets {
			zScore := (float64(count) - mean) / stddev
			if zScore >= opts.Sensitivity && count > 2 {
				severity := SeverityInfo
				if zScore >= opts.Sensitivity*2 {
					severity = SeverityCritical
				} else if zScore >= opts.Sensitivity*1.5 {
					severity = SeverityWarning
				}

				bucketTime := time.Unix(bucket, 0)
				anomalies = append(anomalies, Anomaly{
					Type:        TypeSpike,
					Severity:    severity,
					Service:     service,
					Metric:      "error_logs",
					Description: fmt.Sprintf("Error log spike: %d errors in 5min window (avg %.1f, z=%.1f)", count, mean, zScore),
					Value:       float64(count),
					Expected:    mean,
					Deviation:   zScore,
					DetectedAt:  bucketTime,
					Window:      fmt.Sprintf("%s - %s", bucketTime.Format("15:04"), bucketTime.Add(bucketSize).Format("15:04")),
				})
			}
		}
	}

	// Check for error pattern concentration
	errorPatterns := make(map[string]int)
	for _, log := range logs {
		sev := strings.ToUpper(log.SeverityText)
		if sev == "ERROR" || sev == "FATAL" || sev == "CRITICAL" {
			// Normalize error body to find patterns
			pattern := normalizeLogBody(log.Body)
			errorPatterns[pattern]++
		}
	}

	// Flag repeated error patterns
	for pattern, count := range errorPatterns {
		if count >= 10 {
			severity := SeverityWarning
			if count >= 50 {
				severity = SeverityCritical
			}
			truncated := textutil.Truncate(pattern, 80)
			anomalies = append(anomalies, Anomaly{
				Type:        TypeVariance,
				Severity:    severity,
				Service:     service,
				Metric:      "error_pattern",
				Description: fmt.Sprintf("Repeated error pattern (%dx): %s", count, truncated),
				Value:       float64(count),
				Expected:    1,
				Deviation:   float64(count),
				DetectedAt:  time.Now(),
			})
		}
	}

	return anomalies
}

// detectLatencyAnomalies finds latency anomalies in trace data.
func detectLatencyAnomalies(service string, traces []types.TraceEntry, opts Options) []Anomaly {
	if len(traces) < 5 {
		return nil
	}

	var anomalies []Anomaly

	// Collect latencies per operation
	opLatencies := make(map[string][]float64)
	for _, t := range traces {
		ms := t.DurationMs()
		opLatencies[t.OperationName] = append(opLatencies[t.OperationName], ms)
	}

	for op, latencies := range opLatencies {
		if len(latencies) < 5 {
			continue
		}

		mean, stddev := meanStdDev(latencies)
		p50, p95, p99 := percentiles(latencies)

		// Check for outlier latencies using z-score
		outlierCount := 0
		var maxLatency float64
		for _, lat := range latencies {
			if lat > maxLatency {
				maxLatency = lat
			}
			if stddev > 0 {
				z := (lat - mean) / stddev
				if z >= opts.Sensitivity {
					outlierCount++
				}
			}
		}

		// P99/P50 ratio check - healthy services usually have <10x
		p99p50Ratio := float64(0)
		if p50 > 0 {
			p99p50Ratio = p99 / p50
		}

		opDisplay := textutil.Truncate(op, 50)

		// Flag high p99/p50 ratio (indicates latency spikes)
		if p99p50Ratio > 20 && p99 > 100 { // p99 > 100ms and 20x slower than median
			anomalies = append(anomalies, Anomaly{
				Type:        TypeLatency,
				Severity:    SeverityCritical,
				Service:     service,
				Metric:      "latency_p99",
				Description: fmt.Sprintf("Latency spike on %s: p99=%.0fms (p50=%.0fms, ratio=%.0fx)", opDisplay, p99, p50, p99p50Ratio),
				Value:       p99,
				Expected:    p50,
				Deviation:   p99p50Ratio,
				DetectedAt:  time.Now(),
			})
		} else if p99p50Ratio > 10 && p99 > 50 {
			anomalies = append(anomalies, Anomaly{
				Type:        TypeLatency,
				Severity:    SeverityWarning,
				Service:     service,
				Metric:      "latency_p99",
				Description: fmt.Sprintf("High latency tail on %s: p99=%.0fms (p50=%.0fms, ratio=%.0fx)", opDisplay, p99, p50, p99p50Ratio),
				Value:       p99,
				Expected:    p50,
				Deviation:   p99p50Ratio,
				DetectedAt:  time.Now(),
			})
		}

		// Flag high outlier percentage
		outlierPct := float64(outlierCount) / float64(len(latencies)) * 100
		if outlierPct > 20 && outlierCount > 3 {
			anomalies = append(anomalies, Anomaly{
				Type:        TypeVariance,
				Severity:    SeverityWarning,
				Service:     service,
				Metric:      "latency_variance",
				Description: fmt.Sprintf("High latency variance on %s: %.0f%% outliers (mean=%.0fms, stddev=%.0fms)", opDisplay, outlierPct, mean, stddev),
				Value:       outlierPct,
				Expected:    5,
				Deviation:   outlierPct / 5,
				DetectedAt:  time.Now(),
			})
		}

		// Check for latency trend (are later traces slower?)
		if trend := detectTrend(latencies); trend != 0 {
			if trend > 0 && mean > 50 { // increasing and meaningful
				sev := SeverityInfo
				if p95 > 500 {
					sev = SeverityWarning
				}
				anomalies = append(anomalies, Anomaly{
					Type:        TypeTrend,
					Severity:    sev,
					Service:     service,
					Metric:      "latency_trend",
					Description: fmt.Sprintf("Increasing latency trend on %s: mean=%.0fms, p95=%.0fms", opDisplay, mean, p95),
					Value:       mean,
					Expected:    mean * 0.8,
					Deviation:   trend,
					DetectedAt:  time.Now(),
				})
			}
		}
	}

	return anomalies
}

// meanStdDev calculates mean and standard deviation.
func meanStdDev(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))

	var variance float64
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(values))
	return mean, math.Sqrt(variance)
}

// percentiles calculates p50, p95, p99 from a slice of values.
func percentiles(values []float64) (p50, p95, p99 float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	p50 = sorted[len(sorted)*50/100]
	p95 = sorted[len(sorted)*95/100]
	idx99 := len(sorted) * 99 / 100
	if idx99 >= len(sorted) {
		idx99 = len(sorted) - 1
	}
	p99 = sorted[idx99]
	return
}

// detectTrend returns a positive value if increasing, negative if decreasing, 0 if stable.
// Uses simple linear regression slope normalized by mean.
func detectTrend(values []float64) float64 {
	n := len(values)
	if n < 10 {
		return 0
	}

	// Split into first half and second half
	firstHalf := values[:n/2]
	secondHalf := values[n/2:]

	meanFirst, _ := meanStdDev(firstHalf)
	meanSecond, _ := meanStdDev(secondHalf)

	if meanFirst == 0 {
		return 0
	}

	change := (meanSecond - meanFirst) / meanFirst
	// Only flag if >20% change
	if math.Abs(change) > 0.2 {
		return change
	}
	return 0
}

// normalizeLogBody simplifies a log message for pattern matching.
func normalizeLogBody(body string) string {
	if len(body) > 200 {
		body = body[:200]
	}
	// Simple normalization: remove numbers, UUIDs, timestamps
	var result strings.Builder
	for _, ch := range body {
		if ch >= '0' && ch <= '9' {
			result.WriteRune('#')
		} else {
			result.WriteRune(ch)
		}
	}
	return result.String()
}

// highestSeverity returns the highest severity anomaly for a service.
func highestSeverity(anomalies []Anomaly, service string) string {
	highest := ""
	for _, a := range anomalies {
		if a.Service == service {
			if highest == "" || severityRank(a.Severity) > severityRank(highest) {
				highest = a.Severity
			}
		}
	}
	return highest
}

func severityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// generateAISummary asks Claude to analyze the anomalies.
func generateAISummary(_ context.Context, result *Result, provider ai.Provider) string {
	var sb strings.Builder
	sb.WriteString("Analyze these anomalies detected in our services and provide:\n")
	sb.WriteString("1. Root cause hypotheses\n")
	sb.WriteString("2. Priority order for investigation\n")
	sb.WriteString("3. Recommended actions\n\n")

	for _, a := range result.Anomalies {
		sb.WriteString(fmt.Sprintf("- [%s] %s | %s: %s (value=%.1f, expected=%.1f, deviation=%.1f)\n",
			a.Severity, a.Service, a.Type, a.Description, a.Value, a.Expected, a.Deviation))
	}

	sb.WriteString(fmt.Sprintf("\nServices scanned: %d, Total anomalies: %d\n", result.TotalScanned, len(result.Anomalies)))

	analyzer := ai.NewFromProvider(provider)
	response, err := analyzer.AnalyzeSync(sb.String())
	if err != nil {
		return fmt.Sprintf("AI analysis failed: %v", err)
	}
	return response
}
