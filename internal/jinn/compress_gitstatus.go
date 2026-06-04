package jinn

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// gitStatusStrategy: status entry and tracking patterns.
	reGitStatusEntry = regexp.MustCompile(`^\t(both modified|new file|modified|deleted|renamed):\s+(.*)`)
	reGitTracking    = regexp.MustCompile(`Your branch is (up to date with|ahead of|behind) '([^']+)'`)
	reGitByCount     = regexp.MustCompile(`by (\d+)`)
)

// ---------------------------------------------------------------------------
// gitStatusStrategy
// ---------------------------------------------------------------------------

// gitStatusStrategy compresses verbose git status output to a compact
// branch + file summary format.
type gitStatusStrategy struct{}

func (s *gitStatusStrategy) Name() string { return "git_status" }

func (s *gitStatusStrategy) AppliesTo(output string, tool string) bool {
	hasBranch := strings.HasPrefix(output, "On branch ") || strings.HasPrefix(output, "HEAD detached")
	hasChanges := strings.Contains(output, "Changes") || strings.Contains(output, "Untracked")
	return hasBranch && hasChanges
}

func (s *gitStatusStrategy) Compress(output string) string {
	lines := splitLines(output)
	if len(lines) == 0 {
		return output
	}

	var b strings.Builder

	// First line is branch info.
	b.WriteString(lines[0])

	// Look for tracking info on subsequent lines.
	for _, line := range lines {
		if m := reGitTracking.FindStringSubmatch(line); m != nil {
			direction := m[1]
			ref := m[2]
			switch direction {
			case "up to date with":
				fmt.Fprintf(&b, " (up to date with %s)", ref)
			case "ahead of":
				s.writeAheadBehind(&b, "ahead", ref, line)
			case "behind":
				s.writeAheadBehind(&b, "behind", ref, line)
			}
			break
		}
	}

	entries := parseGitEntries(lines)

	if len(entries) > 0 {
		b.WriteByte('\n')
		b.WriteString(strings.Join(entries, "  "))
	}

	result := b.String()
	if len(result) >= len(output) {
		return output
	}
	return result
}

// parseGitEntries scans git status lines section by section, returning the
// compact per-file entries (status-char prefix + name, or "+name" for untracked).
func parseGitEntries(lines []string) []string {
	var entries []string
	section := ""
	for _, line := range lines {
		switch {
		case strings.Contains(line, "Changes to be committed:"):
			section = "staged"
		case strings.Contains(line, "Changes not staged for commit:"):
			section = "unstaged"
		case strings.Contains(line, "Untracked files:"):
			section = "untracked"
		case strings.HasPrefix(line, "\t"):
			if section == "untracked" {
				name := line[1:] // Trim leading tab.
				if name != "" {
					entries = append(entries, "+"+name)
				}
			} else if m := reGitStatusEntry.FindStringSubmatch(line); m != nil {
				entries = append(entries, gitStatusChar(m[1])+m[2])
			}
		}
	}
	return entries
}

// writeAheadBehind appends ahead/behind tracking info to the builder.
func (s *gitStatusStrategy) writeAheadBehind(b *strings.Builder, direction, ref, line string) {
	if cm := reGitByCount.FindStringSubmatch(line); cm != nil {
		fmt.Fprintf(b, " (%s %s of %s)", direction, cm[1], ref)
	} else {
		fmt.Fprintf(b, " (%s %s)", direction, ref)
	}
}

// gitStatusChar maps a git status word to a single-character prefix code.
func gitStatusChar(status string) string {
	switch status {
	case "modified":
		return "M "
	case "deleted":
		return "D "
	case "new file":
		return "A "
	case "renamed":
		return "R "
	case "both modified":
		return "C "
	default:
		return "? "
	}
}
