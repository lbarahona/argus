package deploy

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRenderTerminal(t *testing.T) {
	result := &Result{
		Instance:    "production",
		Duration:    360,
		Buckets:     12,
		Sensitivity: "medium",
		GeneratedAt: time.Now(),
		Services: []ServiceImpact{
			{
				Name:         "payment-api",
				Impact:       ImpactNegative,
				ImpactScore:  -35.5,
				ErrorsBefore: 5,
				ErrorsAfter:  50,
				P99Before:    45.0,
				P99After:     120.0,
				Changes: []ChangePoint{
					{
						Type:        ChangeErrorSpike,
						Service:     "payment-api",
						DetectedAt:  time.Now().Add(-30 * time.Minute),
						Description: "Error count jumped 900% (5 → 50 avg/bucket)",
						Confidence:  0.85,
					},
				},
			},
		},
		ChangePoints: []ChangePoint{
			{
				Type:        ChangeErrorSpike,
				Service:     "payment-api",
				DetectedAt:  time.Now().Add(-30 * time.Minute),
				Description: "Error count jumped 900%",
				Confidence:  0.85,
			},
		},
		Summary: Summary{
			TotalChanges:    1,
			ServicesAffected: 1,
			MostImpacted:    "payment-api",
			OverallImpact:   ImpactNegative,
			OverallScore:    -35.5,
			Negative:        1,
		},
	}

	var buf bytes.Buffer
	result.RenderTerminal(&buf)
	out := buf.String()

	checks := []string{
		"ARGUS DEPLOY TRACKER",
		"production",
		"payment-api",
		"NEGATIVE",
		"CHANGE TIMELINE",
		"SERVICE IMPACT",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Errorf("terminal output missing %q", check)
		}
	}
}

func TestRenderTerminal_NoChanges(t *testing.T) {
	result := &Result{
		Instance:    "staging",
		Duration:    60,
		Buckets:     6,
		Sensitivity: "medium",
		GeneratedAt: time.Now(),
		Summary:     Summary{OverallImpact: ImpactNeutral},
	}

	var buf bytes.Buffer
	result.RenderTerminal(&buf)
	out := buf.String()

	if !strings.Contains(out, "No significant behavioral changes") {
		t.Error("expected 'no changes' message")
	}
}

func TestRenderMarkdown(t *testing.T) {
	result := &Result{
		Instance:    "production",
		Duration:    360,
		Buckets:     12,
		Sensitivity: "medium",
		GeneratedAt: time.Now(),
		Services: []ServiceImpact{
			{
				Name:         "api",
				Impact:       ImpactNegative,
				ImpactScore:  -20,
				ErrorsBefore: 10,
				ErrorsAfter:  100,
				Changes: []ChangePoint{
					{Type: ChangeErrorSpike, Description: "Errors jumped 900%"},
				},
			},
		},
		ChangePoints: []ChangePoint{
			{
				Type:        ChangeErrorSpike,
				Service:     "api",
				DetectedAt:  time.Now(),
				Description: "Errors jumped 900%",
				Confidence:  0.9,
			},
		},
		Summary: Summary{
			TotalChanges:    1,
			ServicesAffected: 1,
			OverallImpact:   ImpactNegative,
			OverallScore:    -20,
			MostImpacted:    "api",
			Negative:        1,
		},
		AISummary: "This looks like a deployment regression.",
	}

	var buf bytes.Buffer
	result.RenderMarkdown(&buf)
	out := buf.String()

	checks := []string{
		"# 🚀 Argus Deploy Tracker Report",
		"## Summary",
		"## Change Timeline",
		"## Service Impact",
		"## AI Analysis",
		"deployment regression",
	}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Errorf("markdown output missing %q", check)
		}
	}
}

func TestImpactIcon(t *testing.T) {
	tests := []struct {
		impact string
		want   string
	}{
		{ImpactPositive, "🟢"},
		{ImpactNegative, "🔴"},
		{ImpactMixed, "🟡"},
		{ImpactNeutral, "⚪"},
		{"unknown", "⚪"},
	}
	for _, tt := range tests {
		got := impactIcon(tt.impact)
		if got != tt.want {
			t.Errorf("impactIcon(%q) = %q, want %q", tt.impact, got, tt.want)
		}
	}
}

func TestChangeIcon(t *testing.T) {
	icons := map[string]string{
		ChangeErrorSpike:    "📈",
		ChangeErrorDrop:     "📉",
		ChangeLatencySpike:  "🐌",
		ChangeLatencyDrop:   "⚡",
		ChangeNewErrors:     "🆕",
		ChangeServiceAppear: "🌟",
	}
	for change, want := range icons {
		got := changeIcon(change)
		if got != want {
			t.Errorf("changeIcon(%q) = %q, want %q", change, got, want)
		}
	}
}

func TestScoreColor(t *testing.T) {
	// High negative score should have ‼️
	got := scoreColor(-55)
	if !strings.Contains(got, "‼️") {
		t.Errorf("expected ‼️ for score -55, got %q", got)
	}

	// Medium score should have ⚠️
	got = scoreColor(-25)
	if !strings.Contains(got, "⚠️") {
		t.Errorf("expected ⚠️ for score -25, got %q", got)
	}

	// Low score should have neither
	got = scoreColor(5)
	if strings.Contains(got, "‼️") || strings.Contains(got, "⚠️") {
		t.Errorf("expected no warning for score 5, got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Error("should not truncate short strings")
	}
	got := truncate("this is a very long name", 10)
	// Note: truncate works on bytes, and "…" is 3 bytes
	runeLen := len([]rune(got))
	if runeLen > 10 {
		t.Errorf("expected max 10 runes, got %d", runeLen)
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("expected truncation suffix")
	}
}
