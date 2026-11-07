package jinn

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func compileRules(t *testing.T, rules ...string) *launchPolicy {
	t.Helper()
	raw := make([]json.RawMessage, 0, len(rules))
	for _, r := range rules {
		raw = append(raw, json.RawMessage(r))
	}
	p, err := compileLaunchPolicy(raw)
	if err != nil {
		t.Fatalf("compileLaunchPolicy: %v", err)
	}
	return p
}

func TestLaunchPolicy_ExactMatchNoNormalization(t *testing.T) {
	t.Parallel()
	p := compileRules(t,
		`{"command":"npm test","decision":"allow"}`,
		`{"command":"rm -rf build","decision":"deny"}`,
	)
	if got := p.evaluate("npm test"); got != launchAllow {
		t.Errorf("evaluate(npm test) = %q, want allow", got)
	}
	if got := p.evaluate("rm -rf build"); got != launchDeny {
		t.Errorf("evaluate(rm -rf build) = %q, want deny", got)
	}
	// Opaque strings: whitespace variants do not match and fall to ask.
	if got := p.evaluate(" npm test"); got != launchAsk {
		t.Errorf("evaluate( npm test) = %q, want ask", got)
	}
	if got := p.evaluate("npm  test"); got != launchAsk {
		t.Errorf("evaluate(npm  test) = %q, want ask", got)
	}
	if got := p.evaluate("unknown"); got != launchAsk {
		t.Errorf("evaluate(unknown) = %q, want ask", got)
	}
}

func TestLaunchPolicy_PrecedenceDenyOverAskOverAllow(t *testing.T) {
	t.Parallel()
	p := compileRules(t,
		`{"command":"git status","decision":"allow"}`,
		`{"command":"git status","decision":"ask"}`,
		`{"command":"deploy","decision":"deny"}`,
		`{"command":"deploy","decision":"allow"}`,
		`{"command":"deploy","decision":"ask"}`,
	)
	if got := p.evaluate("git status"); got != launchAsk {
		t.Errorf("evaluate(git status) = %q, want ask", got)
	}
	if got := p.evaluate("deploy"); got != launchDeny {
		t.Errorf("evaluate(deploy) = %q, want deny", got)
	}
}

func TestLaunchPolicy_RejectsMalformedRules(t *testing.T) {
	t.Parallel()
	bad := []string{
		`{"command":"npm test","decision":"permit"}`,
		`{"decision":"allow"}`,
		`{"command":"","decision":"allow"}`,
		`{"command":"npm test"}`,
		`{"command":"npm test","decision":"allow","extra":true}`,
		`null`,
		`"npm test"`,
	}
	for _, rule := range bad {
		if _, err := compileLaunchPolicy([]json.RawMessage{json.RawMessage(rule)}); err == nil {
			t.Errorf("compileLaunchPolicy(%s): expected error, got nil", rule)
		}
	}
}

func TestLaunchPolicy_SnapshotsRules(t *testing.T) {
	t.Parallel()
	raw := []json.RawMessage{json.RawMessage(`{"command":"deploy","decision":"deny"}`)}
	p, err := compileLaunchPolicy(raw)
	if err != nil {
		t.Fatalf("compileLaunchPolicy: %v", err)
	}
	// Mutating the source bytes after compile must not change the policy.
	raw[0] = json.RawMessage(`{"command":"deploy","decision":"allow"}`)
	if got := p.evaluate("deploy"); got != launchDeny {
		t.Errorf("evaluate(deploy) after source mutation = %q, want deny", got)
	}
}

func TestRunShell_LaunchPolicyDenyOverridesForce(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	e.launchPolicy = compileRules(t,
		`{"command":"echo hi","decision":"allow"}`,
		`{"command":"rm -rf build","decision":"deny"}`,
	)
	// (a) Denied command is blocked even with force: true — the advantage
	// over the risk classifier, which would allow a dangerous command under
	// force.
	_, meta, err := e.runShell(context.Background(), args("command", "rm -rf build", "force", true))
	if err == nil {
		t.Fatal("expected launch-policy deny error, got nil")
	}
	if !strings.Contains(err.Error(), "launch policy") {
		t.Errorf("error = %q, want launch policy block", err)
	}
	if got := metaString(meta, "launch_policy"); got != "deny" {
		t.Errorf("launch_policy meta = %q, want deny", got)
	}
	// (b) Allowed command runs.
	result, meta, err := e.runShell(context.Background(), args("command", "echo hi"))
	if err != nil {
		t.Fatalf("allowed command failed: %v", err)
	}
	if !strings.Contains(result, "hi") {
		t.Errorf("unexpected result for allowed command: %s", result)
	}
	if got := metaString(meta, "launch_policy"); got != "allow" {
		t.Errorf("launch_policy meta = %q, want allow", got)
	}
	// (c) Unmatched command blocks fail-closed even though it is risk-safe.
	_, meta, err = e.runShell(context.Background(), args("command", "echo yo"))
	if err == nil {
		t.Fatal("expected launch-policy ask block for unmatched command, got nil")
	}
	if !strings.Contains(err.Error(), "confirmation") {
		t.Errorf("error = %q, want ask/confirmation block", err)
	}
	if got := metaString(meta, "launch_policy"); got != "ask" {
		t.Errorf("launch_policy meta = %q, want ask", got)
	}
}

func TestRunShell_NoLaunchPolicy_Unchanged(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	// No policy loaded: echo runs as before.
	result, meta, err := e.runShell(context.Background(), args("command", "echo hi"))
	if err != nil {
		t.Fatalf("unexpected error without policy: %v", err)
	}
	if !strings.Contains(result, "hi") {
		t.Errorf("unexpected result: %s", result)
	}
	if got := metaString(meta, "launch_policy"); got != "" {
		t.Errorf("launch_policy meta = %q, want empty without policy", got)
	}
}

// TestLoadLaunchPolicy_FileRoundTrip exercises the global file loading path.
// It mutates process-global env (JINN_CONFIG_DIR), so it runs serially with
// a one-line justification per the testing rules.
func TestLoadLaunchPolicy_FileRoundTrip(t *testing.T) {
	// Serial: t.Setenv mutates process-global environment.
	base := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", base)
	dir := filepath.Join(base, "jinn")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"launchPolicy":[{"command":"npm test","decision":"allow"},{"command":"rm -rf build","decision":"deny"}]}`
	if err := os.WriteFile(filepath.Join(dir, "launch-policy.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := loadLaunchPolicy()
	if err != nil {
		t.Fatalf("loadLaunchPolicy: %v", err)
	}
	if p == nil {
		t.Fatal("expected policy, got nil")
	}
	if got := p.evaluate("npm test"); got != launchAllow {
		t.Errorf("evaluate(npm test) = %q, want allow", got)
	}
	if got := p.evaluate("rm -rf build"); got != launchDeny {
		t.Errorf("evaluate(rm -rf build) = %q, want deny", got)
	}
}

func TestLoadLaunchPolicy_MissingFileIsNoPolicy(t *testing.T) {
	// Serial: t.Setenv mutates process-global environment.
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	p, err := loadLaunchPolicy()
	if err != nil {
		t.Fatalf("loadLaunchPolicy: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil policy for missing file, got %+v", p)
	}
}

func TestLoadLaunchPolicy_MalformedFileFails(t *testing.T) {
	// Serial: t.Setenv mutates process-global environment.
	base := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", base)
	dir := filepath.Join(base, "jinn")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "launch-policy.json"), []byte(`{"launchPolicy":[{"command":"x","decision":"permit"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLaunchPolicy(); err == nil {
		t.Fatal("expected error for malformed policy file, got nil")
	}
}
