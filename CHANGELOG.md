# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
this project uses date-based entries (no semantic version tags yet).

## [Unreleased]

### Added
- Initial bili2go: Bilibili DASH video downloader in Go (CLI + HTTP API).
  - `internal/domain` — pure domain (Video/Quality/Codec/Stream/Manifest, `SelectStreams` with quality downgrade + codec fallback).
  - `internal/app` — ports (MetadataSource/StreamSource/Fetcher/Muxer) + `Downloader` use case (concurrent fetch, ffmpeg mux).
  - `internal/bilibili` — `view` + `playurl` adapters (anti-corruption boundary; available qualities from actual streams, not `accept_quality`).
  - `internal/media` — concurrent stream fetch with backup-URL failover + ffmpeg `-c copy` muxer.
  - `internal/delivery/{httpapi,cli}` — `GET /api/info`, `GET /download`; `info` / `dl` / `serve` subcommands.
  - `scripts/verify.sh` — end-to-end verification (info → download → ffprobe asserts video+audio).
- Project scaffold: README, LICENSE (MIT), CONTRIBUTING, this changelog, PR + commit templates, GitHub Actions CI (build/vet/gofmt/test-race + `.ai-work/` guard + Markdown link check).
- WS-A concurrency governance (multi-user safety) in the HTTP server:
  - bounded-concurrency limiter (ctx-aware semaphore + bounded wait) on `/download`; `/api/info` stays responsive under saturation.
  - saturation/timeout → `429 Too Many Requests` + `Retry-After` + `{"code":-429,...}`.
  - client-disconnect cancellation aborts in-flight fetch/ffmpeg and frees the slot; per-request `-download-timeout`.
  - graceful shutdown on SIGINT/SIGTERM draining in-flight within `-shutdown-grace`; `http.Server` `ReadHeaderTimeout`/`IdleTimeout`.
  - configurable via `-max-concurrent` (4) / `-max-wait` (5s) / `-download-timeout` (30m) / `-shutdown-grace` (30s). Design: tech-design §7.8.
- WS-C caching + dedup for the HTTP server (`internal/cache`):
  - disk content cache keyed by `(bvid,page,qn,codec)`; a hit is served directly — no upstream call, no limiter slot.
  - single-flight dedup: concurrent identical requests run `produce` once and share the result (one download slot for N requests).
  - LRU eviction by total bytes; sidecar `.json` metadata; startup scan adopts existing entries (cache survives restart).
  - `-cache-dir` / `-cache-size` (default `<tmp>/bili2go-cache`, 2 GiB); `-cache-size 0` disables cache+dedup. Design: tech-design §7.9.
  - measured: cache hit ~0.01s vs miss ~12s for a 40 MB video.

### Fixed
- `internal/media` ffmpeg muxer now passes explicit `-f mp4`, so muxing no longer depends on the output filename's extension (cache temp files are not `.mp4`).
