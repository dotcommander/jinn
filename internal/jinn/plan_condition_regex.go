package jinn

import "strings"

// Slash escaping belongs to shorthand delimiters, not the structured regex.
func unescapeConditionDelimiter(pattern string) string {
	var result strings.Builder
	for i := 0; i < len(pattern); {
		if pattern[i] != '\\' {
			result.WriteByte(pattern[i])
			i++
			continue
		}
		start := i
		for i < len(pattern) && pattern[i] == '\\' {
			i++
		}
		count := i - start
		if i < len(pattern) && pattern[i] == '/' && count%2 == 1 {
			count--
		}
		result.WriteString(pattern[start : start+count])
	}
	return result.String()
}
