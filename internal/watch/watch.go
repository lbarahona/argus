package watch

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
)

// AlertLevel indicates severity of a detected anomaly.
type AlertLevel int

const (
	AlertInfo AlertLevel = iota
	AlertWarning
	AlertCritical
)

func (a AlertLevel) String() string {
	switch a {
	case AlertWarning:
		return "⚠️  WARNING"
	case AlertCritical:
		return "🔴 CRITICAL"
	default:
		return "ℹ️  INFO"
	}
}

func (a AlertLevel) Color() string {
	switch a {
	case AlertWarning:
		return "\033[33m" // yellow
	case AlertCritical:
		return "\033[31m" // red
	default:
		return "\033[36m" // cyan
	}
}

// Alert represents a detected anomaly.
type Alert struct {
	Level     AlertLevel
	Service   string
	Message   string
	Value     float64
	Threshold float64
	Timestamp time.Time
}

// ServiceSnapshot captures a service's health at a point in time.
type ServiceSnapshot struct {
	Name      string
	Calls     float64
	Errors    float64
	ErrorRate float64
	P99       float64
}

// Thresholds configures when to fire alerts.
type Thresholds struct {
	ErrorRateWarning  float64 // error rate % to trigger warning (default 5)
	ErrorRateCritical float64 // error rate % to trigger critical (default 15)
	P99Warning        float64 // p99 latency ms to trigger warning (default 2000)
	P99Critical       float64 // p99 latency ms to trigger critical (default 5000)
	ErrorSpike        float64 // multiplier over baseline for error spike (default 3x)
	NewErrors         bool    // alert on services with new errors (default true)
}

// DefaultThresholds returns sensible defaults.
func DefaultThresholds() Thresholds {
	return Thresholds{
		ErrorRateWarning:  5.0,
		ErrorRateCritical: 15.0,
		P99Warning:        2000,
		P99Critical:       5000,
		ErrorSpike:        3.0,
		NewErrors:         true,
	}
}

// Watcher continuously monitors a Signoz instance.
type Watcher struct {
	client     signoz.SignozQuerier
	instance   string
	interval   time.Duration
	thresholds Thresholds
	out        io.Writer

	mu       sync.RWMutex
	baseline map[string]*ServiceSnapshot // rolling baseline
	history  [][]ServiceSnapshot         // last N snapshots for trend
	alerts   []Alert
}

// New creates a new Watcher.
func New(client signoz.SignozQuerier, instance string, interval time.Duration, thresholds Thresholds, out io.Writer) *Watcher {
	return &Watcher{
		client:     client,
		instance:   instance,
		interval:   interval,
		thresholds: thresholds,
		out:        out,
		baseline:   make(map[string]*ServiceSnapshot),
	}
}

