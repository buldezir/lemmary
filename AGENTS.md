# Agent instructions

## Commits

Write a detailed, multi-paragraph commit message so later agents can reconstruct the change from `git log`.

## Overlay

E2e suites live in a sibling repo at `../lemmary-dev`, found by `scripts/overlay.sh`.
`./scripts/test-all.sh` is tracked and locates it; nothing to attach by hand.

When a change needs an e2e update, create a matching overlay branch first:

```bash
git -C ../lemmary-dev fetch origin
git -C ../lemmary-dev switch -c "$(git rev-parse --abbrev-ref HEAD)" origin/main
```

The PR job uses the overlay branch named like the PR, else `main`. A mismatched name looks green. Push the overlay branch before the public one. Details: overlay `AGENTS.md`.

- `LEMMARY_DEV` overrides the path; `LEMMARY_NO_SYNC=1` skips attaching worktrees.
- The sandbox may refuse git against the overlay (it sits outside this checkout). Ask before working around that.

## Verification (required)

```bash
./scripts/test-all.sh
```

That is the only command. With no overlay it runs Go tests, `go vet`, frontend tests and the SPA build, and prints what it skipped. Report that API and browser e2e did not run. Do not claim a task complete if any stage fails, or claim e2e that did not run.

## Tests

Change production code and unit tests together (tests sit beside the code). Extend existing tests rather than skip or delete. New features get tests at the same layer as similar code.

API and browser e2e live in the overlay; its `AGENTS.md` covers them.

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
