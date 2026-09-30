# Verification and release boundary

## Deployment update — 2026-09-30 14:00 +08:00

The user-authorized new version is now running at `127.0.0.1:8080`.
Both services are healthy on image `988e07463770…`; full private data/model
backups and the rollback image were retained before upgrading. Read-only live
verification preserved all 8 projects, 13 drafts, 4 exports, task states and
model/cookie configuration status, and verified the latest served frontend.
See the final deployment entry below and
`artifacts/deploy/20260930-workflow-parity/live-verification.json`.
This supersedes earlier **not deployed** notes, not the still-open requirement
for separately authorized real-model semantic-quality acceptance.

## Current workflow-parity work — not yet release-accepted

Update: 2026-09-30T13:49:00+08:00 — Engineering closeout; this summary
supersedes the intermediate/historical test totals below. Implementation and
three independent review/fix cycles are complete on `feat/workflow-parity`.
Production deployment and authorized live-model quality comparison remain
separate, unexecuted gates. No commit/push or deployment was performed.

| Gate | Executed result / evidence under `artifacts/parity/20260930/` |
| --- | --- |
| Review 1: domain/store/API/worker | Six findings closed with permanent regressions and independent recheck; `review-1.md`. Includes legacy confirmation replay, subtitle evidence, promo routing, content title disable and orphan-preview recovery. |
| Review 2: failure/concurrency/recovery | Original backend/AI findings and four follow-up findings closed by independent reproductions; `review-2.md`, `review-2b.final.log`. Frontend findings are closed by review 3, not inferred from backend tests. |
| Review 3: actual UI/API workflow | Consent/unknown-confirm recovery, reinspection revision refresh, mixed-mode disclosure, incompatible goal validation and incomplete-import gates have permanent React regressions; `review-3.md`. |
| Full Windows Go | Eight packages passed: 585 passing test/subtest events, no failures; one existing optional Whisper test skipped on Windows. `windows-final-go.jsonl`, `windows-final-go.stderr.log`. |
| Full Linux race + real native tools | Eight packages passed: 587 passing test/subtest events, zero failures/skips, race enabled. Real FFmpeg and Whisper executed; `linux-final-race.jsonl`, `linux-final-exit-code.txt` = 0. This also covers the optional ASR gate skipped on Windows. |
| Go vet | Windows and Linux passed; `windows-final-vet.log`, `linux-final-vet.log`. |
| Frontend contract/typecheck/tests/build | After the last result-label separator correction, typecheck including generated-contract check passed, 88/88 tests in 16 files passed, Vite production build passed; `frontend-closeout-*.log`. |
| Build/deployment/browser harness tests | 21/21 Node tests passed, no skipped tests; `scripts-closeout-tests.log`. These do not constitute production deployment. |
| Final real-browser workflow | Current binary/dist passed on 2026-09-30 13:49:11–13:49:36 +08:00, 24.165s: upload → independent confirmation → two automatic 40s MP4s → full-draft playback → mouse seeking/three-range playback → HTTP downloads/Range/full decode. Actual workflow/goal labels use middle dots. `browser/run-J2cxw9/summary.json`, screenshots and playback/download evidence. Four local mock model calls, zero before confirmation. |
| User's actual source, no paid model | Read-only local copy of project `aa5a2629b91f61ac2d8220d4fb0630ee`: a deliberately selected source interval 30–75s rendered to 45s MP4 with audio, full decode passed, source SHA-256 unchanged. Existing hardcoded captions remained, no new subtitle/title layer; source/output frame inspection and SSIM 0.992113 support the comparison. `user-source-acceptance.log`, `real-source-1459480882/`. This is rendering fidelity, NOT model-selected semantic quality. |

