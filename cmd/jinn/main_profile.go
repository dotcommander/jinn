package main

import "strings"

func requestedMCPProfile(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "ask" || argument == "mcp" || argument == "web" {
			return false
		}
		if argument == "--mcp-profile" || strings.HasPrefix(argument, "--mcp-profile=") {
			return true
		}
	}
	return false
}
