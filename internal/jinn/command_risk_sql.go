package jinn

import (
	"strings"
)

func checkSQLCommandArgs(tokens []string) (RiskLevel, string, bool) {
	for _, sql := range inlineSQLStatements(tokens) {
		if isDestructiveSQL(sql) {
			return RiskDangerous, tokens[0] + " destructive SQL — can delete persistent data", true
		}
	}
	return 0, "", false
}

func inlineSQLStatements(tokens []string) []string {
	switch tokens[0] {
	case "psql":
		return optionValues(tokens[1:], "-c", "--command")
	case "mysql":
		return optionValues(tokens[1:], "-e", "--execute")
	case "sqlite3":
		return sqliteInlineStatements(tokens[1:])
	default:
		return nil
	}
}

func optionValues(tokens []string, shortFlag, longFlag string) []string {
	var values []string
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case tok == shortFlag || tok == longFlag:
			if i+1 < len(tokens) {
				values = append(values, tokens[i+1])
				i++
			}
		case strings.HasPrefix(tok, shortFlag) && len(tok) > len(shortFlag):
			values = append(values, tok[len(shortFlag):])
		case strings.HasPrefix(tok, longFlag+"="):
			values = append(values, strings.TrimPrefix(tok, longFlag+"="))
		}
	}
	return values
}

func sqliteInlineStatements(tokens []string) []string {
	for i, tok := range tokens {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if i+1 < len(tokens) {
			return tokens[i+1:]
		}
		return nil
	}
	return nil
}

func isDestructiveSQL(sql string) bool {
	sql = strings.TrimSpace(sql)
	sql = strings.TrimLeft(sql, "({")
	upper := strings.ToUpper(sql)
	destructivePrefixes := []string{
		"DROP ",
		"DROP\n",
		"TRUNCATE ",
		"TRUNCATE\n",
		"DELETE ",
		"DELETE\n",
	}
	for _, prefix := range destructivePrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return upper == "DROP" || upper == "TRUNCATE" || upper == "DELETE"
}

func checkAliasDefinitions(tokens []string) (RiskLevel, string, bool) {
	maxLevel := RiskSafe
	var maxReason string
	for _, tok := range tokens[1:] {
		name, value, ok := strings.Cut(tok, "=")
		if !ok || name == "" || value == "" {
			continue
		}
		level, reason := ClassifyCommand(value)
		if level > maxLevel {
			maxLevel = level
			maxReason = reason
		}
	}
	if maxLevel > RiskSafe {
		return maxLevel, "alias definition: " + maxReason, true
	}
	return 0, "", false
}

func checkHashBindings(tokens []string) (RiskLevel, string, bool) {
	for i := 1; i < len(tokens); i++ {
		if tokens[i] != "-p" || i+1 >= len(tokens) {
			continue
		}
		level, reason := ClassifyCommand(tokens[i+1])
		if level > RiskSafe {
			return level, "hash binding: " + reason, true
		}
		i += 2
	}
	return 0, "", false
}

func checkTeeTargets(tokens []string) (RiskLevel, string, bool) {
	for _, tok := range tokens[1:] {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if isDangerousDevicePath(tok) {
			return RiskDangerous, "tee to device " + tok + " — can destroy data", true
		}
	}
	return 0, "", false
}
