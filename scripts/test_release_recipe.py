import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parent.parent
JUST = shutil.which("just")


@unittest.skipUnless(JUST, "just is required")
class ReleaseRecipeTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.bin = self.root / "bin"
        self.gobin = self.root / "gobin"
        self.bin.mkdir()
        self.gobin.mkdir()
        shutil.copyfile(ROOT / "justfile", self.root / "justfile")
        self.ignored = self.root / "jinn"
        self.ignored.write_text("preserve this binary\n")
        (self.gobin / "jinn").symlink_to(self.ignored)
        self.trace = self.root / "trace"
        self.stub("just", '''#!/bin/sh
case "$1" in
  version) echo 0.14.1 ;;
  test) echo "just test" >> "$TRACE" ;;
  build) echo "just build old-version" >> "$TRACE"; echo old-version > jinn ;;
esac
''')
        self.stub("git", '#!/bin/sh\necho "git $*" >> "$TRACE"\n')
        self.stub("gh", '#!/bin/sh\necho "gh $*" >> "$TRACE"\n')
        self.stub("go", '''#!/usr/bin/env python3
import os, pathlib, sys
args = sys.argv[1:]
if args == ["env", "GOBIN"]:
    print(os.environ["TEST_GOBIN"])
elif args == ["env", "GOPATH"]:
    print(os.environ["TEST_GOBIN"])
elif args and args[0] == "build":
    output = pathlib.Path(args[args.index("-o") + 1])
    flags = args[args.index("-ldflags") + 1]
    version = flags.split("main.version=", 1)[1].split()[0]
    with open(os.environ["TRACE"], "a") as trace:
        trace.write("go build " + version + "\\n")
    reported = "wrong-version" if os.environ.get("BAD_VERSION") else version
    output.write_text('#!/bin/sh\\necho "binary --version" >> "$TRACE"\\necho ' + reported + '\\n')
    output.chmod(0o755)
else:
    raise SystemExit("unexpected go command: " + repr(args))
''')
        self.env = os.environ.copy()
        self.env.update(
            PATH=str(self.bin) + os.pathsep + self.env["PATH"],
            TRACE=str(self.trace),
            TEST_GOBIN=str(self.gobin),
        )

    def stub(self, name, content):
        path = self.bin / name
        path.write_text(content)
        path.chmod(0o755)

    def run_release(self):
        return subprocess.run(
            [JUST, "--justfile", str(self.root / "justfile"), "release", "0.15.0"],
            cwd=self.root,
            env=self.env,
            text=True,
            capture_output=True,
            timeout=20,
        )

    def test_version_is_installed_before_push_and_ignored_binary_is_preserved(self):
        notes = self.root / "notes.md"
        notes.write_text("release notes\n")
        self.env["JINN_RELEASE_NOTES"] = str(notes)
        result = self.run_release()
        self.assertEqual(result.returncode, 0, result.stderr)
        events = self.trace.read_text().splitlines()
        self.assertEqual(self.ignored.read_text(), "preserve this binary\n")
        self.assertFalse((self.gobin / "jinn").is_symlink())
        self.assertIn("go build v0.15.0", events)
        versions = [i for i, event in enumerate(events) if event == "binary --version"]
        self.assertEqual(len(versions), 2, events)
        self.assertLess(versions[-1], events.index("git push origin main"))
        release = next(event for event in events if event.startswith("gh release create "))
        self.assertIn("--verify-tag", release)
        self.assertIn("--notes-file " + str(notes), release)

    def test_wrong_version_stops_before_git_or_github(self):
        self.env["BAD_VERSION"] = "1"
        result = self.run_release()
        self.assertNotEqual(result.returncode, 0)
        events = self.trace.read_text().splitlines()
        self.assertFalse(any(event.startswith(("git ", "gh ")) for event in events), events)
        self.assertEqual(self.ignored.read_text(), "preserve this binary\n")


if __name__ == "__main__":
    unittest.main()
