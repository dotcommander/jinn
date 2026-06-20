/**
 * pi-go-tool-bridge — a minimal pi extension backed by a Go binary.
 *
 * Each tool's execute() spawns ./bin/bridge-bin, writes a JSON request to its
 * stdin, and reads a JSON response from its stdout — the same wire protocol the
 * production `jinn` extension (dc-jinn) uses. The TypeScript here is a thin
 * shim; all tool logic lives in Go (see bin/main.go).
 *
 * Build the binary first:  (cd bin && go build -o bridge-bin .)
 * Install: symlink/copy this dir to ~/.pi/agent/extensions/pi-go-tool-bridge/
 */
import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { defineTool } from "@mariozechner/pi-coding-agent";
import { Type } from "typebox";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// Resolve the binary relative to THIS file — pi runs from the user's project,
// so process.cwd()/relative paths would break (pi authoring gotcha).
const BIN = join(dirname(fileURLToPath(import.meta.url)), "bin", "bridge-bin");

interface BridgeResponse {
  ok: boolean;
  result?: string;
  error?: string;
}

/**
 * Spawn the Go binary, pipe one JSON request to stdin, parse one JSON response
 * from stdout. The binary writes its error envelope to stdout even when it
 * exits nonzero, so we parse stdout first and only fall back to a spawn error.
 * Throwing here surfaces to the LLM as a tool error.
 */
async function callBridge(
  tool: string,
  args: Record<string, unknown>,
  signal?: AbortSignal,
): Promise<string> {
  const proc = Bun.spawn([BIN], {
    stdin: new Blob([JSON.stringify({ tool, args })]),
    stdout: "pipe",
    stderr: "pipe",
    signal,
  });
  const [stdout, stderr] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
  ]);
  await proc.exited;

  if (!stdout.trim()) {
    throw new Error(
      `bridge produced no output${stderr ? `: ${stderr.trim()}` : ""}`,
    );
  }
  let resp: BridgeResponse;
  try {
    resp = JSON.parse(stdout);
  } catch {
    throw new Error(`bridge returned non-JSON: ${stdout.slice(0, 200)}`);
  }
  if (!resp.ok) throw new Error(resp.error ?? "bridge returned ok=false");
  return resp.result ?? "";
}

// content[] is what the LLM sees; a bare string crashes pi's TUI.
const asResult = (text: string) => ({
  content: [{ type: "text" as const, text }],
});

export default function (pi: ExtensionAPI) {
  pi.registerTool(
    defineTool({
      name: "bridge_echo",
      label: "Bridge Echo",
      description: "Echo text back. Minimal example tool implemented in Go.",
      parameters: Type.Object({ text: Type.String() }),
      async execute(_id, params, signal) {
        return asResult(await callBridge("echo", params, signal));
      },
    }),
  );

  pi.registerTool(
    defineTool({
      name: "bridge_read_file",
      label: "Bridge Read File",
      description: "Read a file's contents. Implemented in Go (bin/main.go).",
      parameters: Type.Object({ path: Type.String() }),
      async execute(_id, params, signal) {
        return asResult(await callBridge("read_file", params, signal));
      },
    }),
  );
}
