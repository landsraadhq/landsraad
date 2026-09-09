#!/usr/bin/env python3
"""Format Go on write, and report vet findings without blocking.

gofmt is applied in place: always safe, always wanted.

`go vet` only reports. This plan is test-first, so a package that does not
compile is the *expected* state between "write the failing test" and "make it
pass" — vet cannot tell that apart from a real defect, and an earlier version
of this hook blocked every RED step because of it. `task ci` is the gate that
actually enforces vet-clean, and every task runs it before committing.

The package is located by running vet from the file's own directory. Building
"./" + an absolute path produced a doubled path that matched nothing, which
made this hook block legitimate writes.
"""
import subprocess, sys, os, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).parent))
from lib import payload, target

path, _ = target(payload())
if not path.endswith(".go") or not os.path.exists(path):
    sys.exit(0)

before = pathlib.Path(path).read_bytes()
subprocess.run(["gofmt", "-w", path], capture_output=True)
if pathlib.Path(path).read_bytes() != before:
    print(f"gofmt: reformatted {path}", file=sys.stderr)

r = subprocess.run(["go", "vet", "."], cwd=os.path.dirname(path) or ".",
                   capture_output=True, text=True)
if r.returncode != 0:
    detail = (r.stderr or r.stdout).strip()
    print("go vet reports (not blocking — `task ci` is the gate; expected "
          "during a test-first RED step):\n" + detail, file=sys.stderr)
sys.exit(0)
