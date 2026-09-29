# Verification and release boundary

Updated: 2026-09-29T15:37:00+08:00

This is an implemented first Go/Web version, **not a claim that Linux Docker
production acceptance is finished**. Original `D:\com\autoclip` and installed
desktop data were not modified. New repository branch: `feat/go-web-docker`.
Nothing was pushed and no desktop secrets were imported.

Final local run: **247 passing Go test events (including subtests), 0 skips**,
with real Chinese ASR enabled; `go vet` and Windows/Linux builds passed.
Frontend: **43/43 tests**. Deployment checks: **3/3**. The final live API/worker
smoke also passed after rebuilding; its processes were stopped by the test
harness and no test service was left running. These outcomes do not close the Linux Docker/external-service
gates listed below.

## Passed locally

| Check | Actual scope / evidence |
| --- | --- |
| Go tests | Domain, SQLite, secrets, revision CAS, queue/recovery/cancellation, HTTP, AI adapters/pipelines, native media and worker integration. `artifacts/go-tests-final.jsonl`. |
| Frontend | Generated-contract check, TypeScript, **43 tests / 10 files**, Vite production build. `web/logs/*-final.log`. |
| Go vet | `go vet -mod=readonly ./...`; `artifacts/go-vet-final.log`. |
| Linux binary | Pure-Go Linux/amd64 cross-compilation; `artifacts/autoclip-linux-amd64`. This is compilation, not Linux runtime testing. |
| Real rendering | Read-only installed Windows FFmpeg/ffprobe. Generated fixtures test scene order/timing, audio flags, layouts, subtitles, PNG styles, title disappearance after 4 seconds and output verification. |
| Live two processes | Actual compiled Go API + worker + built React. Multipart video/SRT import → **manual** draft → FFmpeg MP4 → HTTP Range download → restart/persistence. `artifacts/live-smoke.log`, source/MP4/SQLite under `artifacts/live-smoke-*`. No live model analysis in this test. |
| Real CPU ASR | Official whisper.cpp b5130 Windows build referenced by v1.9.4, verified multilingual base model. Short synthetic English and Chinese speech passed through real `Tools.Transcribe`. `artifacts/media-native-whisper/ACCEPTANCE.md` and language logs. |
| Compose | Official standalone Compose **5.5.1** parsed/resolved `compose.yaml`; **3 deployment tests** check services, volumes, loopback binding, health dependencies and build lock consumption. `artifacts/deployment-tests.log`. |
| Download integrity | Fixed SHA256 for whisper source/base model, yt-dlp and Deno. Base-image tags and manifest digests verified from official docker-library manifests/repo-info. |
| Original checkout | `git status --short` remained empty in the original repository. |

AI tests use httptest/mock responses: independently tested text/vision requests,
endpoint validation, 401/403/404/429, deadlines, malformed/truncated JSON,
redaction, staged checkpoints, timeline/scoring/collection safeguards and consent.
No paid model call or external publishing occurred.

Real ASR is more than a successful model load: it produced recognized English
and Chinese subtitles. However, the Chinese synthetic sample produced Traditional
Chinese and one error (`時別` instead of `識別`). It is **not** a representative
human-speech accuracy benchmark. Review generated subtitles before publication.

## Not yet verified / release gate

1. **Full Linux Docker build/boot and native runtime**: Docker Desktop and WSL
   are now installed on this Windows host, but the required Windows restart and
   Docker first-run agreement are pending; no engine is running yet.
   Compose validation and cross-compiling do not replace runtime acceptance.
   Run `sh scripts/smoke-docker.sh` on the target Linux host;
   `.github/workflows/ci.yml` also supplies that job.
2. **Actual Bilibili/YouTube downloading** with authorized real samples/cookies:
   downloader argv, bounds, errors and progress have deterministic tests; live
   platform restrictions, login and current extraction support remain unverified.
3. **Paid text/vision providers**: configure fresh credentials and explicitly
   authorize tests. Mock HTTP behavior does not prove account/model permissions.
4. **Real Chinese speech comparison and resource baseline**: human-speech quality,
   long-video performance and the 4-core/8-GiB target still need measurement.
5. **Linux process-group cancellation and race detector**: tests are supplied
   and Linux code compiles; local Windows environment lacks a Linux runtime/C
   toolchain for execution. CI's Linux job runs native tests with `-race`.

Do not describe these items as passed until their logs and outcomes are recorded.

Update: 2026-09-29T15:37:00+08:00 — Installed WSL 2.7.14.0 and Docker Desktop
4.93.0 after verifying SHA256 and vendor signatures. Enabled WSL and Virtual
Machine Platform without rebooting. The installed Docker CLI 29.8.1, Compose
5.5.1, Compose config validation and **3/3 deployment regression tests** passed.
`docker info` explicitly failed with a missing engine pipe; Windows reports
RebootPending and no active hypervisor. The project has **not** been built or
started in Docker. Evidence: `artifacts/docker-install/*`; resumption steps and
the Windows 23H2 support caveat are in operations.md. No application code was
changed during installation; README now distinguishes Windows prerequisite
installation from engine readiness.

## Intentional first-version limits

- Trusted LAN/VPN only; no accounts, quotas, isolation, platform publishing,
  scheduling, desktop IPC, GPU support or legacy project/data migration.
- One heavy task at a time on one local host. SQLite/named volumes are not an
  NFS or multi-host queue design.
- OpenAI-compatible Chat Completions only. Text/vision settings and tests are
  independent; image transmission requires explicit consent.
- Current nine Go-rendered title designs, not historical Pillow rasterization.
  Historical template versions 2–5 are rejected. Frosted is a translucent PNG
  card, not a separate live backdrop-blur mask endpoint.
- Original aspect exports keep aspect ratio with a 1920-pixel long-edge cap;
  portrait/landscape are 1080×1920 / 1920×1080 at 30 fps.
- Browser-incompatible source codecs have a visible playback warning; there is
  no background source-compatible-preview transcoding endpoint in this version.
  Final exports use H.264/AAC MP4.
- Visual event boundaries are sampled-frame estimates, not frame-accurate
  full-video vision. Review scenes before export.
- New workflow translations are Chinese/English; other reused languages may
  fall back to English. Collections are ordered drafts within one project.

## Reproduce

```sh
go test -mod=readonly -timeout 180s ./...
go vet -mod=readonly ./...
go run -buildvcs=false ./cmd/specgen
cd web
npm ci
npm run check:api
npm run typecheck
npm test
npm run build
cd ..
node --test scripts/deployment.test.mjs
sh scripts/smoke-docker.sh
```

Native test overrides: `FFMPEG_PATH`, `FFPROBE_PATH`, `REQUIRE_MEDIA_TESTS=1`
for worker integration; `MEDIA_FFMPEG`, `MEDIA_FFPROBE`, `MEDIA_WHISPER`,
`MEDIA_WHISPER_MODEL`, `MEDIA_WHISPER_FIXTURE`, `MEDIA_WHISPER_EXPECT` for media.
Optional speech test skips explicitly if its CLI/model/fixture are absent.
`scripts/local-smoke.mjs` can test an already-built native executable without
Docker. All its data/logs live in a new ignored artifact directory and both
child services are stopped afterward.
