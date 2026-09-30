# Operations

Updated: 2026-09-29

## Boundaries
Single host, one API and one worker, local named volumes. No NFS, replicas,
multi-tenancy, login, automatic source cleanup, GPU or legacy data migration.
Network restriction is the access control: LAN/VPN only.
Do not disable same-origin checks or publish cloud keys in Compose/environment logs.

## Health/progress
`/api/v1/health` checks SQLite even with no model credentials.
Worker starts after API health and maintains its own heartbeat healthcheck.
Task heartbeats expire after 90 seconds; recovery marks them interrupted.
Cancellation of a running job sets `cancel_requested` until the process tree
has exited; only then is the task terminal, avoiding duplicate work on retry.
Global concurrency is one heavy task. Model tests/title previews are light API requests.
Default task timeout is six hours. Native tools and LLM requests have narrower deadlines.

## Logs/failure
`docker compose logs --tail=100 worker web`.
Errors include request_id / stage. Providers' raw response bodies and API keys
must not be logged. Settings errors distinguish endpoint/auth/model/rate limit/
timeout/invalid JSON. A failed whisper/FFmpeg child does not exit the web process.
If the worker is stopped, do not keep retrying: inspect its logs and container health.
After three unchanged observations inspect the native process/CPU/disk before retrying.
Cold-build duration is network-dependent, not a 10–25-minute guarantee. An actual
snapshot-source build reached 41 minutes while still downloading APT packages.
Retain plain build output, set an explicit supervision timeout (45 minutes is the
CI budget), and diagnose a stalled step instead of blindly repeating the build.

## Persistence, backup, restore
`docker compose down` preserves named volumes; **`down -v` destroys them**.
`data` contains SQLite WAL, master.key, cookies/model ciphertext, originals,
analysis checkpoints, edits and exports. `models` contains the base model.
Losing master.key makes encrypted settings unreadable; restore the matching key.

Consistent offline backup:
1. Stop both services: `docker compose stop`.
2. Back up the entire `autoclip-go_data` and `autoclip-go_models` volumes with a
   trusted host/volume backup tool, preserving numeric ownership (10001), mode
   and file names. Capture SQLite plus WAL/SHM, not just a live database file.
3. Keep backup encrypted/private. It includes originals and the master key.
4. Restart: `docker compose start`. Check task states and both healthchecks.

Restore to a fresh stopped deployment, replacing the matching volumes together.
Do not overlay a different master.key. Confirm ownership before starting.
Record the deployed image tag/digest and VERSION with each backup.
For upgrades, backup first, retain the previous image, and run tests before rebuilding.
Database schema version is 2; downgrades after incompatible migrations
require restoring the matching full-volume backup and old image.

Update: 2026-09-30T13:00:00+08:00 — Workflow-parity upgrades add schema-2
workflow records and leave existing project/secret/export data intact. Back up
the full data volume before deploying; an older schema-1 application will reject
the upgraded database, so rollback uses the matching backup, not forced schema
number changes. The current implementation branch and test binaries are not
automatically deployed into the user's running services.

Ingestion is now a local metadata/subtitle check; transcription begins only
after production confirmation, or a subtitle-enabled export requires it.
Content goals automatically queue exports. A partial workflow may contain
downloadable files as well as failed goals; inspect per-goal/task errors and
explicitly retry failed tasks instead of repeating the entire paid workflow.
Added subtitles are off by default; pre-existing draft choices are not reset.

Validation uses isolated databases/processes, blank production model environment
and loopback mock providers. Real-provider A/B and user-video subjective checks
require separate authorization/recorded outcomes. Test logs/screenshots belong
under `artifacts/parity/<run-id>/`; never copy model keys or cookies into evidence.

## Tool upkeep
Native downloads have fixed SHA256 checks. By default Debian packages come from
the current signed Bookworm repositories over the official HTTPS CDN. Opt-in
`APT_SOURCE_MODE=snapshot` uses the signed fixed historical archive instead.
Mirror mode does not pin APT package versions; the Docker image inventory records
the exact installed versions for either mode.
Base images are pinned to official multi-platform manifest digests; Compose
selects linux/amd64. Digests were verified from docker-library/repo-info.
Update tools.lock.json, Dockerfile and checksums together; rerun native media,
real ASR and platform import tests. Do not silently download a different model.
yt-dlp platform support can break independently of application releases.

