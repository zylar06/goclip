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
- `internal/worker`: inspect/import, confirmed production and immutable-snapshot export orchestration.
- `internal/media`: native subprocesses, subtitles, ASR, frame sampling, title PNG and rendering.
- `internal/ai`: OpenAI-compatible HTTP adapters, subtitle/visual analysis, evidence fusion and checkpoints.
- `web`: reused React studio UI with web-only adapter, no analytics or native APIs.
- Docker image contains FFmpeg, yt-dlp, Deno, whisper.cpp, base model and fonts.

## API contract
All JSON responses are direct objects/arrays. Errors: `{code,message,retryable,request_id}`.
Paths below are relative to `/api/v1`.

- `GET /health`, `GET /version`
- `GET /projects` -> Project[]; `POST /projects` multipart (video file, optional subtitle, name) or JSON `{name,url}` -> Project
- `GET /projects/{id}` -> `{project,drafts,tasks,candidates,exports}`
- `DELETE /projects/{id}` (requires JSON `{confirm:true}`; rejects active tasks)
- `POST /projects/{id}/analyze` -> Task, body `{mode:"fused",allow_visual:true,confirmed:true,goals:["highlight"],duration:30,aspect:"original",instruction:""}`. Historical modes remain readable for old tasks.
- `GET /projects/{id}/source` Range video
- `GET /projects/{id}/subtitles` -> Cue[]
- `POST /projects/{id}/drafts` body Draft -> Draft (manual)
- `PUT /projects/{id}/drafts/{draftId}` body Draft with current revision -> Draft
- `POST /projects/{id}/drafts/{draftId}/duplicate` body `{title}` -> Draft
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
Kinds: import, inspect, analyze, preview, export. Source-ready is distinct from drafts-ready and exported.
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

Update: 2026-09-30T11:25:00+08:00 — Source import accepts `b23.tv` share links
in addition to explicit Bilibili/YouTube video pages. URL validation stays a
pure function shared by the API and the media layer; a share link is
shape-checked there and carries a sentinel, and the worker performs the
single-hop HEAD resolution and revalidates the destination through the same
rules before fetching. Redirect resolution deliberately does not happen in the
web process, so the public API gains no caller-driven outbound request. The
consequence for users is that an unresolvable share link fails inside the import
task rather than synchronously at paste time. No API or schema change.

Update: 2026-09-30T12:30:00+08:00 — `POST /projects/{id}/analyze` accepts an
optional `category` field: one of `knowledge`, `speech`, `business`,
`entertainment`, `opinion`, `experience`, `content_review`, or empty for the
shared prompts. It selects the genre-specific outline and timeline prompts ported
from upstream `backend/prompt/<category>/`, with per-stage fallback to the shared
prompt. An unknown value is rejected at the API boundary before a task is queued.
`api/openapi.json` and the generated frontend types were regenerated; that
regeneration also finally removed the `language` fields and the `rewrite` route
from the published contract, which the earlier feature removal had left stale.
No database migration: `category` lives only in the analysis task payload.

Update: 2026-09-30T13:00:00+08:00 — Workflow parity implementation separates
ingestion from production. Import probes and reads provided/platform subtitles
without starting ASR. A durable project `plan` (revision/options/reason/suggested
goals) is shown by the independent import review route; ASR is deferred until a
confirmed text goal or explicit subtitle-enabled export needs it. New drafts
default to `subtitles:false`; analysis text does not grant subtitle-burning
consent. Existing draft settings are unchanged unless explicitly edited.

Schema 2 adds workflow records with unique `(project_id,plan_revision)` and
linked task children. A workflow never holds the global heavy-worker lease.
Successful content analysis atomically publishes drafts and enqueues immutable
export snapshots; highlight/promo only do so when `auto_export` was confirmed.
Per-goal errors and successful results coexist; retry skips already-published
analysis goals and does not re-render already-completed export tasks.

Added relative API routes: `GET/PUT /projects/{id}/plan`,
`POST /projects/{id}/confirm` (`plan_revision,confirmed`), `POST
/projects/{id}/inspect` (`allow_visual,confirmed`), project/draft `thumbnail`,
`GET/POST /projects/{id}/source-preview`, its `/video` stream, and `POST
/projects/{id}/drafts/disable-subtitles` (`confirm:true`). Workspace additionally
returns workflows; tasks optionally carry workflow_id/goal. `AnalysisOptions`
adds optional burn_subtitles; duration 0 means automatic semantic selection,
and category is optional in generated contracts. Existing endpoints remain.
See verification.md for actual executed gates; this entry is not release acceptance.

Update: 2026-09-30T13:21:42.6005111+08:00 — Schema 2 additionally persists workflow-scoped candidate snapshots. Claim leases fence all production worker writes; an OS-owned data-directory execution lock serializes shared media/model checkpoint writers even if a process is suspended beyond its heartbeat. A second worker waits up to 90 seconds (or its context deadline), then reports the lock timeout; the operating system releases ownership when the first worker exits. This preserves checkpoint reuse while preventing overlapping attempt writes. Content drafts use explicit title_enabled:false as well as subtitles:false; a missing title_enabled field retains historical title behavior.

Update: 2026-09-30T15:16:00+08:00 — README reorganized into short Chinese
startup, usage, model setup and maintenance instructions. Removed repeated
implementation details and historical test/build counts; corrected stale claims
about import-time transcription, translation and Bilibili short links. A collapsed
directory tree describes all 226 tracked files individually, including tests,
prompts, fonts and frontend assets. Private/generated local files are identified
separately, not listed as tracked source. Tree paths and exact file coverage were
checked against git ls-files; Markdown fence/disclosure checks and git diff
--check passed. Documentation-only change: no runtime code, deployment, model
calls or data changes; application tests were not rerun.
