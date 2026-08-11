package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/dotcommander/jinn/internal/mcpsnapshot"
	"github.com/voocel/mcp-sdk-go/protocol"
)

func TestParseMCPExplorerRegisteredAlias(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	if err := mcpexplore.SaveRegistry(mcpexplore.Registry{Version: 1, Servers: map[string]mcpexplore.Server{
		"remote": {Transport: "http", URL: "https://example.test/mcp", TokenEnv: "REMOTE_TOKEN"},
	}}); err != nil {
		t.Fatal(err)
	}
	config, err := parseMCPExplorer([]string{"inspect", "@remote", "search"})
	if err != nil {
		t.Fatal(err)
	}
	if config.tool != "search" || config.target.Endpoint != "https://example.test/mcp" || config.target.TokenEnv != "REMOTE_TOKEN" {
		t.Fatalf("registered config = %#v", config)
	}
}

func TestMCPServersAddRemoveUsesIsolatedRegistry(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	if addErr := runMCPServersAdd([]string{"local", "--stdio", "/bin/echo", "--arg", "hello", "--pass-env", "SERVER_TOKEN"}); addErr != nil {
		t.Fatal(addErr)
	}
	registry, exists, loadErr := mcpexplore.LoadRegistry()
	if loadErr != nil || !exists || registry.Servers["local"].Command != "/bin/echo" {
		t.Fatalf("registry = %#v, exists=%v, err=%v", registry, exists, loadErr)
	}
	if addErr := runMCPServersAdd([]string{"local", "--stdio", "/bin/echo"}); addErr == nil {
		t.Fatal("duplicate add succeeded without --replace")
	}
	if replaceErr := runMCPServersAdd([]string{"local", "--stdio", "/bin/printf", "--replace"}); replaceErr != nil {
		t.Fatal(replaceErr)
	}
	registry, _, loadErr = mcpexplore.LoadRegistry()
	if loadErr != nil || registry.Servers["local"].Command != "/bin/printf" {
		t.Fatalf("registry after replace = %#v, %v", registry, loadErr)
	}
	if removeErr := runMCPServersRemove([]string{"local"}); removeErr != nil {
		t.Fatal(removeErr)
	}
	registry, _, loadErr = mcpexplore.LoadRegistry()
	if loadErr != nil || len(registry.Servers) != 0 {
		t.Fatalf("registry after remove = %#v, %v", registry, loadErr)
	}
}

func TestParseMCPServerRegistrationErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"local", "--stdio"}, want: "mcp servers add requires NAME and exactly one of --http URL or --stdio PATH"},
		{args: []string{"local", "--stdio", "/bin/echo", "--http", "https://example.test/mcp"}, want: "use exactly one of --http or --stdio"},
		{args: []string{"local", "--stdio", "/bin/echo", "--unknown"}, want: `unknown mcp servers add option "--unknown"`},
	} {
		registration, err := parseMCPServerRegistration(test.args)
		if err == nil || err.Error() != test.want {
			t.Fatalf("parseMCPServerRegistration(%q) = %#v, %v, want %q", test.args, registration, err, test.want)
		}
	}
}

func TestParseMCPDoctorSuccess(t *testing.T) {
	t.Parallel()

	config, err := parseMCPDoctor([]string{"@local", "--timeout=5s"})
	if err != nil || !config.live || config.all || len(config.aliases) != 1 || config.aliases[0] != "local" || config.timeout != 5*time.Second {
		t.Fatalf("doctor config = %#v, %v", config, err)
	}
	config, err = parseMCPDoctor([]string{"--all", "--timeout", "10s"})
	if err != nil || !config.live || !config.all || config.aliases != nil || config.timeout != 10*time.Second {
		t.Fatalf("doctor all config = %#v, %v", config, err)
	}
}

func TestParseMCPDoctorErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		args        []string
		want        string
		wantTimeout time.Duration
	}{
		{args: []string{"--timeout"}, want: "--timeout requires a value", wantTimeout: mcpExplorerDefaultTimeout},
		{args: []string{"--timeout=0"}, want: `invalid --timeout "0"`, wantTimeout: mcpExplorerDefaultTimeout},
		{args: []string{"--timeout", "5s", "--unknown"}, want: `unknown mcp doctor option "--unknown"`, wantTimeout: 5 * time.Second},
		{args: []string{"--all", "@local"}, want: "mcp doctor accepts either @NAME or --all", wantTimeout: mcpExplorerDefaultTimeout},
		{args: []string{"@one", "@two"}, want: "mcp doctor accepts at most one @NAME", wantTimeout: mcpExplorerDefaultTimeout},
	} {
		got, parseErr := parseMCPDoctor(test.args)
		if parseErr == nil || parseErr.Error() != test.want || got.timeout != test.wantTimeout || got.live || got.aliases != nil {
			t.Fatalf("parseMCPDoctor(%q) = %#v, %v, want %q", test.args, got, parseErr, test.want)
		}
	}
}

