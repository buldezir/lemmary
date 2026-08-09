# Agent instructions

## Resolving GitHub issues

When the user asks to resolve a GitHub issue:

1. Read the issue first (`gh issue view 123`) so the fix matches what was actually reported.
2. Update local `main` so it matches the remote (`git checkout main && git pull`). Stash or commit any work in progress first; do not carry a dirty tree onto `main`.
3. Create a new branch from that up-to-date `main`, named `fix/123-short-description` (or `feat/123-short-description` for enhancements).
4. Implement the fix on that branch; do not commit directly to `main`.
5. Run the full verification stack (below) and fix failures before finishing.
6. Open a pull request targeting `main` (push the branch if needed, then `gh pr create`). Link the issue in the PR body (e.g. `Fixes #123` / `Closes #123`).

Do not consider the issue resolved until the PR exists and verification has passed. Leave the PR for the user to review and merge; do not merge it yourself.

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
