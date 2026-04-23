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
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

type config struct {
	apiKey      string
	baseURL     string
	model       string
	maxTurns    int
	maxHistory  int
	maxToolOutput int
	temperature float64
	topP        float64
	maxTokens   int
	dryRun      bool
	workDir     string
	jinnBin     string
	defuddleBin string
	sessionID   string
	sessionDir  string
	resume      bool
	quiet       bool
	local       bool
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

func parseArgs(argv []string) (*config, string, error) {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		model      = fs.String("model", envDefault("DEMO_MODEL", "openai/gpt-5.4-mini"), "LLM model identifier")
		baseURL    = fs.String("base-url", envDefault("DEMO_BASE_URL", "https://openrouter.ai/api/v1/chat/completions"), "OpenAI-compatible chat/completions endpoint")
		maxTurns   = fs.Int("max-turns", envIntDefault("DEMO_MAX_TURNS", 25), "maximum agent turns before aborting")
		maxHistory = fs.Int("max-history", envIntDefault("DEMO_MAX_HISTORY", 40), "maximum messages to keep in history (preserves system prompt)")
		maxOutput  = fs.Int("max-tool-output", envIntDefault("DEMO_MAX_TOOL_OUTPUT", 32768), "maximum tool output size in bytes before truncation")
		temp       = fs.Float64("temperature", 1.0, "LLM sampling temperature")
		topP       = fs.Float64("top-p", 1.0, "LLM top-p sampling")
		maxTokens  = fs.Int("max-tokens", 4096, "maximum tokens in completion")
		dryRun     = fs.Bool("dry-run", false, "intercept destructive tools and report intent instead of executing")
		sessionID  = fs.String("session", "", "session id for save/resume (auto-generated if empty)")
		resume     = fs.Bool("resume", false, "resume the session named by -session")
		quiet      = fs.Bool("quiet", false, "suppress turn banners and tool previews")
		local      = fs.Bool("local", false, "automatically detect local LLM server (probes ports 8080, 8000, 1234, 11434)")
		jinnBin    = fs.String("jinn-bin", envDefault("JINN_BIN", "jinn"), "path to jinn binary")
		defBin     = fs.String("defuddle-bin", envDefault("DEFUDDLE_BIN", "defuddle"), "path to defuddle binary")
		sessionDir = fs.String("session-dir", defaultSessionDir(), "session storage directory")
		showHelp   = fs.Bool("help", false, "show help")
		showVer    = fs.Bool("version", false, "show version")
	)

	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printHelp(fs)
			return nil, "", nil
		}
		return nil, "", err
	}

	if *showHelp {
		printHelp(fs)
		return nil, "", nil
	}
	if *showVer {
		fmt.Println("demo", version)
		return nil, "", nil
	}

	apiKey := firstNonEmpty(
		os.Getenv("DEMO_API_KEY"),
		os.Getenv("OPENROUTER_API_KEY"),
		os.Getenv("OPENAI_API_KEY"),
	)
	if apiKey == "" {
		return nil, "", errors.New("set DEMO_API_KEY, OPENROUTER_API_KEY, or OPENAI_API_KEY")
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("getwd: %w", err)
	}

	cfg := &config{
		apiKey:      apiKey,
		baseURL:     *baseURL,
		model:       *model,
		maxTurns:    *maxTurns,
		maxHistory:  *maxHistory,
		maxToolOutput: *maxOutput,
		temperature: *temp,
		topP:        *topP,
		maxTokens:   *maxTokens,
		dryRun:      *dryRun,
		workDir:     wd,
		jinnBin:     *jinnBin,
		defuddleBin: *defBin,
		sessionID:   *sessionID,
		sessionDir:  *sessionDir,
		resume:      *resume,
		quiet:       *quiet,
		local:       *local,
	}
	if cfg.sessionID == "" {
		cfg.sessionID = defaultSessionID()
	}

	if cfg.local {
		detectedURL, err := probeLocalServer()
		if err != nil {
			return nil, "", err
		}
		cfg.baseURL = detectedURL
		if cfg.model == "openai/gpt-5.4-mini" {
			cfg.model = "local"
		}
	}

	prompt, err := readPrompt(fs.Args())
	if err != nil {
		return nil, "", err
	}
	return cfg, prompt, nil
}

// probeLocalServer tries GET /v1/models on common local LLM ports and returns
// the chat/completions base URL for the first port that responds HTTP 200.
func probeLocalServer() (string, error) {
	ports := []int{8080, 8000, 1234, 11434}
	client := &http.Client{Timeout: 2 * time.Second}
	for _, port := range ports {
		url := fmt.Sprintf("http://localhost:%d/v1/models", port)
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			base := fmt.Sprintf("http://localhost:%d/v1/chat/completions", port)
			fmt.Fprintf(os.Stderr, "detected local server on port %d\n", port)
			return base, nil
		}
	}
	return "", errors.New("no local LLM server found (tried ports 8080, 8000, 1234, 11434)")
}

func readPrompt(args []string) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if (fi.Mode() & os.ModeCharDevice) != 0 {
		return "", nil
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func printHelp(fs *flag.FlagSet) {
	fmt.Fprintln(os.Stderr, `demo — a minimal coding agent that routes tool calls through jinn.

Usage:
  demo                                    start interactive REPL (TTY only)
  demo [flags] "your prompt here"         one-shot
  echo "your prompt" | demo [flags]       one-shot from pipe
  demo --resume --session <id> [prompt]   resume a session
  demo --local [flags] "your prompt"      use local LLM server (auto-detect port)

Environment:
  DEMO_API_KEY / OPENROUTER_API_KEY / OPENAI_API_KEY   API key (required)
  DEMO_MODEL       Default model (override with --model)
  DEMO_BASE_URL    Chat/completions endpoint
  JINN_BIN             jinn binary (default: "jinn" in PATH)
  DEFUDDLE_BIN         defuddle binary for web_fetch (default: "defuddle" in PATH)

Flags:`)
	fs.SetOutput(os.Stderr)
	fs.PrintDefaults()
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntDefault(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func defaultSessionDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "demo", "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "demo-sessions")
	}
	return filepath.Join(home, ".local", "share", "demo", "sessions")
}

func defaultSessionID() string {
	return fmt.Sprintf("s-%d", time.Now().Unix())
}
