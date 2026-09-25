# Agent instructions

## Commits

Write a detailed commit message so later agents can understand what was done from `git log`, limited to about one paragraph.

## Code Comments Guidelines
* Write comments **only** when workarounds or "hacks" are introduced:
  * When requested directly by the user.
  * When unavoidable due to technical constraints.
* Avoid commenting self-explanatory or clean code.

## Overlay

E2e suites live in a sibling repo at `../lemmary-dev`, found by `scripts/overlay.sh`.
`./scripts/test-all.sh` is tracked and locates it; no test suites need attaching by hand.

If this project checkout is a Git worktree, always create a corresponding overlay
worktree on the matching branch, even if the main overlay checkout is already on
that branch. Keep it outside this project tree and use it for overlay work.

When a change needs an e2e update in a regular checkout, create a matching
branch directly in the overlay checkout first:

```bash
git -C ../lemmary-dev fetch origin
git -C ../lemmary-dev switch -c "$(git rev-parse --abbrev-ref HEAD)" origin/main
```

The PR job uses the overlay branch named like the PR, else `main`. A mismatched name looks green. Push the overlay branch before the public one. Details: overlay `AGENTS.md`.

- `LEMMARY_DEV` overrides the path; `LEMMARY_NO_SYNC=1` skips attaching worktrees.
- The sandbox may refuse git against the overlay (it sits outside this checkout). Ask before working around that.
- Verification runs in Docker and bind-mounts this tree and the overlay. The daemon must be able to see both paths.

## Verification (required)

```bash
./scripts/test-all.sh
```

That is the only command. It locates the overlay and delegates; the overlay runs the suite in Docker. No overlay is a failure, not a reduced pass. Do not claim a task complete if the script fails.

## Tests

Change production code and unit tests together (tests sit beside the code). Extend existing tests rather than skip or delete. New features get tests at the same layer as similar code.

API and browser e2e live in the overlay; its `AGENTS.md` covers them.

## Lint

`test-all.sh` runs golangci-lint with `backend/.golangci.yml`, then `deadcode`, which fails on functions that nothing calls, not even a test. CI skips this stage, so your local run is the only check: a task is not done while it fails.

Fix what it reports rather than excluding it. Delete dead code instead of adding a caller to keep it. A function over a complexity, length or duplication limit gets split along a real seam, not a `//nolint`.

## Feature flags

One binary, one image. Optional behaviour is a runtime env flag, never a build tag. Absent means off, and off is the pre-flag behaviour. Pattern: `VAULT_ENABLED`, `LIMIT_*`.

When adding one: read it once at wiring time and pass it down; document it in `.env.example` with what it costs, not only what it does.

`internal/boot` runs before `pocketbase.New` and is only for encryption at rest. Read its package comment before putting anything else there.

## Docker (when available)

After build-related changes (`Dockerfile`, build scripts, Vite/VitePress, docs baked into the image, lockfiles, Go modules):

```bash
docker info >/dev/null && docker build -t lemmary:local .
```

Skip only if `docker info` fails. Do not claim the work done if the image build fails.
