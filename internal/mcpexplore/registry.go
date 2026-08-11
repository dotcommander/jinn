package mcpexplore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

const registryVersion = 1

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Server is one user-owned, shell-free MCP target.
type Server struct {
	Transport string   `json:"transport"`
	URL       string   `json:"url,omitempty"`
	TokenEnv  string   `json:"token_env,omitempty"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	PassEnv   []string `json:"pass_env,omitempty"`
}

// Registry is the strict versioned on-disk server registry.
type Registry struct {
	Version int               `json:"version"`
	Servers map[string]Server `json:"servers"`
}

// ValidateAlias checks the public, case-sensitive registry alias contract.
func ValidateAlias(alias string) error {
	if !aliasPattern.MatchString(alias) {
		return fmt.Errorf("invalid MCP server alias %q", alias)
	}
	return nil
}

// AliasTarget resolves a registered alias into a transport target and server definition.
func AliasTarget(alias string, environ []string) (Target, Server, error) {
	if err := ValidateAlias(alias); err != nil {
		return Target{}, Server{}, err
	}
	registry, _, err := LoadRegistry()
	if err != nil {
		return Target{}, Server{}, err
	}
	server, ok := registry.Servers[alias]
	if !ok {
		return Target{}, Server{}, fmt.Errorf("MCP server alias @%s not found", alias)
	}
	target, err := TargetForServer(server, environ)
	if err != nil {
		return Target{}, Server{}, err
	}
	return target, server, nil
}

// TargetForServer converts one already-loaded registry server into a constrained target.
func TargetForServer(server Server, environ []string) (Target, error) {
	if err := validateServer(server); err != nil {
		return Target{}, err
	}
	if server.Transport == transportHTTP {
		return Target{Endpoint: server.URL, TokenEnv: server.TokenEnv, DisableDefaultToken: true}, nil
	}
	return Target{Command: server.Command, CommandArgs: append([]string(nil), server.Args...), CommandEnv: RestrictedCommandEnvironment(environ, server.PassEnv)}, nil
}

// SortedAliases returns registered aliases in lexical order.
func (r Registry) SortedAliases() []string {
	aliases := make([]string, 0, len(r.Servers))
	for alias := range r.Servers {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases
}

// RestrictedCommandEnvironment returns the registry subprocess environment.
func RestrictedCommandEnvironment(environ, passEnv []string) []string {
	allowed := registeredEnvironmentNames()
	for _, name := range passEnv {
		allowed[normalizedEnvironmentName(name)] = struct{}{}
	}
	filtered := make([]string, 0, len(environ))
	for _, item := range environ {
		name, _, _ := strings.Cut(item, "=")
		name = normalizedEnvironmentName(name)
		if _, ok := allowed[name]; ok {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func decodeRegistry(data []byte) (Registry, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var registry Registry
	if err := decoder.Decode(&registry); err != nil {
		return Registry{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Registry{}, errors.New("must contain exactly one JSON object")
	}
	if err := validateRegistry(registry); err != nil {
		return Registry{}, err
	}
	return registry, nil
}

func validateRegistry(registry Registry) error {
	if registry.Version != registryVersion {
		return fmt.Errorf("unsupported MCP server registry version %d", registry.Version)
	}
	if registry.Servers == nil {
		return errors.New("MCP server registry servers must be an object")
	}
	for alias, server := range registry.Servers {
		if err := ValidateAlias(alias); err != nil {
			return err
		}
		if err := validateServer(server); err != nil {
			return fmt.Errorf("MCP server @%s: %w", alias, err)
		}
	}
	return nil
}

func validateServer(server Server) error {
	switch server.Transport {
	case transportHTTP:
		return validateHTTPServer(server)
	case transportStdio:
		return validateStdioServer(server)
	default:
		return fmt.Errorf("unsupported transport %q", server.Transport)
	}
}

func validateHTTPServer(server Server) error {
	if server.URL == "" || server.Command != "" || len(server.Args) != 0 || len(server.PassEnv) != 0 {
		return errors.New("http server must contain only url and optional token_env")
	}
	if server.TokenEnv != "" && !environmentNamePattern.MatchString(server.TokenEnv) {
		return fmt.Errorf("invalid token_env %q", server.TokenEnv)
	}
	if err := (Target{Endpoint: server.URL}).Validate(); err != nil {
		return err
	}
	parsed, err := url.Parse(server.URL)
	if err != nil {
		return errors.New("invalid MCP endpoint: use an http:// or https:// URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("registered http url must not contain a query or fragment; use token_env for credentials")
	}
	return nil
}

func validateStdioServer(server Server) error {
	if server.Command == "" || !filepath.IsAbs(server.Command) || server.URL != "" || server.TokenEnv != "" {
		return errors.New("stdio server requires an absolute command and no http fields")
	}
	if err := validatePassEnvironment(server.PassEnv); err != nil {
		return err
	}
	return validateRegisteredCommand(server.Command, server.Args)
}

func validatePassEnvironment(passEnv []string) error {
	seen := make(map[string]struct{}, len(passEnv))
	for _, name := range passEnv {
		normalized := normalizedEnvironmentName(name)
		if !environmentNamePattern.MatchString(name) || normalized == normalizedEnvironmentName(HTTPTokenEnv) {
			return fmt.Errorf("invalid pass_env %q", name)
		}
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("duplicate pass_env %q", name)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}

func validateRegisteredCommand(command string, args []string) error {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(command)), ".exe")
	switch base {
	case "sh", "bash", "dash", "zsh", "fish", "ksh", "csh", "tcsh", "cmd", "powershell", "pwsh",
		"env", "nohup", "nice", "xargs", "time", "sudo", "chroot", "busybox", "toybox":
		return errors.New("registered stdio command must not invoke a shell or command wrapper")
	}
	for _, argument := range args {
		if strings.Contains(argument, "=") || strings.ContainsAny(argument, "\x00\r\n;&|<>`$") {
			return errors.New("registered stdio arguments must not contain inline assignments or shell syntax")
		}
		name := strings.TrimLeft(argument, "-")
		name = strings.ToLower(strings.ReplaceAll(name, "-", "_"))
		switch name {
		case "h", "token", "access_token", "api_token", "api_key", "auth", "auth_token", "bearer_token", "password", "passwd", "secret", "client_secret", "credential", "credentials", "authorization", "proxy_authorization", "header", "headers", "http_header", "http_headers", "cookie", "set_cookie":
			return errors.New("registered stdio arguments must not contain literal credentials; pass credentials through pass_env")
		}
		header, _, hasHeaderValue := strings.Cut(argument, ":")
		header = strings.ToLower(strings.TrimSpace(header))
		if hasHeaderValue && (header == "authorization" || header == "proxy-authorization" || header == "cookie" || header == "set-cookie" || header == "x-api-key" || header == "api-key") {
			return errors.New("registered stdio arguments must not contain literal headers; pass credentials through pass_env")
		}
	}
	return nil
}

func registeredEnvironmentNames() map[string]struct{} {
	var names []string
	if runtime.GOOS == "windows" {
		names = []string{"PATH", "USERPROFILE", "TEMP", "TMP", "SYSTEMROOT", "COMSPEC", "PATHEXT"}
	} else {
		names = []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "LC_CTYPE", "SSL_CERT_FILE", "SSL_CERT_DIR"}
	}
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[normalizedEnvironmentName(name)] = struct{}{}
	}
	return allowed
}

func normalizedEnvironmentName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}
