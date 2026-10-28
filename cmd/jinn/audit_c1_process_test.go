package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This helper runs only in an owned subprocess to exercise actual CLI streams.
func TestAuditCLIProcess(t *testing.T) {
	encoded := os.Getenv("JINN_AUDIT_CLI_ARGUMENTS")
	if encoded == "" {
		return
	}
	var arguments []string
	if err := json.Unmarshal([]byte(encoded), &arguments); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"jinn"}, arguments...)
	if err := run(context.Background()); err != nil {
		writeRunError(err)
		os.Exit(cliExitStatus(err))
	}
	os.Exit(0)
}

func runAuditCLI(t *testing.T, input string, arguments ...string) (string, string, int) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestAuditCLIProcess$")
	command.Env = append(os.Environ(), "JINN_AUDIT_CLI_ARGUMENTS="+string(encoded))
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	status := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			status = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	return stdout.String(), stderr.String(), status
}

func TestAuditC106(t *testing.T) {
	t.Parallel()
	for _, arguments := range [][]string{{"--mcp-profile=read-only"}, {"--mcp", "--mcp-profile=read-only"}, {"--mcp-http", "--mcp-profile=read-only"}} {
		_, profile, _, err := parseCLIArgs(arguments)
		if err != nil || profile != mcpProfileReadOnly {
			t.Fatalf("arguments=%q profile=%q error=%v", arguments, profile, err)
		}
	}
	input := `{"jsonrpc":"2.0","id":"audit","method":"tools/list","params":{` + currentMCPMeta + `}}` + "\n"
	for _, arguments := range [][]string{{"--mcp-profile=read-only"}, {"--mcp", "--mcp-profile=read-only"}} {
		stdout, stderr, status := runAuditMCPCLI(t, input, arguments...)
		if status != 0 || stderr != "" || !strings.Contains(stdout, "jinn_call") {
			t.Fatalf("MCP args=%q status=%d stdout=%q stderr=%q", arguments, status, stdout, stderr)
		}
	}
	for _, profile := range []string{"discover", "read-only"} {
		stdout, stderr, status := runAuditCLI(t, `{"tool":"list_tools"}`, "--mcp-profile="+profile)
		if status != 1 || stderr != "" || !strings.Contains(stdout, "requires --mcp or --mcp-http") {
			t.Fatalf("CLI profile=%s status=%d stdout=%q stderr=%q", profile, status, stdout, stderr)
		}
	}
}

func runAuditMCPCLI(t *testing.T, input string, arguments ...string) (string, string, int) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, executable, "-test.run=^TestAuditCLIProcess$")
	command.Env = append(os.Environ(), "JINN_AUDIT_CLI_ARGUMENTS="+string(encoded))
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	if _, err := stdin.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	// Keep the transport open until the SDK's asynchronous response is received.
	var response json.RawMessage
	if err := json.NewDecoder(stdout).Decode(&response); err != nil {
		t.Fatalf("decode live MCP response: %v", err)
	}
	_ = stdin.Close()
	err = command.Wait()
	status := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			status = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	return string(response), stderr.String(), status
}

func TestAuditC1R02(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
		json bool
	}{
		{"ordinary usage", []string{"unknown"}, true},
		{"web human usage", []string{"web", "fetch"}, false},
		{"web JSON usage", []string{"web", "fetch", "--json"}, true},
		{"web search JSON usage", []string{"web", "search", "--json"}, true},
		{"web JSON parse", []string{"web", "fetch", "--json", "--unknown"}, true},
		{"web requested JSON parse", []string{"web", "search", "--unknown", "--json"}, true},
		{"web human parse", []string{"web", "fetch", "--unknown"}, false},
		{"web explicit human parse", []string{"web", "fetch", "--json=false", "--unknown"}, false},
		{"web format-looking value", []string{"web", "fetch", "--reader", "--json", "--unknown"}, false},
		{"web positional format-looking value", []string{"web", "fetch", "--", "--json", "extra"}, false},
		{"web JSON validation", []string{"web", "fetch", "--json", "--max-bytes=-1", "https://example.test"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, status := runAuditCLI(t, "", test.args...)
			if status != 1 {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if test.json {
				if !json.Valid([]byte(stdout)) || stderr != "" {
					t.Fatalf("JSON stream contract stdout=%q stderr=%q", stdout, stderr)
				}
			} else if stdout != "" || stderr == "" {
				t.Fatalf("human stream contract stdout=%q stderr=%q", stdout, stderr)
			}
			t.Logf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
		})
	}
	t.Log("web usage and validation errors preserve requested JSON mode and status 1")
}
