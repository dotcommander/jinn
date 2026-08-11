package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/voocel/mcp-sdk-go/protocol"
)

const (
	mcpDoctorOptionAll    = "--all"
	mcpDoctorStatusFailed = "failed"
	mcpDoctorStatusOK     = "ok"
)

type mcpDoctorOutput struct {
	SchemaVersion int              `json:"schema_version"`
	Checks        []mcpDoctorCheck `json:"checks"`
	Warnings      []string         `json:"warnings,omitempty"`
}

type mcpDoctorCheck struct {
	Alias           string                   `json:"alias"`
	Transport       string                   `json:"transport"`
	Status          string                   `json:"status"`
	Credential      *mcpDoctorCredential     `json:"credential,omitempty"`
	Environment     []mcpDoctorEnvironment   `json:"environment,omitempty"`
	Executable      string                   `json:"executable,omitempty"`
	Server          *protocol.Implementation `json:"server,omitempty"`
	ProtocolVersion string                   `json:"protocol_version,omitempty"`
	ToolCount       *int                     `json:"tool_count,omitempty"`
	LatencyMS       *int64                   `json:"latency_ms,omitempty"`
	Warnings        []string                 `json:"warnings,omitempty"`
}

type mcpDoctorCredential struct {
	Environment string `json:"environment"`
	Present     bool   `json:"present"`
}

type mcpDoctorEnvironment struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
}

type mcpDoctorConfig struct {
	live    bool
	all     bool
	aliases []string
	timeout time.Duration
}

type mcpDoctorLiveOutcome struct {
	check         mcpDoctorCheck
	failed        bool
	approvalDrift bool
}

type mcpDoctorReportedError struct{}

func (mcpDoctorReportedError) Error() string         { return "MCP doctor found failing checks" }
func (mcpDoctorReportedError) AlreadyReported() bool { return true }

type mcpDoctorDriftReportedError struct{}

func (mcpDoctorDriftReportedError) Error() string {
	return "MCP doctor found missing approval or drift"
}
func (mcpDoctorDriftReportedError) ExitStatus() int       { return 2 }
func (mcpDoctorDriftReportedError) AlreadyReported() bool { return true }

//nolint:gocyclo // Offline, live, operational, and drift outcomes remain visible in one report flow.
func runMCPDoctor(ctx context.Context, args []string) error {
	format, args, err := collectMCPExplorerJSONOrHumanFormat(args)
	if err != nil {
		return err
	}
	config, err := parseMCPDoctor(args)
	if err != nil {
		return err
	}
	registry, _, err := mcpexplore.LoadRegistry()
	if err != nil {
		return err
	}
	if config.aliases == nil {
		config.aliases = registry.SortedAliases()
	}
	warnings, err := mcpRegistryPermissionWarnings()
	if err != nil {
		return fmt.Errorf("check MCP registry permissions: %w", err)
	}
	snapshotWarnings, err := mcpSnapshotPermissionWarnings(config.aliases)
	if err != nil {
		return fmt.Errorf("check MCP snapshot permissions: %w", err)
	}
	warnings = append(warnings, snapshotWarnings...)
	output := mcpDoctorOutput{SchemaVersion: 1, Checks: make([]mcpDoctorCheck, 0, len(config.aliases)), Warnings: warnings}
	failed := len(warnings) != 0
	approvalDrift := false
	for _, alias := range config.aliases {
		server, ok := registry.Servers[alias]
		if !ok {
			return fmt.Errorf("MCP server alias @%s not found", alias)
		}
		check, checkFailed := doctorOfflineCheck(alias, server)
		if !checkFailed && config.live {
			outcome := doctorLiveCheck(ctx, config.timeout, check)
			check, checkFailed = outcome.check, outcome.failed
			approvalDrift = approvalDrift || outcome.approvalDrift
		}
		failed = failed || checkFailed
		output.Checks = append(output.Checks, check)
	}
	if err := writeMCPExplorerOutput(os.Stdout, format, output); err != nil {
		return err
	}
	if failed {
		return mcpDoctorReportedError{}
	}
	if approvalDrift {
		return mcpDoctorDriftReportedError{}
	}
	return nil
}

