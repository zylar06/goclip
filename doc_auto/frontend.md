# AutoClip web frontend

Updated: 2026-09-29T14:55:28+08:00

## Scope and provenance

Owns only `web/**` and this document. Backend, domain, root build/deployment files,
upstream checkout, and installed application data are not modified.

The actual `../autoclip/frontend/src/features/studio/StudioEditor.tsx` is ported,
not replaced with a mock. Also reused: CandidatePicker, DraftVariantDialog,
titlePresets, draftExportState, six bundled title-preset WebP assets,
`ui/index.tsx`, `ui/ac.css`, `studio.css`, the Calm Premium design tokens, and the
upstream i18next initialization/language resolver/eight locale catalogs. Unused
locale entries and native/updater/publishing styles are removed. The new workflow
has English source strings plus Chinese translations; other locales retain the
upstream editor translations and fall back to English for new workflow text.

Removed paths/dependencies: native runtime and downloads, desktop preview
transcoding, telemetry, publishing, old Axios/store/workspace envelopes, legacy
clips/plans, dynamic thumbnail endpoints, separate frosted-title mask endpoints,
Ant Design, Tailwind and remote font imports. Existing styles and actual editor
behavior remain, using a small Go-specific web adapter.

## Build and generated contract

- `web/vite.config.ts` reads `../VERSION`; there is no independent UI version.
- `npm ci`, `npm run typecheck`, `npm test`, `npm run build` are the CI sequence.
- `npm run generate:api` reads `../api/openapi.json` and writes
  `src/generated/api.ts`; generated output is included in version control, not
  gitignored. No generator dependency is required.
- `npm run check:api` detects stale generated output; `typecheck` runs it first.
- Generator supports local schema references, required/optional object fields,
  enums/constants, arrays, nullable types, type unions, oneOf/anyOf/allOf and
  additionalProperties. Invalid/external references and unsupported constructs
  fail explicitly. It generates schema types, not an HTTP client.
- API types reuse the generated schemas, with UI-safe domain enum refinements.
  Compile-time tests check assignability. The boundary additionally accepts Go
  nil slices and optional `cancel_requested` (false when absent); these are not
  alternative legacy response formats.
- Docker's frontend stage must include `../api/openapi.json` and `../VERSION`
  before checking/building. No root Dockerfile change is made by this frontend.
- Node 22.12+; tested on Node 22.22.2. Vite outputs `web/dist`.
- Development: `npm run dev`; same-origin `/api` proxy defaults to
  `http://127.0.0.1:8080`, overridable with `AUTOCLIP_API_ORIGIN`.
- Dev and preview bind to `127.0.0.1` by default, not all network interfaces.
- Production requests use relative `/api/v1` only. Hash-based client routes
  avoid deployment rewrite assumptions. All projects/costs are shared; this is
  not an authentication boundary.

## Backend interfaces used

Source of truth: `internal/domain/types.go`, `internal/httpapi/api.go`,
`api/openapi.json`, and `doc_auto/architecture.md`.
JSON responses are direct objects/arrays. Error responses are
`{code,message,retryable,request_id}`; status, message, and request ID are retained.
No automatic mutation retries are performed.

| Relative to `/api/v1` | Request / use |
| --- | --- |
| GET `/projects` | Project list, explicit refresh. |
| POST `/projects` | Multipart `name`, `video`, optional `subtitle`; browser sets boundary. Or JSON `{name,url}`. Returns Project, never auto-analyzes. |
| GET `/projects/{id}` | `{project,drafts,tasks,candidates,exports}`, normalizing nil lists. Project source-ready, draft-ready and exported are separate UI states. |
| DELETE `/projects/{id}` | JSON `{confirm:true}` only after destructive confirmation; disabled with active tasks. |
| POST `/projects/{id}/analyze` | `{mode,allow_visual,confirmed:true,goals,duration,aspect,language,instruction}`. Explicit paid confirmation every time; separate image opt-in for visual mode, default false/reset on mode change. Text always sends `allow_visual:false`. |
| GET `/projects/{id}/source` | Native HTML video Range playback. Browser decode errors are visible. |
| GET `/projects/{id}/subtitles` | Cue list, loaded on opening the transcript with explicit retry/errors. |
| POST `/projects/{id}/drafts` | Full validated Draft for manual creation or an ordered collection of scenes from this project. New scene IDs avoid duplicate IDs. Backend assigns durable ID/revision/origin. |
| PUT `/projects/{id}/drafts/{draftId}` | Full Draft including current revision; conflicts preserve browser edits. |
| POST `/projects/{id}/drafts/{draftId}/duplicate` | `{title,language}` only. Editor saves changed draft before opening duplicate dialog. |
| POST `/projects/{id}/rewrite` | `{draft,instruction}`; returned text is previewed, then explicitly applied, with undo. Identity, revision, timing and sound cannot be replaced by the suggestion. Not automatically saved. |
| POST `/projects/{id}/title-preview` | Full Draft -> PNG; debounced, abortable, errors visible/retryable, object URLs revoked. Includes frosted style through this same route only. |
| POST `/projects/{id}/drafts/{draftId}/export` | `{revision}` from the returned save result, never an unsaved local revision. Export confirmation warns about translation costs. |
| GET `/projects/{id}/exports/{taskId}/video` | Saved export preview or `?download=true` streaming download. Only entries in completed Export history get download links. |
| GET `/tasks/{id}/events` | EventSource `snapshot` events containing Task; full snapshots make replay IDs unnecessary. |
| GET `/tasks/{id}` | Poll fallback while SSE is unavailable/stale. |
| POST `/tasks/{id}/cancel` | No body. A running task with `cancel_requested:true` remains running; cancellation message/button state reflects the asynchronous stop. |
| POST `/tasks/{id}/retry` | No body. Confirmation plus cost-consent checkbox required, even for a retryable interrupted task. Never retried automatically. |
| GET `/settings` | Independent `text`, `vision` ModelStatus and `cookies_configured`; works without keys. |
| PUT `/settings/{text\|vision}` | `{base_url,model,api_key}` independently; empty key preserves the existing key only when the old and new base URL share scheme and host. A different origin requires re-entering the key; backend 400 errors are displayed. Same-origin model changes retain the key. Entered key is cleared after success, never written to local storage. |
| POST `/settings/{text\|vision}/test` | No body; saved configuration only, never unsaved field values. Independent errors/results. |
| PUT `/settings/cookies` | Raw Netscape text, `Content-Type: text/plain; charset=utf-8`, max 1 MiB client guard. Sensitive shared setting. |
| DELETE `/settings/cookies` | Explicit shared-cookie removal confirmation. |