// Run starts the watch loop. Blocks until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	reset := "\033[0m"
	dim := "\033[2m"
	bold := "\033[1m"
	cyan := "\033[36m"

	fmt.Fprintf(w.out, "\n%s%s🔭 Argus Watch Mode%s\n", bold, cyan, reset)
	fmt.Fprintf(w.out, "%sInstance: %s | Interval: %s%s\n", dim, w.instance, w.interval, reset)
	fmt.Fprintf(w.out, "%sThresholds: error_rate=%.0f%%/%.0f%% p99=%.0fms/%.0fms spike=%.0fx%s\n",
		dim, w.thresholds.ErrorRateWarning, w.thresholds.ErrorRateCritical,
		w.thresholds.P99Warning, w.thresholds.P99Critical,
		w.thresholds.ErrorSpike, reset)
	fmt.Fprintf(w.out, "%sPress Ctrl+C to stop%s\n\n", dim, reset)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run immediately on start
	w.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(w.out, "\n%s%s✋ Watch stopped. %d alerts fired during session.%s\n",
				bold, cyan, len(w.alerts), reset)
			return nil
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Watcher) tick(ctx context.Context) {
	reset := "\033[0m"
	dim := "\033[2m"
	bold := "\033[1m"
	green := "\033[32m"

	now := time.Now()
	fmt.Fprintf(w.out, "%s── %s ──%s\n", dim, now.Format("15:04:05"), reset)

	services, err := w.client.ListServices(ctx)
	if err != nil {
		fmt.Fprintf(w.out, "\033[31m  ✗ Failed to fetch services: %v%s\n", err, reset)
		return
	}

	snapshots := w.buildSnapshots(services)
	alerts := w.analyze(snapshots)

	// Print service summary
	healthyCount := 0
	warnCount := 0
	critCount := 0

	for _, s := range snapshots {
		hasAlert := false
		for _, a := range alerts {
			if a.Service == s.Name {
				hasAlert = true
				if a.Level == AlertCritical {
					critCount++
				} else {
					warnCount++
				}
				break
			}
		}
		if !hasAlert {
			healthyCount++
		}
	}

	fmt.Fprintf(w.out, "  %s%d services%s | %s%s%d healthy%s",
		bold, len(snapshots), reset, green, bold, healthyCount, reset)
	if warnCount > 0 {
		fmt.Fprintf(w.out, " | \033[33m%s%d warning%s", bold, warnCount, reset)
	}
	if critCount > 0 {
		fmt.Fprintf(w.out, " | \033[31m%s%d critical%s", bold, critCount, reset)
	}
	fmt.Fprintln(w.out)

	// Print alerts
	for _, a := range alerts {
		color := a.Level.Color()
		fmt.Fprintf(w.out, "  %s%s%s %s%s — %s%s\n",
			color, bold, a.Level.String(), reset,
			a.Service, a.Message, reset)

		w.mu.Lock()
		w.alerts = append(w.alerts, a)
		w.mu.Unlock()
	}

	if len(alerts) == 0 {
		fmt.Fprintf(w.out, "  %s✓ All clear%s\n", green, reset)
	}

	// Update baseline
	w.updateBaseline(snapshots)

	fmt.Fprintln(w.out)
}

func (w *Watcher) buildSnapshots(services []types.Service) []ServiceSnapshot {
	var snapshots []ServiceSnapshot
	for _, svc := range services {
		s := ServiceSnapshot{
			Name:   svc.Name,
			Calls:  float64(svc.NumCalls),
			Errors: float64(svc.NumErrors),
		}
		if svc.NumCalls > 0 {
			s.ErrorRate = (float64(svc.NumErrors) / float64(svc.NumCalls)) * 100
		}
		snapshots = append(snapshots, s)
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].ErrorRate > snapshots[j].ErrorRate
	})
	return snapshots
}

