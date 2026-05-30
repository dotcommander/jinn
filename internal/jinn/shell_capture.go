package jinn

import (
	"os"
	"sync"
)

// shellOutputCapture keeps a bounded response tail while preserving complete
// command output in a spill file once the in-memory tail limit is crossed.
type shellOutputCapture struct {
	mu          sync.Mutex
	limit       int
	tail        []byte
	totalBytes  int
	newlines    int
	lastNewline bool
	spill       *os.File
	spillPath   string
	spillErr    error
}

func newShellOutputCapture(limit int) *shellOutputCapture {
	return &shellOutputCapture{limit: limit}
}

func (c *shellOutputCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	n := len(p)
	if n == 0 {
		return 0, nil
	}
	if c.totalBytes+n > c.limit {
		c.ensureSpillLocked()
	}
	if c.spill != nil {
		if _, err := c.spill.Write(p); err != nil && c.spillErr == nil {
			c.spillErr = err
		}
	}
	for _, b := range p {
		if b == '\n' {
			c.newlines++
			c.lastNewline = true
		} else {
			c.lastNewline = false
		}
	}
	c.totalBytes += n
	c.tail = append(c.tail, p...)
	if len(c.tail) > c.limit {
		c.tail = append([]byte(nil), c.tail[len(c.tail)-c.limit:]...)
	}
	return n, nil
}

func (c *shellOutputCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.tail)
}

func (c *shellOutputCapture) Truncated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalBytes > len(c.tail)
}

func (c *shellOutputCapture) TotalBytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalBytes
}

func (c *shellOutputCapture) TotalLines() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.totalBytes == 0 {
		return 0
	}
	if c.lastNewline {
		return c.newlines
	}
	return c.newlines + 1
}

func (c *shellOutputCapture) EnsureSpill() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureSpillLocked()
	if c.spill != nil {
		_ = c.spill.Sync()
	}
	return c.spillPath
}

func (c *shellOutputCapture) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.spill != nil {
		_ = c.spill.Close()
		c.spill = nil
	}
}

func (c *shellOutputCapture) ensureSpillLocked() {
	if c.spill != nil || c.spillErr != nil {
		return
	}
	tmp, err := os.CreateTemp("", "jinn-shell-*.log")
	if err != nil {
		c.spillErr = err
		return
	}
	c.spill = tmp
	c.spillPath = tmp.Name()
	if len(c.tail) > 0 {
		if _, err := tmp.Write(c.tail); err != nil {
			c.spillErr = err
		}
	}
}
