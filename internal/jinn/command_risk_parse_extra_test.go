package jinn

import "testing"

// extractSubshellContent coverage

func TestExtractSubshellContent_DollarParen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"echo $(ls -la)", "ls -la"},
		{"echo $(rm -rf /)", "rm -rf /"},
		{"x=$(cat /etc/passwd)", "cat /etc/passwd"},
		// Nested parens — extracts outermost body.
		{"echo $(foo $(bar))", "foo $(bar)"},
		// Unclosed — returns rest of string after $(.
		{"echo $(ls", "ls"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := extractSubshellContent(tc.input)
			if got != tc.want {
				t.Errorf("extractSubshellContent(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestExtractSubshellContent_Backtick(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"`ls -la`", "ls -la"},
		{"echo `rm foo`", "rm foo"},
		// Unterminated backtick.
		{"`unterminated", "unterminated"},
		// No subshell at all.
		{"no subshell here", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := extractSubshellContent(tc.input)
			if got != tc.want {
				t.Errorf("extractSubshellContent(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// stripSubshells coverage

func TestStripSubshells_DollarParen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"echo $(ls)", "echo "},
		{"echo $(rm foo) bar", "echo  bar"},
		// Nested — strips the outer and inner together.
		{"x=$(foo $(bar)) y", "x= y"},
		// No subshell — unchanged.
		{"ls -la", "ls -la"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := stripSubshells(tc.input)
			if got != tc.want {
				t.Errorf("stripSubshells(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestStripSubshells_Backtick(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"echo `ls`", "echo "},
		{"a`b`c", "ac"},
		// Unterminated backtick — strips from backtick to end.
		{"echo `unterminated", "echo "},
		// No backtick — unchanged.
		{"plain cmd", "plain cmd"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := stripSubshells(tc.input)
			if got != tc.want {
				t.Errorf("stripSubshells(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// firstVerb coverage

func TestFirstVerb_SkipsEnvAssignments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		// Pure env assignments before command.
		{"FOO=bar BAZ=qux ls", "ls"},
		{"PATH=/usr/local/bin:$PATH rm -rf /", "rm"},
		// No assignments — first token returned.
		{"ls -la", "ls"},
		// All assignments, no command.
		{"FOO=bar BAZ=qux", ""},
		// Empty string.
		{"", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := firstVerb(tc.input)
			if got != tc.want {
				t.Errorf("firstVerb(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