func TestMCPDoctorOfflineChecksRequiredCredentialAndExecutable(t *testing.T) {
	missing := mcpexplore.Server{Transport: "http", URL: "https://example.test/mcp", TokenEnv: "MISSING_TOKEN"}
	check, failed := doctorOfflineCheck("remote", missing)
	if !failed || check.Credential == nil || check.Credential.Present {
		t.Fatalf("missing credential check = %#v, failed=%v", check, failed)
	}
	badCommand := mcpexplore.Server{Transport: "stdio", Command: "/definitely/not/a/command"}
	check, failed = doctorOfflineCheck("local", badCommand)
	if !failed || check.Executable == "" {
		t.Fatalf("missing command check = %#v, failed=%v", check, failed)
	}
	if os.Getenv("MISSING_TOKEN") != "" {
		t.Fatal("test environment unexpectedly provides MISSING_TOKEN")
	}
	withMissingEnv := mcpexplore.Server{Transport: "stdio", Command: "/bin/echo", PassEnv: []string{"P4_MISSING_ENV"}}
	check, failed = doctorOfflineCheck("local", withMissingEnv)
	if !failed || len(check.Environment) != 1 || check.Environment[0].Present {
		t.Fatalf("missing pass_env check = %#v, failed=%v", check, failed)
	}
}

func TestMCPDoctorChecksBackupPermissions(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	registry := mcpexplore.Registry{Version: 1, Servers: map[string]mcpexplore.Server{
		"local": {Transport: "stdio", Command: "/bin/echo"},
	}}
	if err := mcpexplore.SaveRegistry(registry); err != nil {
		t.Fatal(err)
	}
	if err := mcpexplore.UpdateRegistry(func(current *mcpexplore.Registry) error { return nil }); err != nil {
		t.Fatal(err)
	}
	path, pathErr := mcpexplore.RegistryPath()
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if chmodErr := os.Chmod(path+".bak", 0o644); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	warnings, warningErr := mcpRegistryPermissionWarnings()
	if warningErr != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "servers.json.bak") {
		t.Fatalf("permission warnings = %#v, %v", warnings, warningErr)
	}
}

func TestMCPDoctorSnapshotOutcomes(t *testing.T) {
	identity := &protocol.Implementation{Name: "fixture", Version: "1"}
	cleanTools := []*protocol.Tool{{Name: "tool", Description: "safe", InputSchema: protocol.JSONSchema{"type": "object"}}}

	t.Run("success", func(t *testing.T) {
		saveMCPDoctorSnapshotFixture(t, identity, cleanTools)
		outcome := doctorSnapshotWarnings("local", identity, cleanTools)
		if outcome.failed || outcome.approvalDrift || len(outcome.warnings) != 0 {
			t.Fatalf("success outcome = %#v", outcome)
		}
	})

	t.Run("warning", func(t *testing.T) {
		warningTools := []*protocol.Tool{{Name: "tool", Description: "contains <!-- metadata -->", InputSchema: protocol.JSONSchema{"type": "object"}}}
		saveMCPDoctorSnapshotFixture(t, identity, warningTools)
		outcome := doctorSnapshotWarnings("local", identity, warningTools)
		if outcome.failed || outcome.approvalDrift || !containsMCPDoctorWarning(outcome.warnings, "metadata_html_comment") {
			t.Fatalf("warning outcome = %#v", outcome)
		}
	})

	t.Run("drift", func(t *testing.T) {
		saveMCPDoctorSnapshotFixture(t, identity, cleanTools)
		changedTools := []*protocol.Tool{{Name: "changed", Description: "safe", InputSchema: protocol.JSONSchema{"type": "object"}}}
		outcome := doctorSnapshotWarnings("local", identity, changedTools)
		if outcome.failed || !outcome.approvalDrift || !containsMCPDoctorWarning(outcome.warnings, "snapshot_drift") {
			t.Fatalf("drift outcome = %#v", outcome)
		}
	})

	t.Run("operational failure", func(t *testing.T) {
		t.Setenv("JINN_CONFIG_DIR", t.TempDir())
		if err := mcpexplore.SaveRegistry(mcpexplore.Registry{Version: 1, Servers: map[string]mcpexplore.Server{}}); err != nil {
			t.Fatal(err)
		}
		outcome := doctorSnapshotWarnings("local", identity, cleanTools)
		if !outcome.failed || outcome.approvalDrift || !containsMCPDoctorWarning(outcome.warnings, "snapshot_alias_missing") {
			t.Fatalf("operational outcome = %#v", outcome)
		}
	})
}

func saveMCPDoctorSnapshotFixture(t *testing.T, identity *protocol.Implementation, tools []*protocol.Tool) {
	t.Helper()
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	server := mcpexplore.Server{Transport: mcpServerTransportStdio, Command: "/bin/echo"}
	if err := mcpexplore.SaveRegistry(mcpexplore.Registry{Version: 1, Servers: map[string]mcpexplore.Server{"local": server}}); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := mcpsnapshot.Build("local", server, identity, tools, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := mcpsnapshot.Save(snapshot); err != nil {
		t.Fatal(err)
	}
}

func containsMCPDoctorWarning(warnings []string, target string) bool {
	for _, warning := range warnings {
		if warning == target {
			return true
		}
	}
	return false
}
