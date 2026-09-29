# Verification and release boundary

Updated: 2026-09-29T18:40:00+08:00

This is an implemented first Go/Web version. Linux image build and service
health are verified; **not all production/external-service gates are closed**.
Original `D:\com\autoclip` and installed
desktop data were not modified. Initial branch: `feat/go-web-docker`; the user
subsequently requested renaming it to `main` and publishing commit `e4b625d` to
`zylar06/goclip`, which was verified over HTTPS. Current build optimization work
is on `feat/docker-build-speed`, published as PR #1 (commit `28fac89` before
the subsequent CI fixture fix). Desktop secrets were not imported
during the original migration; the later user-authorized model-only copy is
recorded below.

Final local run: **254 passing Go test events (including subtests), 0 failed,
0 skipped**, with real Chinese ASR enabled; `go vet` passed.
Frontend: **43/43 tests** plus typecheck/contract check and production build.
Build configuration/smoke regression checks: **16/16**. Linux images build and both Compose
services are healthy. The deployed services are intentionally left running at
`127.0.0.1:8080`; their named data/model volumes are preserved.
Real-source acceptance and remaining external-service gates are detailed below.

## Passed locally

| Check | Actual scope / evidence |
| --- | --- |
| Go tests | Domain, SQLite, secrets, revision CAS, queue/recovery/cancellation, HTTP, AI adapters/pipelines, native media and worker integration, Bilibili selection, CJK font matching/wrapping, atomic model environment bootstrap. `artifacts/env-go-tests.jsonl`. |
| Frontend | Generated-contract check, TypeScript, **43 tests / 10 files**, Vite production build. `artifacts/env-frontend-*.log`. |
| Go vet | `go vet -mod=readonly ./...`; `artifacts/env-go-vet.log`. |
| Linux binary | Pure-Go Linux/amd64 cross-compilation; `artifacts/autoclip-linux-amd64`. This is compilation, not Linux runtime testing. |
| Real rendering | Read-only installed Windows FFmpeg/ffprobe. Generated fixtures test scene order/timing, audio flags, layouts, subtitles, PNG styles, title disappearance after 4 seconds and output verification. |
| Linux media runtime | **66 passing test events, 0 failed, 0 skipped** in the actual final runtime image, including Chinese subtitle font/pixels, real Whisper and Linux process-group cancellation. Isolated read-only/no-network container, no application data volume. `artifacts/goal-media-linux-final.log`. |
| Live two processes | Actual compiled Go API + worker + built React. Multipart video/SRT import → **manual** draft → FFmpeg MP4 → HTTP Range download → restart/persistence. `artifacts/live-smoke.log`, source/MP4/SQLite under `artifacts/live-smoke-*`. No live model analysis in this test. |
| Real CPU ASR | Official whisper.cpp b5130 Windows build referenced by v1.9.4, verified multilingual base model. Short synthetic English and Chinese speech passed through real `Tools.Transcribe`. `artifacts/media-native-whisper/ACCEPTANCE.md` and language logs. |
| Compose/build setup | Compose **5.5.1** parsed/resolved `compose.yaml`; **6 deployment contracts + 7 APT setup tests** cover services, volumes, loopback binding, health dependencies, lock consumption, single build ownership, cache mounts, source validation and model runtime-only secrets. `artifacts/env-deployment-tests.log`. |
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

1. **Other Linux hosts / latest CI revision**: this host's Docker build, service health
   and actual Bilibili import/CPU ASR/manual MP4 export pass. The successful
   project survived container recreation during subtitle fixes. The separate
   `scripts/smoke-docker.sh` fixture/restart job now also passes in an isolated
   local Compose project after the transfer fix. New GitHub results are recorded
   below when available; local acceptance does not prove all target servers.
2. **Other Bilibili videos, login-restricted content and YouTube**: the specified
   Bilibili sample downloaded without cookies. This does not verify all videos,
   account/region restrictions, cookie-authenticated imports or YouTube.
3. **Paid text/vision providers**: configure fresh credentials and explicitly
   authorize tests. Mock HTTP behavior does not prove account/model permissions.
4. **Chinese speech quality and resource baseline**: the 671.475-second human
   speech sample produced real subtitles, but visible recognition errors remain
   (including football names/phrases). No accuracy score or reference comparison
   was performed. The 4-core/8-GiB target still needs resource measurement.
5. **Race detector / current CI revision**: GitHub's Ubuntu `test` job passed
   `go test -race -timeout 180s ./...`, vet, generated-contract checking and the
   frontend checks for commit `28fac89`. This does not imply the separate
   container smoke job passed; its fixture-transfer failure is recorded below.

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

