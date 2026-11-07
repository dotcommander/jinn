package main

import "strings"

const (
	cliAsk = "ask"
	cliMCP = "mcp"
	cliWeb = "web"
)

func requestedMCPProfile(arguments []string) bool {
	for _, argument := range arguments {
		if argument == cliAsk || argument == cliMCP || argument == cliWeb {
			return false
		}
		if argument == "--mcp-profile" || strings.HasPrefix(argument, "--mcp-profile=") {
			return true
		}
	}
	return false
}
