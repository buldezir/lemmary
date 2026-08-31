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

Nothing to set up. `scripts/test-all.sh` is tracked, so every worktree has it
the moment it exists, and it resolves the verification overlay itself.

The overlay — the separate repository holding the e2e suites and the
verification stack — lives **outside** this tree, beside the main checkout as
`../lemmary-dev`. It used to be checked out *inside* it, at `dev/`. That is
worth knowing because of the failure it caused: a second git repository inside
the working tree means anything that answers "which repository am I in?" by
walking up from the current directory gets the overlay instead of this repo, so
worktree tooling broke whenever the current directory was under `dev/`, and
every new worktree began with no suites at all.

`scripts/overlay.sh` locates it, and prefers the overlay branch whose name
matches the branch you are on — attaching it as a worktree of the overlay, a
sibling at `../lemmary-dev-worktrees/<branch>`, the first time it is needed. It
never creates a branch that does not already exist somewhere; finding none, it
uses whatever the overlay's main checkout is on and says so on stderr. That is
the same fallback the pull request job makes, so a local run and CI agree about
which suites apply.

Matching names are load-bearing, not a convention: a behaviour change and its
e2e update are two commits in two repositories, and the pull request job looks
in the overlay for a branch named like the pull request, falling back to `main`
when it finds none. Name it anything else and CI runs the change against
`main`'s suites, which know nothing about it — and reports green.

So when a change needs an e2e update, make the overlay branch first, and the
next run picks it up with no further ceremony:

```bash
git -C ../lemmary-dev fetch origin
git -C ../lemmary-dev switch -c "$(git rev-parse --abbrev-ref HEAD)" origin/main
```

Both branches need pushing, and the overlay side wants pushing first — until it
is, the pull request job falls back to the overlay's `main`. See "Changes that
span both repositories" in the overlay's `AGENTS.md`.

Two things worth knowing:

- Set `LEMMARY_DEV=/path/to/overlay` to override where it looks, or
  `LEMMARY_NO_SYNC=1` to stop it attaching worktrees on its own.
- The sandbox may refuse git commands aimed at the overlay, since it sits
  outside this checkout. Ask before working around that.

## Verification (required)

```bash
./scripts/test-all.sh
```

That is the only command, in a worktree or the main checkout. It delegates to
the overlay when one is present, and when none is it runs what this repository
can verify alone — unit tests, the compile-level extension-seam check, frontend
tests and the SPA build — printing which suites did not run.

If it took the reduced path, say plainly in the report that the API and browser
e2e suites did not run. Do not claim a task is complete if any stage fails, and
do not claim e2e coverage that did not run.

## Tests must stay in sync

When changing existing behavior, update or add unit tests for the affected
packages — they sit beside the code they test. Do not leave tests asserting the
old behavior; change production code and tests together, and prefer extending
existing tests over skipping or deleting coverage. New features need tests at
the same layer as similar code already has.

API and browser e2e updates belong in the private overlay repository; when it
is present, its `AGENTS.md` covers them.

## One build, feature flags in the environment

There is one binary and one image. Everything anybody runs is the same build,
so a bug reproduces everywhere and CI tests what ships.

Optional behaviour is therefore a **runtime flag read from the environment**,
never a build tag and never a compiled-in variant. `VAULT_ENABLED` and the
`LIMIT_*` family are the pattern: absent means off, the code path is compiled
into every build, and turning it on needs no rebuild.

When adding one:

- Read it once, at wiring time, and pass the value down. Scattered
  `os.Getenv` calls make the effective configuration unanswerable.
- Off must be the default, and off must be the behaviour this app had before
  the flag existed.
- Document it in `.env.example` beside the others, with what it costs — not
  only what it does.

`internal/boot` is the one exception to "wire it onto a live app": it runs
before `pocketbase.New`, and only encryption at rest needs it. Read its package
comment before putting anything else there.

## Docker build (when available)

If Docker is usable (`docker info` succeeds), also build the image after any
build-related changes (Dockerfile, frontend/backend build scripts, Vite/VitePress
config, `docs/` content that is compiled into the image, package lockfiles that
affect `pnpm run build`, Go module files that affect `go build`, and similar):

```bash
docker info >/dev/null && docker build -t lemmary:local .
```

Skip this only when `docker info` fails (daemon missing or unreachable). Do not
claim build-related work is done if the Docker build fails.