Update: 2026-09-30T13:51:00+08:00 — The final browser run above supersedes
older screenshots/runs and certifies the final separator correction. Executable
SHA-256: `d66c755d0a06e1a9f07d6e721fa13b028ca4dc4fb928ca1a597637621d4e7c46`;
frontend bundle: `index-Deb8oguS.js`, SHA-256
`40ce1af1878619e9658981f1d24efa22cb1695096022dfa57a547071eab93763`.
Both hashes remained stable throughout the run. The main agent additionally
inspected the final project/editor screenshots and actual-source/output frames.
No functional defect was found in this tested path; the existing missing favicon
is a non-blocking resource issue, not evidence of complete visual/UI parity.
Browser download-manager interaction, all-browser support and human-rated
semantic quality are not covered. The harness uses normal export HTTP download
routes and independently decodes those files.

Update: 2026-09-30T13:52:00+08:00 — Cleanup and handoff confirmed:
`final-process-check.json` reports no owned browser, verification server/worker
or verification build process remaining. `linux-final-state.json` records the
isolated test container exited 0. `deployment-unchanged.txt` records both existing
services still healthy on their original `78e8cd651747…` image; neither was
replaced. Source/test/contract hashes are saved in `final-source-hashes.json`;
`git diff --check` passed. Review/debug evidence is retained under artifacts,
not mixed into application packages. No required work is silently classified as
passed: only paid/live quality acceptance and an approved deployment remain
outside this engineering closeout.

### Acceptance boundary and remaining work

- Upload never authorizes ASR, production, image transmission or a paid model
  request by itself. Content MP4 output starts after confirmation; highlight/
  promo default to drafts unless automatic export was selected.
- Reading/transcribing text does not enable subtitle burning. New drafts
  default to no added subtitle layer; content also explicitly disables its
  opening title. Existing draft flags and completed exports remain unchanged.
- Real browser checks cover source upload/SRT, independent confirmation,
  automatic 40-second content MP4s, full-draft and multi-range preview, seeking,
  HTTP downloads, Range and full decode. Model responses are local mocks.
- Live-provider semantic completeness, highlight relevance, promotional-copy
  quality and side-by-side upstream quality have **not** been accepted. They
  need separate permission for paid requests and a human-reviewed sample.
  Live external-platform import is also not certified by offline substitutes.
- No production image rebuild/deployment or existing-volume migration was
  executed. The running 8080 service is still the old build. Back up its full
  data directory before any approved deployment; schema-2 data is not
  compatible with older binaries without restoring a matching backup.
- Ambiguous pre-snapshot workflow-candidate ownership is explicitly rejected
  while preserving old evidence. This safety boundary must not be described as
  universal automatic migration of every historical partial workflow.

Update: 2026-09-30T13:00:00+08:00 — Active work is on
`feat/workflow-parity`; pre-existing dirty changes were preserved, with working
and staged patch/status snapshots in `artifacts/parity/20260930/`.
This section supersedes historical summaries below for the new working tree.

Required gates: independent logic/contract review; independent failure/concurrency
review; independent end-to-end review; complete Go/frontend/contract/build checks;
Linux race; native media tests without required-fixture skips; real browser
upload-confirm-produce-preview-download; and authorized real-model quality
comparison against upstream. Failed or unexecuted gates are not a release pass.

Initial backend integration passed domain/store/HTTP/worker packages. A new
real FFmpeg test imports an 80-second controlled source and eight SRT cues,
confirms content+highlight through the HTTP API, shares four loopback model
responses, creates two 40-second content MP4s automatically and two editable
highlight drafts, checks full decode/Range/audio/duration/source immutability.
No draft has added subtitles enabled, and neither clip is forced below 30s.
A second test proves an import succeeds with an unavailable Whisper binary,
then explicit ASR failure does not prevent a subtitle-disabled export even with
the subtitle asset removed. Both tests passed with required native tools.
Evidence: `backend-integration-first.log`, `worker-real-acceptance.log`.

These are controlled-fixture and mock-model checks, NOT proof of semantic quality
on the user's real video. Further review/browser/Linux/current-tree regression
results will be appended after they actually run.

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

Update: 2026-09-29T20:05:00+08:00 — Fixed the user-reported extraction failure
and removed translation, AI rewrite and collection drafts.

