"""Shared fixtures and token accounting for discoverability smoke tests."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path
from typing import Any, Iterable, Mapping


DISCOVERY_INSTRUCTIONS = """# Jinn discoverability test

When a user asks you to choose, name, recommend, or use a development capability
or tool, you MUST call the sole available Jinn route tool, whose canonical name
is `jinn_route`, before answering or acting, even if the answer seems obvious.
Hosts may add a server namespace to that name. Pass the user's task in `need`
and use the exact recommendation. For unrelated questions, do not call Jinn.
"""
PROJECT_FILES = (".jcode/mcp.json", "AGENTS.md")


def write_minimal_test_project(root: Path, binary: str) -> None:
    """Create the exact project context shared by the Codex and JCode checks."""
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
    (root / "AGENTS.md").write_text(DISCOVERY_INSTRUCTIONS, encoding="utf-8")


def context_inventory(
    route_tool: str,
    native_tools_disabled: Iterable[str],
    instruction_delivery: str,
) -> dict[str, Any]:
    instruction_bytes = DISCOVERY_INSTRUCTIONS.encode("utf-8")
    return {
        "project_files": list(PROJECT_FILES),
        "project_file_count": len(PROJECT_FILES),
        "instruction_bytes": len(instruction_bytes),
        "instruction_sha256": hashlib.sha256(instruction_bytes).hexdigest(),
        "instruction_delivery": instruction_delivery,
        "mcp_tool_count": 1,
        "mcp_tool": route_tool,
        "native_tools_disabled": list(native_tools_disabled),
    }


def _nonnegative_integer(value: Any, field: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ValueError(f"usage field {field!r} was invalid: {value!r}")
    return value


def normalize_usage(
    *,
    reported_scope: str,
    model_requests_lower_bound: int,
    input_tokens_total: Any,
    input_tokens_cached: Any,
    cache_write_input_tokens: Any,
    output_tokens_total: Any,
    output_tokens_reasoning: Any,
) -> dict[str, Any]:
    """Normalize host counters without claiming a per-request split they do not expose."""
    request_count = _nonnegative_integer(model_requests_lower_bound, "model_requests_lower_bound")
    total_input = _nonnegative_integer(input_tokens_total, "input_tokens_total")
    cached_input = _nonnegative_integer(input_tokens_cached, "input_tokens_cached")
    total_output = _nonnegative_integer(output_tokens_total, "output_tokens_total")
    reasoning_output = _nonnegative_integer(output_tokens_reasoning, "output_tokens_reasoning")
    if cached_input > total_input:
        raise ValueError(
            f"usage field 'input_tokens_cached' ({cached_input}) exceeded "
            f"'input_tokens_total' ({total_input})"
        )
    if reasoning_output > total_output:
        raise ValueError(
            f"usage field 'output_tokens_reasoning' ({reasoning_output}) exceeded "
            f"'output_tokens_total' ({total_output})"
        )
    if cache_write_input_tokens is not None:
        cache_write_input_tokens = _nonnegative_integer(
            cache_write_input_tokens, "cache_write_input_tokens"
        )
    return {
        "reported_scope": reported_scope,
        "model_requests_lower_bound": request_count,
        "input_tokens_total": total_input,
        "input_tokens_cached": cached_input,
        "input_tokens_uncached": total_input - cached_input,
        "cache_write_input_tokens": cache_write_input_tokens,
        "output_tokens_total": total_output,
        "output_tokens_reasoning": reasoning_output,
    }


def sum_usage(records: Iterable[Mapping[str, Any]]) -> dict[str, Any]:
    records = list(records)
    numeric_keys = (
        "model_requests_lower_bound",
        "input_tokens_total",
        "input_tokens_cached",
        "input_tokens_uncached",
        "output_tokens_total",
        "output_tokens_reasoning",
    )
    result: dict[str, Any] = {"reported_scope": "sum_of_case_reports"}
    for key in numeric_keys:
        result[key] = sum(_nonnegative_integer(record[key], key) for record in records)
    cache_writes = [record["cache_write_input_tokens"] for record in records]
    result["cache_write_input_tokens"] = (
        None
        if any(value is None for value in cache_writes)
        else sum(_nonnegative_integer(value, "cache_write_input_tokens") for value in cache_writes)
    )
    return result
