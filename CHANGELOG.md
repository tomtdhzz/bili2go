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

- WS-G post-download analysis: `analyze` subcommand producing a readable `summary.md`.
  - `internal/analyze` (Go stdlib only) — `Analyzer` use case + ports `Digester`/`Summarizer`;
    `ExecDigester` execs the host `video-digest` binary (macOS-native transcription + on-screen keywords),
    `HTTPSummarizer` POSTs `digest.json` to the summarizer service. Chain: download → digest → summarize → `summary.md`.
  - `cmd/summarizer` + `internal/summarizer` — Dockerized LLM summary service (Go stdlib): `GET /health`, `POST /summarize`;
    OpenAI-compatible endpoint when configured, deterministic template fallback otherwise (offline-runnable).
    Fixed 5-section `summary.md` with per-conclusion timecodes; gaps reproduced verbatim; no fabricated speech.
  - `deploy/docker-compose.yml` — summarizer service on :8091. Boundary: `video-digest` stays on the host
    (macOS-native frameworks cannot be containerized); only the LLM summary is Dockerized.
  - CLI flags: `-video`/`-bvid`, `-digest-bin`, `-summarizer`, `-lang`, `-keywords`, `-digest-out`, `-top`, `-keep-video`.
    Pre-flights the summarizer `/health` before the expensive download+digest so a down service fails fast (~0.5s).
    Design: `docs/prd/PRD-analyze.md`, `docs/tech-design/tech-design-analyze.md`.
- WS-D summarizer authentication: opt-in bearer token on the summary service.
  - `internal/summarizer` server — when `SUMMARIZER_TOKEN` is set, `POST /summarize` requires
    `Authorization: Bearer <token>` (constant-time compare) or returns `401` with
    `WWW-Authenticate: Bearer`; `GET /health` stays open for probes/preflight. Token unset →
    no auth (backward-compatible, offline-runnable).
  - `internal/analyze` `HTTPSummarizer` sends the bearer header; `analyze` CLI gains
    `-summarizer-token` (default env `BILI_SUMMARIZER_TOKEN`).
  - `deploy/docker-compose.yml` injects `SUMMARIZER_TOKEN`. Closes the H1 exposure gap
    (public `:8091` could be abused to run up LLM cost).
- summarizer single-language rewrite (Go): the summary service is reimplemented from Python stdlib to
  Go stdlib (`cmd/summarizer` + `internal/summarizer`), making the repo single-language (Go + the host
  `video-digest` binary). HTTP contract, five-section output (byte-for-byte parity via golden), bearer
  auth and offline fallback all unchanged; `internal/analyze` consumer untouched. Docker image is now a
  distroless static binary (~3 MB vs the Python image), health check via `summarizer health` (no shell).
  Design: `docs/prd/PRD-summarizer-go.md`, `docs/tech-design/tech-design-summarizer-go.md`.
- Self-hosted, request-driven analysis (transcription + on-screen OCR): `serve` now exposes
  `POST /api/analyze?bvid=<BV|url>[&page&qn&codec]` → download → digest → summarize →
  `200 {markdown, model, fallback, title, engine, segments, duration_s, ...}`. Reuses the
  WS-A limiter (heavy op, saturation → 429); missing bvid → 400; unconfigured → 501.
  - `internal/analyze` `LocalDigester` — a containerizable `Digester` (parallel to the
    macOS-only `ExecDigester`) that execs `ffprobe`+`ffmpeg`+`whisper.cpp` to produce a
    schema-compatible `digest.json` (`transcript.engine="whisper"`). It also samples frames with
    ffmpeg and OCRs them with `tesseract` to fill `screen_keywords` (on-screen text not spoken),
    flagging `spoken_in_transcript` (dual evidence) and `is_chrome` (persistent UI/watermark,
    excluded); OCR gracefully skips to empty when tesseract is unavailable.
  - OCR frame dedup: near-identical consecutive frames (static slides) are skipped via an 8×8
    average-hash / Hamming-distance test before tesseract, cutting redundant OCR work and
    keyword noise on long or slide-heavy videos (`serve`-level `MaxFrames` cap still applies).
  - `serve` gains `-whisper-bin`/`-whisper-model`/`-lang`/`-keywords`/`-tesseract-bin`/`-summarizer`/
    `-summarizer-token`, and `-digest-bin` to opt back into macOS `video-digest`.
  - `serve -artifact-dir DIR` optionally persists each request's `video.mp4` + `summary.md` +
    `digest.json` under `DIR/<bvid>[-p<page>]/` and returns `video_path`/`summary_path`/`digest_path`;
    a repeat request for the same `bvid`(+`page`) is served from those artifacts (`"cached":true`) with
    no re-download/transcribe and no limiter slot. Default stays stateless (temp files, deleted).
  - `deploy/bili2go/Dockerfile` (debian-slim + ffmpeg + `tesseract-ocr`(chi_sim/eng) + statically-built `whisper-cli`) +
    `docker-compose.yml` `bili2go` service; whisper model mounted via `./models`. Runs on
    Linux — no macOS dependency. Design: `docs/prd/PRD-self-hosted-analyze.md`,
    `docs/tech-design/tech-design-self-hosted-analyze.md`.

### Fixed
- `internal/media` ffmpeg muxer now passes explicit `-f mp4`, so muxing no longer depends on the output filename's extension (cache temp files are not `.mp4`).
