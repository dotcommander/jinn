package jinn

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsRemediationSpillIdentity(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "spill")
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := spillFileIdentity(path, info)
	if !ok {
		t.Fatal("missing file identity")
	}
	if got, ok := spillFileIdentity(path, info); !ok || got != first {
		t.Fatalf("unstable identity=%+v, %v", got, ok)
	}
	// Keep the original inode alive so replacement cannot reuse its identifier.
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	replaced, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := spillFileIdentity(path, replaced); !ok || got == first {
		t.Fatalf("replacement identity=%+v, %v", got, ok)
	}
}

func TestWindowsRemediationRootedDelete(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	inside := filepath.Join(engine.workDir, "inside")
	outside := filepath.Join(t.TempDir(), "outside")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.removeFileDurable(inside); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("deleted target stat=%v", err)
	}
	if err := engine.removeFileDurable(outside); err == nil {
		t.Fatal("outside-root delete accepted")
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
		t.Fatalf("outside target=%q, %v", data, err)
	}
}
