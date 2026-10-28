package main

import (
	"flag"
	"strconv"
	"strings"
)

// Preserve the requested error format even when parsing stops before --json.
// Respect flag values and the positional/-- boundary so a URL or query is never
// interpreted as an output-format option.
func webJSONRequested(flags *flag.FlagSet, args []string) bool {
	enabled := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" || argument == "-" || !strings.HasPrefix(argument, "-") {
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(argument, "-"), "-"), "=")
		if name == "json" {
			if !hasValue {
				enabled = true
			} else if parsed, err := strconv.ParseBool(value); err == nil {
				enabled = parsed
			}
			continue
		}
		option := flags.Lookup(name)
		if option == nil || hasValue {
			continue
		}
		boolean, ok := option.Value.(interface{ IsBoolFlag() bool })
		if !ok || !boolean.IsBoolFlag() {
			index++
		}
	}
	return enabled
}
