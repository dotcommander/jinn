package jinn

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePath_Relative(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	got := e.resolvePath("foo/bar.go")
	want := filepath.Join(dir, "foo/bar.go")
	if got != want {
		t.Errorf("resolvePath(relative) = %q, want %q", got, want)
	}
}

func TestResolvePath_Absolute(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	got := e.resolvePath("/tmp/test.go")
	if got != "/tmp/test.go" {
		t.Errorf("resolvePath(absolute) = %q, want /tmp/test.go", got)
	}
	_ = e
}

func TestCheckPath_SensitivePaths(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	cases := []string{
		".git/config",
		".ssh/id_rsa",
		".aws/credentials",
		".gnupg/pubring.kbx",
		".env",
		".env.local",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			_, err := e.checkPath(p)
			if err == nil {
				t.Errorf("checkPath(%q) should have returned error", p)
			}
		})
	}
}

func TestCheckPath_OutsideWD(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.checkPath("/etc/passwd")
	if err == nil {
		t.Error("checkPath(/etc/passwd) should have returned error")
	}
}

func TestCheckPath_SensitiveDirRoots(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	cases := []string{".git", ".ssh", ".aws", ".gnupg"}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			_, err := e.checkPath(p)
			if err == nil {
				t.Errorf("checkPath(%q) should have returned error for bare sensitive dir", p)
			}
		})
	}
}

func TestCheckPath_SymlinkEscape(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Create a symlink inside workdir pointing outside.
	target := t.TempDir() // different temp dir = outside workdir
	os.Symlink(target, filepath.Join(dir, "escape"))
	_, err := e.checkPath("escape/anything")
	if err == nil {
		t.Error("symlink escape should be blocked")
	}
}