Update: 2026-09-29T15:08:00+08:00 — Base digests locked, official Compose parsing
and deployment regression tests passed; live native API/worker restart smoke
passed. Complete Linux container acceptance remains open (see verification.md).

## Windows installation checkpoint

Update: 2026-09-29T15:37:00+08:00 — On this Windows host, installed Microsoft
WSL 2.7.14.0 and Docker Desktop 4.93.0 (per-user, WSL2/Linux backend).
Both installers passed pinned SHA256 and valid vendor Authenticode checks;
both installers exited 0. VirtualMachinePlatform and
Microsoft-Windows-Subsystem-Linux now report Enabled. DISM explicitly reported
reboot required and suppressed restart; Windows still has RebootPending and
HypervisorPresent is false. **No reboot or Windows upgrade was performed.**

Docker CLI 29.8.1 and Compose 5.5.1 work. `docker compose config --quiet`
and all three deployment regression tests passed using the newly installed CLI.
`docker info` failed because the engine pipe does not exist yet; this is not a
running deployment. First-run subscription agreement was not accepted by
automation. No Docker account login, paid subscription or trial was started.

Next: save work and manually restart Windows; open Docker Desktop and review
the first-run agreement, wait for its engine to be ready, then open a new
PowerShell in `D:\com\autoclip-go`. Verify `docker info` returns server details
before `docker compose up -d --build`. Initial build budget: 10–25 minutes,
45-minute timeout; retain logs and investigate stalled stages rather than
repeating the build blindly. Container health and live smoke acceptance remain
required after the build.

Installation evidence is in ignored `artifacts/docker-install/`: installer
manifests, SHA256/signature receipts, DISM/MSI/vendor logs, CLI/config/test
results and the failed daemon check. Docker's default CDN route reset TLS
connections; the official installer downloaded successfully using a reachable
CloudFront edge with per-command `curl --resolve`, HTTPS certificate validation
and the independently obtained package hash. No global DNS/proxy or security
settings were relaxed. The original AutoClip checkout/data were not modified.

Compatibility caveat: this host is Windows 11 Home Chinese 23H2
(22631.3447). Microsoft's Home/Pro lifecycle page lists 23H2 end of support
as November 11, 2025 (Pacific Time); Docker documents support only for serviced
Windows releases. Installer success is not a support/compatibility guarantee.
Official source snapshots: `windows-home-lifecycle.html` and
`windows-install.md` in the installation evidence directory. Do not upgrade
Windows automatically; evaluate that separately with the user.

## Build-speed configuration and cache ownership

Update: 2026-09-29T17:00:00+08:00 — Changed the default APT source from the
historical archive to Debian's official HTTPS CDN. `APT_SOURCE_MODE=mirror` is
the default; `snapshot` explicitly retains the `DEBIAN_SNAPSHOT` timestamp from
`tools.lock.json`. `DEBIAN_MIRROR` and `DEBIAN_SECURITY_MIRROR` can name trusted
HTTP(S) mirrors; credentials, query strings, whitespace and unsupported schemes
are rejected before any APT file is changed. There is no automatic fallback.

`scripts/apt-setup.sh [absolute-apt-config-directory]` only configures APT. Its
optional directory supports hermetic tests; Docker uses `/etc/apt`. An HTTPS CA
bundle is copied from the already-pinned Go base image before the first APT call.
TLS, signed index and package verification remain on. Only explicit snapshot
mode disables expired archive metadata's `Valid-Until` check. APT update errors
are fatal, with two retries and 30-second HTTP/HTTPS inactivity timeouts for both
update and package installation. These are not end-to-end build deadlines.

Native and runtime package installations have distinct, locked BuildKit archive
caches, so their independent stages do not block on the same cache lock. The
base image's `docker-clean` hook is removed, keeping complete/partial downloads
in the builder cache across failed steps where BuildKit retains it. Cache files
are not copied into the runtime image. Package lists are still removed from
image layers. npm, Go module and Go compiler caches also persist in the builder.
BuildKit GC or a new builder can still discard caches; this is not a backup.