Passed locally: `go test -mod=readonly ./...` 8/8 packages; `go vet ./...`
clean; frontend 40/40 tests, typecheck, generated-contract check and production
build; Linux image rebuilt (its build stage runs the full Go and frontend
suites) and both Compose services returned to healthy.

Negative controls: both new regressions were confirmed to fail without their
fix. Reverting the AI tolerance reproduced the exact production error,
`Visual event cites a frame that was not supplied to this request.`; reverting
the sampler reported `sample time 12.493333333333332 is not exact at three
decimals`.

Live provider runs against the user's 299.84-second upload
(`aa5a2629b91f61ac2d8220d4fb0630ee`, qwen3-vl-plus): the frame-matching error no
longer occurs, which closes the reported defect. One later run failed a
different bounds check and one hit a provider timeout (`ai timeout`), each a
single billed request with no automatic retry. The first of those motivated
splitting `validateVisual`'s combined message into per-field diagnostics.

**Not verified**: a completed visual analysis producing accepted highlight
drafts for this source. The reported frame-matching defect is fixed and proven,
but end-to-end highlight quality on this video is not yet demonstrated, and
`no_highlights` remains a legitimate outcome for speech-led educational footage
— subtitle mode is the better path for it. `artifacts/` (1.2 GB of historical
build and install logs, untracked and gitignored) was deleted at the user's
request; the evidence files referenced by earlier entries above are therefore no
longer on disk.

Update: 2026-09-30T11:35:00+08:00 — First phase of the upstream-parity work
(the `profileFor` collapse and Bilibili URL import).

**Bilibili link import now works end to end.** The user's exact URL, tracking
query included
(`https://www.bilibili.com/video/BV1P5h16JE8n/?spm_id_from=...&vd_source=...`),
was posted to the normal project-creation API and reached `source_ready`:
299.84 seconds, 1920x1080, subtitles produced by local ASR. This is the first
successful URL import of this video; every previous attempt failed at format
selection. The tiered selector chose video from the regular CDN and audio from
MCDN, which is exactly the combination the old hard exclusion made
unsatisfiable.

Passed locally: `go build ./...`, `go vet ./...`, `gofmt -l internal/ cmd/`
empty, `go test -mod=readonly -count=1 ./...` 8/8 packages; frontend 40/40
tests, typecheck, generated-contract check. Linux image rebuilt and both
Compose services healthy.

Negative controls, each confirmed by reverting the fix and observing the new
test fail:
- `p.Min = math.Min(p.Min, p.Max)` → `window collapsed to [30,30]` plus
  thirteen further duration/target combinations.
- MCDN hard exclusion restored → `every group excludes MCDN, so MCDN-only audio
  cannot resolve`.
- `b23.tv` case disabled → `a well-formed share link must validate`.

**One change was attempted, shipped, and reverted after a live failure.** The
`--match-filters` optional-field form `duration?<=N` is documented but rejected
by the pinned yt-dlp 2026.08.19 with `Invalid filter part`. It broke the first
real import attempt. Five syntax variants were probed against the live
extractor; all failed except the original form, which was restored. The
underlying gap (a missing-duration extractor is silently filtered out) remains
open and is recorded in media.md rather than claimed as fixed.

**Not verified.** No real-provider call was made for the `profileFor` fix, so a
live subtitle analysis of a source longer than eight minutes still has not run —
the end-to-end claim for that fix rests on a unit-level timeline test. The
`b23.tv` redirect resolution is covered only by loopback `httptest` servers; no
real b23.tv link has been resolved. Cookie-authenticated import, official
Bilibili subtitles and the 1080P high-bitrate stream remain unavailable without
cookies, which is a Bilibili account limitation rather than a code defect.
P2 (validation tolerance) and P3 (clip quality, category prompts) from the
approved plan are not started.

Update: 2026-09-30T13:10:00+08:00 — Second phase complete (P2 validation
tolerance, P3 clip quality and category prompts), with the live paid runs the
user explicitly authorized.

