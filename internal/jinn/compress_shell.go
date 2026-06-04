package jinn

import (
	"strings"
)

// lastSegmentVerb returns the first verb of the LAST pipeline segment of cmd —
// i.e. the command whose stdout the caller actually sees. For "cd x && go test ./..."
// that is "go"; for "git log | head" it is "head".
func lastSegmentVerb(cmd string) string {
	segs := splitOnOperators(cmd)
	if len(segs) == 0 {
		return firstVerb(cmd)
	}
	return firstVerb(segs[len(segs)-1])
}

// lastSegmentArgs returns the argument tokens of the LAST pipeline segment of cmd,
// with leading env assignments and the verb itself stripped, and surrounding
// single/double quotes removed from each token. Not a full parser — strings.Fields
// is used for splitting.
func lastSegmentArgs(cmd string) []string {
	segs := splitOnOperators(cmd)
	seg := cmd
	if len(segs) > 0 {
		seg = segs[len(segs)-1]
	}
	fields := strings.Fields(seg)
	// skip leading env assignments and the verb itself
	start := 0
	for start < len(fields) && isEnvAssignment(fields[start]) {
		start++
	}
	if start < len(fields) {
		start++ // skip the verb
	}
	if start >= len(fields) {
		return nil
	}
	result := make([]string, 0, len(fields)-start)
	for _, f := range fields[start:] {
		// strip surrounding single or double quotes
		if len(f) >= 2 && ((f[0] == '\'' && f[len(f)-1] == '\'') || (f[0] == '"' && f[len(f)-1] == '"')) {
			f = f[1 : len(f)-1]
		}
		result = append(result, f)
	}
	return result
}

// subcommand walks args left-to-right, skipping flag tokens and their values
// (when the flag is in valueFlags), and returns the first non-flag token.
func subcommand(args []string, valueFlags map[string]bool) string {
	i := 0
	for i < len(args) {
		a := args[i]
		if !isFlag(a) {
			return a
		}
		if valueFlags[a] {
			i += 2 // skip flag and its value
		} else {
			i++
		}
	}
	return ""
}

// hasShortFlagL reports whether any arg in args is a short flag containing 'l'.
// Short flags start with '-' but not '--'.
func hasShortFlagL(args []string) bool {
	for _, a := range args {
		if isFlag(a) && !strings.HasPrefix(a, "--") && strings.ContainsRune(a, 'l') {
			return true
		}
	}
	return false
}

// dockerTabularSubcmds lists docker/podman subcommands that produce padded tables.
var dockerTabularSubcmds = map[string]bool{
	"ps": true, "images": true, "image": true, "container": true,
	"network": true, "volume": true, "node": true, "service": true,
	"stack": true, "system": true, "history": true, "top": true,
	"stats": true, "port": true, "version": true, "search": true,
}

// kubectlTabularSubcmds lists kubectl/oc subcommands that produce padded tables.
var kubectlTabularSubcmds = map[string]bool{
	"get": true, "top": true, "version": true, "api-resources": true,
	"api-versions": true, "cluster-info": true, "explain": true, "config": true,
}

// dockerValueFlags lists docker/podman flags that consume a following value token.
var dockerValueFlags = map[string]bool{
	"-H": true, "--host": true, "-c": true, "--context": true,
	"--config": true, "-l": true, "--log-level": true,
	"--tls": true, "--tlscacert": true, "--tlscert": true, "--tlskey": true,
}

// kubectlValueFlags lists kubectl/oc flags that consume a following value token.
var kubectlValueFlags = map[string]bool{
	"-n": true, "--namespace": true, "--context": true, "--kubeconfig": true,
	"--cluster": true, "--user": true, "-s": true, "--server": true,
	"--as": true, "--token": true, "--request-timeout": true,
}

// compressShellOutput applies command-aware compression to raw shell output,
// then runs the generic strategy chain (defaultCompressor). Fail-open: if any
// command-aware transform panics, fall back to the generic chain on the
// untouched input.
func compressShellOutput(raw, command string) (out string) {
	input := raw
	defer func() {
		if r := recover(); r != nil {
			out, _ = defaultCompressor.Compress(input, "run_shell")
		}
	}()

	switch lastSegmentVerb(command) {
	case "ps", "df", "du", "lsof", "netstat", "ss", "free", "vmstat", "iostat", "mount", "lsblk", "top":
		raw = collapseColumnPadding(raw)
	case "ls":
		if hasShortFlagL(lastSegmentArgs(command)) {
			raw = collapseColumnPadding(raw)
		}
	case "docker", "podman":
		if dockerTabularSubcmds[subcommand(lastSegmentArgs(command), dockerValueFlags)] {
			raw = collapseColumnPadding(raw)
		}
	case "kubectl", "oc":
		if kubectlTabularSubcmds[subcommand(lastSegmentArgs(command), kubectlValueFlags)] {
			raw = collapseColumnPadding(raw)
		}
	case "git":
		raw = condenseGitLog(raw)
		// TODO: golangci-lint/eslint/tsc -> group findings by file. (Modest gain, fragile parsing — deferred.)
	}

	out, _ = defaultCompressor.Compress(raw, "run_shell")
	return out
}

// collapseColumnPadding replaces every run of 2 or more spaces with a single
// space, line by line, leaving tabs and single spaces untouched. Used only on
// known-tabular command output (see switch in compressShellOutput).
func collapseColumnPadding(s string) string {
	lines := splitLines(s)
	changed := false
	for i, line := range lines {
		c := collapseInnerSpaces(line)
		if c != line {
			lines[i] = c
			changed = true
		}
	}
	if !changed {
		return s
	}
	return strings.Join(lines, "\n")
}

// collapseInnerSpaces collapses runs of 2+ ASCII spaces to one. Hand-rolled to
// avoid a regexp dependency on a hot path; tabs and other whitespace are kept.
func collapseInnerSpaces(line string) string {
	if !strings.Contains(line, "  ") {
		return line
	}
	var b strings.Builder
	b.Grow(len(line))
	prevSpace := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
			b.WriteByte(' ')
			continue
		}
		prevSpace = false
		b.WriteByte(ch)
	}
	return b.String()
}
