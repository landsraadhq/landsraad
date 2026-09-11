// Package sparsefs names the answers a sparse filesystem has and a complete
// one does not.
//
// A filesystem whose metadata arrives before its content can say three
// things about a path, not two: the content is here, the path is absent from
// a directory we enumerated, or nothing ever enumerated the directory it
// would be in. The last two are these sentinels, and neither is
// fs.ErrNotExist on purpose — a missing-file diagnostic would send somebody
// to look for a file that is sitting in their repository, and "landsraad
// never looked" is not "your file is gone".
//
// They live in a package that imports nothing, rather than beside the
// implementation, so that a pipeline stage can name them without importing
// the transport. internal/catalog, internal/render and internal/scorecard
// all have to tell "this file is missing from your repository" from "this is
// a bug in landsraad's content planner". Importing internal/fetch to do it
// put net/http into the transitive dependencies of three stage packages, and
// both enforcement points for "only internal/fetch speaks HTTP" —
// scripts/check-rules.sh and .claude/hooks/no-network-in-stages.py — grep
// for a literal import line, so neither would have fired on a stage that
// went on to call fetch.NewClient. The rule's prose would have survived
// while its structural backing was gone.
//
// This is io/fs's own arrangement, not a departure from it: fs.ErrNotExist
// lives in the interface package and os.ErrNotExist is an alias for it,
// precisely so a consumer need not import the implementation.
//
// internal/fetch re-exports both names. fetch.ErrNotFetched and
// sparsefs.ErrNotFetched are the same value, so errors.Is cannot tell them
// apart and no existing caller or message changes.
package sparsefs

import "errors"

// ErrNotFetched means the path is in the tree and its content was never
// requested. It is a bug in cmd/'s content planner, never a user's mistake.
var ErrNotFetched = errors.New("content was listed but never fetched")

// ErrNotListed means nothing is known about this path's directory, because
// no listing ever covered it. Also a planner bug, and also deliberately not
// ErrNotExist — see spec §14.1 on why telling somebody a file does not
// exist, when the truth is that landsraad never looked, is worse than saying
// nothing.
var ErrNotListed = errors.New("directory was never listed")
