#!/usr/bin/env python3
"""Reads take fs.FS, writes take io.Writer, only cmd/ touches os (spec §3.1)."""
import re, sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).parent))
from lib import payload, target, block

path, body = target(payload())
if not path.endswith(".go") or path.endswith("_test.go"):
    sys.exit(0)
if "/internal/" not in path and not path.startswith("internal/"):
    sys.exit(0)
if re.search(r'^\s*(?:_\s+)?"os"\s*$', body, re.M):
    block(
        'Blocked: internal/ may not import "os".\n'
        "  Reads take io/fs.FS, writes take io.Writer, and only cmd/ touches the\n"
        "  real filesystem. This is spec §3.1 and a definition-of-done gate.\n"
        "  It also buys a security property: fs.FS rejects absolute paths and '..'.\n"
        "  If this genuinely needs os, it belongs in cmd/ — or say so to Q first."
    )
