# Agent instructions

## Commiting

When doing git commit - write very detailed commit message.
So later agent could see what it did from git log.
It may be multi-line and multi-paragraph.

## Resolving GitHub issues

When the user asks to resolve a GitHub issue:

1. Read the issue first (`gh issue view 123`) so the fix matches what was actually reported.
2. Update local `main` so it matches the remote (`git checkout main && git pull`). Stash or commit any work in progress first; do not carry a dirty tree onto `main`.
3. Create a new branch from that up-to-date `main`, named `fix/123-short-description` (or `feat/123-short-description` for enhancements).
4. Implement the fix on that branch; do not commit directly to `main`.
5. Run the full verification stack (below) and fix failures before finishing.
6. Open a pull request targeting `main` (push the branch if needed, then `gh pr create`). Link the issue in the PR body (e.g. `Fixes #123` / `Closes #123`).

Do not consider the issue resolved until the PR exists and verification has passed. Leave the PR for the user to review and merge; do not merge it yourself.

## Reviewing GitHub pull requests

When the user asks to review a GitHub pull request:

1. Read the PR and its diff (`gh pr view <number|url> --json title,body,files,commits` and `gh pr diff <number|url>`). Resolve `OWNER`, `REPO`, `PULL_NUMBER`, and the head `commit_id` (SHA) from that metadata.
2. Perform the review (code quality, correctness, tests, and anything the user asked for).
3. Post the results on GitHub, not only in chat. Prefer inline comments on the changed lines (via `gh api`); use a top-level-only `gh pr review` only when there are no line-specific findings.

`gh pr review` supports only review-level actions (`--approve`, `--comment`, `--request-changes`, `--body` / `--body-file`). It cannot attach `path` / `line` / `side` / `start_line` inline comments. Use the REST API via `gh api` instead.

### Submitted review with grouped inline comments (preferred for a full review)

```bash
gh api repos/OWNER/REPO/pulls/PULL_NUMBER/reviews \
  --method POST \
  --input - <<'EOF'
{
  "commit_id": "<HEAD_SHA>",
  "event": "COMMENT",
  "body": "## Review\n\nSummary of findings.",
  "comments": [
    {
      "path": "backend/api/handler.go",
      "line": 42,
      "side": "RIGHT",
      "body": "Concrete finding on this line."
    }
  ]
}
EOF
```

- `event`: `COMMENT` by default; use `REQUEST_CHANGES` or `APPROVE` only when the user asked for that outcome.
- `line` / `side` refer to the diff on the PR head commit (`RIGHT` for the new file side). For a multi-line range in a review comment object, also set `start_line` and `start_side`.

### Individual inline comments (direct comments endpoint)

For one-off inline comments without a review summary, POST to the pull request comments endpoint:

Single-line:

```bash
gh api repos/OWNER/REPO/pulls/PULL_NUMBER/comments \
  --method POST \
  -f body='Inline comment body' \
  -f commit_id='<HEAD_SHA>' \
  -f path='backend/api/handler.go' \
  -F line=42 \
  -f side='RIGHT'
```

Multi-line range:

```bash
gh api repos/OWNER/REPO/pulls/PULL_NUMBER/comments \
  --method POST \
  -f body='Inline comment body' \
  -f commit_id='<HEAD_SHA>' \
  -f path='backend/api/handler.go' \
  -F start_line=40 \
  -f start_side='RIGHT' \
  -F line=42 \
  -f side='RIGHT'
```

Line coordinates must land on the PR diff; if GitHub rejects them, fix the path/line/side (or use a multi-line range) and retry. Leave merging to the user.

## Working in a git worktree

A worktree of this repository starts **without** `dev/`. The verification stack
is a separate, private repository overlaid at that path and gitignored here, so
`git worktree add` never brings it along and `./dev/scripts/test-all.sh` is
missing in the new tree.

When the main checkout has the overlay, attach it to the worktree before
touching any code — as a worktree of the overlay repository on a branch of the
**same name** as the feature branch, so the code change and its e2e update stay
on matching branches in both repositories.

The name is load-bearing: the pull request job looks in the overlay for a branch
matching the pull request and falls back to `main` when it finds none, so a
mismatched name means CI runs the change against suites that know nothing about
it — and reports green.

```bash
# Run from inside the fresh lemmary worktree.
branch=$(git rev-parse --abbrev-ref HEAD)
root=$(git rev-parse --show-toplevel)
main=$(git worktree list --porcelain | sed -n '1s/^worktree //p')

git -C "$main/dev" fetch origin
git -C "$main/dev" worktree add "$root/dev" -b "$branch" origin/main
(cd "$root/dev" && npm ci)   # Playwright specs keep their own node_modules
```

- If that branch already exists in the overlay, check it out instead of
  creating it: drop `-b "$branch" origin/main` and pass `"$branch"` as the
  final argument.
- The overlay's `replace lemmary/backend => ../backend` then resolves to the
  worktree's own `backend/`, so the suites exercise the worktree's code.
- `npx playwright install chromium` is only needed once per machine; the
  browsers live in a shared cache, not in the worktree.
- If `$main/dev` does not exist, there is no overlay to attach: run the reduced
  verification below and say plainly that the e2e suites did not run.

When the worktree is finished with, remove the overlay worktree too, or prune
it afterwards, so the overlay repository does not keep a stale entry:

```bash
git -C "$main/dev" worktree remove "$root/dev"   # before deleting the worktree
git -C "$main/dev" worktree prune                # after, if it is already gone
```

Both branches need pushing, and the overlay side wants pushing first — until it
is, the pull request job falls back to the overlay's `main`. See "Changes that
span both repositories" in the overlay's `AGENTS.md`.

Note that the sandbox may refuse git commands aimed at `$main/dev`, since it
sits inside the shared checkout. Leaving the worktree (keeping it), running the
overlay commands, and re-entering is the way through.

## Verification (required)

The e2e suites and the full verification stack live in a private repository
overlaid at `dev/` (see [docs/setup.md](docs/setup.md) for what this repo alone
contains). If that overlay is present, it owns verification:

```bash
./dev/scripts/test-all.sh
```

Without it, run what this repository can verify on its own, and say plainly in
the report that the e2e suites did not run:

```bash
# Subshells on purpose: run from the repo root, and each line is independent.
(cd backend && go test ./... -count=1)
(cd backend && go vet -tags lemmary_exttest ./... && go test -tags lemmary_exttest ./internal/boot/ -count=1)
(cd frontend && npm test)
(cd frontend && npm run build)   # tsc -b + vite + vitepress; nothing else here builds the app
```

Do not claim a task is complete if any stage fails, and do not claim e2e
coverage that did not run.

## Tests must stay in sync

When changing existing behavior, update or add unit tests for the affected
packages — they sit beside the code they test. Do not leave tests asserting the
old behavior; change production code and tests together, and prefer extending
existing tests over skipping or deleting coverage. New features need tests at
the same layer as similar code already has.

API and browser e2e updates belong in the private overlay repository; when it
is present, its `AGENTS.md` covers them.

## Docker build (when available)

If Docker is usable (`docker info` succeeds), also build the image after any
build-related changes (Dockerfile, frontend/backend build scripts, Vite/VitePress
config, `docs/` content that is compiled into the image, package lockfiles that
affect `npm run build`, Go module files that affect `go build`, and similar):

```bash
docker info >/dev/null && docker build -t lemmary:local .
```

Skip this only when `docker info` fails (daemon missing or unreachable). Do not
claim build-related work is done if the Docker build fails.