Compose gives `build` only to `web`; `worker` uses the identical image with
`pull_policy: never`, after web is healthy. Use `docker compose build web` for
an explicit build or the normal `docker compose up -d --build`. With a built
image and unchanged source, `docker compose up -d --no-build` avoids compilation.
Changing source settings does not reconfigure a build already in progress:
the operator must explicitly stop that build and start a new one. Do not run
two full builds simultaneously. The old uncached APT step cannot be recovered
retroactively by these new cache mounts.

The optimization keeps the fixed native-tool/model hashes and all existing
Go/frontend build-time tests. It does not change the application API, persistent
data, credentials, paid-call consent or native feature set. Seven offline shell
regressions and five Compose/build-contract regressions passed on this host;
full optimized Linux image build/runtime and actual speedup remain unverified.

Update: 2026-09-29T17:05:00+08:00 — The old build is no longer running: Buildx
history recorded 48m33s and its APT stages ended as `CANCELED`, not a completed
image. The first optimized attempt failed before APT at `auth.docker.io` with a
connection reset. A bounded HTTPS probe reproduced that reset while Debian's
official CDN returned HTTP 200 in 2.35 seconds. The Dockerfile now uses Docker
Engine 23+'s bundled BuildKit frontend instead of requiring a separate external
Dockerfile frontend image. This removes that extra registry request, not Docker
Hub's authentication requirements for uncached base images. No TLS/authentication
checks or system proxy settings were disabled. Logs: `artifacts/old-docker-build.log`
and `artifacts/optimized-docker-build.*.log`.

Update: 2026-09-29T17:38:00+08:00 — The optimized image build completed in
**13m00s** (Buildx history `r7umct9yltzo32loi9ry704au`), and web/worker both became
healthy. This reused already-pulled base images but newly executed the APT steps:
native dependencies **278.4s**, runtime dependencies **722.7s**, Whisper source
download/build **130.8s**, model download/check **33.4s**. Fixed hash verification
passed. A subsequent backend-only Bilibili fix reused those dependency/model
layers; rebuild plus both healthy services took **34.6s**.
This is an observed improvement over the prior cancelled 48m33s snapshot build,
not a universal speed ratio or a fully cold-builder benchmark. Evidence:
`artifacts/optimized-docker-build-2.*.log`, `artifacts/optimized-docker-rebuild.*.log`.
Real-source acceptance exposed an unreachable Bilibili MCDN audio endpoint and
is tracked in media.md / verification.md rather than being reported as success.

Update: 2026-09-29T17:59:00+08:00 — Real-source manual-clip acceptance now passes
after the documented Bilibili endpoint, CJK font matching and long-line fixes.
Final backend rebuild plus healthy startup took **31.7 seconds** with existing
caches (`artifacts/subtitle-wrap-rebuild.*.log`). Both services are running on
image `sha256:1121dfeb51fd0b1590cd8239022982da1aba93821417f3fc4e2edb64177794de`
at `127.0.0.1:8080`. Persistent data/model volumes were not removed; the real
project survived recreation and its final export is downloadable. An isolated,
no-network runtime-image media test container passed and was automatically
removed. Exact tasks/artifact paths and remaining paid-AI/ASR-quality limits are
in verification.md. These changes remain uncommitted on `feat/docker-build-speed`;
no new commit was pushed and no remote deployment was performed.

## Environment-based model configuration

Update: 2026-09-29T18:18:00+08:00 — README now includes Windows/Bash first
startup, later starts/upgrades, health troubleshooting, manual vs AI workflow,
configuration tables and backup boundaries. `.env.example` includes six empty
model placeholders: `AUTOCLIP_{TEXT,VISION}_{BASE_URL,MODEL,API_KEY}`.
The first upgrade requires a rebuilt image. Subsequent `.env` edits require
`docker compose up -d --no-build --wait --wait-timeout 180`, not just `restart`.
Wait for active jobs to finish before recreating services. Compose shell
environment overrides `.env`; only explicitly mapped variables are passed.
Native Go processes accept the environment variables but do not parse `.env`.

