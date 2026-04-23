// demo is a minimal coding agent that uses jinn as its sandboxed tool
// executor. It ports the shell-script shoop agent to Go, replacing bash
// primitives with the jinn binary's JSON-over-stdio protocol.
//
// Tools map to jinn (run_shell, read_file, write_file, edit_file, multi_edit,
// search_files, stat_file, list_dir) with web_fetch delegated to the defuddle
// CLI. Zero third-party imports — stdlib only, matching jinn itself.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

type config struct {
	apiKey         string
	baseURL        string
	model          string
	maxTurns       int
	maxToolOutput  int
	compactEvery   int    // compaction cadence in user turns; 0 disables
	compactPrompt  string // summarization prompt text (loaded at startup)
	rewritePrompts bool
	rewriterPrompt string
	temperature    float64
	topP           float64
	maxTokens      int
	dryRun         bool
	workDir        string
	jinnBin        string
	defuddleBin    string
	sessionID      string
	sessionDir     string
	resume         bool
	quiet          bool
	local          bool
}

func main() {
	if err := run(); err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, prompt, err := parseArgs(os.Args[1:])
	if err != nil {
		return err
	}
	if cfg == nil {
		return nil
	}

	var messages []message
	if cfg.resume {
		messages, err = loadSession(cfg)
		if err != nil {
			return fmt.Errorf("resume: %w", err)
		}
	} else {
		if cfg.sessionID == "" {
			cfg.sessionID = defaultSessionID()
		}
		messages = []message{{Role: "system", Content: systemPrompt(cfg)}}
	}

	// REPL mode: no prompt AND interactive stdin. The REPL installs its own
	// per-turn signal handler so Ctrl-C at the prompt still exits via the
	// default handler; we must not register a process-wide handler here.
	if prompt == "" && stdinIsTTY() {
		return runREPL(context.Background(), cfg, messages)
	}

	if prompt != "" {
		messages = append(messages, message{Role: "user", Content: prompt})
	} else if len(messages) <= 1 {
		return errors.New("no prompt provided (pass as arg, pipe via stdin, or run interactively)")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runOneShot(ctx, cfg, messages)
}
