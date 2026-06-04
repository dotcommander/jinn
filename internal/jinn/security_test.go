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
	_ = os.Symlink(target, filepath.Join(dir, "escape"))
	_, err := e.checkPath("escape/anything")
	if err == nil {
		t.Error("symlink escape should be blocked")
	}
}

func TestCheckPath_SymlinkEscapeWithMissingChild(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}

	_, err := e.checkPath("escape/newdir/file.txt")
	if err == nil {
		t.Fatal("symlink escape through missing child should be blocked")
	}
	if !strings.Contains(err.Error(), "outside working directory") {
		t.Fatalf("expected outside-workdir error, got: %v", err)
	}
}

func TestCheckPathForRead_BlocksUnregisteredTempSpillSymlink(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	target := filepath.Join(dir, ".git", "config")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(os.TempDir(), spillFilePrefix+"security-test-symlink.log")
	_ = os.Remove(link)
	t.Cleanup(func() { _ = os.Remove(link) })
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	_, err := e.checkPathForRead(link)
	if err == nil {
		t.Fatal("expected unregistered temp spill symlink to be blocked")
	}
	if !strings.Contains(err.Error(), "unregistered shell spill file") {
		t.Fatalf("expected unregistered spill error, got: %v", err)
	}
}

func TestCheckPathForRead_BlocksUnregisteredTempSpillFile(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	path := filepath.Join(os.TempDir(), spillFilePrefix+"security-test-regular.log")
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
	if err := os.WriteFile(path, []byte("not a jinn spill"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := e.checkPathForRead(path)
	if err == nil {
		t.Fatal("expected unregistered temp spill file to be blocked")
	}
	if !strings.Contains(err.Error(), "unregistered shell spill file") {
		t.Fatalf("expected unregistered spill error, got: %v", err)
	}
}

func TestCheckPathForRead_AllowsRegisteredTempSpill(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e, _ := testEngine(t)
	capture := newShellOutputCapture(4)
	if _, err := capture.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	spill := capture.EnsureSpill()
	t.Cleanup(func() { _ = os.Remove(spill) })
	capture.Close()

	got, err := e.checkPathForRead(spill)
	if err != nil {
		t.Fatalf("expected registered spill to be readable: %v", err)
	}
	if got != filepath.Clean(spill) {
		t.Fatalf("checkPathForRead returned %q, want %q", got, filepath.Clean(spill))
	}
}

func TestCheckPathForRead_BlocksReplacedRegisteredTempSpill(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e, _ := testEngine(t)
	capture := newShellOutputCapture(4)
	if _, err := capture.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	spill := capture.EnsureSpill()
	capture.Close()
	t.Cleanup(func() { _ = os.Remove(spill) })

	if err := os.WriteFile(spill, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := e.checkPathForRead(spill)
	if err == nil {
		t.Fatal("expected replaced registered spill to be blocked")
	}
	if !strings.Contains(err.Error(), "unregistered shell spill file") {
		t.Fatalf("expected unregistered spill error, got: %v", err)
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
