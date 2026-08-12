#!/usr/bin/env python3
"""Opt-in provider-backed smoke test for JCode discovery of Jinn MCP tools."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from typing import Any


@dataclass(frozen=True)
class Case:
    name: str
    prompt: str
    expected_tools: tuple[str, ...]
    expected_answer: str | None = None


ROUTE_TOOL = "mcp__jinn__jinn_route"
CASES = (
    Case(
        name="route-only",
        prompt=(
            "Choose the safest available capability for previewing a project-wide symbol rename "
            "without modifying files. Do not use the shell or inspect files. Return only the exact capability name."
        ),
        expected_tools=(ROUTE_TOOL,),
    ),
    Case(
        name="ambiguous-route",
        prompt=(
            "Choose the safest available capability for reading the final 40 lines from six files "
            "while preserving successful results if one path fails. Do not use the shell or inspect "
            "files. Return only the exact capability name."
        ),
        expected_tools=(ROUTE_TOOL,),
    ),
    Case(
        name="negative-control",
        prompt="Answer with only the number: what is seven multiplied by six?",
        expected_tools=(),
        expected_answer="42",
    ),
)

FORBIDDEN_PROMPT_NAMES = ("jinn", "jinn_route", "jinn_call")
JCODE_DISCOVERY_INSTRUCTIONS = """# Jinn discoverability test

