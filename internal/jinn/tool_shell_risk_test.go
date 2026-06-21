package jinn

import (
	"context"
	"errors"
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

func TestShellRisk_DangerousCommand_Blocked(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args("command", "rm -rf /tmp/jinntest"))
	if err == nil {
		t.Fatal("expected error for dangerous command")
	}
	if !strings.Contains(err.Error(), "blocked by risk classifier") {
		t.Errorf("expected blocked message, got: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous in meta, got %q", got)
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ErrWithSuggestion, got %T", err)
	}
	if !strings.Contains(sErr.Suggestion, "force:true") {
		t.Errorf("expected force:true in suggestion, got: %s", sErr.Suggestion)
	}
}

func TestShellRisk_DangerousCommand_Force(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Create a file to rm so the command actually runs.
	writeTestFile(t, dir, "canary.txt", "delete me")
	result, meta, err := e.runShell(context.Background(), args(
		"command", "rm "+dir+"/canary.txt",
		"force", true,
	))
	if err != nil {
		t.Fatalf("unexpected error with force=true: %v", err)
	}
	if got := metaString(meta, "risk"); got != "dangerous" {
		t.Errorf("expected risk=dangerous, got %q", got)
	}
	if !strings.Contains(result, "[exit: 0]") {
		t.Errorf("expected successful exit, got: %s", result)
	}
}

func TestShellRisk_MetaClassification(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, meta, err := e.runShell(context.Background(), args("command", "echo ok"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metaString(meta, "classification"); got != "success" {
		t.Errorf("expected classification=success, got %q", got)
	}
}

func TestDispatch_MemoryRoute(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e, _ := testEngine(t)
	// list action requires no key — just verify the route reaches memoryTool.
	result, _, err := e.Dispatch(context.Background(), "memory", args("action", "list"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "keys") {
		t.Errorf("expected keys field in list result, got: %s", result.Text)
	}
}
