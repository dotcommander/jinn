package mcpexplore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryStrictDecodeAndAliasValidation(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	for _, data := range []string{
		`{"version":2,"servers":{}}`,
		`{"version":1,"unknown":true,"servers":{}}`,
		`{"version":1,"servers":{"bad name":{"transport":"http","url":"https://example.test/mcp"}}}`,
		`{"version":1,"servers":{"remote":{"transport":"http","url":"https://example.test/mcp","headers":{}}}}`,
		`{"version":1,"servers":{"local":{"transport":"stdio","command":"relative"}}}`,
		`{"version":1,"servers":{"remote":{"transport":"http","url":"https://example.test/mcp?access_token=literal"}}}`,
		`{"version":1,"servers":{"local":{"transport":"stdio","command":"/bin/sh","args":["-c","echo ok"]}}}`,
		`{"version":1,"servers":{"local":{"transport":"stdio","command":"/bin/echo","args":["api_token=literal"]}}}`,
		`{"version":1,"servers":{"local":{"transport":"stdio","command":"/bin/echo","args":["--api-key","literal"]}}}`,
		`{"version":1,"servers":{"local":{"transport":"stdio","command":"/bin/echo","args":["-H","Authorization: Bearer literal"]}}}`,
		`{"version":1,"servers":{"local":{"transport":"stdio","command":"/usr/bin/env","args":["sh","-c","echo ok"]}}}`,
	} {
		if _, err := decodeRegistry([]byte(data)); err == nil {
			t.Fatalf("decodeRegistry(%s) succeeded", data)
		}
	}
}

func TestRegistryCredentialRejectionDoesNotRenderLiteral(t *testing.T) {
	const literal = "do-not-render-this-credential"
	_, err := decodeRegistry([]byte(`{"version":1,"servers":{"local":{"transport":"stdio","command":"/bin/echo","args":["api_token=` + literal + `"]}}}`))
	if err == nil || strings.Contains(err.Error(), literal) {
		t.Fatalf("credential rejection error = %q", err)
	}
}

func TestRegistryRejectsDuplicatePassEnv(t *testing.T) {
	_, err := decodeRegistry([]byte(`{"version":1,"servers":{"local":{"transport":"stdio","command":"/bin/echo","pass_env":["SERVER_TOKEN","SERVER_TOKEN"]}}}`))
	if err == nil || err.Error() != `MCP server @local: duplicate pass_env "SERVER_TOKEN"` {
		t.Fatalf("decodeRegistry error = %q", err)
	}
}

func TestRegistryWritesPrivateDurableBackup(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	initial := Registry{Version: registryVersion, Servers: map[string]Server{
		"remote": {Transport: transportHTTP, URL: "https://example.test/mcp", TokenEnv: "REMOTE_TOKEN"},
	}}
	if err := SaveRegistry(initial); err != nil {
		t.Fatal(err)
	}
	path, err := RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []string{filepath.Dir(path), path} {
		info, statErr := os.Stat(item)
		if statErr != nil {
			t.Fatal(statErr)
		}
		want := os.FileMode(0o600)
		if item == filepath.Dir(path) {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s permissions = %o, want %o", item, info.Mode().Perm(), want)
		}
	}
	if updateErr := UpdateRegistry(func(registry *Registry) error {
		server := registry.Servers["remote"]
		server.URL = "https://changed.example.test/mcp"
		registry.Servers["remote"] = server
		return nil
	}); updateErr != nil {
		t.Fatal(updateErr)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil || !strings.Contains(string(backup), "example.test/mcp") || strings.Contains(string(backup), "changed") {
		t.Fatalf("backup = %q, %v", backup, err)
	}
}

func TestRegistryNeverOverwritesCorruptCurrentFile(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	path, err := RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	const corrupt = "{not json"
	if writeErr := os.WriteFile(path, []byte(corrupt), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	err = UpdateRegistry(func(registry *Registry) error {
		registry.Servers["remote"] = Server{Transport: transportHTTP, URL: "https://example.test/mcp"}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite corrupt") {
		t.Fatalf("UpdateRegistry error = %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != corrupt {
		t.Fatalf("corrupt registry changed to %q, %v", got, readErr)
	}
}

func TestAliasTargetRestrictsRegisteredStdioEnvironment(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	registry := Registry{Version: registryVersion, Servers: map[string]Server{
		"local":  {Transport: transportStdio, Command: "/bin/echo", PassEnv: []string{"SERVER_TOKEN"}},
		"remote": {Transport: transportHTTP, URL: "https://example.test/mcp", TokenEnv: "REMOTE_TOKEN"},
	}}
	if err := SaveRegistry(registry); err != nil {
		t.Fatal(err)
	}
	target, _, err := AliasTarget("local", []string{"PATH=/bin", "SERVER_TOKEN=kept", "SECRET=removed", HTTPTokenEnv + "=removed"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(target.CommandEnv, "\n")
	if !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "SERVER_TOKEN=kept") || strings.Contains(joined, "SECRET=removed") || strings.Contains(joined, HTTPTokenEnv) {
		t.Fatalf("registered command environment = %q", joined)
	}
	remote, _, err := AliasTarget("remote", nil)
	if err != nil || remote.Endpoint != "https://example.test/mcp" || remote.TokenEnv != "REMOTE_TOKEN" {
		t.Fatalf("http target = %#v, %v", remote, err)
	}
}

func TestRegistryHTTPAliasWithoutTokenEnvDoesNotUseDirectToken(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	t.Setenv(HTTPTokenEnv, "direct-only-token")
	if err := SaveRegistry(Registry{Version: registryVersion, Servers: map[string]Server{
		"remote": {Transport: transportHTTP, URL: "https://example.test/mcp"},
	}}); err != nil {
		t.Fatal(err)
	}
	target, _, err := AliasTarget("remote", nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := httpClientForTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.Transport.(roundTripper)
	if !ok || transport.headers.Get("Authorization") != "" {
		t.Fatalf("registry alias authorization = %#v", client.Transport)
	}
}