On web startup, each nonempty group completely replaces the saved model tuple
after all groups validate. A blank key in a complete environment tuple means
no authentication. An entirely blank group preserves saved settings; removing
environment variables does not erase encrypted database credentials. UI edits
are temporary relative to a nonempty env tuple and are overwritten at the next
web startup. Worker and healthchecks do not run the bootstrap or provider tests.

Use single-quoted `.env` values to preserve literal `$` and `#`; escape a literal
single quote as `\'`. Local `.env` remains ignored by Git and excluded from
Docker build context. Keys are runtime environment, never Docker build args,
but Docker administrators can inspect them. Do not share full Compose config,
container inspection output, environment dumps or `.env`. Database encryption
does not encrypt the host `.env` file; secure file permissions and backups.
No automatic paid model test is performed when copying/saving/loading settings.

Update: 2026-09-29T18:20:00+08:00 — The user-authorized text/vision configuration
copy has been applied to local `.env`; both services were recreated on the
rebuilt image and became healthy in 31.1 seconds. Runtime metadata/key-redaction,
environment matching and preservation of the existing real-video export passed.
No cloud model request was made. Copying two credential tuples was an explicit
one-off local action, not an automatic migration feature. README startup,
configuration and architecture sections now describe env-based setup.

Update: 2026-09-29T18:39:00+08:00 — Fixed the CI fixture transfer in
`scripts/smoke-docker.sh`. Runtime `/tmp` is tmpfs; Docker's archive-based
`compose cp worker:/tmp/smoke.mp4` failed despite successful generation and
healthy containers. The script now executes `cat` inside the live worker with
`exec -T` and redirects its binary stdout using POSIX sh. Transfer errors abort
before API testing/restart. No root-filesystem, tmpfs or container-security
settings were relaxed. Two real-shell/fake-command regressions exercise byte
preservation and failure propagation; a third executes the actual restart-check
program against a loopback server to test URL override and HTTP failure.
The restart check now uses the same
`AUTOCLIP_URL` override as the import/export harness, checks HTTP status, and
has a 30-second request deadline, allowing an isolated local Compose project.

Update: 2026-09-29T18:40:00+08:00 — The complete smoke script passed locally
in 35 seconds using the separate `autoclip-ci-smoke-20260929` project on
loopback port 18081, with model environment values cleared. It generated and
streamed the fixture, imported video/SRT, rendered/downloaded a real MP4, then
restarted services and verified persistence. Only this test project's containers
and volumes were removed afterward; the normal app/services/data were untouched.
All 16 setup/deployment/smoke regressions pass. Logs:
`artifacts/ci-smoke-local.*.log`, `artifacts/ci-smoke-local-result.json`,
`artifacts/ci-smoke-regression-after.log`.

Update: 2026-09-29T18:55:00+08:00 — Rebuilt and recreated the local web/worker
services with the vision connection-test fixture fix (8×8 to 64×64). Both
services are healthy; no active tasks existed before recreation, and persistent
data/model volumes were retained. The actual saved vision test returned HTTP
200 / `ok:true` for qwen3-vl-plus. No Base URL, key, cookie, or environment change
was required. Diagnostic evidence: `artifacts/vision-probe-8.json`,
`artifacts/vision-fixed-live-test.json`, and `artifacts/vision-fix-build.log`.
These ignored artifacts contain no API keys. A successful synthetic connection
test is not verification of full-video visual analysis or highlight quality.

Update: 2026-09-29T19:17:00+08:00 — Deployed general-video visual prompts,
separate `no_highlights` diagnostics and visual-only mode guidance. Full Go
tests inside the Docker build and all 44 frontend tests passed; local AI/worker
tests, vet and frontend typecheck also passed. Web and worker were recreated
only after confirming zero active tasks and are healthy. All five projects'
task IDs/statuses, draft counts and completed export counts remained unchanged;
the served frontend bundle contains the new guidance. Historical failures were
not rewritten, and no paid analysis/test request was made during this fix.
Evidence: `artifacts/visual-guidance-before.log`, `visual-guidance-after.log`,
`visual-guidance-build.log` and `visual-guidance-before-restart.json` (under
`artifacts/`). Real-provider visual-highlight quality remains unverified.

