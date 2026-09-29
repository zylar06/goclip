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