Update: 2026-09-29T17:00:00+08:00 — Added opt-in snapshot/default HTTPS CDN
source selection, input validation, APT request limits, dependency cache mounts,
and a single Compose build owner. `scripts/apt-setup.test.mjs` **7/7** and
`scripts/deployment.test.mjs` **5/5** passed. The shell tests execute the real
configuration script against isolated temporary APT directories, without
network or software installation; they cover both modes, mirrors, cache hooks,
error policy and rejected input. Git Bash needed execution outside the sandbox
to access its temporary fixture directories. Evidence:
`artifacts/build-speed-tests.log`. The new configuration does not alter an
already-running build; it has not been claimed to reduce measured build time.
Full-suite rerun and the goal's specified real Bilibili clip acceptance are
tracked separately; mock tests cannot close that video-acceptance gate.

Update: 2026-09-29T17:38:00+08:00 — After the Bilibili format-selection fix:
**248 Go test events passed, 0 failed, 0 skipped**, including the real native
Chinese ASR fixture; `go vet` passed. Frontend typecheck/contract check,
**43/43 frontend tests**, production build, and **12/12 build configuration
regressions** passed. The Go/frontend build-time tests also passed in Linux
Docker. Evidence: `artifacts/goal-go-tests-final.jsonl`,
`artifacts/goal-go-vet-final.log`, `artifacts/build-speed-frontend-*.log`,
`artifacts/build-speed-tests.log`. Four additional offline checks against pinned
yt-dlp verified real selector semantics (regular CDN video+audio, combined stream,
all-MCDN rejection, missing safe audio rejection); these are an ignored local
acceptance harness, not a Python application/runtime dependency.

The first live import of `BV1TRhs6hEQp` failed explicitly after downloading video
because its selected MCDN audio host refused port 8082. It was not an ASR/model
failure or a successful clip. The failed project and logs were retained; no
browser cookies or paid provider calls were used. Retest uses the rebuilt
Go service's normal URL-import API and an explicitly selected manual segment;
it does not substitute a synthetic video or claim model-selected highlights.

## Specified real-video acceptance

Update: 2026-09-29T17:59:00+08:00 — **Passed for the manual-clip scope**:
`BV1TRhs6hEQp` (671.475 seconds) downloaded through the normal URL-import API
without cookies and produced **56 real CPU-ASR subtitle cues**. Import completed
at 17:42:52 +08:00. Project `c952142c62a2ec165b12db314ce723ee` and draft
`0c7700c2d1f143d9bb35ffa70253df93` survived service recreation; neither source
download nor transcription was repeated for subsequent render fixes.

The initial completed MP4 failed visual acceptance: private font-name matching
produced boxed Chinese glyphs. Fixing that exposed unwrapped long CJK lines.
Both defects were fixed and regression-tested, rather than treating FFmpeg's
successful exit alone as a usable video. The final backend rebuild and healthy
startup took **31.7 seconds**, reusing native dependencies/model caches.

Final export task `23ad3318f194d5793b32ec08cb995618` completed at
17:57:06 +08:00. It contains source **30–60 seconds**, exactly **30.000 seconds**,
**1920×1080 / 30 fps H.264**, **48 kHz stereo AAC**, **22,796,807 bytes**.
HTTP 206 range download, full MP4 download, an independent host ffprobe,
and frame inspection at output seconds 1 and 15 passed. The Chinese title and
wrapped subtitles render without missing-glyph boxes or horizontal clipping.
Both deployed services were healthy and on the same rebuilt image; the isolated
Linux test container exited and was removed, with no background test task left.

Final evidence under `artifacts/bilibili-BV1TRhs6hEQp/reexport-Jlm1AM/`:
`summary.json`, `independent-verification.json`, `export.mp4`,
`frame-title.jpg`, `frame-subtitles.jpg`. MP4 SHA256:
`ea10e2c9e06a0bbb3ee014b205ccad95af60d04e588772b65f5561dc11991a31`.
Earlier failed/visually defective attempts remain as diagnostic evidence, not
the accepted output. Build evidence: `artifacts/subtitle-wrap-rebuild.*.log`.

After all subtitle changes, **252 Go test events passed with 0 failures/skips**,
`go vet` passed, and the runtime Linux media suite passed **66/66** (including
subtests). Existing frontend **43/43** and build setup **12/12** remain passing;
no frontend or build-setup code changed during the subtitle fixes.

