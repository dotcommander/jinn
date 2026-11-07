package jinn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// planAudit is the append-only JSONL trail for one run_plan execution: one
// event per completed node plus a final run event. It is best-effort — a
// workspace that cannot hold the audit file must never break plan execution,
// so creation failure yields a nil audit and append failures are swallowed.
type planAudit struct {
	file *os.File
	path string // workspace-relative, reported as AuditPath
	seq  int
}

var planAuditCounter atomic.Uint64

// newPlanAudit creates <workDir>/.jinn/plan-runs/<run-id>.jsonl. Returns nil
// (no error) when the audit file cannot be created.
func newPlanAudit(workDir string) *planAudit {
	if workDir == "" {
		return nil
	}
	runID := fmt.Sprintf("%d-%d-%d", time.Now().UnixNano(), os.Getpid(), planAuditCounter.Add(1))
	rel := filepath.Join(".jinn", "plan-runs", runID+".jsonl")
	full := filepath.Join(workDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return nil
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	return &planAudit{file: f, path: rel}
}

// planAuditEvent is one JSONL line of the audit trail.
type planAuditEvent struct {
	Seq        int            `json:"seq"`
	Time       string         `json:"time"`
	Type       string         `json:"type"` // "node" | "run"
	NodeID     string         `json:"node_id,omitempty"`
	Depth      int            `json:"depth,omitempty"`
	Ops        []PlanOpResult `json:"ops,omitempty"`
	PathTaken  []string       `json:"path_taken,omitempty"`
	Stopped    StopReason     `json:"stopped_reason,omitempty"`
	ExpectFail *ExpectFailure `json:"expect_failure,omitempty"`
}

func (a *planAudit) appendEvent(ev planAuditEvent) {
	if a == nil {
		return
	}
	a.seq++
	ev.Seq = a.seq
	ev.Time = time.Now().UTC().Format(time.RFC3339Nano)
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = a.file.Write(append(line, '\n'))
}

func (a *planAudit) nodeEvent(nodeResult PlanNodeResult, _, _ int) {
	a.appendEvent(planAuditEvent{
		Type: "node", NodeID: nodeResult.NodeID, Depth: nodeResult.Depth, Ops: nodeResult.Ops,
	})
}

func (a *planAudit) runEvent(result *PlanRunResult) {
	if result == nil {
		return
	}
	a.appendEvent(planAuditEvent{
		Type: "run", PathTaken: result.PathTaken, Stopped: result.StoppedReason, ExpectFail: result.ExpectFailure,
	})
}

func (a *planAudit) close() {
	if a == nil {
		return
	}
	_ = a.file.Close()
}

func (a *planAudit) relPath() string {
	if a == nil {
		return ""
	}
	return a.path
}
