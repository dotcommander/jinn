package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dotcommander/jinn/internal/jinn"
)

const inspectorMaxBody = 1 << 20

func serveInspector(ctx context.Context, addr string, version string) error {
	if err := validateInspectorAddr(addr); err != nil {
		return fail(jinn.Response{
			Error:      err.Error(),
			Suggestion: "use a loopback address such as 127.0.0.1:8787",
			ErrorCode:  "invalid_args",
		})
	}

	wd, err := os.Getwd()
	if err != nil {
		return fail(jinn.Response{Error: fmt.Sprintf("getwd: %s", err)})
	}

	engine := jinn.New(wd, version)
	defer func() { _ = engine.Close() }()

	srv := &http.Server{
		Addr:              addr,
		Handler:           newInspectorHandler(engine, jinn.ResolveVersion(version)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-sigCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Fprintf(os.Stderr, "jinn inspector listening on http://%s\n", addr)
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func validateInspectorAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid inspector address %q: expected host:port", addr)
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return fmt.Errorf("inspector address must be loopback, got %q", host)
	}
	return nil
}

func newInspectorHandler(engine *jinn.Engine, version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", inspectorIndex)
	mux.HandleFunc("/api/schema", inspectorSchema)
	mux.HandleFunc("/api/list_tools", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		result, _, err := engine.Dispatch(r.Context(), "list_tools", map[string]any{})
		if err != nil {
			writeInspectorError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, result.Text)
	})
	mux.HandleFunc("/api/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, inspectorMaxBody)
		defer r.Body.Close()

		var req jinn.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeInspectorJSON(w, http.StatusBadRequest, jinn.Response{
				Error:     fmt.Sprintf("invalid JSON: %s", err),
				ErrorCode: "invalid_json",
			})
			return
		}
		if req.Tool == "" {
			writeInspectorJSON(w, http.StatusBadRequest, jinn.Response{
				Error:      "missing tool",
				Suggestion: `send {"tool":"list_dir","args":{"path":"."}}`,
				ErrorCode:  "invalid_args",
			})
			return
		}
		if req.Args == nil {
			req.Args = make(map[string]any)
		}
		attachRequestID(&req)

		result, meta, err := engine.Dispatch(r.Context(), req.Tool, req.Args)
		if err != nil {
			writeInspectorJSON(w, http.StatusOK, errorResponse(err, meta, req.RequestID))
			return
		}
		applyCompression(req, result)
		writeInspectorJSON(w, http.StatusOK, successResponse(req, result, meta))
	})
	return inspectorSecurityHeaders(version, mux)
}

func inspectorIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, inspectorHTML)
}

func inspectorSchema(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	schema, err := jinn.LeanSchema()
	if err != nil {
		writeInspectorError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, schema)
}

func inspectorSecurityHeaders(version string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "inspector only accepts localhost requests", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src 'self' data:")
		w.Header().Set("X-Jinn-Version", version)
		next.ServeHTTP(w, r)
	})
}

func writeInspectorError(w http.ResponseWriter, status int, err error) {
	writeInspectorJSON(w, status, jinn.Response{Error: err.Error()})
}

func writeInspectorJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const inspectorHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Jinn Inspector</title>
<style>
:root {
  --bg: #0b0a14;
  --panel: #14111f;
  --panel-2: #1e1a2e;
  --text: #ece8f6;
  --muted: #9b93b3;
  --border: #2e2944;
  --gold: #f2b441;
  --gold-soft: #f6d27a;
  --magic: #a78bfa;
  --smoke: #5eead4;
  --danger: #fb7185;
  --warning: #fbbf24;
  --focus: #f2b441;
  color-scheme: dark;
}
* { box-sizing: border-box; }
::selection { background: rgba(167, 139, 250, 0.35); }
body {
  margin: 0;
  min-height: 100vh;
  background:
    radial-gradient(1100px 540px at 85% -10%, rgba(167, 139, 250, 0.18), transparent 60%),
    radial-gradient(900px 500px at -10% 110%, rgba(242, 180, 65, 0.12), transparent 55%),
    var(--bg);
  color: var(--text);
  font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}
