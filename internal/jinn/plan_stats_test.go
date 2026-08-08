package jinn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStatsFilePath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)

	got, err := statsFilePath()
	if err != nil {
		t.Fatalf("statsFilePath() error: %v", err)
	}

	want := filepath.Join(dir, "jinn", "stats", "run_plan.jsonl")
	if got != want {
		t.Fatalf("statsFilePath() = %q, want %q", got, want)
	}
}

func TestEstimateRequestsSaved(t *testing.T) {
	tests := []struct {
		name   string
		result *PlanRunResult
		want   int
	}{
		{
			name:   "nil result",
			result: nil,
			want:   0,
		},
		{
			name:   "empty transcript",
			result: &PlanRunResult{Transcript: nil},
			want:   0,
		},
		{
			name: "single node single op",
			result: &PlanRunResult{
				Transcript: []PlanNodeResult{
					{NodeID: "a", Ops: []PlanOpResult{{OK: true}}},
				},
			},
			want: 0,
		},
		{
			name: "single node three ops",
			result: &PlanRunResult{
				Transcript: []PlanNodeResult{
					{NodeID: "a", Ops: []PlanOpResult{{OK: true}, {OK: true}, {OK: true}}},
				},
			},
			want: 2,
		},
		{
			name: "two nodes five ops total",
			result: &PlanRunResult{
				Transcript: []PlanNodeResult{
					{NodeID: "a", Ops: []PlanOpResult{{OK: true}, {OK: true}}},
					{NodeID: "b", Ops: []PlanOpResult{{OK: true}, {OK: true}, {OK: true}}},
				},
			},
			want: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateRequestsSaved(tt.result)
			if got != tt.want {
				t.Fatalf("estimateRequestsSaved() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEstimateRequestsSavedUsesExecutionCountAfterTranscriptFitting(t *testing.T) {
	t.Parallel()
	result := &PlanRunResult{
		Transcript:         []PlanNodeResult{{NodeID: "retained", Ops: []PlanOpResult{{OK: true}}}},
		executedNodes:      16,
		executedOperations: maxPlanOperations,
	}
	if got, want := estimateRequestsSaved(result), maxPlanOperations-1; got != want {
		t.Fatalf("estimateRequestsSaved() = %d, want %d", got, want)
	}
	if got, want := planRunNodeCount(result), 16; got != want {
		t.Fatalf("planRunNodeCount() = %d, want %d", got, want)
	}
}

func TestRecordPlanStats(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)
	path, err := statsFilePath()
	if err != nil {
		t.Fatalf("statsFilePath() error: %v", err)
	}
	statsDir := filepath.Dir(path)
	if err = os.MkdirAll(statsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error: %v", statsDir, err)
	}
	if err = os.Chmod(statsDir, 0o755); err != nil {
		t.Fatalf("Chmod(%q) error: %v", statsDir, err)
	}
	if err = os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error: %v", path, err)
	}
	if err = os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod(%q) error: %v", path, err)
	}

	result := &PlanRunResult{
		Transcript: []PlanNodeResult{
			{NodeID: "a", Ops: []PlanOpResult{{OK: true}, {OK: true}}},
		},
		StoppedReason:  StopLeaf,
		DepthReached:   1,
		EdgesEvaluated: 2,
		EdgesMatched:   1,
	}

	recordPlanStats(result)
	recordPlanStats(result)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error: %v", path, err)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat(%q) error: %v", filepath.Dir(path), err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("stats directory permissions = %o, want 700", got)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error: %v", path, err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("stats file permissions = %o, want 600", got)
	}

	lines := splitNonEmptyLines(string(data))
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (raw: %q)", len(lines), string(data))
	}

	for _, line := range lines {
		var rec PlanStatsRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("json.Unmarshal(%q) error: %v", line, err)
		}
		if rec.V != 1 {
			t.Errorf("rec.V = %d, want 1", rec.V)
		}
		if rec.StoppedReason != string(StopLeaf) {
			t.Errorf("rec.StoppedReason = %q, want %q", rec.StoppedReason, StopLeaf)
		}
		if rec.Nodes != 1 {
			t.Errorf("rec.Nodes = %d, want 1", rec.Nodes)
		}
		if rec.Ops != 2 {
			t.Errorf("rec.Ops = %d, want 2", rec.Ops)
		}
		if rec.RequestsSaved != 1 {
			t.Errorf("rec.RequestsSaved = %d, want 1", rec.RequestsSaved)
		}
	}
}

func splitNonEmptyLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				lines = append(lines, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
