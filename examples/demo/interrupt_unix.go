//go:build darwin || linux

package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// startEscInterrupt arms an Esc-key watcher tied to parent. Returns a
// derived context that cancels when:
//   - parent cancels (Ctrl-C via signal.NotifyContext, or caller cancel), OR
//   - the user presses Esc (0x1b with no follow-up byte within 50ms).
//
// stop() is idempotent: it closes the internal stop channel, waits for the
// watcher goroutine to exit, and restores the terminal to cooked mode.
// Callers must defer stop() in the same function that called
// startEscInterrupt.
//
// Non-TTY stdin, MakeRaw failure, or any unrecoverable setup error → returns
// (parent, no-op stop) and logs a one-line warning to stderr. The REPL never
// fails because of Esc-interrupter setup.
func startEscInterrupt(parent context.Context) (context.Context, func()) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return parent, func() {}
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: esc-interrupt disabled (raw mode: %v)\n", err)
		return parent, func() {}
	}

	ctx, cancel := context.WithCancel(parent)
	stopCh := make(chan struct{})
	done := make(chan struct{})

	go watchEsc(fd, cancel, stopCh, done)

	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(stopCh)
			<-done
			_ = term.Restore(fd, oldState)
			cancel() // ensure ctx is canceled after stop so callers see Done
		})
	}
	return ctx, stop
}

// watchEsc polls stdin with unix.Poll (50ms timeout) and cancels on Esc.
// Exit conditions:
//   - stopCh closed (caller invoked stop())
//   - Esc detected (cancels ctx and returns)
//   - Poll returns an unrecoverable error (returns quietly)
//
// ANSI escape sequences (0x1b followed by another byte within 50ms) are
// drained until a terminator byte — a letter @A-Za-z or '~' — is seen, or
// the stream goes quiet for 50ms. Drained sequences are discarded.
func watchEsc(fd int, cancel context.CancelFunc, stopCh <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	pollOnce := func(timeoutMs int) (bool, error) {
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(pfd, timeoutMs)
		if err != nil {
			if err == unix.EINTR {
				return false, nil
			}
			return false, err
		}
		return n > 0 && (pfd[0].Revents&unix.POLLIN) != 0, nil
	}

	readByte := func() (byte, bool) {
		var buf [1]byte
		n, err := unix.Read(fd, buf[:])
		if err != nil || n != 1 {
			return 0, false
		}
		return buf[0], true
	}

	isTerminator := func(b byte) bool {
		return b == '~' ||
			(b >= 'A' && b <= 'Z') ||
			(b >= 'a' && b <= 'z') ||
			b == '@'
	}

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		readable, err := pollOnce(50)
		if err != nil {
			return
		}
		if !readable {
			continue
		}

		b, ok := readByte()
		if !ok {
			return
		}
		if b != 0x1b {
			continue // ignore non-Esc bytes (including stray typing during dispatch)
		}

		// Peek for a follow-up byte: if something arrives within 50ms,
		// it's an ANSI sequence; drain it. Otherwise, real Esc.
		peekReadable, err := pollOnce(50)
		if err != nil {
			return
		}
		if !peekReadable {
			// Real Esc.
			cancel()
			return
		}
		// Drain the rest of the ANSI sequence.
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			drainReadable, err := pollOnce(50)
			if err != nil {
				return
			}
			if !drainReadable {
				break // sequence ended
			}
			db, ok := readByte()
			if !ok {
				return
			}
			if isTerminator(db) {
				break
			}
		}
	}
}
