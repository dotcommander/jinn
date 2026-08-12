#!/usr/bin/env python3
"""Opt-in provider-backed smoke test for Codex discovery of Jinn MCP tools."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from typing import Any, Optional

from discoverability_smoke_common import (
    DISCOVERY_INSTRUCTIONS,
    context_inventory,
    normalize_usage,
    sum_usage,
    write_minimal_test_project,
)


@dataclass(frozen=True)
class Case:
    name: str
    prompt: str
    expected_tools: tuple[str, ...]
    expected_answer: Optional[str] = None


CASES = (
    Case(
        name="route-only",
        prompt=(
            "Choose the safest available capability for previewing a project-wide symbol rename "
            "without modifying files. Do not use the shell or inspect files. Return only the exact capability name."
        ),
        expected_tools=("jinn_route",),
    ),
    Case(
        name="ambiguous-route",
        prompt=(
            "Choose the safest available capability for reading the final 40 lines from six files "
            "while preserving successful results if one path fails. Do not use the shell or inspect "
            "files. Return only the exact capability name."
        ),
        expected_tools=("jinn_route",),
    ),
    Case(
        name="negative-control",
        prompt="Answer with only the number: what is seven multiplied by six?",
        expected_tools=(),
        expected_answer="42",
    ),
)

FORBIDDEN_PROMPT_NAMES = ("jinn", "jinn_route", "jinn_call")
CODEX_ROUTE_TOOL = "jinn_route"
CODEX_DISABLED_FEATURES = (
    "browser_use",
    "computer_use",
    "goals",
    "image_generation",
    "memories",
    "multi_agent",
    "plugins",
    "remote_plugin",
    "shell_tool",
    "skill_search",
    "tool_suggest",
    "view_image",
)


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


def config_string(value: str) -> str:
    return json.dumps(value)


def codex_command(
    codex: str, binary: str, project: Path, model: Optional[str], prompt: str
) -> list[str]:
    command = [
        codex,
        "exec",
        "--strict-config",
        "--ignore-user-config",
        "--ignore-rules",
        "--ephemeral",
        "--skip-git-repo-check",
        "-C",
        str(project),
        "--json",
        "--sandbox",
        "read-only",
        "-c",
        'model_reasoning_effort="low"',
        "-c",
        f"developer_instructions={config_string(DISCOVERY_INSTRUCTIONS)}",
        "-c",
        "project_doc_max_bytes=0",
        "-c",
        'web_search="disabled"',
        "-c",
        "agents.enabled=false",
        "-c",
        "apps._default.enabled=false",
        "-c",
        f"mcp_servers.jinn.command={config_string(binary)}",
        "-c",
        'mcp_servers.jinn.args=["--mcp"]',
        "-c",
        "mcp_servers.jinn.required=true",
        "-c",
        'mcp_servers.jinn.enabled_tools=["jinn_route"]',
        "-c",
        'mcp_servers.jinn.tools.jinn_route.approval_mode="approve"',
    ]
    for feature in CODEX_DISABLED_FEATURES:
        command.extend(["--disable", feature])
    if model is not None:
        command.extend(["--model", model])
    command.append(prompt)
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
        events.append(event)
    if not events:
        fail(f"{label} produced no JSONL events")
    return events


def completed_mcp_calls(events: list[dict[str, Any]], label: str) -> list[str]:
    calls: list[str] = []
    for event in events:
        if event.get("type") != "item.completed":
            continue
        item = event.get("item")
        if not isinstance(item, dict):
            continue
        if item.get("type") == "command_execution":
            fail(f"{label} used a shell command: {item!r}")
        if item.get("type") != "mcp_tool_call":
            continue
        if item.get("server") != "jinn":
            fail(f"{label} called an unexpected MCP server: {item!r}")
        if item.get("status") != "completed" or item.get("error") is not None:
            fail(f"{label} MCP call failed: {item!r}")
        tool = item.get("tool")
        if not isinstance(tool, str):
            fail(f"{label} MCP call omitted a tool name: {item!r}")
        calls.append(tool)
    return calls


def final_message(events: list[dict[str, Any]], label: str) -> str:
    messages: list[str] = []
    for event in events:
        if event.get("type") != "item.completed":
            continue
        item = event.get("item")
        if isinstance(item, dict) and item.get("type") == "agent_message" and isinstance(item.get("text"), str):
            messages.append(item["text"].strip())
    if not messages:
        fail(f"{label} produced no final agent message")
    return messages[-1]


def usage(events: list[dict[str, Any]], model_requests_lower_bound: int) -> dict[str, Any]:
    for event in reversed(events):
        if event.get("type") == "turn.completed" and isinstance(event.get("usage"), dict):
            raw = event["usage"]
            return normalize_usage(
                reported_scope="turn_completed_cumulative",
                model_requests_lower_bound=model_requests_lower_bound,
                input_tokens_total=raw.get("input_tokens", 0),
                input_tokens_cached=raw.get("cached_input_tokens", 0),
                cache_write_input_tokens=None,
                output_tokens_total=raw.get("output_tokens", 0),
                output_tokens_reasoning=raw.get("reasoning_output_tokens", 0),
            )
    fail("Codex emitted no turn.completed usage event")
    return {}


def run_case(
    codex: str,
    binary: str,
    project: Path,
    model: Optional[str],
    timeout: float,
    case: Case,
) -> dict[str, Any]:
    lowered = case.prompt.lower()
    for forbidden in FORBIDDEN_PROMPT_NAMES:
        if forbidden in lowered:
            fail(f"{case.name} prompt names {forbidden!r}; the test is not cold")
    try:
        completed = subprocess.run(
            codex_command(codex, binary, project, model, case.prompt),
            stdin=subprocess.DEVNULL,
            text=True,
            capture_output=True,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired as exc:
        fail(f"{case.name} timed out after {timeout:g}s: {exc}")
    if completed.returncode != 0:
        fail(f"{case.name} exited {completed.returncode}: stderr={completed.stderr!r}")
    events = parse_events(completed.stdout, case.name)
    calls = completed_mcp_calls(events, case.name)
    answer = final_message(events, case.name)
    if tuple(calls) != case.expected_tools:
        fail(
            f"{case.name} MCP calls = {calls!r}, want {list(case.expected_tools)!r}; "
            f"answer={answer!r}"
        )
    if case.expected_answer is not None and answer != case.expected_answer:
        fail(f"{case.name} answer = {answer!r}, want {case.expected_answer!r}")
    return usage(events, len(calls) + 1)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Run opt-in provider-backed Codex checks for cold Jinn MCP discoverability."
    )
    parser.add_argument("--binary", default="jinn", help="path to the Jinn binary")
    parser.add_argument("--codex", default="codex", help="path to the Codex CLI")
    parser.add_argument("--model", help="explicit Codex model for repeatable certification")
    parser.add_argument("--timeout", type=float, default=180.0, help="per-case timeout in seconds")
    parser.add_argument("--case", choices=[case.name for case in CASES], help="run one case instead of all three")
    args = parser.parse_args()
    try:
        binary = resolve_executable(args.binary)
        codex = resolve_executable(args.codex)
        selected = [case for case in CASES if args.case is None or case.name == args.case]
        reports: list[dict[str, Any]] = []
        with tempfile.TemporaryDirectory(prefix="jinn-codex-discovery-") as temporary:
            project = Path(temporary)
            write_minimal_test_project(project, binary)
            print(
                "codex_discoverability_context="
                + json.dumps(
                    context_inventory(
                        CODEX_ROUTE_TOOL,
                        CODEX_DISABLED_FEATURES,
                        "developer_instructions",
                    ),
                    separators=(",", ":"),
                )
            )
            for case in selected:
                case_usage = run_case(codex, binary, project, args.model, args.timeout, case)
                reports.append(case_usage)
                print(
                    f"codex_discoverability_{case.name}=passed "
                    f"usage={json.dumps(case_usage, separators=(',', ':'))}"
                )
        print(f"codex_discoverability_total={json.dumps(sum_usage(reports), separators=(',', ':'))}")
    except (RuntimeError, ValueError) as exc:
        print(f"codex_discoverability_failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
