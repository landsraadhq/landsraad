#!/bin/sh
# The three rules CONTRIBUTING.md says are enforced, enforced for everyone.
#
# They were previously checked only by Claude Code PreToolUse hooks in
# .claude/settings.json, which run inside one agent workflow and nowhere else.
# An external contributor was told the rule was mechanical and got no
# enforcement at all, so the violation reached review instead of their shell.
#
# Patterns mirror .claude/hooks/*.py. POSIX grep only: this runs on a
# contributor's macOS laptop and on ubuntu-latest in CI.
set -u

status=0

report() {
	printf '\n%s\n' "$1" >&2
	printf '%s\n' "$2" | sed 's/^/  /' >&2
	status=1
}

# 1. Reads take io/fs.FS, writes take io.Writer, and only cmd/ touches the
#    real filesystem (spec §3.1). This also buys a security property: an
#    fs.FS rejects absolute paths and "..".
#    Trailing comments and os/* subpackages both used to slip past: `"os" //
#    just this once` defeated the end anchor, and os/exec — the likeliest way
#    to shell out to git in a fetch adapter — was not matched at all.
found=$(grep -rnE '^[[:space:]]*(import[[:space:]]+)?([A-Za-z0-9_.]+[[:space:]]+|_[[:space:]]+)?"os(/[A-Za-z0-9_/]+)?"[[:space:]]*(//.*)?$' \
	internal --include='*.go' --exclude='*_test.go' 2>/dev/null || true)
if [ -n "$found" ]; then
	report 'internal/ may not import "os" or any os/* package — reads take io/fs.FS, writes take io.Writer, and only cmd/ touches the filesystem:' "$found"
fi

# 2. Package-level mutable state means two configurations cannot coexist in
#    one process, and initialisation failure cannot be tested.
#    The sync.Once check matches a USE, not any occurrence. The bare word used
#    to match the COMMENT explaining why the code avoids sync.Once, and a Task 1
#    implementer had to reword their reasoning to get past it. In a project
#    whose thesis is that the recorded reasoning is the artifact, a rule that
#    punishes writing down *why* is backwards.
#    grep -E has no lookbehind, so this is two steps: find every line naming
#    it, then drop the ones where the name is inside a comment. Only a line
#    that is ENTIRELY a comment is dropped — a trailing `// sync.Once` after
#    real code stays flagged, because such a line is ambiguous and this rule
#    fails closed. `.claude/hooks/no-package-state.py` mirrors both steps.
found=$(grep -rnE 'sync\.Once|^func init\(\)' \
	internal --include='*.go' --exclude='*_test.go' 2>/dev/null \
	| grep -vE '^[^:]*:[0-9]+:[[:space:]]*//' || true)
if [ -n "$found" ]; then
	report 'no sync.Once and no init() below cmd/ — construct the value the caller needs instead:' "$found"
fi

# 3. Error message quality is the product: a substring check passes against a
#    badly worded message. Assert the exact string with ==.
found=$(grep -rnE 'strings\.Contains.*\.(Message|Hint)([^A-Za-z0-9_]|$)' \
	cmd internal --include='*_test.go' 2>/dev/null || true)
if [ -n "$found" ]; then
	report 'assert diagnostic wording exactly, not with strings.Contains against .Message or .Hint:' "$found"
fi

# 4. Only internal/fetch speaks HTTP. Every other package under internal/ is
#    a pipeline stage, and a stage that blocks on a socket cannot be tested
#    offline, cannot be run in a service repo's PR CI, and makes "score runs
#    offline in under a second" a claim about which filesystem you happened
#    to pass it. Plan 4 ruling R25.
found=$(grep -rnE '^[[:space:]]*(import[[:space:]]+)?([A-Za-z0-9_.]+[[:space:]]+)?"(net/http|net)"[[:space:]]*(//.*)?$' \
	internal --include='*.go' --exclude='*_test.go' 2>/dev/null \
	| grep -v '^internal/fetch/' || true)
if [ -n "$found" ]; then
	report 'only internal/fetch may import net/http — a pipeline stage that blocks on a socket cannot run offline:' "$found"
fi

if [ "$status" -ne 0 ]; then
	printf '\nThese rules are in CONTRIBUTING.md. They are not style preferences.\n' >&2
fi
exit "$status"
