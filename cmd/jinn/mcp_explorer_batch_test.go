package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/jinn/internal/mcpbatch"
	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/dotcommander/jinn/internal/mcpsnapshot"
	"github.com/voocel/mcp-sdk-go/protocol"
)

func TestParseMCPBatch(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
		want mcpBatchConfig
	}{
		{
			name: "inline and separated values",
			args: []string{"@local", "--file=batch.json", "--timeout", "45s", "--call-timeout=5s", "--format", "human", "--dry-run"},
			want: mcpBatchConfig{alias: "local", file: "batch.json", timeout: 45 * time.Second, callTimeout: 5 * time.Second, format: "human", dryRun: true},
		},
		{
			name: "separated file and inline durations",
			args: []string{"--file", "-", "--timeout=1m", "--call-timeout", "30s", "@remote"},
			want: mcpBatchConfig{alias: "remote", file: "-", timeout: time.Minute, callTimeout: 30 * time.Second},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config, err := parseMCPBatch(test.args)
			if err != nil {
				t.Fatal(err)
			}
			if config != test.want {
				t.Fatalf("batch config = %#v, want %#v", config, test.want)
			}
		})
	}

	for _, test := range []struct {
		name     string
		args     []string
		want     string
		wantFile string
	}{
		{name: "direct endpoint", args: []string{"https://example.test/mcp"}, want: "usage: jinn mcp batch @NAME [--file FILE|-] [--timeout 2m] [--call-timeout 30s] [--dry-run]", wantFile: "-"},
		{name: "invalid duration", args: []string{"@local", "--timeout=0s"}, want: `invalid --timeout "0s"`, wantFile: "-"},
		{name: "missing operand", args: []string{"@local", "--file"}, want: "--file requires a value", wantFile: "-"},
		{name: "unknown inline option", args: []string{"@local", "--unknown=value"}, want: `unknown mcp batch option "--unknown"`, wantFile: "-"},
		{name: "invalid format preserves file", args: []string{"@local", "--file", "batch.json", "--format=xml"}, want: `invalid --format "xml": use json or human`, wantFile: "batch.json"},
		{name: "invalid alias", args: []string{"@bad name"}, want: `invalid MCP server alias "bad name"`, wantFile: "-"},
		{name: "dry run value", args: []string{"@local", "--dry-run=true"}, want: "--dry-run does not accept a value", wantFile: "-"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config, err := parseMCPBatch(test.args)
			if err == nil || err.Error() != test.want {
				t.Fatalf("parseMCPBatch(%q) error = %v, want %q", test.args, err, test.want)
			}
			if config.file != test.wantFile {
				t.Fatalf("parseMCPBatch(%q) partial config = %#v", test.args, config)
			}
		})
	}
}

func TestMCPBatchApprovalMustRemainCurrent(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	identity := &protocol.Implementation{Name: "fixture", Version: "1"}
	initial, _, buildErr := mcpsnapshot.Build("local", mcpexplore.Server{Transport: "stdio", Command: "/bin/fixture"}, identity, []*protocol.Tool{}, time.Now())
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	if saveErr := mcpsnapshot.Save(initial); saveErr != nil {
		t.Fatal(saveErr)
	}
	if approvalErr := requireMCPBatchApprovalCurrent("local", initial); approvalErr != nil {
		t.Fatalf("unchanged approval: %v", approvalErr)
	}
	changed, _, changeErr := mcpsnapshot.Build("local", mcpexplore.Server{Transport: "stdio", Command: "/bin/other"}, identity, []*protocol.Tool{}, time.Now())
	if changeErr != nil {
		t.Fatal(changeErr)
	}
	if saveErr := mcpsnapshot.Save(changed); saveErr != nil {
		t.Fatal(saveErr)
	}
	if approvalErr := requireMCPBatchApprovalCurrent("local", initial); approvalErr == nil || !strings.Contains(approvalErr.Error(), "changed during preflight") {
		t.Fatalf("changed approval error = %v", approvalErr)
	}
	path, pathErr := mcpsnapshot.Path("local")
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if removeErr := os.Remove(path); removeErr != nil {
		t.Fatal(removeErr)
	}
	if approvalErr := requireMCPBatchApprovalCurrent("local", initial); approvalErr == nil || !strings.Contains(approvalErr.Error(), "removed during preflight") {
		t.Fatalf("removed approval error = %v", approvalErr)
	}
}

func TestMCPBatchFailureExitStatus(t *testing.T) {
	if got := cliExitStatus(mcpBatchReportedError{}); got != 2 {
		t.Fatalf("batch failure exit status = %d", got)
	}
}

func TestProjectMCPBatchResultBypassesToolError(t *testing.T) {
	pointer := "/structuredContent/items"
	head := 1
	result := mcpExplorerProjectionTestOutput()
	result.IsError = true
	projected, err := projectMCPBatchResult(&protocol.CallToolResult{Content: result.Content, StructuredContent: result.StructuredContent, IsError: true}, mcpbatch.Call{Select: &pointer, Head: &head})
	if err != nil {
		t.Fatal(err)
	}
	output, ok := projected.(mcpExplorerCallOutput)
	if !ok || !output.IsError {
		t.Fatalf("tool error projection = %#v", projected)
	}
}

func TestProjectMCPBatchResultSelectAndHead(t *testing.T) {
	pointer := "/structuredContent/items"
	head := 1
	result := mcpExplorerProjectionTestOutput()
	projected, err := projectMCPBatchResult(&protocol.CallToolResult{Content: result.Content, StructuredContent: result.StructuredContent}, mcpbatch.Call{Select: &pointer, Head: &head})
	if err != nil {
		t.Fatal(err)
	}
	output, ok := projected.(mcpExplorerProjectionOutput)
	if !ok || output.Projection.TotalItems == nil || *output.Projection.TotalItems != 2 || output.Projection.ReturnedItems == nil || *output.Projection.ReturnedItems != 1 || !output.Projection.Truncated {
		t.Fatalf("projection = %#v", projected)
	}
}
