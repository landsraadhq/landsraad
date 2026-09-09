"""Shared helpers for landsraad hooks. Hooks receive tool JSON on stdin."""
import json, sys

def payload():
    """Parse the tool payload.

    Unparseable input exits 0 — a broken hook must not brick the session — but
    it says so on stderr. A guard that fails open silently is the silent
    fallback CLAUDE.md forbids: degraded mode has to be visible.
    """
    raw = sys.stdin.read()
    try:
        return json.loads(raw)
    except Exception as e:
        print(f"hook: could not parse tool payload ({e}); allowing by default",
              file=sys.stderr)
        sys.exit(0)

def target(p):
    """(file_path, content_being_written). Content is '' when unknown."""
    ti = p.get("tool_input") or {}
    path = ti.get("file_path") or ""
    body = ti.get("content") or ti.get("new_string") or ""
    return path, body

def block(msg):
    print(msg, file=sys.stderr)
    sys.exit(2)              # exit 2 = block, stderr is shown to Claude