When a user asks you to choose, name, recommend, or use a development capability
or tool, you MUST call `mcp__jinn__jinn_route` before answering or acting, even
if the answer seems obvious. Pass the user's task in `need` and use the exact
recommendation. For unrelated questions, do not call Jinn.
"""
USAGE_KEYS = ("input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens")


def fail(message: str) -> None:
    raise RuntimeError(message)


def resolve_executable(value: str) -> str:
    if "/" in value:
        path = Path(value).expanduser().resolve()
        if not path.is_file():
            fail(f"executable not found: {path}")
        return str(path)
    resolved = shutil.which(value)
    if resolved is None:
        fail(f"executable not found on PATH: {value}")
    return resolved


def write_test_project(root: Path, binary: str) -> None:
    config_dir = root / ".jcode"
    config_dir.mkdir()
    config = {
        "mcpServers": {
            "jinn": {
                "command": binary,
                "args": ["--mcp"],
                "env": {},
                "shared": False,
            }
        }
    }
    (config_dir / "mcp.json").write_text(json.dumps(config, indent=2) + "\n", encoding="utf-8")
    (root / "AGENTS.md").write_text(JCODE_DISCOVERY_INSTRUCTIONS, encoding="utf-8")


def jcode_command(jcode: str, project: Path, provider: str | None, model: str | None, prompt: str) -> list[str]:
    command = [
        jcode,
        "--no-update",
        "--no-selfdev",
        "--quiet",
        "-C",
        str(project),
        "--disable-base-tools",
        "--tools",
        ROUTE_TOOL,
        "--disabled-tools",
        "",
    ]
    if provider is not None:
        command.extend(["--provider", provider])
    if model is not None:
        command.extend(["--model", model])
    command.extend(["run", "--ndjson", prompt])
    return command


def parse_events(output: str, label: str) -> list[dict[str, Any]]:
    events: list[dict[str, Any]] = []
    for line_number, line in enumerate(output.splitlines(), 1):
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError as exc:
            fail(f"{label} output line {line_number} was not JSON: {exc}: {line!r}")
        if not isinstance(event, dict):
            fail(f"{label} output line {line_number} was not an object: {event!r}")
        if event.get("type") == "error":
            fail(f"{label} emitted an error event: {event!r}")
        if str(event.get("type", "")).startswith("auto_poke"):
            fail(f"{label} unexpectedly used auto-poke: {event!r}")
        events.append(event)
    if not events:
        fail(f"{label} produced no NDJSON events")
    return events


def completed_tool_calls(events: list[dict[str, Any]], label: str) -> list[str]:
    started: dict[str, str] = {}
    executed: set[str] = set()
    completed: list[str] = []
    seen_ids: set[str] = set()
    for event in events:
        event_type = event.get("type")
        if event_type not in {"tool_start", "tool_exec", "tool_done"}:
            continue
        call_id = event.get("id")
        name = event.get("name")
        if not isinstance(call_id, str) or not isinstance(name, str):
            fail(f"{label} emitted a malformed {event_type} event: {event!r}")
        if name != ROUTE_TOOL:
            fail(f"{label} called an unexpected tool: {name!r}")
        if event_type == "tool_start":
            if call_id in seen_ids:
                fail(f"{label} reused tool call id {call_id!r}")
            seen_ids.add(call_id)
            started[call_id] = name
        elif event_type == "tool_exec":
            if started.get(call_id) != name:
                fail(f"{label} executed an unstarted tool call: {event!r}")
            executed.add(call_id)
        else:
            if started.get(call_id) != name or call_id not in executed:
                fail(f"{label} completed an unexecuted tool call: {event!r}")
            if event.get("error") is not None:
                fail(f"{label} tool call failed: {event!r}")
            if not isinstance(event.get("output"), str) or not event["output"].strip():
                fail(f"{label} tool call returned no output: {event!r}")
            completed.append(name)
            del started[call_id]
            executed.remove(call_id)
    if started or executed:
        fail(f"{label} left tool calls incomplete: {sorted(started)!r}")
    return completed


def final_event(events: list[dict[str, Any]], label: str) -> dict[str, Any]:
    done = [event for event in events if event.get("type") == "done"]
    if len(done) != 1:
        fail(f"{label} emitted {len(done)} done events, want exactly one")
    event = done[0]
    for key in ("session_id", "provider", "model", "text"):
        if not isinstance(event.get(key), str):
            fail(f"{label} done event omitted string field {key!r}: {event!r}")
    return event


def usage(done: dict[str, Any], label: str) -> dict[str, int]:
    raw = done.get("usage")
    if not isinstance(raw, dict):
        fail(f"{label} done event omitted usage: {done!r}")
    result: dict[str, int] = {}
    for key in USAGE_KEYS:
        value = raw.get(key)
        if value is None:
            value = 0
        if isinstance(value, bool) or not isinstance(value, int) or value < 0:
            fail(f"{label} usage field {key!r} was invalid: {value!r}")
        result[key] = value
    return result


def run_case(
    jcode: str, project: Path, provider: str | None, model: str | None,
    timeout: float, mcp_wait_ms: int, case: Case,
) -> dict[str, Any]:
    lowered = case.prompt.lower()
    for forbidden in FORBIDDEN_PROMPT_NAMES:
        if forbidden in lowered:
            fail(f"{case.name} prompt names {forbidden!r}; the test is not cold")
    environment = os.environ.copy()
    environment.update(
        {
            "JCODE_RUN_AUTO_POKE": "0",
            "JCODE_RUN_MCP": "1",
            "JCODE_RUN_MCP_WAIT_MS": str(mcp_wait_ms),
        }
    )
    try:
        process = subprocess.run(
            jcode_command(jcode, project, provider, model, case.prompt),
            stdin=subprocess.DEVNULL,
            text=True,
            capture_output=True,
            timeout=timeout,
            check=False,
            env=environment,
        )
    except subprocess.TimeoutExpired as exc:
        fail(f"{case.name} timed out after {timeout:g}s: {exc}")
    if process.returncode != 0:
        fail(f"{case.name} exited {process.returncode}: stderr={process.stderr[-2000:]!r}")
    events = parse_events(process.stdout, case.name)
    calls = completed_tool_calls(events, case.name)
    done = final_event(events, case.name)
    answer = done["text"].strip()
    if tuple(calls) != case.expected_tools:
        fail(f"{case.name} tool calls = {calls!r}, want {list(case.expected_tools)!r}; answer={answer!r}")
    if case.expected_answer is not None and answer != case.expected_answer:
        fail(f"{case.name} answer = {answer!r}, want {case.expected_answer!r}")
    return {
        "session_id": done["session_id"],
        "provider": done["provider"],
        "model": done["model"],
        "usage": usage(done, case.name),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Run opt-in provider-backed JCode checks for cold Jinn MCP discoverability.")
    parser.add_argument("--binary", default="jinn", help="path to the Jinn binary")
    parser.add_argument("--jcode", default="jcode", help="path to the JCode CLI")
    parser.add_argument("--provider", help="explicit JCode provider for repeatable certification")
    parser.add_argument("--model", help="explicit JCode model for repeatable certification")
    parser.add_argument("--timeout", type=float, default=180.0, help="per-case timeout in seconds")
    parser.add_argument("--mcp-wait-ms", type=int, default=10000, help="cold MCP schema wait in milliseconds")
    parser.add_argument("--case", choices=[case.name for case in CASES], help="run one case instead of all three")
    args = parser.parse_args()
    try:
        if args.timeout <= 0:
            fail("--timeout must be greater than zero")
        if args.mcp_wait_ms < 0:
            fail("--mcp-wait-ms must not be negative")
        binary = resolve_executable(args.binary)
        jcode = resolve_executable(args.jcode)
        selected = [case for case in CASES if args.case is None or case.name == args.case]
        totals = {key: 0 for key in USAGE_KEYS}
        with tempfile.TemporaryDirectory(prefix="jinn-jcode-discovery-") as temporary:
            project = Path(temporary)
            write_test_project(project, binary)
            for case in selected:
                result = run_case(
                    jcode,
                    project,
                    args.provider,
                    args.model,
                    args.timeout,
                    args.mcp_wait_ms,
                    case,
                )
                for key, value in result["usage"].items():
                    totals[key] += value
                print(
                    f"jcode_discoverability_{case.name}=passed "
                    f"{json.dumps(result, separators=(',', ':'))}"
                )
        print(f"jcode_discoverability_total={json.dumps(totals, separators=(',', ':'))}")
    except RuntimeError as exc:
        print(f"jcode_discoverability_failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
