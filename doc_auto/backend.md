# Backend implementation

Updated: 2026-09-29

Go standard HTTP mux, modernc SQLite and AES-GCM configuration vault.
All mutable authoritative state is SQLite. Task payloads contain immutable
analysis options/export snapshots; no secrets. Assets record relative paths.
SRT/ASR and model checkpoints are data artifacts, never a second task state store.

Queue claims are transactional and globally serialize heavy work. Per-project
active tasks prohibit starting a second task or deleting the project.
Revision CAS prevents lost draft updates. Export snapshots do not follow edits.
API-key/Cookie ciphertext is in SQLite with persistent 0600 `master.key`.
Running cancellation is cooperative: heartbeat notices cancel_requested,
subprocess context terminates, then the task becomes cancelled.

Same-origin browser write checks, strict JSON request parsing, file size limits,
whitelisted source URLs, validated internal IDs and non-shell argument vectors.
Media files are served with HTTP Range only through known project/task IDs.
MP4 requires a completed export task; files alone do not imply success.
SSE emits durable task snapshots; subscribers reconnect with a fresh snapshot.

`go run ./cmd/specgen` regenerates `api/openapi.json` from domain structs.
Generated frontend types must be refreshed and checked after contract changes.
Tests cover persistence, queue/recovery/cancel, optimistic revisions, snapshot
isolation, encrypted secrets, CSRF, upload, error redaction and Range responses.

Update: 2026-09-29T14:55:00+08:00 — Reject newer database schemas and missing
master keys when ciphertext exists; reuse downloaded SRT checkpoints; title
previews share the renderer's exact output dimensions (1920 long-edge cap).
Changing a model server's origin requires explicitly entering its key; blank-key
preservation cannot silently redirect a stored credential to another host/scheme.

Update: 2026-09-29T14:57:00+08:00 — Worker suppresses internal model-stage weights:
LLM tasks always report indeterminate progress, with stage/elapsed/completed-step
information instead. Historical template versions 2–5 are explicitly unsupported.

Update: 2026-09-29T15:08:00+08:00 — Added generator and configurable-health-port
regressions; OpenAPI duplicate response is 201; live native API/worker/React
smoke and real speech/media integration are recorded in verification.md.
Late cancellation/completion now resolves atomically: accepted cancellation
cannot be overwritten by a success, and project state follows the durable result.

Update: 2026-09-29T18:18:00+08:00 — Added explicit runtime model bootstrap:
`AUTOCLIP_TEXT_BASE_URL`, `AUTOCLIP_TEXT_MODEL`, `AUTOCLIP_TEXT_API_KEY` and the
equivalent `AUTOCLIP_VISION_*` group. Only `web` applies these on startup, before
serving requests; worker/healthcheck never rewrite settings. All-blank groups
preserve the database. Any nonempty group requires both base URL and model,
validates with the existing AI validator, and replaces the complete tuple;
an empty key means unauthenticated, never reuse of a previous server's key.
All supplied groups validate first and are encrypted/written in one transaction
via `Store.PutModels`, so a validation/write failure cannot partially replace
text settings. Startup errors do not contain input values or credentials.
Startup makes no provider calls. UI edits remain effective until a subsequent
web startup reapplies a nonempty environment group. Removing environment
settings does not delete previously saved database secrets.

Tests cover preservation, partial/invalid groups, redaction, encryption,
keyless endpoint changes, startup precedence, worker no-op, cookie isolation
and rollback on the second database write. README now covers startup, complete
configuration ownership, feature workflow and service architecture.

Update: 2026-09-29T19:16:00+08:00 — Worker now preserves a typed
`ai.CodeNoHighlights` result as an explicit failed task with `retryable:false`,
not a transient API error inviting identical paid retries. Cancellation,
interruption and other failures retain their existing behavior. The source and
subtitles remain available; users can submit a new analysis with a different
mode/instruction after consent. Historical task records are not rewritten.
No new API fields, database migration, automatic mode switch or paid retry.

