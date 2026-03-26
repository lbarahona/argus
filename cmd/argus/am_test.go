package main

import (
	"testing"
)

func TestParseMatcher(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantN   string
		wantV   string
		wantEq  bool
		wantRe  bool
		wantErr bool
	}{
		{
			name:   "simple equal",
			input:  "alertname=HighLatency",
			wantN:  "alertname",
			wantV:  "HighLatency",
			wantEq: true,
			wantRe: false,
		},
		{
			name:   "not equal",
			input:  "severity!=info",
			wantN:  "severity",
			wantV:  "info",
			wantEq: false,
			wantRe: false,
		},
		{
			name:   "regex equal",
			input:  "alertname=~High.*",
			wantN:  "alertname",
			wantV:  "High.*",
			wantEq: true,
			wantRe: true,
		},
		{
			name:   "regex not equal",
			input:  "env!=~prod.*",
			wantN:  "env",
			wantV:  "prod.*",
			wantEq: false,
			wantRe: true,
		},
		{
			name:    "no operator",
			input:   "justtext",
			wantErr: true,
		},
		{
			name:   "value with equals",
			input:  "label=value=extra",
			wantN:  "label",
			wantV:  "value=extra",
			wantEq: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := parseMatcher(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Name != tt.wantN {
				t.Errorf("Name = %q, want %q", m.Name, tt.wantN)
			}
			if m.Value != tt.wantV {
				t.Errorf("Value = %q, want %q", m.Value, tt.wantV)
			}
			if m.IsEqual != tt.wantEq {
				t.Errorf("IsEqual = %v, want %v", m.IsEqual, tt.wantEq)
			}
			if m.IsRegex != tt.wantRe {
				t.Errorf("IsRegex = %v, want %v", m.IsRegex, tt.wantRe)
			}
		})
	}
}
