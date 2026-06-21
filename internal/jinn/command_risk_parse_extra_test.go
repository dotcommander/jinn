package jinn

import "testing"

func TestShellFields_RemovesShellQuotes(t *testing.T) {
	t.Parallel()
	got := shellFields(`python3 '-c' "print('x y')" escaped\ value empty''`)
	want := []string{"python3", "-c", "print('x y')", "escaped value", "empty"}
	if len(got) != len(want) {
		t.Fatalf("len(shellFields) = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("shellFields[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractSubshellContents_AllSubstitutions(t *testing.T) {
	t.Parallel()
	got := extractSubshellContents("echo $(ls) '$(ignored)' \"$(quoted)\" \"'$(single-in-double)'\" $(printf ' ) '; rm -rf /tmp/x) `git status` \"`quoted-tick`\" `rm y`")
	want := []string{"ls", "quoted", "single-in-double", "printf ' ) '; rm -rf /tmp/x", "git status", "quoted-tick", "rm y"}
	if len(got) != len(want) {
		t.Fatalf("len(extractSubshellContents) = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("extractSubshellContents[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractProcessSubstitutionContents(t *testing.T) {
	t.Parallel()
	got := extractProcessSubstitutionContents("printf π; cat <(ls) >(printf ' ) '; rm -rf /tmp/x) '<(ignored)'")
	want := []string{"ls", "printf ' ) '; rm -rf /tmp/x"}
	if len(got) != len(want) {
		t.Fatalf("len(extractProcessSubstitutionContents) = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("extractProcessSubstitutionContents[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractFunctionBodyContents(t *testing.T) {
	t.Parallel()
	got := extractFunctionBodyContents("printf π; safe(){ echo ok; }; cleanup(){ rm -rf /tmp/x; }; echo 'ignored(){ rm y; }'")
	want := []string{" echo ok; ", " rm -rf /tmp/x; "}
	if len(got) != len(want) {
		t.Fatalf("len(extractFunctionBodyContents) = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("extractFunctionBodyContents[%d] = %q, want %q", i, got[i], want[i])
		}
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
		// Quoted parens inside the substitution body must not leave unmatched
		// quotes that hide following shell operators from outer classification.
		{"echo $(printf ' ) '); rm -rf /tmp/x", "echo ; rm -rf /tmp/x"},
		// Single-quoted substitutions are literal text and should not be stripped.
		{"echo '$(literal)' $(actual)", "echo '$(literal)' "},
		// Double-quoted substitutions execute, but the surrounding quotes remain
		// part of the outer command.
		{`echo "$(actual)"`, `echo ""`},
		// No subshell — unchanged.
		{"ls -la", "ls -la"},
	}
	for _, tc := range cases {
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
		{"echo '`literal`' `actual`", "echo '`literal`' "},
		{"echo \"`actual`\"", "echo \"\""},
		// Unterminated backtick — strips from backtick to end.
		{"echo `unterminated", "echo "},
		// No backtick — unchanged.
		{"plain cmd", "plain cmd"},
	}
	for _, tc := range cases {
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
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := firstVerb(tc.input)
			if got != tc.want {
				t.Errorf("firstVerb(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