Update: 2026-09-30T13:21:42.6005111+08:00 — Workflow-parity verification remains isolated from the running 8080 deployment. Worker startup holds data/worker.lock with an OS advisory/exclusive file lock for checkpoint ownership; a paused process must exit before a standby worker can execute, and standby acquisition has a 90-second timeout. Lease fencing separately rejects stale DB writes. Only local filesystems supported; do not place SQLite/checkpoints on a network filesystem. Schema 2 remains a one-way additive update for old binaries: back up the entire data directory before deployment, and restore a matching backup to downgrade. No deployment or paid-model request was made by these review fixes.

Update: 2026-09-30T13:40:00+08:00 — R2-12 health ownership fix: the worker
command now supplies its heartbeat-file writer through `Worker.HealthBeat`.
There is no independent command-level heartbeat goroutine. The worker invokes
the callback only after acquiring the OS execution lock, on idle loop iterations,
and from its one-second task monitor during execution. Standby processes cannot
refresh the owner's shared `worker.heartbeat` and mask a stalled owner.
The existing healthcheck still rejects a timestamp older than 30 seconds;
this is data-directory owner health, not proof that every standby is healthy.
Heartbeat write errors propagate through `Run`/`run()`; during a task the worker
cancels execution and reports the failure rather than silently continuing.
The OS lock is not expired or stolen to recover health.

Permanent `cmd/autoclip/health_ownership_test.go` uses the real `run()` in child
test processes and isolated temporary directories. It covers standby exclusion,
initial publication and subsequent refresh by the owner, and initial/later
write-error propagation. A directory at the heartbeat filename injects portable
write failure. The standby test holds a real OS lock with a stale timestamp;
it does not suspend an existing process. The new regression failed before the
command change and the complete cmd package passes afterward. The original
review overlay and main's active-task health-failure regression also pass.
Logs: `artifacts/parity/20260930/r2-12-{before,after,integration,vet}.log`.
All tests are offline; no running deployment or existing database was touched.
Windows execution is verified here; the Linux race run started at 13:27
predates this command change and requires a final-source cmd/worker rerun.

Update: 2026-09-30T13:49:08.7123891+08:00 — Final-source Linux verification (13:39–13:42) supersedes the first timed-out run and includes the owner-only health callback and its regressions: all eight packages passed with race enabled, including real Whisper and native media tests, and vet passed. The container autoclip-parity-linux-20260930 exited 0; it had no network and no deployment data mounts. The initial package-download verification-image build was explicitly stopped after timeout; its BuildKit record is terminal Error, not an unattended build. The offline replacement reuses native libraries/models from the locally installed image and is a verification image, NOT a newly built production/release image. Existing web/worker deployment and volumes remain untouched.

Update: 2026-09-30T13:59:28.3058828+08:00 — User explicitly requested starting the new version. Built the repository Dockerfile/Compose web image successfully in 52.2s (BuildKit Completed n8v88qvo9h32hjfffongtvbqr); build Go tests and 88 frontend tests passed. Both prior services had zero queued/running tasks and were cleanly stopped. Full data/model tar backups were made with numeric ownership/modes, listed successfully, checked for DB/master.key/model files and SHA-256 hashed. Backups are private local archives (restricted Windows ACL; not encrypted) under artifacts/deploy/20260930-workflow-parity/backup. Previous image retained as autoclip-go:rollback-20260930-parity; rollback guidance is in that evidence directory's ROLLBACK.md.

Started both services through docker compose up -d --no-build --wait --wait-timeout 180. They are healthy on image sha256:988e07463770f2f6a0d017013418e2bf2516248461443f1d4211eb23071bde3f, still bound to 127.0.0.1:8080. Read-only live verification confirms all 8 projects, 13 drafts and 4 completed exports remain unchanged, task IDs/statuses unchanged with no unexpected production, model/cookie configuration status unchanged, ready legacy plans accessible, all historical exports respond Range 206, and the served frontend is index-Deb8oguS.js with SHA-256 40ce1af1878619e9658981f1d24efa22cb1695096022dfa57a547071eab93763. Evidence: build-verification.json, backup/sha256.json, start.log, deployed-services.txt and live-verification.json in the deployment folder. No model test or paid production request was made; live-model semantic quality acceptance remains open. No source commit or push.
