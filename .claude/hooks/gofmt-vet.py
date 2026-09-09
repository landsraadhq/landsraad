#!/usr/bin/env python3
"""Format Go on write and surface vet failures. The Taskfile gates on gofmt."""
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

pkg = "./" + os.path.dirname(path) if os.path.dirname(path) else "./..."
r = subprocess.run(["go", "vet", pkg], capture_output=True, text=True)
if r.returncode != 0:
    print("go vet:\n" + (r.stderr or r.stdout).strip(), file=sys.stderr)
    sys.exit(2)
