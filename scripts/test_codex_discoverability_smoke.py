from __future__ import annotations

import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("codex-discoverability-smoke-test.py")
SPEC = importlib.util.spec_from_file_location("codex_discoverability_smoke_test", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"could not load {SCRIPT}")
SMOKE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = SMOKE
SPEC.loader.exec_module(SMOKE)


class CodexDiscoverabilitySmokeTest(unittest.TestCase):
    def test_discovers_top_level_and_system_skill_instruction_files(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            codex_skills = root / "codex" / "skills"
            agent_skills = root / "agents" / "skills"
            expected = (
                codex_skills / ".system" / "built-in" / "SKILL.md",
                codex_skills / "local" / "SKILL.md",
                agent_skills / "shared" / "SKILL.md",
            )
            for path in expected:
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("test\n", encoding="utf-8")

            discovered = SMOKE.discover_skill_instruction_files((codex_skills, agent_skills))

            self.assertEqual(discovered, tuple(sorted(path.absolute() for path in expected)))

    def test_command_replaces_builtin_prompt_and_disables_skills(self) -> None:
        project = Path("/tmp/jinn-codex-test-project")
        skill = Path("/tmp/jinn-codex-test-skill/SKILL.md")

        command = SMOKE.codex_command(
            "codex",
            "/tmp/jinn",
            project,
            "gpt-test",
            "choose a tool",
            (skill,),
        )
        configs = [command[index + 1] for index, value in enumerate(command[:-1]) if value == "-c"]

        self.assertIn(
            f'model_instructions_file="{project / "AGENTS.md"}"',
            configs,
        )
        self.assertFalse(any(config.startswith("developer_instructions=") for config in configs))
        self.assertIn(
            f'skills.config=[{{path="{skill}",enabled=false}}]',
            configs,
        )


if __name__ == "__main__":
    unittest.main()