body::before {
  content: "";
  position: fixed;
  inset: 0;
  z-index: -1;
  pointer-events: none;
  background-image:
    radial-gradient(circle, rgba(255, 255, 255, 0.35) 0, transparent 1.2px),
    radial-gradient(circle, rgba(255, 255, 255, 0.18) 0, transparent 1px),
    radial-gradient(circle, rgba(242, 180, 65, 0.35) 0, transparent 1.4px);
  background-size: 310px 310px, 170px 170px, 430px 430px;
  background-position: 17px 23px, 89px 131px, 201px 67px;
}
button, input, select, textarea { font: inherit; }
button, select, input, textarea {
  border-radius: 8px;
  border: 1px solid var(--border);
}
button { transition: background 0.15s ease, border-color 0.15s ease, box-shadow 0.15s ease, transform 0.1s ease; }
button:focus-visible, select:focus-visible, input:focus-visible, textarea:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}
.app {
  display: grid;
  grid-template-columns: minmax(240px, 330px) minmax(0, 1fr);
  min-height: 100vh;
}
.sidebar {
  border-right: 1px solid var(--border);
  background: rgba(18, 15, 28, 0.88);
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.brand {
  padding: 16px;
  border-bottom: 1px solid var(--border);
  background: linear-gradient(180deg, rgba(167, 139, 250, 0.08), transparent);
}
.brand-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.brand h1 {
  margin: 0;
  font-size: 19px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.brand .lamp { filter: drop-shadow(0 0 6px rgba(242, 180, 65, 0.55)); }
.brand .brand-name {
  font-family: "Iowan Old Style", "Palatino Linotype", Palatino, Georgia, serif;
  letter-spacing: 0.02em;
  background: linear-gradient(120deg, var(--gold-soft), var(--gold) 45%, var(--magic));
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}
.tagline {
  margin-top: 6px;
  font-size: 11.5px;
  font-style: italic;
  color: var(--muted);
}
.status {
  min-width: 72px;
  padding: 4px 10px;
  border-radius: 999px;
  background: rgba(167, 139, 250, 0.12);
  border: 1px solid rgba(167, 139, 250, 0.35);
  color: var(--magic);
  font-size: 12px;
  text-align: center;
  white-space: nowrap;
}
.status.ok {
  background: rgba(94, 234, 212, 0.1);
  border-color: rgba(94, 234, 212, 0.4);
  color: var(--smoke);
  box-shadow: 0 0 10px rgba(94, 234, 212, 0.25);
}
.status.bad {
  background: rgba(251, 113, 133, 0.12);
  border-color: rgba(251, 113, 133, 0.45);
  color: var(--danger);
}
.tool-search {
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
}
.tool-search input, .field input, .field select, textarea {
  width: 100%;
  background: var(--panel-2);
  color: var(--text);
  padding: 10px 11px;
}
.tool-list {
  overflow: auto;
  padding: 8px;
}
.tool-button {
  display: block;
  width: 100%;
  min-height: 42px;
  padding: 9px 12px;
  margin: 2px 0;
  text-align: left;
  background: transparent;
  border-color: transparent;
  color: var(--text);
  cursor: pointer;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
}
.tool-button:hover {
  background: rgba(167, 139, 250, 0.1);
  border-color: rgba(167, 139, 250, 0.3);
}
.tool-button.active {
  background: linear-gradient(90deg, rgba(242, 180, 65, 0.14), rgba(167, 139, 250, 0.08));
  border-color: rgba(242, 180, 65, 0.45);
  color: var(--gold-soft);
}
.main {
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  min-width: 0;
}
.topbar {
  border-bottom: 1px solid var(--border);
  padding: 14px 18px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  background: rgba(20, 17, 31, 0.6);
}
.topbar h2 {
  margin: 0;
  font-size: 17px;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.actions button {
  min-height: 38px;
  padding: 8px 14px;
  background: var(--panel-2);
  color: var(--text);
  cursor: pointer;
}
.actions button:hover { border-color: var(--magic); }
.actions button.primary {
  background: linear-gradient(135deg, var(--gold-soft), var(--gold) 60%, #c98a1e);
  border-color: var(--gold);
  color: #221604;
  font-weight: 600;
  box-shadow: 0 0 14px rgba(242, 180, 65, 0.35);
}
.actions button.primary:hover {
  box-shadow: 0 0 22px rgba(242, 180, 65, 0.55);
  transform: translateY(-1px);
}
.actions button.danger {
  color: #ffd4dc;
  border-color: rgba(251, 113, 133, 0.5);
}
.actions button.danger:hover {
  border-color: var(--danger);
  background: rgba(251, 113, 133, 0.1);
}
.workspace {
  min-height: 0;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
}
.pane {
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--border);
}
.pane:last-child { border-right: 0; }
.pane-head {
  min-height: 54px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.pane-head strong {
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.12em;
  color: var(--muted);
}
.pane-head strong::before {
  content: "✦ ";
  color: var(--gold);
}
.fields {
  padding: 14px;
  border-bottom: 1px solid var(--border);
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}
.field label {
  display: block;
  margin: 0 0 6px;
  font-size: 12px;
  color: var(--muted);
}
textarea {
  flex: 1;
  min-height: 280px;
  border: 0;
  border-radius: 0;
  resize: none;
  background: rgba(13, 11, 22, 0.85);
  caret-color: var(--gold);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
  line-height: 1.45;
}
pre {
  margin: 0;
  flex: 1;
  overflow: auto;
  padding: 14px;
  background: rgba(9, 7, 16, 0.92);
  color: #e9e4f5;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
  line-height: 1.45;
  white-space: pre-wrap;
  word-break: break-word;
}
pre:empty::before {
  content: "Rub the lamp — summon a tool and the answer will appear here.";
  color: var(--muted);
  font-style: italic;
}
.meta {
  color: var(--muted);
  font-size: 12px;
}
.ok { color: var(--smoke); }
.bad { color: var(--danger); }
.warn { color: var(--warning); }
.meta.warn { animation: shimmer 1.1s ease-in-out infinite; }
@keyframes shimmer { 50% { opacity: 0.4; } }
::-webkit-scrollbar { width: 10px; height: 10px; }
::-webkit-scrollbar-track { background: transparent; }
::-webkit-scrollbar-thumb { background: rgba(167, 139, 250, 0.25); border-radius: 999px; }
::-webkit-scrollbar-thumb:hover { background: rgba(167, 139, 250, 0.45); }
@media (max-width: 880px) {
  .app { grid-template-columns: 1fr; }
  .sidebar { max-height: 38vh; border-right: 0; border-bottom: 1px solid var(--border); }
  .workspace { grid-template-columns: 1fr; }
  .pane { min-height: 360px; border-right: 0; border-bottom: 1px solid var(--border); }
  .fields { grid-template-columns: 1fr; }
  .topbar { align-items: flex-start; flex-direction: column; }
}
</style>
</head>
<body>
<div class="app">
  <aside class="sidebar">
    <div class="brand">
      <div class="brand-row">
        <h1><span class="lamp">🧞</span><span class="brand-name">Jinn Inspector</span></h1>
        <span id="status" class="status">waking…</span>
      </div>
      <div class="tagline">your wish is my command</div>
    </div>
    <div class="tool-search">
      <input id="filter" type="search" placeholder="Search the lamp…" aria-label="Filter tools">
    </div>
    <nav id="tools" class="tool-list" aria-label="Tools"></nav>
  </aside>
  <main class="main">
    <header class="topbar">
      <div>
        <h2 id="toolTitle">Choose your wish</h2>
        <div id="toolDescription" class="meta"></div>
      </div>
      <div class="actions">
        <button id="format" type="button">Format JSON</button>
        <button id="sample" type="button">Sample Args</button>
        <button id="run" type="button" class="primary">✨ Summon</button>
        <button id="clear" type="button" class="danger">Clear</button>
      </div>
    </header>
    <section class="workspace">
      <div class="pane">
        <div class="pane-head">
          <strong>Your Wish</strong>
          <span id="requestMeta" class="meta"></span>
        </div>
        <div class="fields">
          <div class="field">
            <label for="tool">Tool</label>
            <select id="tool"></select>
          </div>
          <div class="field">
            <label for="compress">Compress</label>
            <select id="compress">
              <option value="false">false</option>
              <option value="true">true</option>
            </select>
          </div>
        </div>
        <textarea id="args" spellcheck="false" aria-label="Tool arguments JSON">{}</textarea>
      </div>
      <div class="pane">
        <div class="pane-head">
          <strong>The Jinn's Answer</strong>
          <span id="responseMeta" class="meta"></span>
        </div>
        <pre id="output" tabindex="0"></pre>
      </div>
    </section>
  </main>
</div>
<script>
const state = { schema: [], capabilities: null, selected: "" };
const el = id => document.getElementById(id);

function toolName(def) {
  return def && def.function && def.function.name || "";
}

function toolDef(name) {
  return state.schema.find(def => toolName(def) === name);
}

function sampleValue(schema, propName) {
  if (!schema) return null;
  if (schema.default !== undefined) return schema.default;
  if (schema.enum && schema.enum.length) return schema.enum[0];
  if (schema.type === "string") {
    if (propName === "path") return ".";
    if (propName === "pattern") return "TODO";
    if (propName === "command") return "pwd";
    return "";
  }
  if (schema.type === "integer" || schema.type === "number") return 1;
  if (schema.type === "boolean") return false;
  if (schema.type === "array") return [sampleValue(schema.items || {}, "")];
  if (schema.type === "object") return buildSample(schema);
  return null;
}

function buildSample(params) {
  const out = {};
  const props = params && params.properties || {};
  const required = params && params.required || Object.keys(props).slice(0, 3);
  for (const name of required) out[name] = sampleValue(props[name], name);
  return out;
}

function renderTools() {
  const filter = el("filter").value.toLowerCase();
  const tools = state.schema.map(toolName).filter(Boolean).filter(name => name.toLowerCase().includes(filter));
  el("tools").innerHTML = tools.map(name => '<button type="button" class="tool-button' + (name === state.selected ? ' active' : '') + '" data-tool="' + name + '">' + name + '</button>').join("");
  el("tool").innerHTML = state.schema.map(toolName).filter(Boolean).map(name => '<option value="' + name + '">' + name + '</option>').join("");
  if (state.selected) el("tool").value = state.selected;
}

function selectTool(name, replaceArgs) {
  state.selected = name;
  const def = toolDef(name);
  el("toolTitle").textContent = name || "Choose your wish";
  el("toolDescription").textContent = def && def.function && def.function.description || "";
  if (replaceArgs && def) {
    el("args").value = JSON.stringify(buildSample(def.function.parameters), null, 2);
  }
  renderTools();
}

function parseArgs() {
  const text = el("args").value.trim();
  return text ? JSON.parse(text) : {};
}

async function load() {
  const [schemaRes, capsRes] = await Promise.all([fetch("/api/schema"), fetch("/api/list_tools")]);
  state.schema = await schemaRes.json();
  state.capabilities = await capsRes.json();
  const first = state.schema[0] && toolName(state.schema[0]);
  el("status").textContent = (state.capabilities.tools || []).length + " wishes";
  el("status").className = "status ok";
  selectTool(first, true);
}

async function runTool() {
  const started = performance.now();
  el("responseMeta").textContent = "summoning…";
  el("responseMeta").className = "meta warn";
  try {
    const body = {
      tool: el("tool").value,
      args: parseArgs(),
      compress: el("compress").value === "true",
      client: "jinn-inspector",
      request_id: crypto.randomUUID ? crypto.randomUUID() : String(Date.now())
    };
    el("requestMeta").textContent = JSON.stringify({ tool: body.tool, compress: body.compress });
    const res = await fetch("/api/run", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    });
    const json = await res.json();
    el("output").textContent = JSON.stringify(json, null, 2);
    el("responseMeta").textContent = (json.ok ? "wish granted" : "wish denied") + " · HTTP " + res.status + " in " + Math.round(performance.now() - started) + "ms";
    el("responseMeta").className = "meta " + (json.ok ? "ok" : "bad");
  } catch (err) {
    el("output").textContent = String(err);
    el("responseMeta").textContent = "the lamp is unresponsive";
    el("responseMeta").className = "meta bad";
  }
}

el("tools").addEventListener("click", event => {
  const button = event.target.closest("button[data-tool]");
  if (button) selectTool(button.dataset.tool, true);
});
el("tool").addEventListener("change", event => selectTool(event.target.value, true));
el("filter").addEventListener("input", renderTools);
el("sample").addEventListener("click", () => selectTool(el("tool").value, true));
el("format").addEventListener("click", () => {
  el("args").value = JSON.stringify(parseArgs(), null, 2);
});
el("run").addEventListener("click", runTool);
el("clear").addEventListener("click", () => {
  el("args").value = "{}";
  el("output").textContent = "";
  el("requestMeta").textContent = "";
  el("responseMeta").textContent = "";
});
el("args").addEventListener("keydown", event => {
  if ((event.metaKey || event.ctrlKey) && event.key === "Enter") runTool();
});
load().catch(err => {
  el("status").textContent = "lamp is dark";
  el("status").className = "status bad";
  el("output").textContent = String(err);
});
</script>
</body>
</html>
`
