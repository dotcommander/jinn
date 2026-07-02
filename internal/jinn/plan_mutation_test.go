package jinn

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPlanMutation(t *testing.T) {
	// Serial-only: uses historyEngine which calls t.Setenv.
	t.Run("caution_risk_executes_and_records_undo", func(t *testing.T) {
		e, dir := historyEngine(t)

		targetPath := filepath.Join(dir, "output.txt")
		content := "caution write test"
		plan := &PlanTree{
			Root: "n1",
			Nodes: []PlanNode{
				{
					ID:      "n1",
					Mutates: true,
					Commands: []PlanOp{
						{Tool: "write_file", Args: map[string]any{"path": targetPath, "content": content}},
					},
				},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopLeaf {
			t.Errorf("expected StoppedReason %q, got %q", StopLeaf, result.StoppedReason)
		}

		got, readErr := os.ReadFile(targetPath)
		if readErr != nil {
			t.Fatalf("expected file to exist: %v", readErr)
		}
		if string(got) != content {
			t.Errorf("file content: got %q, want %q", string(got), content)
		}

		hf, loadErr := e.loadHistory()
		if loadErr != nil {
			t.Fatalf("loadHistory: %v", loadErr)
		}
		if len(hf.Entries) != 1 {
			t.Fatalf("history entries: got %d, want 1", len(hf.Entries))
		}
		if hf.Entries[0].Op != "write_file" {
			t.Errorf("history entry op: got %q, want write_file", hf.Entries[0].Op)
		}
	})

	t.Run("dangerous_risk_executes_with_double_force", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)

		victimPath := filepath.Join(dir, "victim.txt")
		if err := os.WriteFile(victimPath, []byte("precious data"), 0o644); err != nil {
			t.Fatal(err)
		}

		plan := &PlanTree{
			Force: true,
			Root:  "n1",
			Nodes: []PlanNode{
				{
					ID:      "n1",
					Mutates: true,
					Force:   true,
					Commands: []PlanOp{
						{Shell: "rm " + victimPath},
					},
				},
			},
		}
		result, err := e.runPlanTree(context.Background(), plan)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.StoppedReason != StopLeaf {
			t.Errorf("expected StoppedReason %q, got %q", StopLeaf, result.StoppedReason)
		}
		if len(result.Transcript) < 1 || len(result.Transcript[0].Ops) < 1 {
			t.Fatalf("expected at least one transcript entry with one op")
		}
		if !result.Transcript[0].Ops[0].OK {
			t.Error("expected op OK == true for dangerous mutation with double force")
		}

		_, statErr := os.Stat(victimPath)
		if !os.IsNotExist(statErr) {
			t.Errorf("expected victim file to be deleted, but it still exists: %v", statErr)
		}
	})
}
