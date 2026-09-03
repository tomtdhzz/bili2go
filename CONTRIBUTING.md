# Contributing

Thanks for improving bili2go. Please keep the design and workflow below.

## Prerequisites

- Go 1.24+
- [ffmpeg](https://ffmpeg.org/) (`ffmpeg` + `ffprobe` in PATH) for muxing and end-to-end verification.

## Develop & test

```bash
go build ./...
go vet ./...
gofmt -l .            # must print nothing
go test -race ./...
./scripts/verify.sh {BVID}   # end-to-end: info → download → ffprobe asserts video+audio
```

## Design rules

- **Domain-centric + ports/adapters.** `internal/domain` is pure (types + `SelectStreams`, no IO). `internal/app` defines ports and the `Downloader` use case. `internal/bilibili` and `internal/media` are adapters that implement the ports.
- **Anti-corruption boundary.** Bilibili JSON DTOs live only in `internal/bilibili`; map them to `domain` types. Never let transport shapes (`codecid`, `baseUrl`, …) leak into `domain`.
- **Test-first (TDD)** for non-trivial logic (selection, parsing, orchestration): write the failing test, then implement to green, then refactor.
- **Idiomatic Go**: `gofmt`, `go vet` clean; consumer-defined interfaces; no `util`/`types` dumping packages.

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/): `type(scope): imperative summary`, then a body that **clearly states what changed and why** — one bullet per change. The subject is the shape; the body is where a reader learns what you actually did.

- **Types**: `feat`, `fix`, `docs`, `refactor`, `test`, `ci`, `chore`, `perf`. Scope optional (e.g. `feat(media)`, `fix(bilibili)`).
- **One concern per commit**; keep behavior changes separate from docs.
- A commit template lives at [`.gitmessage`](.gitmessage) — enable it once: `git config commit.template .gitmessage`.

Example:

```text
feat(media): retry backup URLs on CDN failure

- fetcher.go: try baseUrl then each backupUrl in order before failing
- why: single baseUrl occasionally 403s; backups recover the download
```

## Before you open a PR

- `go build`/`go vet`/`gofmt -l`/`go test -race` all clean.
- No secrets (never commit a real `SESSDATA`), tokens, or personal paths. Never commit `.ai-work/`.
- README commands still run as written; docs/diagrams render.
- Update `CHANGELOG.md` under `[Unreleased]`.
