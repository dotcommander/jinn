package jinn

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// TestWithFileLock_SerializesReadModifyWrite proves lost-update protection:
// 50 goroutines (each call opens its own fd, so flock serializes them like
// separate processes) increment a counter file under the lock. Any lost
// update yields a final value below 50.
func TestWithFileLock_SerializesReadModifyWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "counter.lock")
	counterPath := filepath.Join(dir, "counter")
	if err := os.WriteFile(counterPath, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}

	const workers = 50
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- withFileLock(lockPath, func() error {
				data, err := os.ReadFile(counterPath)
				if err != nil {
					return err
				}
				n, err := strconv.Atoi(string(data))
				if err != nil {
					return err
				}
				return os.WriteFile(counterPath, []byte(strconv.Itoa(n+1)), 0o600)
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "50" {
		t.Errorf("counter = %s, want 50 (lost updates)", data)
	}
}
