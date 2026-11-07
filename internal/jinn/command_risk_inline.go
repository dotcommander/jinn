package jinn

import (
	"strings"
)

func isInlineCodeExecution(verb string, tokens []string) bool {
	inlineFlags := inlineCodeFlags(verb)
	if len(inlineFlags) == 0 {
		return false
	}
	for _, tok := range tokens[1:] {
		for flag := range inlineFlags {
			if matchesInlineCodeFlag(tok, flag) {
				return true
			}
		}
	}
	return false
}

func matchesInlineCodeFlag(tok, flag string) bool {
	if tok == flag {
		return true
	}
	if flag != "" && strings.HasPrefix(tok, flag+"=") {
		return true
	}
	return isAttachedShortInlineFlag(tok, flag)
}

func isAttachedShortInlineFlag(tok, flag string) bool {
	if len(flag) != 2 || len(tok) <= len(flag) || tok[0] != '-' || strings.HasPrefix(tok, "--") {
		return false
	}
	return strings.ContainsRune(tok[1:], rune(flag[1]))
}

func inlineCodeFlags(verb string) map[string]bool {
	if flags, ok := inlineCodeFlagRules[verb]; ok {
		return flags
	}
	if isPythonVerb(verb) {
		return inlineCodeFlagRules["python"]
	}
	if strings.HasPrefix(verb, "lua5.") {
		return inlineCodeFlagRules["lua"]
	}
	return nil
}

var inlineCodeFlagRules = map[string]map[string]bool{
	"python":         {"-c": true},
	"node":           {"-e": true, "--eval": true, "-p": true, "--print": true},
	"nodejs":         {"-e": true, "--eval": true, "-p": true, "--print": true},
	"perl":           {"-e": true, "-E": true},
	"ruby":           {"-e": true},
	"php":            {"-r": true, "-B": true, "-R": true, "-E": true},
	"lua":            {"-e": true},
	"Rscript":        {"-e": true, "--expression": true},
	"rscript":        {"-e": true, "--expression": true},
	"julia":          {"-e": true, "--eval": true},
	"osascript":      {"-e": true},
	"pwsh":           {"-c": true, "-Command": true, "-command": true, "-EncodedCommand": true, "-encodedcommand": true},
	"powershell":     {"-c": true, "-Command": true, "-command": true, "-EncodedCommand": true, "-encodedcommand": true},
	"powershell.exe": {"-c": true, "-Command": true, "-command": true, "-EncodedCommand": true, "-encodedcommand": true},
	"cmd":            {"/c": true, "/C": true},
	"cmd.exe":        {"/c": true, "/C": true},
}

func isInlineCodeInterpreter(verb string) bool {
	return len(inlineCodeFlags(verb)) > 0
}

func isStdinCodeInterpreterInvocation(verb string, tokens []string) bool {
	if !isInlineCodeInterpreter(verb) {
		return false
	}
	program, implicit := stdinCodeProgram(verb, tokens)
	return implicit || isStdinCodeProgram(program)
}

func stdinCodeProgram(verb string, tokens []string) (string, bool) {
	for i := 1; i < len(tokens); i++ {
		tok := tokens[i]
		if skipped, redirection := inputRedirectionArgumentCount(tok); redirection {
			i += skipped
			continue
		}
		if tok == "-" {
			return tok, false
		}
		role, value, attached := interpreterOptionValue(verb, tok)
		if role == interpreterConfigArgument {
			if !attached {
				i++
			}
			continue
		}
		if role == interpreterSourceArgument {
			return interpreterSourceProgram(tokens, i, value, attached)
		}
		if role == interpreterOptionNone && !isFlag(tok) {
			return tok, false
		}
	}
	return "", true
}

func inputRedirectionArgumentCount(token string) (int, bool) {
	if !isInputRedirectionToken(token) {
		return 0, false
	}
	if inputRedirectionConsumesNext(token) {
		return 1, true
	}
	return 0, true
}

