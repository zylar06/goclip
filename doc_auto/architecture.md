# AutoClip Go architecture

Updated: 2026-09-29

Independent migration; upstream `../autoclip` and installed desktop data are read-only.
Go web + Go worker, SQLite WAL on local Docker volume, React same-origin UI.
No desktop, Python business code, Redis, login, publishing or telemetry.
Anonymous trusted LAN/VPN users share all projects and cloud costs.

## Ownership and boundaries
- `internal/domain`: shared entities and validation.
- `internal/store`: sole durable state source, settings encryption, task queue.
- `internal/httpapi`: `/api/v1`, SSE snapshots, same-origin mutation checks.
- `internal/worker`: import, analyze and immutable-snapshot export orchestration.
- `internal/media`: native subprocesses, subtitles, ASR, frame sampling, title PNG and rendering.
- `internal/ai`: OpenAI-compatible HTTP adapters, six-step text and visual analysis.
- `web`: reused React studio UI with web-only adapter, no analytics or native APIs.
- Docker image contains FFmpeg, yt-dlp, Deno, whisper.cpp, base model and fonts.

## API contract
All JSON responses are direct objects/arrays. Errors: `{code,message,retryable,request_id}`.
Paths below are relative to `/api/v1`.

- `GET /health`, `GET /version`
- `GET /projects` -> Project[]; `POST /projects` multipart (video file, optional subtitle, name) or JSON `{name,url}` -> Project
- `GET /projects/{id}` -> `{project,drafts,tasks,candidates,exports}`
- `DELETE /projects/{id}` (requires JSON `{confirm:true}`; rejects active tasks)
- `POST /projects/{id}/analyze` -> Task, body `{mode:"subtitle"|"visual",allow_visual:boolean,confirmed:true,goals:["content"],duration:30,aspect:"original",language:"source",instruction:""}`
- `GET /projects/{id}/source` Range video
- `GET /projects/{id}/subtitles` -> Cue[]
- `POST /projects/{id}/drafts` body Draft -> Draft (manual/collection)
- `PUT /projects/{id}/drafts/{draftId}` body Draft with current revision -> Draft
- `POST /projects/{id}/drafts/{draftId}/duplicate` body `{title,language}` -> Draft
- `POST /projects/{id}/rewrite` body `{draft,instruction}` -> Draft, not saved
- `POST /projects/{id}/title-preview` body Draft -> PNG
- `POST /projects/{id}/drafts/{draftId}/export` body `{revision}` -> Task
- `GET /projects/{id}/exports/{taskId}/video` Range MP4, `?download=true`
- `GET /tasks/{id}`, `GET /tasks/{id}/events` SSE event `snapshot`, data Task
- `POST /tasks/{id}/cancel`, `POST /tasks/{id}/retry` -> Task (explicit paid retry consent)
- `GET /settings` -> `{text:ModelStatus,vision:ModelStatus,cookies_configured:boolean}`
- `PUT /settings/{text|vision}` -> ModelStatus; `{base_url,model,api_key}`; empty key preserves existing
- `POST /settings/{text|vision}/test` -> `{ok:true,message}` (uses saved setting)
- `PUT /settings/cookies` raw Netscape cookies text; `DELETE /settings/cookies`

Project: `{id,name,url?,status,duration,width,height,created_at,updated_at,error?}`.
Task: `{id,project_id,kind,status,stage,progress:null|number,completed_steps:string[],heartbeat,created_at,updated_at,error?,retryable,payload?}`.
Task states: queued, running, completed, failed, interrupted, cancelled.
Kinds: import, analyze, export. Source-ready is distinct from drafts-ready and exported.
Export: `{task_id,draft_id,revision,title,created_at}`; only completed files are downloadable.
ModelStatus: `{base_url,model,configured}`; no plaintext credentials are returned.
Cue: `{start,end,text}`. Candidate: Scene plus `{score,kind}`.
Draft mirrors upstream editor fields, with ID/project ID and optimistic revision.

## Reliability and validation
One heavy worker. Restart reclaims expired leases as interrupted, not automatic billed retries.
Checkpoints are atomic JSON/media files but SQLite alone is task/project authority.
Subprocess contexts/timeouts terminate process groups. Partial exports verified before publication.
4 GiB and 2 hours default limits. Secrets encrypted with persistent 0600 AES-GCM key.
Source imports only explicit Bilibili/YouTube HTTPS links; model endpoints validated separately.

Update: 2026-09-29T18:18:00+08:00 — Documented the full user workflow and module
diagram in README: local/manual import → CPU ASR → draft → MP4 is independent
of paid AI; subtitle/visual analysis and translation have distinct model needs.
Added environment-to-store bootstrap without API/schema changes:
Compose `.env` → explicit runtime model variables → web startup validation →
one atomic encrypted settings transaction → web/worker read the shared store.
`cmd/autoclip/model_env.go` owns validation/precedence; `internal/store/models.go`
owns atomic encryption/persistence. No settings are baked into build arguments
or images. Runtime UI changes can be superseded at next web startup, and
all-empty env groups preserve existing settings. No legacy automatic migration
is implemented; the user's separately authorized local copy is recorded in
verification.md.
