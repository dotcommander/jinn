package jinn

import (
	"testing"
)

func TestHashContent_Deterministic(t *testing.T) {
	t.Parallel()
	a := HashContent("hello world")
	b := HashContent("hello world")
	if a != b {
		t.Errorf("HashContent not deterministic: %s != %s", a, b)
	}
}

func TestHashContent_DifferentInputs(t *testing.T) {
	t.Parallel()
	a := HashContent("hello")
	b := HashContent("world")
	if a == b {
		t.Error("HashContent returned same hash for different inputs")
	}
}

func TestStateToken_Deterministic(t *testing.T) {
	t.Parallel()
	a := StateToken("/foo/bar.go", 12345, HashContent("content"))
	b := StateToken("/foo/bar.go", 12345, HashContent("content"))
	if a != b {
		t.Errorf("StateToken not deterministic: %s != %s", a, b)
	}
}

func TestStateToken_ChangesOnContentChange(t *testing.T) {
	t.Parallel()
	a := StateToken("/foo/bar.go", 12345, HashContent("before"))
	b := StateToken("/foo/bar.go", 12345, HashContent("after"))
	if a == b {
		t.Error("StateToken should differ when content changes")
	}
}

func TestStateToken_ChangesOnMtimeChange(t *testing.T) {
	t.Parallel()
	hash := HashContent("same content")
	a := StateToken("/foo/bar.go", 1000, hash)
	b := StateToken("/foo/bar.go", 2000, hash)
	if a == b {
		t.Error("StateToken should differ when mtime changes")
	}
}

func TestStateToken_ChangesOnPathChange(t *testing.T) {
	t.Parallel()
	hash := HashContent("same content")
	a := StateToken("/foo/a.go", 1000, hash)
	b := StateToken("/foo/b.go", 1000, hash)
	if a == b {
		t.Error("StateToken should differ when path changes")
	}
}

func TestStateToken_Length(t *testing.T) {
	t.Parallel()
	tok := StateToken("/foo.go", 0, HashContent(""))
	if len(tok) != StateTokenLen {
		t.Errorf("StateToken length = %d, want %d", len(tok), StateTokenLen)
	}
}
