package jinn

import (
	"context"
	"testing"
)

func TestShellRisk_FindExecShellIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "find /tmp -maxdepth 0 -exec sh -c 'rm -rf /tmp/jinntest' \\;",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for find -exec shell payload, got %q", got)
	}
}

func TestShellRisk_FindOkShellIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "find /tmp -maxdepth 0 -ok sh -c 'rm -rf /tmp/jinntest' \\;",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for find -ok shell payload, got %q", got)
	}
}

func TestShellRisk_FindFileOutputIsCaution(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "find /tmp -maxdepth 0 -fprint /tmp/jinntest-out",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "caution" {
		t.Errorf("expected risk=caution for find file output, got %q", got)
	}
}

func TestShellRisk_XargsShellIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "printf x | xargs sh -c 'rm -rf /tmp/jinntest'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for xargs shell payload, got %q", got)
	}
}

func TestShellRisk_GitShellAliasIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "git -c alias.nuke='!rm -rf /tmp/jinntest' nuke",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for git shell alias, got %q", got)
	}
}

func TestShellRisk_PythonInlineCodeIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "python3 -c 'import os; os.remove(\"/tmp/jinntest\")'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for python inline code, got %q", got)
	}
}

func TestShellRisk_AttachedPythonInlineCodeIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "python3 -c'import os; os.remove(\"/tmp/jinntest\")'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for attached python inline code, got %q", got)
	}
}

func TestShellRisk_BundledPerlInlineCodeIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "perl -ne'unlink \"/tmp/jinntest\"'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for bundled perl inline code, got %q", got)
	}
}

func TestShellRisk_AlternateShellInlineCommandIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "dash -c 'rm -rf /tmp/jinntest'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for alternate shell inline command, got %q", got)
	}
}

func TestShellRisk_PowerShellInlineCommandIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "pwsh -Command 'Remove-Item -Recurse -Force /tmp/jinntest'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for powershell inline command, got %q", got)
	}
}

func TestShellRisk_QuotedPythonInlineFlagIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "python3 '-c' 'import os; os.remove(\"/tmp/jinntest\")'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for quoted python inline flag, got %q", got)
	}
}

func TestShellRisk_PythonHeredocIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "python3 <<EOF\nimport os\nos.remove(\"/tmp/jinntest\")\nEOF\n",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for python heredoc code, got %q", got)
	}
}
