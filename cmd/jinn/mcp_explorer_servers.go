package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dotcommander/jinn/internal/mcpexplore"
)

const (
	mcpServerFormatNames      = "names"
	mcpServerOptionForwardEnv = "--pass-env"
	mcpServerOptionHTTP       = "--http"
	mcpServerOptionStdio      = "--stdio"
	mcpServerTransportHTTP    = "http"
	mcpServerTransportStdio   = "stdio"
)

type mcpServerListOutput struct {
	SchemaVersion int                 `json:"schema_version"`
	Servers       []mcpServerListItem `json:"servers"`
}

type mcpServerListItem struct {
	Name   string            `json:"name"`
	Server mcpexplore.Server `json:"server"`
}

type mcpServerRegistration struct {
	name    string
	server  mcpexplore.Server
	replace bool
}

func runMCPServers(args []string) error {
	if len(args) == 0 || args[0] == mcpExplorerHelpLong || args[0] == mcpExplorerHelpShort || args[0] == mcpExplorerHelpWord {
		return errors.New("usage: jinn mcp servers list | add NAME --http URL | add NAME --stdio PATH | remove NAME")
	}
	switch args[0] {
	case mcpExplorerActionList:
		format, err := parseMCPServersListFormat(args[1:])
		if err != nil {
			return err
		}
		return runMCPServersList(format)
	case "add":
		return runMCPServersAdd(args[1:])
	case "remove":
		return runMCPServersRemove(args[1:])
	default:
		return fmt.Errorf("unknown mcp servers command %q: use list, add, or remove", args[0])
	}
}

//nolint:gosec // The index increment is guarded by an explicit remaining-argument check.
func parseMCPServersListFormat(args []string) (string, error) {
	format := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == mcpExplorerOptionFormat:
			if index+1 >= len(args) {
				return "", errors.New("--format requires a value")
			}
			index++
			format = args[index]
		case strings.HasPrefix(arg, mcpExplorerOptionFormat+"="):
			format = strings.TrimPrefix(arg, mcpExplorerOptionFormat+"=")
		default:
			return "", errors.New("mcp servers list does not accept arguments other than --format")
		}
	}
	if format != "" && format != mcpExplorerFormatJSON && format != mcpExplorerFormatHuman && format != mcpServerFormatNames {
		return "", fmt.Errorf("invalid --format %q: use json, human, or names", format)
	}
	return format, nil
}

func runMCPServersList(format string) error {
	registry, _, err := mcpexplore.LoadRegistry()
	if err != nil {
		return err
	}
	output := mcpServerListOutput{SchemaVersion: 1, Servers: make([]mcpServerListItem, 0, len(registry.Servers))}
	for _, name := range registry.SortedAliases() {
		output.Servers = append(output.Servers, mcpServerListItem{Name: name, Server: registry.Servers[name]})
	}
	if format == mcpServerFormatNames {
		for _, name := range mcpServerNames(output) {
			if _, err := fmt.Fprintln(os.Stdout, name); err != nil {
				return err
			}
		}
		return nil
	}
	return writeMCPExplorerOutput(os.Stdout, format, output)
}

func mcpServerNames(output mcpServerListOutput) []string {
	names := make([]string, 0, len(output.Servers))
	for _, item := range output.Servers {
		names = append(names, item.Name)
	}
	return names
}

func runMCPServersAdd(args []string) error {
	registration, err := parseMCPServerRegistration(args)
	if err != nil {
		return err
	}
	return updateMCPServerRegistration(registration)
}

func parseMCPServerRegistration(args []string) (mcpServerRegistration, error) {
	if len(args) < 3 {
		return mcpServerRegistration{}, errors.New("mcp servers add requires NAME and exactly one of --http URL or --stdio PATH")
	}
	registration := mcpServerRegistration{name: args[0]}
	for index := 1; index < len(args); index++ {
		option := args[index]
		switch option {
		case "--replace":
			registration.replace = true
		case mcpServerOptionHTTP, mcpServerOptionStdio, "--token-env", mcpExplorerOptionArg, mcpServerOptionForwardEnv:
			remaining := args[index+1:]
			if len(remaining) == 0 {
				return registration, fmt.Errorf("%s requires a value", option)
			}
			value := remaining[0]
			index++
			if err := applyMCPServerRegistrationOption(&registration.server, option, value); err != nil {
				return registration, err
			}
		default:
			return registration, fmt.Errorf("unknown mcp servers add option %q", option)
		}
	}
	if registration.server.Transport == "" {
		return registration, errors.New("mcp servers add requires exactly one of --http or --stdio")
	}
	return registration, nil
}

func applyMCPServerRegistrationOption(server *mcpexplore.Server, option, value string) error {
	switch option {
	case mcpServerOptionHTTP:
		if server.Transport != "" {
			return errors.New("use exactly one of --http or --stdio")
		}
		server.Transport, server.URL = mcpServerTransportHTTP, value
	case mcpServerOptionStdio:
		if server.Transport != "" {
			return errors.New("use exactly one of --http or --stdio")
		}
		server.Transport, server.Command = mcpServerTransportStdio, value
	case "--token-env":
		server.TokenEnv = value
	case mcpExplorerOptionArg:
		server.Args = append(server.Args, value)
	case mcpServerOptionForwardEnv:
		server.PassEnv = append(server.PassEnv, value)
	}
	return nil
}

func updateMCPServerRegistration(registration mcpServerRegistration) error {
	return mcpexplore.UpdateRegistry(func(registry *mcpexplore.Registry) error {
		if _, exists := registry.Servers[registration.name]; exists && !registration.replace {
			return fmt.Errorf("MCP server @%s already exists; use --replace to replace it", registration.name)
		}
		registry.Servers[registration.name] = registration.server
		return nil
	})
}

func runMCPServersRemove(args []string) error {
	if len(args) != 1 {
		return errors.New("mcp servers remove requires exactly one NAME")
	}
	name := args[0]
	return mcpexplore.UpdateRegistry(func(registry *mcpexplore.Registry) error {
		if _, exists := registry.Servers[name]; !exists {
			return fmt.Errorf("MCP server alias @%s not found", name)
		}
		delete(registry.Servers, name)
		return nil
	})
}
