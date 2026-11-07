package jinn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Launch policy: a global, declarative exact-match gate for run_shell.
//
// The policy is an optional JSON file at
// ($JINN_CONFIG_DIR or os.UserConfigDir())/jinn/launch-policy.json with the
// shape {"launchPolicy": [{"command": "...", "decision": "allow|ask|deny"}]}.
// Absent file means no policy and run_shell behaves exactly as before.
// There is no workspace/project-level policy: only the global file is read,
// so workspace content can never weaken it.
//
// Commands are opaque strings matched exactly — no shell parsing or
// normalization. Several rules for one command resolve deterministically:
// deny wins over ask, which wins over allow. Unmatched commands return ask.
// In runShell, ask (and unmatched) blocks fail-closed with a suggestion,
// because the one-shot protocol has no confirmation UI; deny always blocks,
// even with force: true.

type launchDecision string

const (
	launchAllow launchDecision = "allow"
	launchAsk   launchDecision = "ask"
	launchDeny  launchDecision = "deny"
)

var launchDecisionRank = map[launchDecision]int{
	launchAllow: 1,
	launchAsk:   2,
	launchDeny:  3,
}

// launchPolicy is the compiled, immutable snapshot of the user's rules.
type launchPolicy struct {
	decisions map[string]launchDecision
}

// evaluate returns the decision for an exact command string; unmatched
// commands return ask.
func (p *launchPolicy) evaluate(command string) launchDecision {
	if d, ok := p.decisions[command]; ok {
		return d
	}
	return launchAsk
}

// compileLaunchPolicy validates raw rule entries strictly and snapshots them
// into an immutable policy. Each entry must be an object with exactly the
// keys "command" (non-empty string) and "decision" (allow|ask|deny); any
// other shape is rejected so a malformed policy fails loudly at load instead
// of silently permitting a launch.
func compileLaunchPolicy(rules []json.RawMessage) (*launchPolicy, error) {
	decisions := make(map[string]launchDecision, len(rules))
	for i, raw := range rules {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, fmt.Errorf("invalid launchPolicy rule at index %d: expected an object with command and decision", i)
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid launchPolicy rule at index %d: expected only command and decision", i)
		}
		rawCommand, ok := fields["command"]
		if !ok {
			return nil, fmt.Errorf("invalid launchPolicy rule at index %d: missing command", i)
		}
		rawDecision, ok := fields["decision"]
		if !ok {
			return nil, fmt.Errorf("invalid launchPolicy rule at index %d: missing decision", i)
		}
		var command string
		if err := json.Unmarshal(rawCommand, &command); err != nil || command == "" {
			return nil, fmt.Errorf("invalid launchPolicy rule at index %d: command must be a non-empty string", i)
		}
		var decision launchDecision
		if err := json.Unmarshal(rawDecision, &decision); err != nil {
			return nil, fmt.Errorf("invalid launchPolicy rule at index %d: decision must be allow, ask, or deny", i)
		}
		rank, ok := launchDecisionRank[decision]
		if !ok {
			return nil, fmt.Errorf("invalid launchPolicy rule at index %d: decision must be allow, ask, or deny", i)
		}
		if prev, exists := decisions[command]; !exists || rank > launchDecisionRank[prev] {
			decisions[command] = decision
		}
	}
	return &launchPolicy{decisions: decisions}, nil
}

// launchPolicyPath resolves the global policy file path: JINN_CONFIG_DIR
// when set, else os.UserConfigDir(), joined with jinn/launch-policy.json —
// the same base convention as the run_plan stats file.
func launchPolicyPath() (string, error) {
	base := os.Getenv("JINN_CONFIG_DIR")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("launch policy: resolve config dir: %w", err)
		}
		base = dir
	}
	return filepath.Join(base, "jinn", "launch-policy.json"), nil
}

// loadLaunchPolicy reads and compiles the global policy file. A missing file
// returns (nil, nil) — no policy. A present but malformed file returns an
// error so the engine fails to construct rather than running unguarded.
func loadLaunchPolicy() (*launchPolicy, error) {
	path, err := launchPolicyPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("launch policy: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("launch policy: read %s: %w", path, err)
	}
	var doc struct {
		Rules []json.RawMessage `json:"launchPolicy"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid launch policy %s: %w", path, err)
	}
	if doc.Rules == nil {
		return nil, fmt.Errorf("invalid launch policy %s: missing launchPolicy array", path)
	}
	policy, err := compileLaunchPolicy(doc.Rules)
	if err != nil {
		return nil, fmt.Errorf("invalid launch policy %s: %w", path, err)
	}
	return policy, nil
}
