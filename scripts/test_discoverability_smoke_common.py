from __future__ import annotations

import unittest

from discoverability_smoke_common import (
    DISCOVERY_INSTRUCTIONS,
    context_inventory,
    normalize_usage,
    sum_usage,
)


class NormalizeUsageTest(unittest.TestCase):
    def test_context_inventory_exposes_instruction_parity(self) -> None:
        codex = context_inventory("jinn_route", ("shell_tool",), "model_instructions_file")
        jcode = context_inventory("mcp__jinn__jinn_route", ("base_tools",), "project_AGENTS.md")

        self.assertEqual(codex["project_files"], jcode["project_files"])
        self.assertEqual(codex["instruction_bytes"], jcode["instruction_bytes"])
        self.assertEqual(codex["instruction_sha256"], jcode["instruction_sha256"])
        self.assertNotEqual(codex["instruction_delivery"], jcode["instruction_delivery"])
        self.assertIn("omit `max_tools`", DISCOVERY_INSTRUCTIONS)

    def test_derives_uncached_input(self) -> None:
        usage = normalize_usage(
            reported_scope="test",
            model_requests_lower_bound=2,
            input_tokens_total=120,
            input_tokens_cached=20,
            cache_write_input_tokens=None,
            output_tokens_total=30,
            output_tokens_reasoning=10,
        )

        self.assertEqual(usage["input_tokens_uncached"], 100)
        self.assertIsNone(usage["cache_write_input_tokens"])

    def test_rejects_cached_input_larger_than_total(self) -> None:
        with self.assertRaisesRegex(ValueError, "exceeded"):
            normalize_usage(
                reported_scope="test",
                model_requests_lower_bound=1,
                input_tokens_total=10,
                input_tokens_cached=11,
                cache_write_input_tokens=0,
                output_tokens_total=1,
                output_tokens_reasoning=0,
            )

    def test_sums_reports_and_preserves_unknown_cache_writes(self) -> None:
        first = normalize_usage(
            reported_scope="first",
            model_requests_lower_bound=2,
            input_tokens_total=100,
            input_tokens_cached=25,
            cache_write_input_tokens=None,
            output_tokens_total=10,
            output_tokens_reasoning=2,
        )
        second = normalize_usage(
            reported_scope="second",
            model_requests_lower_bound=1,
            input_tokens_total=40,
            input_tokens_cached=10,
            cache_write_input_tokens=5,
            output_tokens_total=6,
            output_tokens_reasoning=1,
        )

        total = sum_usage((first, second))

        self.assertEqual(total["reported_scope"], "sum_of_case_reports")
        self.assertEqual(total["model_requests_lower_bound"], 3)
        self.assertEqual(total["input_tokens_uncached"], 105)
        self.assertIsNone(total["cache_write_input_tokens"])


if __name__ == "__main__":
    unittest.main()
