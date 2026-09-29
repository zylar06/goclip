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
For initial image build (10–25 minutes), watch build stage output. At 50 minutes
identify network/download/build bottleneck before continuing.

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
Database schema version is 1; downgrades after future incompatible migrations
require restoring the matching full-volume backup and old image.

## Tool upkeep
Native downloads have fixed SHA256 checks. Debian packages come from a fixed
signed snapshot; Docker image inventory records exact package versions.
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
