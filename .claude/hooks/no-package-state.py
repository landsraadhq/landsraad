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

# A USE of sync.Once, not a mention of it. Searching the whole body matched the
# COMMENT explaining why the code avoids sync.Once, and a Task 1 implementer had
# to reword their reasoning to get past it — a rule that punishes writing down
# *why* is backwards in a project whose thesis is that the reasoning is the
# artifact. Only a line that is ENTIRELY a comment is exempt: a trailing
# "// sync.Once" after real code is ambiguous, so it stays blocked and this
# fails closed. scripts/check-rules.sh mirrors this exactly.
def uses(pattern, text):
    return any(
        re.search(pattern, line) and not line.lstrip().startswith("//")
        for line in text.splitlines()
    )

if uses(r'\bsync\.Once\b', body):
    block("Blocked: sync.Once in internal/ is package-level mutable state.\n"
          "  Make it a value the caller constructs (see /composition).\n"
          "  A singleton means two configurations cannot coexist, and the\n"
          "  failure path of initialisation cannot be tested.\n"
          "  (A comment that only MENTIONS sync.Once is fine — write down why.)")
if re.search(r'^func init\(\)', body, re.M):
    block("Blocked: init() in internal/ is hidden initialisation.\n"
          "  Register explicitly from cmd/ instead (see /composition).")
