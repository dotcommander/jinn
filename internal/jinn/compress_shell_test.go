package jinn

import (
	"strings"
	"testing"
)

func TestLastSegmentVerb(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cmd  string
		want string
	}{
		{"go test ./...", "go"},
		{"cd x && go test ./...", "go"},
		{"git log | head -5", "head"},
		{"FOO=1 ps aux", "ps"},
		{"", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.cmd, func(t *testing.T) {
			t.Parallel()
			got := lastSegmentVerb(tc.cmd)
			if got != tc.want {
				t.Errorf("lastSegmentVerb(%q) = %q, want %q", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestCollapseColumnPadding(t *testing.T) {
	t.Parallel()

	t.Run("collapses multiple spaces", func(t *testing.T) {
		t.Parallel()
		input := "USER       PID  %CPU  MEM\nroot         1   0.0  0.1\ngary      1234   0.5  1.2"
		result := collapseColumnPadding(input)
		if strings.Contains(result, "  ") {
			t.Errorf("expected no double-space runs, got: %q", result)
		}
		if len(result) >= len(input) {
			t.Errorf("expected shorter result, got len=%d vs input len=%d", len(result), len(input))
		}
	})

	t.Run("no double spaces returns same string", func(t *testing.T) {
		t.Parallel()
		input := "single space only\nanother line here"
		result := collapseColumnPadding(input)
		if result != input {
			t.Errorf("expected identical string returned, got %q", result)
		}
	})

	t.Run("tabs are preserved", func(t *testing.T) {
		t.Parallel()
		input := "col1\tcol2\tcol3\nval1\tval2\tval3"
		result := collapseColumnPadding(input)
		if !strings.Contains(result, "\t") {
			t.Errorf("expected tabs to be preserved, got: %q", result)
		}
	})
}

func TestCompressShellOutput_PsDedents(t *testing.T) {
	t.Parallel()

	// Build a fake ps aux-style multi-line string with heavy column padding.
	raw := "USER       PID  %CPU  %MEM      VSZ     RSS TTY      STAT START   TIME COMMAND\n" +
		"root         1   0.0   0.1   168804    11728 ?        Ss   09:00   0:01 /sbin/init\n" +
		"gary      1234   0.5   1.2  1234560   102400 pts/0    Sl   09:01   0:42 /usr/bin/some-program\n" +
		"root      9999   0.0   0.0    12345      987 ?        S    09:00   0:00 [kworker/0:0]\n"

	// Tabular verb: padding should be collapsed.
	resultPS := compressShellOutput(raw, "ps aux")
	if strings.Contains(resultPS, "  ") {
		t.Errorf("expected no double-space runs for 'ps aux', got: %q", resultPS)
	}
	if len(resultPS) >= len(raw) {
		t.Errorf("expected shorter result for 'ps aux', got len=%d vs raw len=%d", len(resultPS), len(raw))
	}

	// Non-tabular verb: padding must NOT be collapsed.
	resultEcho := compressShellOutput(raw, "echo hi")
	if !strings.Contains(resultEcho, "  ") {
		t.Errorf("expected double-space runs preserved for 'echo hi' (non-tabular), got: %q", resultEcho)
	}
}

func TestCompressGoTestPlain_AllPass(t *testing.T) {
	t.Parallel()

	// Build ≥5 test lines so AppliesTo fires; no === RUN markers.
	input := "ok  \texample.com/a\t0.100s\n" +
		"ok  \texample.com/b\t0.200s\n" +
		"ok  \texample.com/c\t(cached)\n" +
		"ok  \texample.com/d\t0.050s\n" +
		"ok  \texample.com/e\t0.010s\n" +
		"?   \texample.com/x\t[no test files]\n"

	result := compressShellOutput(input, "go test ./...")
	if !strings.Contains(result, "✓") {
		t.Errorf("expected ✓ in result, got: %q", result)
	}
	if !strings.Contains(result, "package(s) ok") {
		t.Errorf("expected 'package(s) ok' in result, got: %q", result)
	}
	if !strings.Contains(result, "no test files") {
		t.Errorf("expected no-test-files count in result, got: %q", result)
	}
	if len(result) >= len(input) {
		t.Errorf("expected shorter result, got len=%d vs input len=%d", len(result), len(input))
	}
	// Per-package ok lines should be gone.
	if strings.Contains(result, "example.com/a") {
		t.Errorf("expected per-package ok lines removed, got: %q", result)
	}
}

func TestLastSegmentArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cmd  string
		want []string
	}{
		{"ls -la", []string{"-la"}},
		{"X=1 docker -H tcp://x ps -a", []string{"-H", "tcp://x", "ps", "-a"}},
		{"git log | head -5", []string{"-5"}},
		{"go test ./...", []string{"test", "./..."}},
		{"ls", nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.cmd, func(t *testing.T) {
			t.Parallel()
			got := lastSegmentArgs(tc.cmd)
			if len(got) != len(tc.want) {
				t.Fatalf("lastSegmentArgs(%q) = %v (len %d), want %v (len %d)", tc.cmd, got, len(got), tc.want, len(tc.want))
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("lastSegmentArgs(%q)[%d] = %q, want %q", tc.cmd, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSubcommand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args       []string
		valueFlags map[string]bool
		want       string
	}{
		{[]string{"-n", "foo", "get", "pods"}, kubectlValueFlags, "get"},
		{[]string{"-H", "tcp://x", "ps"}, dockerValueFlags, "ps"},
		{[]string{"ps", "-a"}, dockerValueFlags, "ps"},
		{[]string{"--help"}, dockerValueFlags, ""},
		{[]string{}, dockerValueFlags, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()
			got := subcommand(tc.args, tc.valueFlags)
			if got != tc.want {
				t.Errorf("subcommand(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestHasShortFlagL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"-l"}, true},
		{[]string{"-la"}, true},
		{[]string{"-l", "--color"}, true},
		{[]string{}, false},
		{[]string{"-a"}, false},
		{[]string{"--color"}, false},
		{[]string{"--long"}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()
			got := hasShortFlagL(tc.args)
			if got != tc.want {
				t.Errorf("hasShortFlagL(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestCompressShellOutput_LsLDedents(t *testing.T) {
	t.Parallel()
	raw := "total 8\n" +
		"-rw-r--r--  1 gary  staff  1234 May 10 12:00 a.go\n" +
		"-rw-r--r--  1 gary  staff    42 May 10 12:00 b.go\n"

	// ls -l: column padding should be collapsed.
	resultL := compressShellOutput(raw, "ls -l")
	if strings.Contains(resultL, "  ") {
		t.Errorf("ls -l: expected no double-space runs, got: %q", resultL)
	}
	if !strings.Contains(resultL, "-rw-r--r-- 1 gary staff") {
		t.Errorf("ls -l: expected single-spaced columns, got: %q", resultL)
	}

	// bare ls: double spaces must be preserved.
	resultBare := compressShellOutput(raw, "ls")
	if !strings.Contains(resultBare, "  1 gary") {
		t.Errorf("ls (bare): expected double spaces preserved, got: %q", resultBare)
	}
}

func TestCompressShellOutput_DockerPsDedents(t *testing.T) {
	t.Parallel()
	raw := "CONTAINER ID   IMAGE          COMMAND   CREATED        STATUS        PORTS   NAMES\n" +
		"abc123def456   ubuntu:22.04   bash      2 hours ago    Up 2 hours            mybox\n"

	// docker ps: padding should be collapsed.
	resultPS := compressShellOutput(raw, "docker ps")
	if strings.Contains(resultPS, "   ") {
		t.Errorf("docker ps: expected no triple-space runs, got: %q", resultPS)
	}

	// docker logs: should not collapse.
	resultLogs := compressShellOutput(raw, "docker logs abc")
	if !strings.Contains(resultLogs, "   ") {
		t.Errorf("docker logs: expected double spaces preserved, got: %q", resultLogs)
	}
}

func TestCompressShellOutput_KubectlGetDedents(t *testing.T) {
	t.Parallel()
	raw := "NAME          READY   STATUS    RESTARTS   AGE\n" +
		"my-pod        1/1     Running   0          2d\n" +
		"other-pod     0/1     Pending   3          5m\n"

	// kubectl -n kube-system get pods: -n skipped, "get" found → dedent.
	resultGet := compressShellOutput(raw, "kubectl -n kube-system get pods")
	if strings.Contains(resultGet, "   ") {
		t.Errorf("kubectl get: expected no triple-space runs, got: %q", resultGet)
	}

	// kubectl describe: should not collapse.
	resultDesc := compressShellOutput(raw, "kubectl describe pod abc")
	if !strings.Contains(resultDesc, "   ") {
		t.Errorf("kubectl describe: expected spaces preserved, got: %q", resultDesc)
	}
}

func TestCompressGoTestPlain_WithFailure(t *testing.T) {
	t.Parallel()

	input := "ok  \texample.com/a\t0.100s\n" +
		"ok  \texample.com/b\t0.200s\n" +
		"ok  \texample.com/c\t(cached)\n" +
		"ok  \texample.com/d\t0.050s\n" +
		"--- FAIL: TestX (0.01s)\n" +
		"    foo_test.go:10: boom\n" +
		"FAIL\n" +
		"FAIL\texample.com/b\t0.01s\n"

	result := compressShellOutput(input, "go test ./...")
	if !strings.Contains(result, "--- FAIL: TestX") {
		t.Errorf("expected '--- FAIL: TestX' preserved, got: %q", result)
	}
	if !strings.Contains(result, "foo_test.go:10: boom") {
		t.Errorf("expected detail line preserved, got: %q", result)
	}
	if !strings.Contains(result, "FAIL\texample.com/b") {
		t.Errorf("expected 'FAIL\\texample.com/b' preserved, got: %q", result)
	}
	// Passing-package ok lines should be dropped.
	if strings.Contains(result, "ok  \texample.com/a") {
		t.Errorf("expected passing-package ok lines dropped, got: %q", result)
	}
	if len(result) >= len(input) {
		t.Errorf("expected shorter result, got len=%d vs input len=%d", len(result), len(input))
	}
}
