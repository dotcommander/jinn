package jinn

import (
	"testing"
)

func TestClassifyExitCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		argv0     string
		exitCode  int
		wantClass Classification
	}{
		// Exit 0 is always success regardless of command.
		{name: "zero_any", argv0: "anycmd", exitCode: 0, wantClass: ClassSuccess},

		// grep/rg/ag: exit 1 = no matches (expected nonzero).
		{name: "grep_no_match", argv0: "grep", exitCode: 1, wantClass: ClassExpectedNonzero},
		{name: "rg_no_match", argv0: "rg", exitCode: 1, wantClass: ClassExpectedNonzero},
		{name: "grep_real_error", argv0: "grep", exitCode: 2, wantClass: ClassError},

		// diff: exit 1 = files differ (expected nonzero).
		{name: "diff_differ", argv0: "diff", exitCode: 1, wantClass: ClassExpectedNonzero},

		// timeout via explicit guard: argv0=bash wrapping `timeout bash -c ...`
		// means argv0 resolves to bash, not timeout, but exit 124 must still classify as timeout.
		{name: "bash_exit_124", argv0: "bash", exitCode: 124, wantClass: ClassTimeout},

		// timeout command itself in the table.
		{name: "timeout_exit_124", argv0: "timeout", exitCode: 124, wantClass: ClassTimeout},

		// exit 127 = command not found — falls through to ClassError default.
		{name: "unknown_127", argv0: "anycmd", exitCode: 127, wantClass: ClassError},

		// exit > 128 = signal termination (128+signum convention).
		{name: "signal_130", argv0: "anycmd", exitCode: 130, wantClass: ClassSignal},

		// Negative exit code = signal termination.
		{name: "negative_exit", argv0: "anycmd", exitCode: -1, wantClass: ClassSignal},

		// find exit 1 must be ClassError — GNU and macOS find both exit 0 on
		// no matches; exit 1 indicates a real error (bad expression, permission denied).
		{name: "find_exit_1", argv0: "find", exitCode: 1, wantClass: ClassError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _ := classifyExitCode(tc.argv0, tc.exitCode)
			if got != tc.wantClass {
				t.Errorf("classifyExitCode(%q, %d) = %q, want %q",
					tc.argv0, tc.exitCode, got, tc.wantClass)
			}
		})
	}
}
