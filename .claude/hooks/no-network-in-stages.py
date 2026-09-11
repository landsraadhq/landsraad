#!/usr/bin/env python3
"""Only internal/fetch speaks HTTP; every other package under internal/ is a
pipeline stage (Plan 4 ruling R25)."""
import re, sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).parent))
from lib import payload, target, block

path, body = target(payload())
if not path.endswith(".go") or path.endswith("_test.go"):
    sys.exit(0)
if "/internal/" not in path and not path.startswith("internal/"):
    sys.exit(0)
if "/internal/fetch/" in path or path.startswith("internal/fetch/"):
    sys.exit(0)
# Matches every import spec form: block ("net/http" / _ "net/http" / alias
# "net/http") and single-line (import "net/http" / import _ "net/http"), plus
# the bare "net" package. Mirrors no-os-in-internal.py's pattern shape.
if re.search(r'^\s*(?:import\s+)?(?:[\w.]+\s+|_\s+)?"(?:net/http|net)"\s*(?://.*)?$',
             body, re.M):
    block(
        'Blocked: only internal/fetch may import net/http (or net).\n'
        "  Every other package under internal/ is a pipeline stage. A stage\n"
        "  that blocks on a socket cannot run in a service repo's PR CI,\n"
        "  cannot be tested offline, and makes \"score runs offline in under\n"
        "  a second\" a claim about which filesystem you happened to pass it.\n"
        "  This is Plan 4 ruling R25. If this genuinely needs HTTP, it belongs\n"
        "  in internal/fetch — or say so to Q first."
    )