No extra backend endpoint is required or assumed. All thumbnails are bundled
assets. Browser URL preflight follows explicit YouTube watch/shorts/youtu.be and
Bilibili video-page rules; canonicalization strips unrelated query fields.
Credentials, ports, arbitrary hosts, short redirectors and non-video pages are
rejected. Backend validation remains authoritative.

## Editor and reliability behavior

- Scene trim, reorder, append/replace from candidates, remove, source preview,
  title/hook editing, nine title styles, template variants, color/scale/position,
  motion flag, language, portrait recommendation, crop X, fit/crop/blur, original
  audio, subtitle flags, rewrite preview/undo, duplication and revisioned exports.
- Draft validation matches domain limits: 1–30 unique scenes, >=0.1 seconds,
  source bounds, <=30 minutes, title/hook lengths, enum/placement/accent limits.
- Browser draft recovery is scoped by project/draft. Writes/parse failures are
  logged and visible. Page unload and in-app navigation protect dirty edits.
  Newer server revisions do not overwrite local edits; explicit confirmed reload
  is available after conflicts. No API keys/cookies enter this cache.
- Switching import modes clears file selections along with their remounted file
  inputs, preventing accidental submission of an invisible previously chosen file.
- SSE reconnects at most three times per subscription, with 1/2/4-second backoff.
  Disconnects and a 20-second no-snapshot watchdog activate 3-second polling.
  Polling continues during reconnection; a valid live snapshot stops polling.
  Updates older than the current `updated_at` are ignored.
- Six consecutive polling failures stop automatic monitoring with an explicit
  reconnect message. Subscriptions have a 30-minute lifetime; project discovery
  polls every 10 seconds for at most 180 attempts / six consecutive failures.
  Focus, visibility return, and the Refresh/reconnect button restart monitoring.
  Completion refreshes drafts/exports; cancellation or unmount aborts outstanding
  reads and closes streams/timers. Task stages are arbitrary backend strings;
  null progress is shown as indeterminate, never fabricated percentages.
- Worker heartbeat older than two minutes is displayed as stale, not interpreted
  as task completion/failure or permission to repeat paid work.
- Render-job view models are derived from Export records and
  `Task.payload.draft` (ExportPayload). Tasks without a payload remain visible in
  the task panel; the editor cannot associate them with a draft revision.
- Default HTTP timeout 30 seconds; upload 15 minutes, rewrite 310 seconds, saved
  model test 90 seconds. Timeouts explain that task state must be checked before
  manually retrying. Browser lifecycle aborts are deliberate cancellation, not
  swallowed application failures.
- Dialogs use portals, accessible labels, focus containment, Escape handling and
  focus restoration. Controls use native labels; errors are announced with
  `role=alert`. Responsive styling, OS color scheme and reduced motion supported.

## Verification and limitations

Tests exercise real component behavior through the fetch adapter (not screenshots
or source-string assertions): upload/URL import, image/cost consent, settings
separation, raw cookie writes/removal, paid retry, cancellation display, ordered
collections, export history, editor save-before-duplicate/export, revision
conflicts, rewrite preview/apply/undo, local recovery/navigation guard, PNG preview
cleanup, SSE reconnect/poll/error/deadline behavior, task completion refresh,
domain validation, generated contract assignability and generator drift/errors.
Command logs are under ignored `web/logs/`; npm lockfile is retained.

