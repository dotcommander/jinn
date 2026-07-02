package jinn

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

// TestRecordSnapshot_ConcurrentEngines proves no undo entries or blobs are
// lost when multiple Engine instances (jinn's real one-process-per-call
// shape) mutate files in the same workdir concurrently: the on-disk store
// is guarded by the cross-process file lock, not in-process state.
// Must NOT call t.Parallel() — historyEngine uses t.Setenv (serial only).
func TestRecordSnapshot_ConcurrentEngines(t *testing.T) {
	_, workDir := historyEngine(t)

	const n = 8
	for i := 0; i < n; i++ {
		writeTestFile(t, workDir, fmt.Sprintf("f%d.txt", i), "before")
	}

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := New(workDir, "dev")
			_, err := e.writeFile(args("path", fmt.Sprintf("f%d.txt", i), "content", "after"))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	e := New(workDir, "dev")
	hf, err := e.loadHistoryLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(hf.Entries) != n {
		t.Errorf("history entries = %d, want %d (lost updates)", len(hf.Entries), n)
	}
	blobs, err := os.ReadDir(e.blobsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) != n {
		t.Errorf("blob files = %d, want %d (orphaned or missing blobs)", len(blobs), n)
	}
}
