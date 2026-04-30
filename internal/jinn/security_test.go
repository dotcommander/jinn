package jinn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePath_Relative(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	got, err := e.resolvePath("foo/bar.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, "foo/bar.go")
	if got != want {
		t.Errorf("resolvePath(relative) = %q, want %q", got, want)
	}
}

func TestResolvePath_Absolute(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	got, err := e.resolvePath("/tmp/test.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/tmp/test.go" {
		t.Errorf("resolvePath(absolute) = %q, want /tmp/test.go", got)
	}
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

// Change 6: resolvePath expands leading ~ to home directory.
func TestResolvePath_ExpandsTilde(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	// "~" alone expands to home dir.
	got, err := e.resolvePath("~")
	if err != nil {
		t.Fatalf("unexpected error expanding ~: %v", err)
	}
	home, _ := os.UserHomeDir()
	if got != home {
		t.Errorf("resolvePath('~') = %q, want %q", got, home)
	}

	// "~/foo/bar" expands correctly.
	got2, err := e.resolvePath("~/foo/bar")
	if err != nil {
		t.Fatalf("unexpected error expanding ~/foo/bar: %v", err)
	}
	want2 := filepath.Join(home, "foo/bar")
	if got2 != want2 {
		t.Errorf("resolvePath('~/foo/bar') = %q, want %q", got2, want2)
	}

	// checkPath rejects the expanded path as outside workdir.
	_, err = e.checkPath("~/foo/bar")
	if err == nil {
		t.Error("expected checkPath to reject ~/foo/bar as outside workdir")
	}
	if !strings.Contains(err.Error(), "outside working directory") {
		t.Errorf("expected 'outside working directory', got: %v", err)
	}
}