**The `profileFor` collapse is now proven on real hardware, not only in unit
tests.** A 899.52-second (15-minute) source was built by concatenating the
reported video three times with the image's own ffmpeg, imported through the
normal upload API (290 ASR cues), and analyzed at the default 30-second target —
the exact combination that collapsed the window to [30,30] and failed 100% of the
time before the fix. It completed and produced six drafts, every one inside the
widened 15-30 second window (18.7s to 29.3s). Arithmetic for this case:
before Min=30.0/Max=30.0 (degenerate), after Min=15.0/Max=30.0.

**Bilibili URL import works end to end.** The user's exact URL with its tracking
query reached `source_ready` at 299.84 seconds, 1920x1080, with ASR subtitles,
then produced five highlight drafts. The tiered selector took video from the
regular CDN and audio from MCDN — the combination the old hard exclusion made
unsatisfiable.

**Category prompts and the scoring fallback are confirmed against a real
provider.** A `knowledge`-category analysis of the 299.84-second import produced
six drafts whose titles track the mathematical content specifically (for example
"Apéry's 1979 Breakthrough on ζ(3) — Why ζ(5) and Higher Odd Zetas Remain
Unresolved"). One candidate in that run carries the 0.5 neutral score, so the
`alignScores` back-fill is verified on a live model response and not only in
mocks. The long-source run also exercised the multi-chunk outline path.

Passed locally: `go build ./...`, `go vet ./...`, `gofmt -l internal/ cmd/` empty,
`go test -mod=readonly -count=1 ./...` 8/8; frontend 40/40, typecheck,
generated-contract check, production build. Image rebuilt and both services
healthy.

Ten negative controls were confirmed across both phases by reverting each fix and
observing the specific new test fail — `profileFor`, the MCDN exclusion, the
`b23.tv` case, the visual `kind` default, the twelve-event truncation, the title
fallback, the client single-choice requirement, the scoring alignment, the context
buffer, and the guidance priority assertion.

Three defects were found during this work that were not in the plan: a
byte-versus-character length limit that would reject a valid 100-character
Chinese label; a bug this change itself introduced, where skipping an empty
outline chunk left the timeline stage paying for a chunk with zero topics; and
`validateVisual` taking its result by value, so truncation and score rescaling
never reached the caller. All three are fixed and covered.

**Not verified.** The `b23.tv` redirect resolution is covered only by loopback
httptest servers; no real b23.tv link has been resolved end to end. The seven
category prompts are English ports written for this project rather than
translations of upstream's Chinese text, and only `knowledge` has been exercised
against a live model — the other six are structurally verified but their output
quality is unmeasured. Whether refining one candidate instead of six degrades
boundary accuracy on real footage remains unmeasured. Cookie-authenticated
Bilibili import, official subtitles and the 1080P high-bitrate stream are still
unavailable without cookies, which is an account limitation rather than a code
defect. The `--match-filters` missing-duration gap is still open, as recorded in
media.md. Items deliberately left undone are listed as P4 in the approved plan:
text-pipeline retries, denser frame sampling, the promo three-hook variant,
upstream's screening/recommendation tier, per-goal partial success and download
resume.

Update: 2026-09-30T13:19:47+08:00 — Review-1 permanent regressions (tests-only owner).

Added 11 focused regressions and strengthened the existing real production-render
integration test in `internal/httpapi/production_test.go`,
`internal/store/production_test.go`, `internal/media/preview_integration_test.go`,
`internal/media/title_test.go` and `internal/worker/production_integration_test.go`.
All six review-1 findings now have passing permanent coverage: accepted old
confirmation replay; validated legacy cue evidence including direct GET-plan to
POST-confirm without PUT; analysis retry isolated from previous export failure;
explicitly transparent content title with real rendering and legacy nil-flag
compatibility; legacy analyze linked to an executed promo workflow; and recovery
of a genuinely valid orphan H264/AAC preview without re-encoding.

The direct legacy confirmation regression initially exposed a remaining mismatch
between Plan and ConfirmProduction evidence baselines. Main fixed business code;
the assertion was retained and now passes. Adapted the synthetic legacy fixture
to complete import with Claim+Finish and refresh Project before equality checks,
as required by the new incomplete-import gate. Real import tests use runTask and
are compatible with CompleteImport atomic completion and per-Claim lease fencing.

Verification (offline, real installed FFmpeg/ffprobe, Go test timeout 120s):
- Targeted four-package run: 12 top-level tests plus 22 subtests passed, zero
  failures or skips; httpapi/store/media/worker 0.578/0.578/6.445/8.904s.
- Full httpapi/store/media/worker run: 91 top-level tests plus 66 subtests passed,
  zero failures; package times 2.706/1.515/33.771/17.932s. The pre-existing optional
  real Whisper test skipped because executable/model/speech fixture were not
  configured; this run does not claim real ASR validation. No new test skipped.
- Four-package `go vet -mod=readonly` exit 0; owned tests gofmt clean and scoped
  `git diff --check` passed. Cache Access Denied was rerun with escalation.
- Content render retained source pixels at 0.5/2.5/3.5/4.5s under the documented
  compression tolerance; title-enabled positive control changed 3263 pixels.
  Valid orphan recovery retained source/output hashes with FFmpeg unavailable
  and returned ID-based HTTP Range 206. Six playable incompatible previews were
  rejected; legacy promo used four text stages plus one mocked promo call.

Closure details and exact commands: `artifacts/parity/20260930/review-1.md`.
Logs in the same directory: `review-1-permanent-targeted.log`,
`review-1-permanent-full.log`, `review-1-permanent-vet.log` (empty successful vet).
No business code, other owners' tests, production data or original installation
was changed; no paid calls or credential reads. Historical documentation was
preserved and this result appended. Review-2 is not claimed closed by this run.


Update: 2026-09-30T13:27:55+08:00 — Final review-1 handoff.

Read-only re-check of the integrated six fix paths confirms the content title flag/transparent renderer, legacy analyze workflow/promo wiring, verified orphan recovery, attempt-local analysis status, accepted-revision replay, and shared evidence-based Plan/ConfirmProduction baseline remain present. Review-1 R1–R6: closed within the documented regression scope. The passing Windows targeted/full/vet results above are from 13:17–13:18; no claim is made that they cover later concurrent edits. Main's current-tree Linux race run and review-2 regression acceptance remain separate and pending its report. This is not overall project acceptance; no additional tests or business files were edited in this final read-only re-check.

Update: 2026-09-30T13:39:34.4642613+08:00 — First isolated Linux race pass completed seven packages, but media exceeded its 180-second package deadline. Diagnosis: race-instrumented fake native subprocesses each incur Go race's default 1-second exit sleep; no race report was emitted, and real Whisper transcribed the local speech fixture correctly. The retry keeps race detection enabled and sets only GORACE=atexit_sleep_ms=0; it reuses the stopped test container's compilation cache, with a 240-second package deadline/600-second command timeout and separate final logs. This timed-out first run is not counted as a pass.

Update: 2026-09-30T13:59:28.3058828+08:00 — User explicitly requested starting the new version. Built the repository Dockerfile/Compose web image successfully in 52.2s (BuildKit Completed n8v88qvo9h32hjfffongtvbqr); build Go tests and 88 frontend tests passed. Both prior services had zero queued/running tasks and were cleanly stopped. Full data/model tar backups were made with numeric ownership/modes, listed successfully, checked for DB/master.key/model files and SHA-256 hashed. Backups are private local archives (restricted Windows ACL; not encrypted) under artifacts/deploy/20260930-workflow-parity/backup. Previous image retained as autoclip-go:rollback-20260930-parity; rollback guidance is in that evidence directory's ROLLBACK.md.

Started both services through docker compose up -d --no-build --wait --wait-timeout 180. They are healthy on image sha256:988e07463770f2f6a0d017013418e2bf2516248461443f1d4211eb23071bde3f, still bound to 127.0.0.1:8080. Read-only live verification confirms all 8 projects, 13 drafts and 4 completed exports remain unchanged, task IDs/statuses unchanged with no unexpected production, model/cookie configuration status unchanged, ready legacy plans accessible, all historical exports respond Range 206, and the served frontend is index-Deb8oguS.js with SHA-256 40ce1af1878619e9658981f1d24efa22cb1695096022dfa57a547071eab93763. Evidence: build-verification.json, backup/sha256.json, start.log, deployed-services.txt and live-verification.json in the deployment folder. No model test or paid production request was made; live-model semantic quality acceptance remains open. No source commit or push.

Update: 2026-09-30T14:12:21.7092649+08:00 — User requested GitHub publication of the deployed workflow-parity changes. Preparing one reviewed feature-branch commit on feat/workflow-parity, based on origin/main 2dd456f (fast-forward through the prior build-speed PR merge; no source conflict). The staged-path and configured-secret scan found no credentials/private/generated artifacts; .env, data, backups, models, logs and build output remain excluded. Publication carries the tested implementation, permanent regressions and documentation, not local acceptance media. Existing Windows/Linux race/frontend/browser/deployment results above apply; live-model semantic quality remains unaccepted. Push/PR receipt will be recorded separately after GitHub confirms it.

Update: 2026-09-30T14:35:00+08:00 — GoClip frontend workbench final acceptance.

- Final web source: typecheck/generated-contract check passed; 98/98 tests across
  18 files passed in 37.31s; build passed in 2.05s. Includes permanent library,
  import-dialog, keyboard, hidden-file reset, progressive-disclosure, template-save
  and light/dark contrast regressions, plus all retained production/recovery tests.
- Browser harness tests: 5/5 passed. Current-source full isolated browser workflow
  passed in 24.266s: video/SRT upload -> review -> explicit confirmation -> two real
  automatic 40-second MP4s -> HTTP download/Range/full decode -> editor playback,
  slider dragging and continuous three-range playback. Four deterministic local
  model responses, none before confirmation; no paid provider or production data.
  Evidence: artifacts/parity/20260930/browser/run-n5EsHj/summary.json and
  artifacts/frontend-redesign/full-workflow.log. All owned test processes stopped.
- Read-only real-data visual acceptance: 14 desktop/mobile/dark snapshots with
  loaded video/thumbnails, no horizontal overflow, Escape focus restored, no
  mutation or API/runtime error. Evidence: frontend-redesign/browser-QSxRH9.
  This pass preceded only the decorative plus-text -> SVG icon correction; the
  final isolated workflow verified the resulting index-BXDI7hbi.js build.
- Earlier rejected runs are retained, not counted as passes: initial JSDOM tests
  incorrectly treated closed-details children as absent rather than invisible;
  a template-save test picked the already-selected default; the contrast test
  used a browser URL with Node fs; first full browser attempt could not match the
  text-plus-icon import button. Assertions/fixtures or decoration were corrected,
  with no consent or persistence requirement weakened.
- git diff --check passed. No Go/backend logic or deployment image changed; no
  source commit/push. Full historical backend/race results were not rerun or
  represented as new verification in this frontend-only change.
- Independent frontend preview started at http://127.0.0.1:4173, PID 21012;
  frontend and proxied projects endpoint both returned HTTP 200. Existing 8080
  web/worker were not restarted/replaced. Preview uses the existing backend/data:
  deliberate user operations there are real, not a sandbox. No operation was
  automatically submitted. See frontend-workbench.md and operations.md.

Update: 2026-09-30T14:40:00+08:00 — User requested commit/push of the frontend workbench. Publishing only web source, permanent tests, browser harness and affected documentation on feat/workflow-parity. GitHub PR #2 is open for this branch and will receive the commit; no direct protected-branch push or merge. The verified 98-test/typecheck/build and isolated browser evidence above apply. Local .env, data, screenshots, logs, dependency/build output and preview-process files remain ignored. Publication receipt is reported after remote verification.
