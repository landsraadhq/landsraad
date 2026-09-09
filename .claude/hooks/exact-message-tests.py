#!/usr/bin/env python3
"""Spec §14: every diagnostic type asserts its EXACT message string.

A substring check passes against a badly-worded message, which is how the
first draft reached 0/10 compliance while looking thoroughly tested.
"""
import re, sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).parent))
from lib import payload, target

path, body = target(payload())
if not path.endswith("_test.go"):
    sys.exit(0)

bad = [l.strip() for l in body.splitlines()
       if "strings.Contains" in l and re.search(r'\.(Message|Hint)\b', l)]
if bad:
    print("Diagnostic wording is asserted with a substring check:\n"
          + "\n".join("  " + l for l in bad[:5])
          + "\n\nSpec §14: assert the exact string. Error message quality is the\n"
            "product, so a wording regression must fail a test. Use:\n"
            "  if got != want { t.Errorf(\"\\n got: %s\\nwant: %s\", got, want) }",
          file=sys.stderr)
    sys.exit(2)