Update: 2026-09-29T20:05:00+08:00 — Removed the AI rewrite endpoint
(`POST /projects/{id}/rewrite`), the render-time translation step and the draft
`language` field at the user's request. `duplicate` now takes `{title}` only;
it remains a version-saving operation and makes no model call. Analysis options
no longer carry `language`. `api/openapi.json` and the generated frontend types
were regenerated; the contract check passes. No database migration is included:
drafts saved before this change still load and their obsolete `language` field
is ignored, since the store decodes leniently while the HTTP boundary keeps
rejecting unknown request fields.

Update: 2026-09-30T13:00:00+08:00 — Added revisioned plans and production
workflows, confirmation idempotency, atomic result publication/completion and
content-export scheduling. Schema 2 is additive; original source, draft JSON,
export history and encrypted settings remain intact. Saving options forcibly
clears `confirmed`; only the confirmation endpoint starts production, checking
required model settings and explicit visual permission. Reconfirming an accepted
revision returns its existing workflow rather than another charged analysis.

Imports accept exactly one multipart video or URL, optional SRT/name/instruction
(JSON URL imports also accept instruction). Import never transcribes. Source
availability, audio presence and subtitle availability/source are separate
project metadata; source-ready does not imply transcription succeeded. Missing
subtitle assets cannot block a subtitle-disabled export. Subtitle-enabled
exports perform explicit on-demand transcript preparation and fail visibly
when unavailable. Existing transcript artifacts are reused, not inferred from
hardcoded picture text.

Completed exports and generated drafts publish with task terminal state in one
SQLite transaction; cancellation accepted first prevents publication.
Recover/retry/cancel update durable workflow/project state. The single worker
executes child tasks rather than blocking on its own queued work. Unknown or
permanent AI errors no longer automatically advertise a useful retry.
Inspection and compatible-preview tasks use the same queue and cancellation
rules. Thumbnails return only JPEG bytes; streams resolve assets through IDs.
Bulk disabling added subtitles is explicit, increments draft revisions, rejects
busy projects and preserves historical MP4 files.

Update: 2026-09-30T13:21:42.6005111+08:00 — Review hardening: worker transactions use a Claim-generated lease via Store.ForTask; stale attempts cannot heartbeat, mutate source metadata/assets, or complete outputs after recovery/retry. SQLite uses immediate write transactions to avoid deferred read-to-write BUSY_SNAPSHOT contention. CompleteImport publishes source/subtitle references, metadata, plan and completion in one transaction. Incomplete historical imports require explicit import retry before plans, transcript preparation or exports. Uploaded SRT takes precedence over platform captions. Per-workflow candidate snapshots preserve successful other-mode evidence on retry without mixing separate workflows. Accepted confirmation replay and old-project subtitle evidence share the same store/HTTP semantics; legacy analyze validates all required providers and uses goal workflows. Permanent offline lease/concurrency/import/candidate tests pass in backend-review-fixes-2.log; final full-suite verification remains pending.

Update: 2026-09-30T13:49:08.7123891+08:00 — Final recovery/contract closure: CompleteTranscript atomically publishes a validated transcript reference and subtitle metadata. A separate, bounded subtitles-asr.json checkpoint recovers finished ASR after DB failure without overwriting uploaded evidence or invoking Whisper again; registered legacy cues reconcile missing metadata. URL imports retain a private task-ID/URL/path-bound download checkpoint and re-probe files on retry, so failed source publication does not download again. When a user supplies SRT, the worker calls DownloadWithSubtitles(false), avoiding even conversion/validation of irrelevant platform captions; ordinary platform subtitle failures remain explicit.

Pre-snapshot schema-2 candidate evidence migrates only when a single workflow and an existing successful draft prove ownership by candidate ID/time. Ambiguous ownership raises ErrLegacyCandidateOwnership, preserves the transaction's old evidence and disables blind retry; this is not universal automatic legacy recovery. JSON/multipart upload contracts now include instructions and URL/SRT support, multipart source selection is exclusive, and draft thumbnail revision is a required query parameter. These additions have permanent tests and passed the final Windows suite and Linux race/vet run; see verification.md for evidence and limitations.
