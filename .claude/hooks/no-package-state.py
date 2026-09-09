#!/usr/bin/env python3
"""No singletons, no init() registration, no hidden globals below cmd/."""
import re, sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).parent))
from lib import payload, target, block

path, body = target(payload())
if not path.endswith(".go") or path.endswith("_test.go"):
    sys.exit(0)
if "/internal/" not in path and not path.startswith("internal/"):
    sys.exit(0)

if re.search(r'\bsync\.Once\b', body):
    block("Blocked: sync.Once in internal/ is package-level mutable state.\n"
          "  Make it a value the caller constructs (see /composition).\n"
          "  A singleton means two configurations cannot coexist, and the\n"
          "  failure path of initialisation cannot be tested.")
if re.search(r'^func init\(\)', body, re.M):
    block("Blocked: init() in internal/ is hidden initialisation.\n"
          "  Register explicitly from cmd/ instead (see /composition).")