func parseMCPDoctor(args []string) (mcpDoctorConfig, error) {
	config := mcpDoctorConfig{timeout: mcpExplorerDefaultTimeout}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == mcpDoctorOptionAll:
			config.all, config.live = true, true
		case arg == mcpExplorerOptionTimeout:
			remaining := args[index+1:]
			if len(remaining) == 0 {
				return mcpDoctorConfig{timeout: config.timeout}, errors.New("--timeout requires a value")
			}
			index++
			value := remaining[0]
			parsed, err := time.ParseDuration(value)
			if err != nil || parsed <= 0 {
				return mcpDoctorConfig{timeout: config.timeout}, fmt.Errorf("invalid --timeout %q", value)
			}
			config.timeout = parsed
		case strings.HasPrefix(arg, mcpExplorerOptionTimeout+"="):
			parsed, err := time.ParseDuration(strings.TrimPrefix(arg, mcpExplorerOptionTimeout+"="))
			if err != nil || parsed <= 0 {
				return mcpDoctorConfig{timeout: config.timeout}, fmt.Errorf("invalid --timeout %q", strings.TrimPrefix(arg, mcpExplorerOptionTimeout+"="))
			}
			config.timeout = parsed
		case strings.HasPrefix(arg, "@"):
			config.aliases = append(config.aliases, strings.TrimPrefix(arg, "@"))
			config.live = true
		default:
			return mcpDoctorConfig{timeout: config.timeout}, fmt.Errorf("unknown mcp doctor option %q", arg)
		}
	}
	if config.all && len(config.aliases) != 0 {
		return mcpDoctorConfig{timeout: config.timeout}, errors.New("mcp doctor accepts either @NAME or --all")
	}
	if len(config.aliases) > 1 {
		return mcpDoctorConfig{timeout: config.timeout}, errors.New("mcp doctor accepts at most one @NAME")
	}
	return config, nil
}

func doctorOfflineCheck(alias string, server mcpexplore.Server) (mcpDoctorCheck, bool) {
	check := mcpDoctorCheck{Alias: alias, Transport: server.Transport, Status: mcpDoctorStatusOK}
	if server.Transport == mcpServerTransportHTTP && server.TokenEnv != "" {
		present := strings.TrimSpace(os.Getenv(server.TokenEnv)) != ""
		check.Credential = &mcpDoctorCredential{Environment: server.TokenEnv, Present: present}
		if !present {
			check.Status = mcpDoctorStatusFailed
			check.Warnings = append(check.Warnings, "required credential environment variable is not set")
		}
	}
	if server.Transport == mcpServerTransportStdio {
		check.Executable = server.Command
		info, err := os.Stat(server.Command)
		if err != nil || info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
			check.Status = mcpDoctorStatusFailed
			check.Warnings = append(check.Warnings, "registered command is not executable")
		}
		for _, name := range server.PassEnv {
			_, present := os.LookupEnv(name)
			check.Environment = append(check.Environment, mcpDoctorEnvironment{Name: name, Present: present})
			if !present {
				check.Status = mcpDoctorStatusFailed
				check.Warnings = append(check.Warnings, fmt.Sprintf("required environment variable %s is not set", name))
			}
		}
	}
	return check, check.Status != mcpDoctorStatusOK
}

//nolint:nestif // The initialize/discover/list sequence must retain its first operational failure.
func doctorLiveCheck(parent context.Context, timeout time.Duration, check mcpDoctorCheck) mcpDoctorLiveOutcome {
	target, _, err := mcpexplore.AliasTarget(check.Alias, os.Environ())
	if err != nil {
		check.Status = mcpDoctorStatusFailed
		check.Warnings = append(check.Warnings, err.Error())
		return mcpDoctorLiveOutcome{check: check, failed: true}
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	started := time.Now()
	client, err := mcpexplore.New(ctx, target)
	if err == nil {
		defer func() { _ = client.Close() }()
		var discovery *protocol.DiscoverResult
		discovery, err = client.Discover(ctx)
		if err == nil {
			var tools []*protocol.Tool
			tools, err = client.ListTools(ctx)
			if err == nil {
				count := len(tools)
				check.ToolCount = &count
				check.Server = discovery.Meta.ServerInfo
				check.ProtocolVersion = protocol.Version
				snapshotOutcome := doctorSnapshotWarnings(check.Alias, discovery.Meta.ServerInfo, tools)
				check.Warnings = append(check.Warnings, snapshotOutcome.warnings...)
				if snapshotOutcome.failed {
					check.Status = mcpDoctorStatusFailed
				}
				latency := time.Since(started).Milliseconds()
				check.LatencyMS = &latency
				return mcpDoctorLiveOutcome{check: check, failed: snapshotOutcome.failed, approvalDrift: snapshotOutcome.approvalDrift}
			}
		}
	}
	latency := time.Since(started).Milliseconds()
	check.LatencyMS = &latency
	if err != nil {
		check.Status = mcpDoctorStatusFailed
		check.Warnings = append(check.Warnings, "live initialize/discover/tools-list failed")
		return mcpDoctorLiveOutcome{check: check, failed: true}
	}
	return mcpDoctorLiveOutcome{check: check}
}

func mcpRegistryPermissionWarnings() ([]string, error) {
	path, err := mcpexplore.RegistryPath()
	if err != nil {
		return nil, err
	}
	warnings := make([]string, 0, 4)
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{filepath.Dir(path), 0o700}, {path, 0o600}, {path + ".bak", 0o600}, {path + ".lock", 0o600}} {
		info, err := os.Stat(item.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode().Perm() != item.mode {
			warnings = append(warnings, fmt.Sprintf("%s permissions are more permissive than required", filepath.Base(item.path)))
		}
	}
	return warnings, nil
}
