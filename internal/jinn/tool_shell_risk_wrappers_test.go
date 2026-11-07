package jinn

import (
	"context"
	"testing"
)

func TestShellRisk_MultiplexerWrappedDangerousCommand(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "busybox rm -rf /tmp/jinntest",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for multiplexer-wrapped rm, got %q", got)
	}
}

func TestShellRisk_EnvSplitStringDangerousCommand(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "env -S 'rm -rf /tmp/jinntest'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for env split-string rm, got %q", got)
	}
}

func TestShellRisk_NewlineSeparatedDangerousCommand(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "echo ok\nrm -rf /tmp/jinntest",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for newline-separated rm, got %q", got)
	}
}

func TestShellRisk_BackgroundSeparatedDangerousCommand(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "echo ok & rm -rf /tmp/jinntest",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for background-separated rm, got %q", got)
	}
}

func TestShellRisk_DynamicCommandExpansionIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "cmd='rm -rf /tmp/jinntest'; $cmd",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for dynamic command expansion, got %q", got)
	}
}

func TestShellRisk_EmbeddedDynamicCommandExpansionIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "rm${IFS}-rf /tmp/jinntest",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for embedded dynamic command expansion, got %q", got)
	}
}

func TestShellRisk_NegatedDangerousCommand(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "! rm -rf /tmp/jinntest",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for negated rm, got %q", got)
	}
}

func TestShellRisk_CaseBranchDangerousCommand(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "case x in x) rm -rf /tmp/jinntest ;; esac",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for case branch rm, got %q", got)
	}
}

func TestShellRisk_CoprocDangerousCommand(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "coproc rm -rf /tmp/jinntest",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for coproc rm, got %q", got)
	}
}

func TestShellRisk_EvalIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "eval 'echo hello'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for eval, got %q", got)
	}
}

func TestShellRisk_DangerousAliasDefinitionIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "alias nuke='rm -rf /tmp/jinntest'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for dangerous alias definition, got %q", got)
	}
}

func TestShellRisk_DangerousHashBindingIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "hash -p /bin/rm nuke; nuke -rf /tmp/jinntest",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for dangerous hash binding, got %q", got)
	}
}

func TestShellRisk_BuiltinEvalIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "builtin eval 'rm -rf /tmp/jinntest'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for builtin eval, got %q", got)
	}
}

func TestShellRisk_ProcessSubstitutionIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "cat <(rm -rf /tmp/jinntest)",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for process substitution, got %q", got)
	}
}

func TestShellRisk_DoubleQuotedCommandSubstitutionIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", `echo "$(rm -rf /tmp/jinntest)"`,
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for double-quoted command substitution, got %q", got)
	}
}

func TestShellRisk_FunctionBodyIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "cleanup(){ rm -rf /tmp/jinntest; }; cleanup",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for shell function body, got %q", got)
	}
}
