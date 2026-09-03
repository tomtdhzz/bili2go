<!-- Keep it concise. Delete sections that don't apply. Comments (<!-- -->) don't render. -->

## What & why
<!-- What does this change do, and why is it needed? A few sentences. -->

## Change type
- [ ] `feat` — new capability
- [ ] `fix` — bug fix
- [ ] `refactor` — no behavior change
- [ ] `docs` — README / docs / comments
- [ ] `test` — tests only
- [ ] `ci` / `chore` — workflows, tooling, maintenance

## How it was tested
<!-- Commands run and result, e.g. `go test -race ./...`, or `./scripts/verify.sh <BVID>` with ffprobe output. -->

## Pre-merge checklist
- [ ] `go build ./...`, `go vet ./...`, `go test -race ./...` pass
- [ ] `gofmt -l .` is clean
- [ ] Non-trivial logic was built test-first (TDD)
- [ ] Architecture respected: B站 DTOs stay in `internal/bilibili`; `internal/domain` stays IO-free
- [ ] No secrets (e.g. `SESSDATA`), tokens, or personal paths; `.ai-work/` not committed
- [ ] Commits follow Conventional Commits with a body stating **what & why** (see `CONTRIBUTING.md`)
- [ ] `CHANGELOG.md` updated under `[Unreleased]`

## Related issues
<!-- e.g. Closes #123 -->
