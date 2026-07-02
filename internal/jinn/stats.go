package jinn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// PlanStatsRecord is a single row in the run_plan stats JSONL log.
type PlanStatsRecord struct {
	V              int    `json:"v"`
	Ts             string `json:"ts"`
	StoppedReason  string `json:"stopped_reason"`
	DepthReached   int    `json:"depth_reached"`
	Nodes          int    `json:"nodes"`
	Ops            int    `json:"ops"`
	EdgesEvaluated int    `json:"edges_evaluated"`
	EdgesMatched   int    `json:"edges_matched"`
	RequestsSaved  int    `json:"requests_saved"`
}

// statsFilePath resolves the run_plan stats JSONL path: JINN_CONFIG_DIR when
// set, else os.UserConfigDir(), joined with jinn/stats/run_plan.jsonl.
func statsFilePath() (string, error) {
	base := os.Getenv("JINN_CONFIG_DIR")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("stats: resolve config dir: %w", err)
		}
		base = dir
	}
	return filepath.Join(base, "jinn", "stats", "run_plan.jsonl"), nil
}

// estimateRequestsSaved approximates the number of individual tool-call
// round-trips avoided by batching into a single run_plan call: total ops
// executed across the visited path, minus the one round-trip run_plan itself
// cost, floored at 0.
func estimateRequestsSaved(result *PlanRunResult) int {
	if result == nil {
		return 0
	}
	total := 0
	for _, n := range result.Transcript {
		total += len(n.Ops)
	}
	saved := total - 1
	if saved < 0 {
		return 0
	}
	return saved
}

// recordPlanStats fire-and-forget appends a stats row for a completed
// run_plan call. All errors are swallowed — stats are best-effort and must
// never affect the caller's result.
func recordPlanStats(result *PlanRunResult) {
	if result == nil {
		return
	}
	path, err := statsFilePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	total := 0
	for _, n := range result.Transcript {
		total += len(n.Ops)
	}

	rec := PlanStatsRecord{
		V:              1,
		Ts:             time.Now().UTC().Format(time.RFC3339),
		StoppedReason:  string(result.StoppedReason),
		DepthReached:   result.DepthReached,
		Nodes:          len(result.Transcript),
		Ops:            total,
		EdgesEvaluated: result.EdgesEvaluated,
		EdgesMatched:   result.EdgesMatched,
		RequestsSaved:  estimateRequestsSaved(result),
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(data, '\n'))
}
