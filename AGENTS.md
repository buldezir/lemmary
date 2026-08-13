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

## Verification (required)

Before considering a task done, run the full verification stack and fix failures:

```bash
./scripts/test-all.sh
```

That covers:

1. Backend unit tests (`go test`, excluding `/e2e`)
2. Backend API e2e (`go test ./e2e/`)
3. Frontend Playwright e2e (`npm run test:e2e`)

Do not claim the task is complete if any stage fails. Prefer the full script over running only the package you touched.

A task is incomplete until `./scripts/test-all.sh` passes and related tests reflect the new behavior.

## Docker build (when available)

If Docker is usable (`docker info` succeeds), also build the image after any build-related changes (Dockerfile, frontend/backend build scripts, Vite/VitePress config, `docs/` content that is compiled into the image, package lockfiles that affect `npm run build`, Go module files that affect `go build`, and similar):

```bash
docker info >/dev/null && docker build -t paperless-go:local .
```

Skip this only when `docker info` fails (daemon missing or unreachable). Do not claim build-related work is done if the Docker build fails.

## Tests must stay in sync

When changing existing behavior:

- Update or add unit tests for the affected packages.
- Update API e2e under `backend/e2e/` when HTTP/API behavior changes.
- Update Playwright specs under `frontend/` when UI flows change.
- Do not leave tests asserting the old behavior; change production code and tests together.
- Prefer extending existing tests over skipping or deleting coverage.

New features need tests at the same layer as similar code already has.
