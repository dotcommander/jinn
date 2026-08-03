#!/usr/bin/env python3
"""Black-box smoke test for Jinn's MCP 2026-07-28 stdio broker."""

from __future__ import annotations

import argparse
import json
import select
import subprocess
import sys
from typing import Any


PROTOCOL_VERSION = "2026-07-28"
META = {
    "_meta": {
        "io.modelcontextprotocol/protocolVersion": PROTOCOL_VERSION,
        "io.modelcontextprotocol/clientCapabilities": {},
    }
}


def fail(message: str) -> None:
    raise RuntimeError(message)


def start(binary: str) -> subprocess.Popen[str]:
    return subprocess.Popen(
        [binary, "--mcp"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        bufsize=1,
    )


def send(proc: subprocess.Popen[str], message: dict[str, Any]) -> None:
    if proc.stdin is None:
        fail("MCP stdin is unavailable")
    proc.stdin.write(json.dumps(message, separators=(",", ":")) + "\n")
    proc.stdin.flush()


def receive(proc: subprocess.Popen[str], label: str) -> dict[str, Any]:
    if proc.stdout is None:
        fail("MCP stdout is unavailable")
    ready, _, _ = select.select([proc.stdout], [], [], 5.0)
    if not ready:
        fail(f"timed out waiting for {label}")
    line = proc.stdout.readline()
    if not line:
        stderr = proc.stderr.read() if proc.stderr is not None else ""
        fail(f"MCP exited before {label}; stderr={stderr!r}")
    try:
        value = json.loads(line)
    except json.JSONDecodeError as exc:
        fail(f"{label} was not JSON: {exc}: {line!r}")
    if not isinstance(value, dict):
        fail(f"{label} was not a JSON object: {value!r}")
    return value


def finish(proc: subprocess.Popen[str], label: str) -> None:
    if proc.stdin is not None:
        proc.stdin.close()
    try:
        code = proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()
        fail(f"{label} did not exit after stdin close")
    stderr = proc.stderr.read() if proc.stderr is not None else ""
    if code != 0:
        fail(f"{label} exited with {code}; stderr={stderr!r}")
    if stderr:
        fail(f"{label} wrote to stderr: {stderr!r}")


def current_smoke(binary: str) -> None:
    proc = start(binary)
    try:
        discover = {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "server/discover",
            "params": META,
        }
        response = receive_after_send(proc, discover, "server/discover")
        result = response.get("result")
        if not isinstance(result, dict) or result.get("supportedVersions") != [PROTOCOL_VERSION]:
            fail(f"unexpected discover result: {result!r}")
        if "tools" not in result.get("capabilities", {}):
            fail(f"tools capability missing: {result!r}")

        tools_list = {
            "jsonrpc": "2.0",
            "id": 2,
            "method": "tools/list",
            "params": META,
        }
        response = receive_after_send(proc, tools_list, "tools/list")
        tools = response.get("result", {}).get("tools", [])
        if len(tools) != 1 or tools[0].get("name") != "jinn_route":
            fail(f"expected one jinn_route tool: {tools!r}")
        input_schema = tools[0].get("inputSchema", {})
        output_schema = tools[0].get("outputSchema", {})
        if input_schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
            fail(f"input schema is not JSON Schema 2020-12: {input_schema!r}")
        if output_schema.get("$schema") != "https://json-schema.org/draft/2020-12/schema":
            fail(f"output schema is not JSON Schema 2020-12: {output_schema!r}")
        if "$defs" not in output_schema:
            fail(f"output schema has no $defs: {output_schema!r}")

        call = {
            "jsonrpc": "2.0",
            "id": 3,
            "method": "tools/call",
            "params": {
                **META,
                "name": "jinn_route",
                "arguments": {"need": "run tests", "max_tools": 2},
            },
        }
        response = receive_after_send(proc, call, "tools/call")
        result = response.get("result")
        structured = result.get("structuredContent") if isinstance(result, dict) else None
        if not isinstance(structured, dict) or structured.get("query") != "run tests":
            fail(f"structured route result missing: {result!r}")
        if not isinstance(structured.get("matches"), list):
            fail(f"structured route matches missing: {structured!r}")
    finally:
        finish(proc, "current MCP smoke")


def receive_after_send(proc: subprocess.Popen[str], message: dict[str, Any], label: str) -> dict[str, Any]:
    send(proc, message)
    response = receive(proc, label)
    if "error" in response:
        fail(f"{label} returned protocol error: {response!r}")
    return response


def legacy_smoke(binary: str) -> None:
    proc = start(binary)
    try:
        send(
            proc,
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "initialize",
                "params": {
                    "protocolVersion": "2025-06-18",
                    "capabilities": {},
                    "clientInfo": {"name": "legacy-smoke", "version": "0"},
                },
            },
        )
        response = receive(proc, "legacy initialize")
        if response.get("result", {}).get("protocolVersion") != "2025-06-18":
            fail(f"legacy compatibility response changed: {response!r}")
        send(
            proc,
            {
                "jsonrpc": "2.0",
                "id": 2,
                "method": "tools/list",
            },
        )
        response = receive(proc, "legacy tools/list")
        tools = response.get("result", {}).get("tools", [])
        if len(tools) != 1 or tools[0].get("name") != "jinn_route":
            fail(f"legacy route tool missing: {tools!r}")
    finally:
        finish(proc, "legacy MCP smoke")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", default="jinn", help="path to the Jinn binary")
    args = parser.parse_args()
    try:
        current_smoke(args.binary)
        legacy_smoke(args.binary)
    except RuntimeError as exc:
        print(f"mcp_smoke_failed: {exc}", file=sys.stderr)
        return 1
    print("mcp_current_stdio_smoke=passed")
    print("mcp_legacy_compatibility_smoke=passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
