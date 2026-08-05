#!/usr/bin/env python3
"""Black-box smoke test for Jinn's stateless MCP Streamable HTTP broker."""

from __future__ import annotations

import argparse
import json
import os
import secrets
import signal
import socket
import subprocess
import sys
import time
from typing import Any, Optional
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


PROTOCOL_VERSION = "2026-07-28"
TOKEN_ENV = "JINN_MCP_HTTP_TOKEN"
ORIGINS_ENV = "JINN_MCP_HTTP_ORIGINS"
META = {
    "_meta": {
        "io.modelcontextprotocol/protocolVersion": PROTOCOL_VERSION,
        "io.modelcontextprotocol/clientCapabilities": {},
    }
}


def fail(message: str) -> None:
    raise RuntimeError(message)


def pick_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def start(
    binary: str,
    profile: Optional[str] = None,
    token: Optional[str] = None,
    origins: Optional[str] = None,
) -> tuple[subprocess.Popen[str], str, str]:
    port = pick_port()
    host_port = f"127.0.0.1:{port}"
    command = [binary]
    if profile is not None:
        command.extend(["--mcp-profile", profile])
    command.extend(["--mcp-http", host_port])
    env = os.environ.copy()
    env.pop(TOKEN_ENV, None)
    env.pop(ORIGINS_ENV, None)
    if token is not None:
        env[TOKEN_ENV] = token
    if origins is not None:
        env[ORIGINS_ENV] = origins
    proc = subprocess.Popen(
        command,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=env,
    )
    deadline = time.monotonic() + 5.0
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            stderr = proc.stderr.read() if proc.stderr is not None else ""
            fail(f"HTTP broker exited during startup with {proc.returncode}; stderr={stderr!r}")
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                return proc, f"http://127.0.0.1:{port}/mcp", host_port
        except OSError:
            time.sleep(0.05)
    proc.terminate()
    proc.wait(timeout=5)
    stderr = proc.stderr.read() if proc.stderr is not None else ""
    fail(f"HTTP broker did not listen on {host_port}; stderr={stderr!r}")


def decode_body(raw: bytes) -> Any:
    if not raw:
        return None
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return raw.decode("utf-8", errors="replace")


def post(
    url: str,
    method: str,
    params: dict[str, Any],
    token: Optional[str] = None,
    origin: Optional[str] = None,
) -> tuple[int, Any, dict[str, str]]:
    body = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": method,
        "params": params,
    }
    request = Request(
        url,
        data=json.dumps(body, separators=(",", ":")).encode("utf-8"),
        method="POST",
        headers={
            "Content-Type": "application/json",
            "MCP-Protocol-Version": PROTOCOL_VERSION,
            "Mcp-Method": method,
        },
    )
    if method == "tools/call":
        name = params.get("name")
        if not isinstance(name, str):
            fail(f"tools/call params have no name: {params!r}")
        request.add_header("Mcp-Name", name)
    if token is not None:
        request.add_header("Authorization", f"Bearer {token}")
    if origin is not None:
        request.add_header("Origin", origin)
    try:
        with urlopen(request, timeout=5.0) as response:
            raw = response.read()
            headers = {key.lower(): value for key, value in response.headers.items()}
            return response.status, decode_body(raw), headers
    except HTTPError as exc:
        raw = exc.read()
        headers = {key.lower(): value for key, value in exc.headers.items()}
        return exc.code, decode_body(raw), headers
    except URLError as exc:
        fail(f"HTTP {method} request failed: {exc}")


def require_ok(response: tuple[int, Any, dict[str, str]], label: str) -> dict[str, Any]:
    status, body, _headers = response
    if status != 200:
        fail(f"{label} status = {status}, body = {body!r}")
    if not isinstance(body, dict):
        fail(f"{label} body is not an object: {body!r}")
    if "error" in body:
        fail(f"{label} returned protocol error: {body!r}")
    return body


def finish(proc: subprocess.Popen[str], host_port: str, label: str) -> None:
    if proc.poll() is None:
        proc.send_signal(signal.SIGTERM)
    try:
        code = proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()
        fail(f"{label} did not exit after SIGTERM")
    stdout = proc.stdout.read() if proc.stdout is not None else ""
    stderr = proc.stderr.read() if proc.stderr is not None else ""
    expected_stderr = f"jinn MCP HTTP listening on http://{host_port}/mcp\n"
    if code != 0:
        fail(f"{label} exited with {code}; stdout={stdout!r}; stderr={stderr!r}")
    if stdout:
        fail(f"{label} wrote protocol output to stdout: {stdout!r}")
    if stderr != expected_stderr:
        fail(f"{label} stderr = {stderr!r}, want {expected_stderr!r}")