func interpreterSourceProgram(tokens []string, index int, attachedValue string, attached bool) (string, bool) {
	if attached {
		return attachedValue, false
	}
	if index+1 < len(tokens) {
		return tokens[index+1], false
	}
	return "", true
}

func isStdinCodeProgram(program string) bool {
	return program == "-" || program == "/dev/stdin" || program == "/dev/fd/0"
}

func isImplicitNodeStdinInvocation(verb string, tokens []string) bool {
	if verb != "node" && verb != "nodejs" {
		return false
	}
	for _, tok := range tokens[1:] {
		if tok == "--input-type" || strings.HasPrefix(tok, "--input-type=") {
			program, implicit := stdinCodeProgram(verb, tokens)
			return implicit && program == ""
		}
	}
	return false
}

type interpreterOptionRole uint8

const (
	interpreterOptionNone interpreterOptionRole = iota
	interpreterConfigArgument
	interpreterSourceArgument
)

// interpreterOptionValue distinguishes options that configure an interpreter
// from options that select the source program it executes.
func interpreterOptionValue(verb, token string) (interpreterOptionRole, string, bool) {
	for _, option := range interpreterOptionSpecs(verb) {
		if token == option.name {
			return option.role, "", false
		}
		if strings.HasPrefix(option.name, "--") && strings.HasPrefix(token, option.name+"=") {
			return option.role, strings.TrimPrefix(token, option.name+"="), true
		}
		if !strings.HasPrefix(option.name, "--") && strings.HasPrefix(token, option.name) && len(token) > len(option.name) {
			return option.role, token[len(option.name):], true
		}
	}
	return interpreterOptionNone, "", false
}

type interpreterOptionSpec struct {
	name string
	role interpreterOptionRole
}

func interpreterOptionSpecs(verb string) []interpreterOptionSpec {
	if isPythonVerb(verb) {
		return []interpreterOptionSpec{
			{"-W", interpreterConfigArgument}, {"-X", interpreterConfigArgument},
			{"-m", interpreterSourceArgument}, {"--check-hash-based-pycs", interpreterConfigArgument},
		}
	}
	switch verb {
	case "node", "nodejs":
		return []interpreterOptionSpec{
			{"-r", interpreterConfigArgument}, {"--require", interpreterConfigArgument},
			{"--import", interpreterConfigArgument}, {"--loader", interpreterConfigArgument},
			{"--experimental-loader", interpreterConfigArgument}, {"-C", interpreterConfigArgument},
			{"--conditions", interpreterConfigArgument}, {"--input-type", interpreterConfigArgument},
		}
	case "perl":
		return []interpreterOptionSpec{{"-I", interpreterConfigArgument}, {"-M", interpreterConfigArgument}, {"-m", interpreterConfigArgument}}
	case "ruby":
		return []interpreterOptionSpec{{"-I", interpreterConfigArgument}, {"-r", interpreterConfigArgument}, {"-C", interpreterConfigArgument}, {"--directory", interpreterConfigArgument}}
	case "php":
		return []interpreterOptionSpec{{"-c", interpreterConfigArgument}, {"-d", interpreterConfigArgument}, {"-f", interpreterSourceArgument}, {"--file", interpreterSourceArgument}}
	default:
		return nil
	}
}

func isPythonVerb(verb string) bool {
	return verb == "python" || strings.HasPrefix(verb, "python2") || strings.HasPrefix(verb, "python3")
}

func isShellInterpreter(verb string) bool {
	switch verb {
	case "sh", "bash", "zsh", "dash", "ksh", "mksh", "ash", "fish", "csh", "tcsh":
		return true
	default:
		return false
	}
}

// applyArgHeuristics upgrades risk based on flag/argument patterns.
// It returns the base rule unchanged when no heuristic fires.
// Each per-verb helper returns (level, reason, true) when its heuristic fires.
