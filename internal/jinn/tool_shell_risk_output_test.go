package jinn

import (
	"context"
	"strings"
	"testing"
)

func TestShellRisk_SafeCommand_Allowed(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result, meta, err := e.runShell(context.Background(), args("command", "echo hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "safe" {
		t.Errorf("expected risk=safe, got %q", got)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected output, got: %s", result)
	}
}

func TestShellRisk_OutputRedirectionIsCaution(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "echo hello > out.txt",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "caution" {
		t.Errorf("expected risk=caution for output redirection, got %q", got)
	}
}

func TestShellRisk_DeviceOutputRedirectionIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "cat image.iso > /dev/sda",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for device output redirection, got %q", got)
	}
}

func TestShellRisk_CurlDeviceOutputIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "curl https://example.com/image.iso -o /dev/sda",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for curl device output, got %q", got)
	}
}

func TestShellRisk_TarDeviceOutputIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "tar -cf /dev/sda src/",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for tar device output, got %q", got)
	}
}

func TestShellRisk_TeeDeviceTargetIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "cat image.iso | tee /dev/sda",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for tee device target, got %q", got)
	}
}

func TestShellRisk_ReadWriteRedirectionIsCaution(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "cat <> /tmp/jinntest-redir",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "caution" {
		t.Errorf("expected risk=caution for read/write redirection, got %q", got)
	}
}

func TestShellRisk_InterpreterInputRedirectionIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "python3 < /tmp/jinntest-script.py",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for interpreter input redirection, got %q", got)
	}
}

func TestShellRisk_CompactInterpreterInputRedirectionIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "python3</tmp/jinntest-script.py",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for compact interpreter input redirection, got %q", got)
	}
}

func TestShellRisk_TimeoutWrappedDangerousCommandBlocked(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args("command", "timeout 30 rm -rf /tmp/jinntest"))
	if err == nil {
		t.Fatal("expected error for timeout-wrapped dangerous command")
	}
	if !strings.Contains(err.Error(), "blocked by risk classifier") {
		t.Errorf("expected blocked message, got: %v", err)
	}
	if got := metaString(meta, "risk"); got != RiskDangerous.String() {
		t.Errorf("risk = %q, want %q", got, RiskDangerous)
	}
}

func TestShellRisk_TimeDeviceOutputBlocked(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args("command", "/usr/bin/time -o /dev/./sda echo ok"))
	if err == nil {
		t.Fatal("expected error for time output to a device")
	}
	if !strings.Contains(err.Error(), "blocked by risk classifier") {
		t.Errorf("expected blocked message, got: %v", err)
	}
	if got := metaString(meta, "risk"); got != RiskDangerous.String() {
		t.Errorf("risk = %q, want %q", got, RiskDangerous)
	}
}

func TestShellRisk_CurlEtagDeviceOutputBlocked(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args("command", "curl --etag-save /dev/./sda https://example.com"))
	if err == nil {
		t.Fatal("expected error for curl etag output to a device")
	}
	if !strings.Contains(err.Error(), "blocked by risk classifier") {
		t.Errorf("expected blocked message, got: %v", err)
	}
	if got := metaString(meta, "risk"); got != RiskDangerous.String() {
		t.Errorf("risk = %q, want %q", got, RiskDangerous)
	}
}

func TestShellRisk_CurlWriteOutDeviceBlocked(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args("command", "curl -w '%output{/dev/./sda}' https://example.com"))
	if err == nil {
		t.Fatal("expected error for curl write-out to a device")
	}
	if !strings.Contains(err.Error(), "blocked by risk classifier") {
		t.Errorf("expected blocked message, got: %v", err)
	}
	if got := metaString(meta, "risk"); got != RiskDangerous.String() {
		t.Errorf("risk = %q, want %q", got, RiskDangerous)
	}
}

func TestShellRisk_PHPBeginCodeBlocked(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args("command", "php -B 'echo 1;'"))
	if err == nil {
		t.Fatal("expected error for PHP begin code")
	}
	if !strings.Contains(err.Error(), "blocked by risk classifier") {
		t.Errorf("expected blocked message, got: %v", err)
	}
	if got := metaString(meta, "risk"); got != RiskDangerous.String() {
		t.Errorf("risk = %q, want %q", got, RiskDangerous)
	}
}

func TestShellRisk_InterpreterStdinAliasBlocked(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args("command", "python3 -W ignore /dev/stdin"))
	if err == nil {
		t.Fatal("expected error for interpreter stdin alias")
	}
	if !strings.Contains(err.Error(), "blocked by risk classifier") {
		t.Errorf("expected blocked message, got: %v", err)
	}
	if got := metaString(meta, "risk"); got != RiskDangerous.String() {
		t.Errorf("risk = %q, want %q", got, RiskDangerous)
	}
}

func TestShellRisk_PathWrappedDangerousCommand(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "env -i /bin/rm -rf /tmp/jinntest",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for wrapped absolute rm, got %q", got)
	}
}

func TestShellRisk_UnlinkIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "unlink /tmp/jinntest",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for unlink, got %q", got)
	}
}

func TestShellRisk_TruncateIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "truncate -s 0 /tmp/jinntest",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for truncate, got %q", got)
	}
}

func TestShellRisk_ChmodDeviceIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "chmod 777 /dev/sda",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for chmod device target, got %q", got)
	}
}

func TestShellRisk_RsyncDeleteIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "rsync -a --delete src/ dest/",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for rsync delete mode, got %q", got)
	}
}

func TestShellRisk_RsyncRemoveSourceFilesIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "rsync -a --remove-source-files src/ dest/",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for rsync remove-source-files, got %q", got)
	}
}

func TestShellRisk_DockerVolumeRemovalIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "docker volume rm important-data",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for docker volume removal, got %q", got)
	}
}

func TestShellRisk_KubectlDeleteIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "kubectl delete namespace prod",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for kubectl delete, got %q", got)
	}
}

func TestShellRisk_DestructiveSQLIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "psql -c 'DROP DATABASE prod'",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for destructive SQL, got %q", got)
	}
}

func TestShellRisk_TerraformDestroyIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "terraform destroy -auto-approve",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for terraform destroy, got %q", got)
	}
}

func TestShellRisk_PulumiDestroyIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "pulumi --non-interactive destroy --yes",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for pulumi destroy, got %q", got)
	}
}

func TestShellRisk_GitHubAPIDeleteIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "gh api -X DELETE repos/owner/repo",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for gh api DELETE, got %q", got)
	}
}

func TestShellRisk_PackagePurgeIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "apt-get purge postgresql -y",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for package purge, got %q", got)
	}
}

func TestShellRisk_LanguagePackageUninstallIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "pip uninstall requests -y",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for language package uninstall, got %q", got)
	}
}

func TestShellRisk_AzureGroupDeleteIsDangerous(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args(
		"command", "az group delete --name prod --yes",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous for az group delete, got %q", got)
	}
}
