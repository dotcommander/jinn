# pi-go-tool-bridge

A minimal, copy-pasteable example of writing a [pi](https://github.com/badlogic/pi-mono)
extension whose tools are implemented **in Go**.

pi extensions are TypeScript. This example keeps the TypeScript to a thin shim:
each tool spawns a small Go binary, hands it a JSON request on stdin, and reads a
JSON response from stdout. **All the real logic lives in Go** — the place a Go
developer is fluent — and the ~95 lines of TypeScript are boilerplate you copy
once. This is the same pattern the production `jinn` binary uses (wrapped by the
`dc-jinn` extension); this directory is the stripped-down teaching version.

## The wire contract

One JSON request in on stdin, one JSON response out on stdout, one request per
process:

```jsonc
// stdin  → the binary
{ "tool": "echo", "args": { "text": "hi" } }

// stdout ← the binary (success)
{ "ok": true, "result": "hi" }

// stdout ← the binary (failure: envelope on stdout AND a nonzero exit code)
{ "ok": false, "error": "unknown tool: \"nope\"" }
```

The Go side owns the request/response shape and the per-tool argument
deserialization. The TS side never needs to know what a tool does — only how to
spawn the binary and shuttle JSON. That asymmetry is the whole point: the
interesting work is in Go.

## Layout

```
pi-go-tool-bridge/
├── index.ts        # pi extension: registers tools, forwards each call to the binary
├── bin/
│   ├── main.go     # the Go binary: stdin JSON → switch{tool} → stdout JSON
│   └── go.mod      # standalone nested module, stdlib only
└── README.md
```

`bin/` is a **standalone nested Go module** (its own `go.mod`, stdlib only) so it
builds on its own and stays independent of any parent module.

## Build

```bash
cd bin && go build -o bridge-bin .
```

That single artifact (`bin/bridge-bin`) is what the extension spawns. It is
git-ignored — rebuild it on each machine.

## Try it without pi

The binary is just a pipe — exercise the contract directly:

```bash
echo '{"tool":"echo","args":{"text":"hello bridge"}}' | bin/bridge-bin
# → {"ok":true,"result":"hello bridge"}

echo '{"tool":"read_file","args":{"path":"go.mod"}}' | bin/bridge-bin
# → {"ok":true,"result":"module example.com/..."}
```

## Install into pi

pi discovers directory-style extensions at these paths (an `index.ts` inside a
named directory):

| Path | Scope |
|------|-------|
| `~/.pi/agent/extensions/<name>/index.ts` | global |
| `.pi/extensions/<name>/index.ts` | project-local |

Symlink this directory into one of them, then start pi:

```bash
ln -s "$(pwd)" ~/.pi/agent/extensions/pi-go-tool-bridge
pi            # the bridge_echo and bridge_read_file tools are now available
```

Or load it ad-hoc for a quick test without installing:

```bash
pi -e ./index.ts
```

The `@mariozechner/pi-coding-agent` and `typebox` imports resolve from pi's own
installation — no local `node_modules` is required for this example.

## How it works

1. pi calls a tool's `execute(id, params, signal)`.
2. `callBridge()` spawns `bin/bridge-bin`, writes `{tool, args}` to its stdin.
3. The Go binary deserializes its args, runs the tool, prints `{ok, result}`.
4. `callBridge()` parses stdout and returns the text; a `{ok:false}` envelope
   (or no output) becomes a thrown error, which pi surfaces to the model.

Note the parameter schemas (`Type.Object({ text: Type.String() })`) live in the
TypeScript because pi validates tool arguments with typebox before `execute`
runs. Keep them in sync with the Go `switch` by hand — there are only two here.

## Scaling up

This example hardcodes two tools. For a real tool surface you don't want to
hand-maintain schemas on both sides. The production `jinn` binary makes the Go
side the single source of truth: it self-describes with `jinn --schema`
(OpenAI function-calling format), so an extension can fetch the schema once at
startup and register every tool dynamically. See the `jinn` binary and the
`dc-jinn` extension for that production-scale variant of this same pattern.
