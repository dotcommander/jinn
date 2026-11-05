package jinn

import (
	"strings"
	"testing"
)

func TestCompressGoTestOutput(t *testing.T) {
	t.Parallel()
	base := "=== RUN   TestOne\n--- PASS: TestOne (0.00s)\n=== RUN   TestTwo\n--- PASS: TestTwo (0.00s)\n=== RUN   TestThree\n--- PASS: TestThree (0.00s)\n"
	for _, tc := range []struct {
		name, suffix string
		failed       bool
	}{
		{"package fail", "FAIL\tgithub.com/example/broken\t0.01s\n", true},
		{"bare fail", "FAIL\n", true},
		{"panic", "panic: unexpected crash\n\nstack trace\n", true},
		{"clean pass", "PASS\nok  \tgithub.com/example/pkg\t0.01s\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := base + tc.suffix
			s := &testResultStrategy{}
			if !s.AppliesTo(input, "run_shell") {
				t.Fatal("verbose test output should trigger compression")
			}
			got := s.Compress(input)
			if tc.failed && got != input {
				t.Errorf("failure output must be preserved verbatim: %q", got)
			}
			if !tc.failed && (!strings.Contains(got, "3 passed") || got == input) {
				t.Errorf("clean output should compress to a pass summary: %q", got)
			}
		})
	}
}