func (w *Watcher) analyze(snapshots []ServiceSnapshot) []Alert {
	var alerts []Alert

	for _, s := range snapshots {
		// Skip services with no traffic
		if s.Calls == 0 {
			continue
		}

		// Error rate thresholds
		if s.ErrorRate >= w.thresholds.ErrorRateCritical {
			alerts = append(alerts, Alert{
				Level:     AlertCritical,
				Service:   s.Name,
				Message:   fmt.Sprintf("Error rate %.1f%% (threshold: %.0f%%)", s.ErrorRate, w.thresholds.ErrorRateCritical),
				Value:     s.ErrorRate,
				Threshold: w.thresholds.ErrorRateCritical,
				Timestamp: time.Now(),
			})
		} else if s.ErrorRate >= w.thresholds.ErrorRateWarning {
			alerts = append(alerts, Alert{
				Level:     AlertWarning,
				Service:   s.Name,
				Message:   fmt.Sprintf("Error rate %.1f%% (threshold: %.0f%%)", s.ErrorRate, w.thresholds.ErrorRateWarning),
				Value:     s.ErrorRate,
				Threshold: w.thresholds.ErrorRateWarning,
				Timestamp: time.Now(),
			})
		}

		// P99 latency thresholds
		if s.P99 >= w.thresholds.P99Critical {
			alerts = append(alerts, Alert{
				Level:     AlertCritical,
				Service:   s.Name,
				Message:   fmt.Sprintf("P99 latency %.0fms (threshold: %.0fms)", s.P99, w.thresholds.P99Critical),
				Value:     s.P99,
				Threshold: w.thresholds.P99Critical,
				Timestamp: time.Now(),
			})
		} else if s.P99 >= w.thresholds.P99Warning {
			alerts = append(alerts, Alert{
				Level:     AlertWarning,
				Service:   s.Name,
				Message:   fmt.Sprintf("P99 latency %.0fms (threshold: %.0fms)", s.P99, w.thresholds.P99Warning),
				Value:     s.P99,
				Threshold: w.thresholds.P99Warning,
				Timestamp: time.Now(),
			})
		}

		// Error spike detection (compared to baseline)
		w.mu.RLock()
		baseline, exists := w.baseline[s.Name]
		w.mu.RUnlock()

		if exists && baseline.Errors > 0 && s.Errors > 0 {
			spike := s.Errors / baseline.Errors
			if spike >= w.thresholds.ErrorSpike {
				alerts = append(alerts, Alert{
					Level:     AlertWarning,
					Service:   s.Name,
					Message:   fmt.Sprintf("Error spike %.1fx baseline (%.0f → %.0f errors)", spike, baseline.Errors, s.Errors),
					Value:     spike,
					Threshold: w.thresholds.ErrorSpike,
					Timestamp: time.Now(),
				})
			}
		}

		// New errors detection
		if w.thresholds.NewErrors && exists && baseline.Errors == 0 && s.Errors > 0 {
			alerts = append(alerts, Alert{
				Level:   AlertWarning,
				Service: s.Name,
				Message: fmt.Sprintf("New errors detected (%.0f errors, was clean)", s.Errors),
				Value:   s.Errors,
				Timestamp: time.Now(),
			})
		}
	}

	return alerts
}

func (w *Watcher) updateBaseline(snapshots []ServiceSnapshot) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Keep history for trend (max 10 snapshots)
	w.history = append(w.history, snapshots)
	if len(w.history) > 10 {
		w.history = w.history[1:]
	}

	// Update baseline using exponential moving average
	for _, s := range snapshots {
		if prev, ok := w.baseline[s.Name]; ok {
			// EMA with alpha=0.3 (more weight on recent)
			prev.Errors = ema(prev.Errors, s.Errors, 0.3)
			prev.Calls = ema(prev.Calls, s.Calls, 0.3)
			prev.ErrorRate = ema(prev.ErrorRate, s.ErrorRate, 0.3)
			prev.P99 = ema(prev.P99, s.P99, 0.3)
		} else {
			copy := s
			w.baseline[s.Name] = &copy
		}
	}
}

func ema(old, new float64, alpha float64) float64 {
	return alpha*new + (1-alpha)*old
}

// Summary returns a human-readable summary of the watch session.
func (w *Watcher) Summary() string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if len(w.alerts) == 0 {
		return "No alerts during watch session."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Watch session: %d total alerts\n", len(w.alerts)))

	// Group by service
	byService := make(map[string][]Alert)
	for _, a := range w.alerts {
		byService[a.Service] = append(byService[a.Service], a)
	}

	var services []string
	for s := range byService {
		services = append(services, s)
	}
	sort.Strings(services)

	for _, svc := range services {
		alerts := byService[svc]
		maxLevel := AlertInfo
		for _, a := range alerts {
			if a.Level > maxLevel {
				maxLevel = a.Level
			}
		}
		sb.WriteString(fmt.Sprintf("  %s %s: %d alerts (max: %s)\n",
			maxLevel.String(), svc, len(alerts), maxLevel))
	}

	return sb.String()
}

