#!/usr/bin/env python3
"""Black-box smoke test for jinn's MCP explorer against explicit-argv stdio."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from typing import Any


PROTOCOL_VERSION = "2026-07-28"
SERVER_INFO = {"name": "jinn-explorer-fixture", "version": "1.0"}
RESULT_META = {"io.modelcontextprotocol/serverInfo": SERVER_INFO}


def tool(name: str, description: str) -> dict[str, Any]:
    properties = {
        "message": {
            "type": "string",
            "description": "A deliberately documented fixture argument.",
        }
    }
    return {
        "name": name,
        "description": description,
        "inputSchema": {
            "type": "object",
            "properties": properties,
            "required": ["message"],
            "additionalProperties": False,
        },
        "annotations": {"readOnlyHint": True, "destructiveHint": False},
    }


TOOLS = [tool("alpha", "First fixture tool."), tool("echo", "Echo fixture tool.")]


def fixture_server(drift_after_first_list: bool) -> int:
    list_requests = 0
    for line in sys.stdin:
        request = json.loads(line)
        method = request.get("method")
        request_id = request.get("id")
        if request_id is None:
            continue
        if method == "server/discover":
            result: dict[str, Any] = {
                "_meta": RESULT_META,
                "supportedVersions": [PROTOCOL_VERSION],
                "capabilities": {"tools": {}},
            }
        elif method == "tools/list":
            list_requests += 1
            cursor = request.get("params", {}).get("cursor", "")
            tools = TOOLS
            if drift_after_first_list and list_requests > 2:
                tools = [tool("alpha", "First fixture tool."), tool("echo", "Changed fixture tool.")]
            result = {"_meta": RESULT_META, "tools": [tools[1] if cursor else tools[0]]}
            if not cursor:
                result["nextCursor"] = "second"
        elif method == "tools/call":
            params = request.get("params", {})
            name = params.get("name")
            arguments = params.get("arguments", {})
            is_error = name == "alpha"
            result = {
                "_meta": RESULT_META,
                "content": [{"type": "text", "text": "fixture tool error" if is_error else "ok"}],
                "structuredContent": {"tool": name, "arguments": arguments},
                "isError": is_error,
            }
        else:
            response = {
                "jsonrpc": "2.0",
                "id": request_id,
                "error": {"code": -32601, "message": f"unsupported method: {method}"},
            }
            print(json.dumps(response, separators=(",", ":")), flush=True)
            continue
        result["resultType"] = "complete"
        response = {"jsonrpc": "2.0", "id": request_id, "result": result}
        print(json.dumps(response, separators=(",", ":")), flush=True)
    return 0


def fail(message: str) -> None:
    raise RuntimeError(message)


def invoke_value(binary: str, *args: str) -> Any:
    script = str(Path(__file__).resolve())
    command = [
        binary,
        "mcp",
        *args,
        "--command",
        sys.executable,
        "--arg",
        script,
        "--arg",
        "--fixture-server",
    ]
    completed = subprocess.run(command, text=True, capture_output=True, timeout=15, check=False)
    if completed.returncode != 0:
        fail(f"{' '.join(args)} exited {completed.returncode}: {completed.stderr!r}")
    if completed.stderr:
        fail(f"{' '.join(args)} wrote stderr: {completed.stderr!r}")
    try:
        value = json.loads(completed.stdout)
    except json.JSONDecodeError as exc:
        fail(f"{' '.join(args)} returned invalid JSON: {exc}: {completed.stdout!r}")
    return value


def invoke(binary: str, *args: str) -> dict[str, Any]:
    value = invoke_value(binary, *args)
    if not isinstance(value, dict):
        fail(f"{' '.join(args)} returned non-object JSON: {value!r}")
    return value


def invoke_registered(binary: str, config_dir: str, *args: str) -> tuple[int, dict[str, Any]]:
    environment = os.environ.copy()
    environment["JINN_CONFIG_DIR"] = config_dir
    completed = subprocess.run([binary, "mcp", *args], text=True, capture_output=True, timeout=15, check=False, env=environment)
    if completed.stderr:
        fail(f"{' '.join(args)} wrote stderr: {completed.stderr!r}")
    try:
        value = json.loads(completed.stdout)
    except json.JSONDecodeError as exc:
        fail(f"{' '.join(args)} returned invalid JSON: {exc}: {completed.stdout!r}")
    if not isinstance(value, dict):
        fail(f"{' '.join(args)} returned non-object JSON: {value!r}")
    return completed.returncode, value


def invoke_registered_text(binary: str, config_dir: str, *args: str) -> tuple[int, str]:
    environment = os.environ.copy()
    environment["JINN_CONFIG_DIR"] = config_dir
    completed = subprocess.run([binary, "mcp", *args], text=True, capture_output=True, timeout=15, check=False, env=environment)
    if completed.stderr:
        fail(f"{' '.join(args)} wrote stderr: {completed.stderr!r}")
    return completed.returncode, completed.stdout


def smoke(binary: str) -> None:
    listed = invoke(binary, "list")
    if [item.get("name") for item in listed.get("tools", [])] != ["alpha", "echo"]:
        fail(f"paginated list changed: {listed!r}")

    inspected = invoke(binary, "inspect", "echo")
    if inspected.get("name") != "echo":
        fail(f"inspect changed: {inspected!r}")

    called = invoke(binary, "call", "echo", "--args", '{"message":"hello"}')
    if called.get("isError") or called.get("structuredContent", {}).get("arguments", {}).get("message") != "hello":
        fail(f"call changed: {called!r}")

    tool_error = invoke(binary, "call", "alpha", "-a", "message", '"bad"')
    if tool_error.get("isError") is not True:
        fail(f"tool error was hidden: {tool_error!r}")

    signatures = invoke(binary, "list", "--format=signatures")
    compact_tools = signatures.get("tools", [])
    if [item.get("signature") for item in compact_tools] != ["alpha(message:string)", "echo(message:string)"]:
        fail(f"signature list changed: {signatures!r}")
    if any("name" in item for item in compact_tools):
        fail(f"signature list grew an unexpected name field: {signatures!r}")

    human_list = invoke(binary, "list", "--format=human")
    if [item.get("name") for item in human_list.get("tools", [])] != ["alpha", "echo"]:
        fail(f"human list changed: {human_list!r}")

    exported = invoke(binary, "export", "--format=mcp")
    if [item.get("name") for item in exported.get("tools", [])] != ["alpha", "echo"]:
        fail(f"MCP export changed: {exported!r}")
    responses = invoke_value(binary, "export", "--format=openai-responses")
    if not isinstance(responses, list) or [item.get("name") for item in responses] != ["alpha", "echo"]:
        fail(f"OpenAI Responses export changed: {responses!r}")
    if any(item.get("type") != "function" or item.get("strict") is not False for item in responses):
        fail(f"OpenAI Responses export shape changed: {responses!r}")

    cost = invoke(binary, "cost")
    if cost.get("schema_version") != 1 or cost.get("encoding") != "o200k_base":
        fail(f"cost identity changed: {cost!r}")
    if cost.get("tool_count") != 2 or cost.get("formats", {}).get("canonical_list", {}).get("tokens", 0) <= 0:
        fail(f"cost metrics missing: {cost!r}")

    with tempfile.TemporaryDirectory() as directory:
        registry_path = Path(directory) / "jinn" / "mcp" / "servers.json"
        registry_path.parent.mkdir(parents=True)
        registry_path.write_text(json.dumps({"version": 1, "servers": {"fixture": {
            "transport": "stdio", "command": sys.executable, "args": [str(Path(__file__).resolve()), "--fixture-server"]
        }}}), encoding="utf-8")
        code, names = invoke_registered_text(binary, directory, "servers", "list", "--format=names")
        if code != 0 or names != "fixture\n":
            fail(f"server names completion output changed: code={code}, output={names!r}")
        accepted, _ = invoke_registered(binary, directory, "snapshot", "@fixture", "--accept")
        if accepted != 0:
            fail("snapshot acceptance for batch fixture failed")
        batch_path = Path(directory) / "batch.json"
        batch_path.write_text(json.dumps({"version": 1, "calls": [{
            "id": "echo", "tool": "echo", "arguments": {"message": "batch"},
            "select": "/structuredContent/arguments/message"
        }]}), encoding="utf-8")
        code, batch = invoke_registered(binary, directory, "batch", "@fixture", "--file", str(batch_path))
        if code != 0 or batch.get("results", [{}])[0].get("result", {}).get("value") != "batch":
            fail(f"snapshot-gated batch changed: code={code}, output={batch!r}")
        code, dry_run = invoke_registered(binary, directory, "batch", "@fixture", "--file", str(batch_path), "--dry-run")
        if code != 0 or dry_run.get("dry_run") is not True or dry_run.get("results") != []:
            fail(f"dry-run batch changed: code={code}, output={dry_run!r}")

        tool_error_path = Path(directory) / "batch-tool-error.json"
        tool_error_path.write_text(json.dumps({"version": 1, "calls": [{
            "id": "alpha", "tool": "alpha", "arguments": {"message": "batch"}
        }]}), encoding="utf-8")
        code, tool_error_batch = invoke_registered(binary, directory, "batch", "@fixture", "--file", str(tool_error_path))
        tool_error_result = tool_error_batch.get("results", [{}])[0]
        if code != 2 or tool_error_result.get("status") != "tool_error" or tool_error_result.get("result", {}).get("isError") is not True:
            fail(f"batch tool error changed: code={code}, output={tool_error_batch!r}")

        drift_registry_path = Path(directory) / "jinn" / "mcp" / "servers.json"
        registry = json.loads(drift_registry_path.read_text(encoding="utf-8"))
        registry["servers"]["drift"] = {
            "transport": "stdio", "command": sys.executable,
            "args": [str(Path(__file__).resolve()), "--fixture-server", "--drift-after-first-list"],
        }
        drift_registry_path.write_text(json.dumps(registry), encoding="utf-8")
        accepted, _ = invoke_registered(binary, directory, "snapshot", "@drift", "--accept")
        if accepted != 0:
            fail("drift fixture snapshot acceptance failed")
        code, drift = invoke_registered(binary, directory, "batch", "@drift", "--file", str(batch_path))
        if code != 1 or "manifest changed during preflight" not in drift.get("error", ""):
            fail(f"final manifest drift gate changed: code={code}, output={drift!r}")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", default="jinn")
    parser.add_argument("--fixture-server", action="store_true")
    parser.add_argument("--drift-after-first-list", action="store_true")
    args = parser.parse_args()
    if args.fixture_server:
        return fixture_server(args.drift_after_first_list)
    try:
        smoke(args.binary)
    except RuntimeError as exc:
        print(f"mcp_explorer_smoke_failed: {exc}", file=sys.stderr)
        return 1
    print("mcp_explorer_stdio_smoke=passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
