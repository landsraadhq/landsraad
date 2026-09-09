package main

import (
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// gitLastEdit answers "when was this path last changed" from git history.
//
// It lives in cmd/ because internal/ may not import os/exec — that rule is
// what keeps every stage below here testable in memory, and spec §9 records
// this as the one place the answer has to come from outside.
//
// The result is cached per path: `git log` is cheap but a repository with
// forty entities would otherwise fork forty processes for the same handful of
// directories.
func gitLastEdit(root string) scorecard.LastEditFunc {
	cache := map[string]struct {
		t  time.Time
		ok bool
	}{}
	return func(p string) (time.Time, bool) {
		if hit, seen := cache[p]; seen {
			return hit.t, hit.ok
		}
		t, ok := gitLastEditUncached(root, p)
		cache[p] = struct {
			t  time.Time
			ok bool
		}{t, ok}
		return t, ok
	}
}

func gitLastEditUncached(root, p string) (time.Time, bool) {
	// %ct is the committer date as a Unix timestamp: no locale, no timezone
	// parsing, no ambiguity.
	cmd := exec.Command("git", "-C", root, "log", "-1", "--format=%ct", "--", p)
	out, err := cmd.Output()
	if err != nil {
		// Not a git repository, git not installed, or the path has no
		// history. All three mean the same thing to the caller: unknown.
		// Reporting a date we do not have would silently pass docs-fresh.
		return time.Time{}, false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0).UTC(), true
}

// noLastEdit is the answer for a repository with no git history: a fetched
// tree, or a directory that was never a repository. docs-fresh renders
// not-reported for every entity, which is the honest answer.
func noLastEdit() scorecard.LastEditFunc {
	return func(string) (time.Time, bool) { return time.Time{}, false }
}
