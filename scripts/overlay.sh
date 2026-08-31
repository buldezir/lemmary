#!/usr/bin/env bash
# Resolve the verification overlay for this checkout and print its path.
#
# The e2e suites and the verification stack live in a separate repository. A
# clone of this one does not have it, so every caller must cope with it being
# absent: this script exits 3 and prints nothing when there is none.
#
# It used to be checked out *inside* this tree, at dev/. That put a second git
# repository in the working tree, so anything that answers "which repository am
# I in?" by walking up from the current directory -- worktree tooling above all
# -- got the overlay instead of this repo, and every new worktree started with
# no suites at all. The overlay now lives outside the tree and is located by
# this script rather than by its position in it.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Everything below addresses git with -C "$ROOT" and never relies on the
# current directory. That is the whole point of this script; keep it that way.
log()  { printf 'overlay: %s\n' "$*" >&2; }
emit() { printf '%s\n' "$1"; exit 0; }
is_overlay() { [[ -n "${1:-}" && -x "$1/scripts/test-all.sh" ]]; }

SYNC=1
[[ "${LEMMARY_NO_SYNC:-}" == 1 ]] && SYNC=0
[[ "${1:-}" == --no-sync ]] && SYNC=0

# 1. An explicit location wins, and a wrong one is an error rather than a
#    silent fallback -- someone who sets this means it.
if [[ -n "${LEMMARY_DEV:-}" ]]; then
  is_overlay "$LEMMARY_DEV" || { log "LEMMARY_DEV=$LEMMARY_DEV is not an overlay checkout"; exit 3; }
  emit "$LEMMARY_DEV"
fi

# 2. Nested at dev/. CI checks the overlay out there because actions/checkout
#    cannot place a repository outside the workspace, and a nested checkout
#    still works fine where no worktrees are involved.
is_overlay "$ROOT/dev" && emit "$ROOT/dev"

# 3. A sibling of the *main* checkout, so a worktree resolves to the same place
#    its main checkout does.
MAIN="$(git -C "$ROOT" worktree list --porcelain 2>/dev/null | sed -n '1s/^worktree //p')"
[[ -n "$MAIN" ]] || MAIN="$ROOT"
BASE="$(dirname "$MAIN")/lemmary-dev"
is_overlay "$BASE" || { log "not found (looked in \$LEMMARY_DEV, $ROOT/dev, $BASE)"; exit 3; }

BRANCH="$(git -C "$ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null || echo HEAD)"
[[ "$BRANCH" == main || "$BRANCH" == HEAD ]] && emit "$BASE"

# A behaviour change and its e2e update are two commits in two repositories, on
# branches of the same name. Prefer the overlay branch matching this one and
# fall back to whatever the main overlay checkout is on -- the same fallback the
# pull request job makes, so a local run and CI agree about which suites apply.
git -C "$BASE" worktree prune 2>/dev/null || true

if git -C "$BASE" show-ref --verify --quiet "refs/heads/$BRANCH"; then
  HAVE=local
elif git -C "$BASE" show-ref --verify --quiet "refs/remotes/origin/$BRANCH"; then
  HAVE=remote
else
  log "no branch '$BRANCH'; using $(git -C "$BASE" rev-parse --abbrev-ref HEAD) (CI falls back the same way)"
  emit "$BASE"
fi

# The main checkout already being on it is the common case for a single feature.
[[ "$(git -C "$BASE" rev-parse --abbrev-ref HEAD)" == "$BRANCH" ]] && emit "$BASE"

# Otherwise a worktree of the overlay, as a *sibling* of it. Never inside a
# lemmary tree: that is the nesting this script exists to undo.
DIR="$BASE-worktrees/$BRANCH"

if [[ -d "$DIR" ]]; then
  CUR="$(git -C "$DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo '')"
  [[ "$CUR" == "$BRANCH" ]] && emit "$DIR"
  log "$DIR is on '$CUR', not '$BRANCH' -- remove it or fix it by hand"
  exit 3
fi

if [[ "$SYNC" != 1 ]]; then
  log "branch '$BRANCH' exists but has no worktree, and syncing is off; using $BASE"
  emit "$BASE"
fi

mkdir -p "$(dirname "$DIR")"
# Only ever checks out a branch that already exists somewhere. It never invents
# one, so repeated runs cannot litter the overlay with branches.
if [[ "$HAVE" == local ]]; then
  git -C "$BASE" worktree add "$DIR" "$BRANCH" >&2
else
  git -C "$BASE" worktree add "$DIR" -b "$BRANCH" "origin/$BRANCH" >&2
fi
log "attached branch '$BRANCH' at $DIR"
emit "$DIR"