Limitations:
- No source-compatible transcoding endpoint. Unsupported source codecs produce
  an explicit playback warning; timing editing/export remain possible.
- No separate frosted backdrop mask endpoint. Only the documented PNG is shown.
  Translation, animation, blur layout and final subtitle rendering are verified
  through an actual export, not simulated as a complete browser compositor.
- Original aspect preserves aspect ratio, not source resolution. The export
  summary reflects the media worker's current 1920-pixel longest-edge limit.
- A collection is a new ordered, same-project draft, not a separate backend
  collection entity. Cross-project concatenation is not supported by the contract.
- Tests mock network/model/media responses; they do not claim live cloud billing,
  downloader authentication, media-codec playback, or an end-to-end Go-worker
  rendering run. No model calls are made during tests.
- Native anchor downloads stream via the browser download manager. HTTP/download
  failures are handled by the browser; the UI does not claim download completion.
- New workflow text outside Chinese/English falls back to English.
- Lists are not paginated/virtualized; very large shared workspaces may require
  future pagination contracts.

## Update log

- 2026-09-29T14:57:00+08:00 — Integration review removed unsupported historical
  title-template selectors. Only current Go designs are advertised; versions
  2–5 are rejected, with frontend/domain regression tests.

- 2026-09-29T14:50:00+08:00 — Initial web-only port and generated contract
  integration; behavioral test suite and ownership/interface documentation.

- 2026-09-29T14:55:00+08:00 — Final verification passed: npm ci; generated schema check + typecheck; 41 tests across 9 files; production build. Dev/preview bind to 127.0.0.1; original-aspect summary reflects the shared 1920-pixel limit.

- 2026-09-29T14:55:28+08:00 - Documented backend credential-origin restriction: blank keys are reusable only for same-origin model endpoints. No frontend behavior change; explicit API errors already surface in the settings form.

## Final verification results

- npm ci: passed (172 packages, lockfile reproducible).
- npm run typecheck: passed, including check:api against the current OpenAPI spec.
- npm test: final integration run **43 passed, 10 test files**, 15.08 seconds.
- npm run build: passed, 2.72 seconds; output in web/dist.
- No forbidden runtime/native/publishing/telemetry imports or unsupported preview routes found in source scan.
- Initial delegated inventory below: 66 files including this document; integration adds `web/tests/titleVersions.test.ts`. Backend/domain/root deployment were integrated separately.
- Verification logs: web/logs/ci-install.log, typecheck.log, test.log, build.log (ignored build artifacts).

## Owned file inventory

The following files were created or ported in this implementation (build/log output excluded):

```text
web/.gitignore
web/README.md
web/index.html
web/package-lock.json
web/package.json
web/scripts/generate-api.mjs
web/src/App.tsx
web/src/api/client.ts
web/src/api/contracts.ts
web/src/api/taskMonitor.ts
web/src/assets/title-presets/arena.webp
web/src/assets/title-presets/comic.webp
web/src/assets/title-presets/editorial.webp
web/src/assets/title-presets/frosted.webp
web/src/assets/title-presets/neon.webp
web/src/assets/title-presets/pixel.webp
web/src/components/AnalysisPanel.tsx
web/src/components/CollectionDialog.tsx
web/src/components/TaskPanel.tsx
web/src/features/studio/CandidatePicker.tsx
web/src/features/studio/DraftVariantDialog.tsx
web/src/features/studio/StudioDownloadLink.tsx
web/src/features/studio/StudioEditor.tsx
web/src/features/studio/StudioResults.tsx
web/src/features/studio/TitleArtwork.tsx
web/src/features/studio/api.ts
web/src/features/studio/draftExportState.ts
web/src/features/studio/studio.css
web/src/features/studio/titlePresets.ts
web/src/features/studio/types.ts
web/src/features/studio/useWorkspace.ts
web/src/generated/api.ts
web/src/i18n/index.ts
web/src/i18n/language.ts
web/src/i18n/locales/en.json
web/src/i18n/locales/es.json
web/src/i18n/locales/fr.json
web/src/i18n/locales/ja.json
web/src/i18n/locales/ko.json
web/src/i18n/locales/pt.json
web/src/i18n/locales/ru.json
web/src/i18n/locales/zh.json
web/src/i18n/web.zh.json
web/src/index.css
web/src/main.tsx
web/src/pages/HomePage.tsx
web/src/pages/ProjectPage.tsx
web/src/pages/SettingsPage.tsx
web/src/ui/ac.css
web/src/ui/index.tsx
web/src/vite-env.d.ts
web/src/web.css
web/tests/api.test.ts
web/tests/contract.test.ts
web/tests/editing.test.ts
web/tests/editor.test.tsx
web/tests/fixtures.ts
web/tests/generator.test.ts
web/tests/settings.test.tsx
web/tests/setup.ts
web/tests/taskMonitor.test.ts
web/tests/titleVersions.test.ts
web/tests/workflows.test.tsx
web/tests/workspace.test.tsx
web/tsconfig.json
web/vite.config.ts
doc_auto/frontend.md
```