def route_only_smoke(binary: str) -> None:
    proc, url, host_port = start(binary)
    try:
        discover = require_ok(post(url, "server/discover", dict(META)), "HTTP server/discover")
        result = discover.get("result")
        if not isinstance(result, dict) or result.get("supportedVersions") != [PROTOCOL_VERSION]:
            fail(f"unexpected HTTP discover result: {result!r}")

        tools = require_ok(post(url, "tools/list", dict(META)), "HTTP tools/list")
        listed = tools.get("result", {}).get("tools", [])
        if len(listed) != 1 or listed[0].get("name") != "jinn_route":
            fail(f"expected route-only HTTP tool surface: {listed!r}")

        call_params = {
            **META,
            "name": "jinn_route",
            "arguments": {"need": "run tests", "max_tools": 2},
        }
        call = require_ok(post(url, "tools/call", call_params), "HTTP jinn_route")
        structured = call.get("result", {}).get("structuredContent")
        if not isinstance(structured, dict) or structured.get("query") != "run tests":
            fail(f"structured HTTP route result missing: {call!r}")

        status, body, _ = post(url.removesuffix("/mcp") + "/other", "server/discover", dict(META))
        if status != 404:
            fail(f"HTTP non-MCP path status = {status}, body = {body!r}")
    finally:
        finish(proc, host_port, "route-only HTTP smoke")


def readonly_smoke(binary: str) -> None:
    token = "http-smoke-" + secrets.token_urlsafe(16)
    proc, url, host_port = start(
        binary,
        profile="read-only",
        token=token,
        origins="https://allowed.example.com",
    )
    try:
        status, body, headers = post(url, "server/discover", dict(META))
        if status != 401 or headers.get("www-authenticate") != "Bearer":
            fail(f"missing HTTP auth status/body/headers = {status}/{body!r}/{headers!r}")

        tools = require_ok(post(url, "tools/list", dict(META), token=token), "read-only HTTP tools/list")
        listed = tools.get("result", {}).get("tools", [])
        names = {tool.get("name") for tool in listed}
        if names != {"jinn_route", "jinn_call"}:
            fail(f"unexpected read-only HTTP tools: {listed!r}")
        call_tool = next(tool for tool in listed if tool.get("name") == "jinn_call")
        enum = call_tool.get("inputSchema", {}).get("properties", {}).get("tool", {}).get("enum", [])
        if not isinstance(enum, list) or not enum:
            fail(f"read-only HTTP jinn_call enum missing: {call_tool!r}")
        if {"write_file", "run_shell", "memory", "undo"}.intersection(enum):
            fail(f"mutating tool leaked into read-only HTTP enum: {enum!r}")

        read_params = {
            **META,
            "name": "jinn_call",
            "arguments": {
                "tool": "read_file",
                "arguments": {"path": "README.md"},
                "compress": False,
            },
        }
        read_call = require_ok(post(url, "tools/call", read_params, token=token), "read-only HTTP read_file")
        structured = read_call.get("result", {}).get("structuredContent")
        if not isinstance(structured, dict) or structured.get("tool") != "read_file":
            fail(f"read-only HTTP structured result missing: {read_call!r}")
        if "# jinn" not in structured.get("result", ""):
            fail(f"read-only HTTP result did not read README.md: {structured!r}")

        for blocked_tool, arguments in (
            ("write_file", {"path": "http-smoke-blocked.txt", "content": "nope"}),
            ("run_shell", {"command": "printf blocked"}),
        ):
            blocked_params = {
                **META,
                "name": "jinn_call",
                "arguments": {"tool": blocked_tool, "arguments": arguments},
            }
            blocked = require_ok(post(url, "tools/call", blocked_params, token=token), f"read-only HTTP {blocked_tool}")
            result = blocked.get("result")
            if not isinstance(result, dict) or result.get("isError") is not True:
                fail(f"read-only HTTP {blocked_tool} was not rejected: {blocked!r}")

        status, body, _ = post(
            url,
            "server/discover",
            dict(META),
            token=token,
            origin="https://blocked.example.com",
        )
        if status != 403:
            fail(f"blocked HTTP origin status/body = {status}/{body!r}")
    finally:
        finish(proc, host_port, "read-only HTTP smoke")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", default="jinn", help="path to the Jinn binary")
    args = parser.parse_args()
    try:
        route_only_smoke(args.binary)
        readonly_smoke(args.binary)
    except RuntimeError as exc:
        print(f"mcp_http_smoke_failed: {exc}", file=sys.stderr)
        return 1
    print("mcp_http_route_only_smoke=passed")
    print("mcp_http_readonly_auth_smoke=passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
