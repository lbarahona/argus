package types

import "testing"

func TestGetAPIVersionDefault(t *testing.T) {
	inst := Instance{URL: "http://localhost"}
	if v := inst.GetAPIVersion(); v != "v3" {
		t.Errorf("expected v3, got %s", v)
	}
}

func TestGetAPIVersionExplicit(t *testing.T) {
	inst := Instance{URL: "http://localhost", APIVersion: "v5"}
	if v := inst.GetAPIVersion(); v != "v5" {
		t.Errorf("expected v5, got %s", v)
	}
}

func TestTraceEntryDurationMs(t *testing.T) {
	entry := TraceEntry{DurationNano: 15000000}
	if ms := entry.DurationMs(); ms != 15.0 {
		t.Errorf("expected 15.0ms, got %f", ms)
	}
}

func TestTraceEntryDurationMsSubMs(t *testing.T) {
	entry := TraceEntry{DurationNano: 500000}
	if ms := entry.DurationMs(); ms != 0.5 {
		t.Errorf("expected 0.5ms, got %f", ms)
	}
}
