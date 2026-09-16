#!/usr/bin/env bash
# Resolve the verification overlay for this checkout and print its path.
# Exits 3 and prints nothing on stdout when there is none; callers treat that
# as failure. The overlay lives outside this tree on purpose: a nested git
# repository makes worktree tooling that walks up from cwd find the wrong repo.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Always git -C "$ROOT"; never rely on the current directory.
log()  { printf 'overlay: %s\n' "$*" >&2; }
emit() { printf '%s\n' "$1"; exit 0; }
is_overlay() { [[ -n "${1:-}" && -x "$1/scripts/test-all.sh" ]]; }

SYNC=1
[[ "${LEMMARY_NO_SYNC:-}" == 1 ]] && SYNC=0
[[ "${1:-}" == --no-sync ]] && SYNC=0

# An explicit location that is wrong is an error, not a silent fallback.
if [[ -n "${LEMMARY_DEV:-}" ]]; then
  is_overlay "$LEMMARY_DEV" || { log "LEMMARY_DEV=$LEMMARY_DEV is not an overlay checkout"; exit 3; }
  emit "$LEMMARY_DEV"
fi

# CI checks the overlay out at dev/: actions/checkout cannot place a
# repository outside the workspace.
is_overlay "$ROOT/dev" && emit "$ROOT/dev"

# Sibling of the *main* checkout, so a worktree resolves where its main does.
MAIN="$(git -C "$ROOT" worktree list --porcelain 2>/dev/null | sed -n '1s/^worktree //p')"
[[ -n "$MAIN" ]] || MAIN="$ROOT"
BASE="$(dirname "$MAIN")/lemmary-dev"
is_overlay "$BASE" || { log "not found (looked in \$LEMMARY_DEV, $ROOT/dev, $BASE)"; exit 3; }

BRANCH="$(git -C "$ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null || echo HEAD)"
[[ "$BRANCH" == main || "$BRANCH" == HEAD ]] && emit "$BASE"

# Prefer the overlay branch named like this one, else whatever the main overlay
# checkout is on: the same fallback the PR job makes, so local and CI agree.
git -C "$BASE" worktree prune 2>/dev/null || true

if git -C "$BASE" show-ref --verify --quiet "refs/heads/$BRANCH"; then
  HAVE=local
elif git -C "$BASE" show-ref --verify --quiet "refs/remotes/origin/$BRANCH"; then
  HAVE=remote
else
  log "no branch '$BRANCH'; using $(git -C "$BASE" rev-parse --abbrev-ref HEAD) (CI falls back the same way)"
  emit "$BASE"
fi

[[ "$(git -C "$BASE" rev-parse --abbrev-ref HEAD)" == "$BRANCH" ]] && emit "$BASE"

# A worktree of the overlay as a sibling of it, never inside a lemmary tree.
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
# Only checks out a branch that already exists, so reruns cannot litter branches.
if [[ "$HAVE" == local ]]; then
  git -C "$BASE" worktree add "$DIR" "$BRANCH" >&2
else
  git -C "$BASE" worktree add "$DIR" -b "$BRANCH" "origin/$BRANCH" >&2
fi
log "attached branch '$BRANCH' at $DIR"
emit "$DIR"
