package jinn

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

// TestRegisterShellSpill_Concurrent proves concurrent registrations do not
// drop each other: the registry read-modify-write is guarded by a
// cross-process file lock. Must NOT call t.Parallel() — t.Setenv is serial.
func TestRegisterShellSpill_Concurrent(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())

	const n = 8
	paths := make([]string, n)
	for i := 0; i < n; i++ {
		f, err := os.CreateTemp(os.TempDir(), spillFilePrefix)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(fmt.Sprintf("spill %d", i)); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		paths[i] = f.Name()
		t.Cleanup(func() { _ = os.Remove(f.Name()) })
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			registerShellSpill(p)
		}(paths[i])
	}
	wg.Wait()

	for _, p := range paths {
		if !isRegisteredShellSpill(p) {
			t.Errorf("registration lost for %s", p)
		}
	}
}
