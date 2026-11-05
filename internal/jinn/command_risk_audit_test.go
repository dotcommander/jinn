package jinn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditC301GitDiffOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, command string
		want          RiskLevel
	}{
		{"plain diff", "git diff", RiskSafe},
		{"equals output", "git diff --output=existing.txt", RiskCaution},
		{"separate output", "git diff --output existing.txt", RiskCaution},
		{"setup flag", "git -C . diff --output=existing.txt", RiskCaution},
		{"show output", "git show --output=existing.txt", RiskCaution},
		{"log output", "git log --output existing.txt", RiskCaution},
		{"pathspec", "git diff -- --output=existing.txt", RiskSafe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			level, reason := ClassifyCommand(tc.command)
			if level != tc.want {
				t.Errorf("ClassifyCommand(%q) = %v (%s), want %v", tc.command, level, reason, tc.want)
			}
		})
	}

	for _, flag := range []string{"--output=existing.txt", "--output existing.txt"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			e, dir := testEngine(t)
			file := filepath.Join(dir, "existing.txt")
			if err := os.WriteFile(file, []byte("unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}
			plan := &PlanTree{
				Root: "n1", Cwd: dir,
				Nodes: []PlanNode{{ID: "n1", Commands: []PlanOp{{Shell: "git diff " + strings.Replace(flag, "existing.txt", file, 1)}}}},
			}
			result, err := e.runPlanTree(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			if result.StoppedReason != StopMutationBlocked || len(result.Transcript) != 1 || len(result.Transcript[0].Ops) != 1 || result.Transcript[0].Ops[0].OK {
				t.Errorf("wanted blocked shell operation, got %+v", result)
			}
			data, err := os.ReadFile(file)
			if err != nil || string(data) != "unchanged" {
				t.Errorf("target modified: %q, %v", data, err)
			}
		})
	}
}