**Scope limits:** there were **zero paid model calls**; text and vision models
are not configured. No automatic AI highlight selection, translation, rewriting
or visual-provider analysis was exercised. Whisper produced visibly imperfect
word/name recognition; font/wrapping fixes do not improve transcript accuracy.
The source already has hardcoded captions, so enabling new captions overlaps
them. Users can disable the new subtitle layer and should review text before
publication; automatic hardcoded-subtitle removal is not implemented.

## Environment model setup acceptance

Update: 2026-09-29T18:20:00+08:00 — At the user's explicit request, copied only
the active text model (`qwen-plus`) and independent vision model
(`qwen3-vl-plus`), each with its base URL and API key, from installed AutoClip
settings into this checkout's ignored `.env`. No Cookie, project data or
unrelated provider keys were imported; original settings files were verified
unchanged. Text had no custom base; it was mapped to the original provider's
documented-in-source DashScope compatible base. No actual provider requests were
sent, so account/model access and automatic AI selection remain unverified.

After implementation, **254 Go test events passed, 0 failed/skipped**, with
real local Chinese ASR enabled; vet, frontend typecheck/contract check,
**43/43 frontend tests**, frontend build and **13/13 configuration tests**
passed. Startup tests verify complete validation before writes, atomic rollback,
encrypted persistence, redaction, whole-tuple precedence and worker no-op.

Image rebuild and both healthy services completed in **31.1 seconds**.
Runtime environment was compared in memory against resolved Compose values,
and `/api/v1/settings` returned matching model metadata with no API keys.
The two copied keys were checked against tracked source and build logs in memory
without displaying them; no matches were found. Existing real-video export
`23ad3318f194d5793b32ec08cb995618` remained available after recreation.
Evidence: `artifacts/env-model-rebuild.*.log`,
`artifacts/env-model-verification.json`, `artifacts/env-go-tests.jsonl`,
`artifacts/env-go-vet.log`, `artifacts/env-deployment-tests.log` and
`artifacts/env-frontend-*.log`. Local `.env` is not part of Git or build context.
The application does not automatically discover/import desktop settings.

## CI fixture transfer correction

Update: 2026-09-29T18:39:00+08:00 — GitHub runs `36555977801` (PR) and
`36555936204` (push) both passed the ordinary `test` job, including `-race`.
Their `container` jobs built images and started healthy services but failed at
`docker compose cp worker:/tmp/smoke.mp4`: the archive API could not find the
file in the live tmpfs mount. The initial main run `36544382705` had the same
failure. This was a smoke-harness defect, not model authentication or an
application export failure. Logs are retained in `artifacts/ci-container-*.log`.

The fixture transfer now streams bytes via `docker compose exec -T worker cat`.
Two new shell regressions first failed against the old implementation; one
checks exact binary contents including NUL/invalid UTF-8/CRLF, the other checks
nonzero transfer exit aborts before API smoke/restart. A third test runs the
actual inline restart check against a loopback HTTP server to verify the
isolated API override and non-2xx rejection.

Update: 2026-09-29T18:40:00+08:00 — All **16/16** APT/deployment/smoke
regressions passed. The full `sh scripts/smoke-docker.sh` run passed locally
in **35 seconds**, including real fixture transfer, video/SRT import, draft,
FFmpeg MP4, Range download and post-restart persistence. It ran on a separate
Compose project/port with blank model environment and no production data
volumes; test resources were removed afterward. The normal web/worker stayed
healthy and were not restarted. Evidence: `artifacts/ci-smoke-local-result.json`,
`artifacts/ci-smoke-local.*.log`, `artifacts/ci-smoke-regression-after.log`.
Fresh GitHub job success is not implied until its result is observed.

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
node --test scripts/apt-setup.test.mjs scripts/deployment.test.mjs scripts/smoke-docker.test.mjs
sh scripts/smoke-docker.sh
```

Native test overrides: `FFMPEG_PATH`, `FFPROBE_PATH`, `REQUIRE_MEDIA_TESTS=1`
for worker integration; `MEDIA_FFMPEG`, `MEDIA_FFPROBE`, `MEDIA_WHISPER`,
`MEDIA_WHISPER_MODEL`, `MEDIA_WHISPER_FIXTURE`, `MEDIA_WHISPER_EXPECT` for media.
Optional speech test skips explicitly if its CLI/model/fixture are absent.
`scripts/local-smoke.mjs` can test an already-built native executable without
Docker. All its data/logs live in a new ignored artifact directory and both
child services are stopped afterward.
