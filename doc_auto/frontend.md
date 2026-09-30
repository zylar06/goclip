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
| POST `/projects` | Multipart `name`, exactly one of `video`/`url`, optional `subtitle` and `instruction`; browser sets boundary. Or JSON `{name,url,instruction?}`. Returns Project, never starts ASR or production. |
| GET `/projects/{id}` | `{project,drafts,tasks,candidates,exports,workflows?}`, normalizing nil/missing lists. Project source-ready, draft-ready and exported are separate UI states. |
| GET / PUT `/projects/{id}/plan` | GET restores the durable/local-derived plan. PUT `{revision,options,auto_export}` always sends `options.confirmed:false`; saves image opt-in only when explicitly checked in visual mode. |
| POST `/projects/{id}/confirm` | `{plan_revision,confirmed:true}` uses the revision returned by the immediately preceding successful PUT. The confirmation checkbox is cost consent. |
| POST `/projects/{id}/inspect` | Review-page local recheck uses `{allow_visual:false,confirmed:false}`; no model request. There is no automatic visual inspection. |
| POST `/projects/{id}/drafts/disable-subtitles` | `{confirm:true}` after an explicit project dialog; updates saved draft revisions, never existing MP4s. |
| GET `/projects/{id}/drafts/{draftId}/thumbnail?revision=N` | Source thumbnail on each result card; image failure retains the complete-playback link. |
| GET / POST `/projects/{id}/source-preview` | Read-only discovery restores state. Explicit POST `{confirmed:true}` queues local compatibility conversion; the returned Task uses the existing bounded SSE/poll monitor. |
| GET `/projects/{id}/source-preview/video` | Completed compatible MP4 used instead of the original source, without replacing/deleting it. |
| DELETE `/projects/{id}` | JSON `{confirm:true}` only after destructive confirmation; disabled with active tasks. |
| POST `/projects/{id}/analyze` | `{mode,allow_visual,confirmed:true,goals,duration,aspect,instruction}`. Explicit paid confirmation every time; separate image opt-in for visual mode, default false/reset on mode change. Text always sends `allow_visual:false`. |
| GET `/projects/{id}/source` | Native HTML video Range playback. Browser decode errors are visible. |
| GET `/projects/{id}/subtitles` | Cue list, loaded on opening the transcript with explicit retry/errors. |
| POST `/projects/{id}/drafts` | Full validated Draft for manual creation. Backend assigns durable ID/revision/origin. |
| PUT `/projects/{id}/drafts/{draftId}` | Full Draft including current revision; conflicts preserve browser edits. |
| POST `/projects/{id}/drafts/{draftId}/duplicate` | `{title}` only. Editor saves the changed draft before duplicating. |
| POST `/projects/{id}/title-preview` | Full Draft -> PNG; debounced, abortable, errors visible/retryable, object URLs revoked. Includes frosted style through this same route only. |
| POST `/projects/{id}/drafts/{draftId}/export` | `{revision}` from the returned save result, never an unsaved local revision. Export confirmation describes an immutable saved snapshot. |
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

No extra backend endpoint is required beyond the table above. Title-preset thumbnails
are bundled; result cards use the documented source-thumbnail endpoint.
Browser URL preflight follows explicit YouTube watch/shorts/youtu.be and
Bilibili video-page rules; canonicalization strips unrelated query fields.
Credentials, ports, arbitrary hosts, short redirectors and non-video pages are
rejected. Backend validation remains authoritative.

## Editor and reliability behavior

- Scene trim, reorder, append/replace from candidates or manual source ranges,
  continuous full-draft playback with an assembled-duration seek bar, remove, source preview,
  title/hook editing, nine title styles, template variants, color/scale/position,
  motion flag, portrait recommendation, crop X, fit/crop/blur, original
  audio, subtitle flags, duplication and revisioned exports.
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
- Default HTTP timeout 30 seconds; upload 15 minutes, saved
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
export history, editor save-before-duplicate/export, revision
conflicts, local recovery/navigation guard, PNG preview
cleanup, SSE reconnect/poll/error/deadline behavior, task completion refresh,
domain validation, generated contract assignability and generator drift/errors.
Command logs are under ignored `web/logs/`; npm lockfile is retained.

Limitations:
- Source-compatible transcoding is explicitly started and tracked through the
  documented preview endpoints. Unsupported source codecs produce an explicit
  warning; timing editing/export remain possible while conversion is pending.
- No separate frosted backdrop mask endpoint. Only the documented PNG is shown.
  Animation, blur layout and final subtitle rendering are verified
  through an actual export, not simulated as a complete browser compositor.
- Original aspect preserves aspect ratio, not source resolution. The export
  summary reflects the media worker's current 1920-pixel longest-edge limit.
- Tests mock network/model/media responses; they do not claim live cloud billing,
  downloader authentication, media-codec playback, or an end-to-end Go-worker
  rendering run. No model calls are made during tests.
- Native anchor downloads stream via the browser download manager. HTTP/download
  failures are handled by the browser; the UI does not claim download completion.
- New workflow text outside Chinese/English falls back to English.
- Lists are not paginated/virtualized; very large shared workspaces may require
  future pagination contracts.

## Update log

- 2026-09-29T19:16:00+08:00 — Visual mode now explicitly explains in Chinese and
  English that only sampled images are sent, not audio/full transcripts, and
  recommends subtitle mode for speech-led highlights. Component tests assert
  guidance before consent and no retry button/network request for a typed
  no-highlight task with `retryable:false`. No automatic mode switch or billing.

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

Update: 2026-09-29T20:05:00+08:00 — Removed the copy-rewrite box and its
preview dialog, the collection builder (`CollectionDialog`), the draft variant
dialog and every draft/analysis output-language control. "另存为新版本" now
saves and duplicates directly instead of opening a dialog, keeping the
save-before-duplicate order the editor test asserts. The interface language
switcher and the i18n catalogs are unchanged — only per-draft *output* language
is gone. Tests: 40/40 pass, typecheck, contract check and production build
clean; the removed features' tests were deleted rather than skipped.

Update: 2026-09-29T21:20:00+08:00 — Visual design pass. Presentation only: no
API call, request/response shape, route path or state flow changed, and no npm
dependency added.

Design system. `src/index.css` is now the single token source: warm-stone
surfaces (canvas / sunken / card / raised), three ink levels, three hairline
weights, one indigo accent plus reserved semantic colors (ok / warn / error,
each with a soft tinted surface), a 4px spacing scale, a seven-step type scale,
radius, elevation, motion tokens, and `--ac-gutter` (`clamp(16px, 4vw, 56px)`).
Every token is declared on bare `:root`; the dark palette is redefined in BOTH
`@media (prefers-color-scheme: dark) :root:not([data-theme="light"])` and
`:root[data-theme="dark"]`, each setting `color-scheme: dark`, so the OS setting
and an explicit choice resolve identically and no component rule sits inside a
media block. `html`, `body` and `#root` carry an explicit token background.
Contrast was computed, not estimated: every ink/accent/semantic token clears
4.5:1 against all three surfaces of its own theme (light 4.59–17.57, dark
4.68–15.32); hairlines are decorative only.

Components. Buttons gained distinct hover / active / disabled / `aria-pressed`
states and a visible loading spinner that survives the disabled state; inputs
gained hover, focus ring (`0 0 0 3px` accent) and disabled states; `:focus-visible`
draws a 2px accent outline everywhere. Task and draft state is now encoded as a
`web-pill` — a dot plus a label plus a tinted border — so state is never carried
by color alone. Empty states are real designed blocks with a bold lead line;
list loading uses shimmer skeletons instead of a bare "Loading…" line.
`prefers-reduced-motion` suppresses animation, and because that would park the
indeterminate bar off its track, the reduced-motion rule replaces the sweep with
a static striped fill.

Task, progress and export flow. `ProgressLine` takes `tone` and `large` and is
now an 8–10px bordered track with a rounded data end. The `TaskPanel` row is a
card that turns accent-ringed while running and error-bordered on failure, with
the percentage set large in tabular mono; the editor's render block is a
bordered, accent-ringed status panel with a heading, a large percentage and the
"runs in the background" reassurance. `StudioDownloadLink` is now a distinct
green outlined affordance with a download glyph (`aria-hidden`, so the
accessible name is still exactly `下载成片`), and export history rows are cards.

Responsive and a11y. One gutter token gives every page a >=16px side gutter with
no horizontal page scroll; the header, dialog footers, editor grid, scene rows
and history rows collapse to one column at phone width. All `aria-label`, `role`
and accessible-name text was preserved — no test was modified. `RouteError` and
the 404 route previously rendered hard-coded English; both now go through `t()`,
the 404 as a new `NotFound` component so it follows language changes.

New strings: 12 keys added, each with its Chinese source key plus an English
entry (`locales/en.json` + `locales/zh.json` identity) or, for English-source
strings, a Chinese entry in `web.zh.json`. No untranslated literal remains in
JSX apart from the "AutoClip WEB" wordmark. The language switcher is untouched,
and no output-language or copy-rewrite UI was reintroduced.

Verification: `npx tsc --noEmit` clean; `npm test` 40 passed / 10 files
(11.98 s), zero test files edited; `npm run build` succeeded (99 modules,
CSS 47.27 kB → 8.80 kB gzip, JS 403.46 kB → 128.45 kB gzip); `npm run typecheck`
passed including `check:api` ("Generated API types are current"). Not verified:
the rendered result was never opened in a browser, so layout and both themes
still need visual confirmation on a real screen.

Update: 2026-09-30T11:25:00+08:00 — `validateSourceURL` in `pages/HomePage.tsx`
now accepts `b23.tv` share links, which is the form Bilibili's own share sheet
produces; pasting one previously failed in the browser before any request was
sent. Only the share-code shape is checked client-side — the server resolves the
redirect and revalidates the destination — so the error copy now says a share
link is supported and no longer claims short redirects are unsupported. The
Bilibili video-path pattern is case-insensitive, matching the Go side. Tests
cover an accepted share link with and without a trailing slash, rejection of a
malformed code, a nested path and a query on a short link, and that a full
Bilibili URL's tracking query (`spm_id_from`, `vd_source`) is dropped during
canonicalization. Frontend suite 40/40, typecheck and contract check clean.

Update: 2026-09-30T12:30:00+08:00 — The analysis panel gained a "Content type"
selector covering the seven ported genres plus General, which sends the new
optional `category` field. General remains the default and is what every previous
analysis effectively used. Ten translation keys were added in both the Chinese
source catalog and English. Tests 40/40, typecheck, contract check and build
clean; the analyze-body assertion in workflows.test.tsx was updated for the new
field.

Update: 2026-09-30T12:52:00+08:00 — Approved inspect/confirm production flow
and complete-draft playback. This entry supersedes the original import-ASR,
direct-project navigation and unavailable-compatible-preview descriptions above.
Existing in-progress changes were retained; this work touches only `web/**`
and this document. Upstream CreativeImport, ImportReview, PlanSummary,
DraftResultCard and StudioEditor were reviewed before implementation.

Import and durable confirmation:
- Upload returns to the independent `/import/:id` route, never directly to
  project production. The deployed router and route tests share `src/routes.tsx`.
- File and URL imports accept optional SRT and persisted instructions
  (maximum 4000 UTF-8 bytes). URL plus SRT uses multipart with exactly one
  source field; URL without SRT uses JSON. Import requests do not call analyze,
  inspect, confirm or transcription from the browser.
- Review shows the original source, local-only evidence, subtitle origin/status
  and whether transcription is needed. Local recheck is explicit and sends
  `allow_visual:false,confirmed:false`. No automatic image inspection occurs.
- `PlanSummary` is shared by review and project pages. GET restores saved
  options; empty local suggested goals stay empty and require a user choice.
  Goals are content/highlight/promo; modes subtitle/visual; duration 0 means
  complete-semantic automatic selection, 10–120 is an expectation rather than
  forced truncation. Aspect, category and instruction remain editable.
- Newly added subtitles default off. Auto-export defaults off for optional
  highlight/promo one-click export; visible copy explicitly explains that
  content always produces MP4 automatically.
- Saving always PUTs `options.confirmed:false`, plus separately granted image
  permission for visual mode. Changed plans save first, then POST
  `{plan_revision:<returned revision>,confirmed:true}` only after explicit cost
  consent. Unchanged saved plans confirm their existing revision directly;
  unknown confirmation outcomes replay only that revision, never another PUT.
  Conflicts prevent confirmation; failed confirmation retains the saved plan
  and shows the error without automatic paid retry.
  Cost/image checkbox authorization is not restored on a refresh.
- Active workflow/task state survives refresh; starting duplicate production
  is disabled while it is active. Model errors are visible without deleting
  source media, old drafts or completed files.

Results and editing:
- Result cards show source thumbnails, source time ranges, draft duration,
  full-draft playback/edit links, and completed-MP4 playback. Workflow sections
  display each goal's status/error independently. A completed export Task
  without an Export record does not produce a download link. Scene count is
  no longer the prominent result-card description.
- `DraftPlayer` follows every range in edit order (including repeated ranges),
  stops only after the final range, replays from the beginning, and offers an
  assembled-duration seek bar. Switching to source mode removes range limits
  so the current source time can set a manual start or end. Opening artwork
  appears only in the opening range, for at most its first four seconds.
- Manual ranges can be appended without candidates. New manual drafts set
  `subtitles:false`; source-end validation permits exactly the domain's
  1ms tolerance instead of the former 50ms. Old draft subtitle flags are
  preserved on load. A separate project confirmation explicitly disables
  added subtitle layers in existing drafts; revisions increase but old MP4s
  and source hardcoded captions do not change.
- Duplicate still saves first. Successful navigation now bypasses only the
  completed duplicate operation's own dirty/busy blocker; ordinary unsaved
  navigation remains protected.
- Compatibility-preview discovery is read-only. Local conversion starts only
  through an explicit button, tracks the returned task with the existing
  bounded SSE/poll monitor, and switches to `/source-preview/video` only when
  completed. Codec warnings clear only when replacement media actually loads.
  Original source files are retained.
- Chinese production strings live in `src/i18n/production.zh.json`. Obsolete
  AI copy-editing/output-language copy was removed from all eight old locale
  catalogs and the web Chinese catalog, without removing interface languages.

Verification (no dependency installation, external model calls or paid jobs):
- `npm.cmd run generate:api`: passed against the main agent's updated OpenAPI
  file. `src/generated/api.ts` was generated, never hand-edited.
- `npm.cmd run typecheck`: passed, including generated-contract drift checking.
- `npm.cmd test`: **59 passed, 13 files**, 28.49 seconds.
- `npm.cmd run build`: passed, 2.06 seconds.
- New regression suites: `tests/player.test.tsx`, `tests/production.test.tsx`,
  `tests/routes.test.tsx`; expanded editor, workflow and contract tests.
  They cover the actual deployed route table, multipart URL/SRT/instructions,
  no implicit production, empty recommendations, save-before-confirm,
  conflict and failed-confirm recovery, fresh cost/image consent, duration
  validation, batch subtitle confirmation, goal failures, download gating,
  continuous/repeated-range playback and seeking, source selection, manual
  edits, legacy subtitles, duplicate navigation and preview restoration.
- Evidence: `web/logs/production-typecheck.log`,
  `web/logs/production-final-tests.log`, `web/logs/production-build.log`.
  These tests mock HTTP/media events; they do not establish real browser codec
  decoding, frame-accurate source playback, model quality or Go-worker end-to-end
  rendering. Those backend/live integration checks remain with the main agent.

Update: 2026-09-30T13:18:25+08:00 — Independent review-3 recovery fixes
(R2-02/R2-08 and the mixed-mode/invalid-goal findings).

- `PlanSummary` consumes cost and image grants before submitting production,
  including failures. While a confirmation outcome is unknown it locks the
  options and retains the exact pending revision; explicit reconfirmation needs
  fresh grants and sends only POST for that revision. There is no automatic
  mutation retry or paid/background reconciliation.
- Changed choices save before confirmation; unchanged saved choices confirm
  the existing revision directly. This also avoids incrementing the revision
  after a refresh during an unknown request. A plan already marked confirmed
  cannot start again until the user chooses “Prepare another production round”
  and gives fresh consent. A synchronous in-flight guard prevents rapid clicks;
  project-keyed component state prevents cross-project authorization reuse.
- Workspace plan revision/status changes are reconciled without reacting to
  ordinary heartbeats. Clean forms reload local evidence and saved choices;
  dirty forms keep their inputs, revoke consent, show a stale-plan explanation
  and block writes until an explicit reload (which discards those local edits).
- Mixed visual/content plans explicitly disclose the content subtitle/ASR path
  and text-provider transmission as well as sampled-image transmission.
  Visual-only content is rejected locally with an actionable mode/goal message,
  matching Go validation and the original UI; goals are never silently changed.
- Permanent regression coverage: `tests/plan-recovery.test.tsx` (9 cases),
  updated production and deployed-route tests. Full `vitest run`: **68 passed,
  14 files, 27.73s**. `tsc --noEmit`: passed. `vite build`: passed, 2.47s.
  Generated `src/generated/api.ts` was not edited/regenerated by this review
  agent; schema regeneration remains owned by the main agent.
- Evidence/commands and read-only backend recheck are recorded in
  `artifacts/parity/20260930/review-3.md` and its logs. This agent did not run a
  real browser; the browser agent's earlier Chrome artifacts do not establish
  live-browser acceptance of these subsequent recovery changes.

Update: 2026-09-30T13:23:04+08:00 — Import crash gate alignment (R2-06).

- `ImportReview` and `ProjectPage` require positive source duration **and no
  import task whose status differs from completed** before mounting the plan.
  Legacy partial metadata no longer bypasses queued/running/failed/interrupted/
  cancelled imports. Legacy projects with positive duration and no import task
  remain eligible for backend plan/evidence checking.
- Incomplete terminal imports link the user conceptually to the visible
  TaskPanel recovery controls. Retryable imports require explicit import-retry
  confirmation; this is not model-cost consent and does not authorize ASR or
  cloud production. Non-retryable failures explain reimporting the source.
  Completed retry refreshes into confirmation without starting production.
- Added `tests/import-readiness.test.tsx`: **17 passed** across both routes,
  all incomplete states, completed/legacy controls, explicit retry and recovery.
  Final all-web run: **85 passed, 15 files, 36.87s**. Narrowed the fixture status
  array with `as const` for the main agent's generated TaskStatus contract;
  subsequent TypeScript check and 17-case suite passed. Vite build passed
  (1.63s). Logs: `review-3.web-import-final.log`, `review-3.import-gate-final.log`,
  `review-3.typecheck-final.log`, `review-3.build-final.log` in the parity folder.
  No generated API, backend or browser-script files were edited by this agent.

Update: 2026-09-30T13:30:00+08:00 — Final frontend handoff / title toggle regression.

- The existing `title_enabled` editor checkbox now also gates the draft preview
  artwork and export-dialog packaging description; false never shows a title
  layer or promises opening packaging, while preserving the text for re-enable.
  Undefined legacy values retain the prior enabled behavior without a write.
- Added permanent `tests/title-enabled.test.tsx` coverage for false on load,
  legacy omission, explicit off/on, persistence across remount, unchanged
  subtitles/audio/scenes, and save-before-export with the returned revision.
- `vitest run tests/title-enabled.test.tsx tests/editor.test.tsx`: **9/9 passed,
  2 files, 17.28s**; current generated API `tsc --noEmit`: passed. Evidence:
  `review-3.title-enabled.log`, `review-3.title-typecheck.log`.
- Last full web gate before this addition was 85/85, with build passed.
  The final all-suite/build/browser gates belong to the main/browser agents;
  this handoff does not claim the three new cases were in that earlier run.
  Frontend writes stop after these report/document updates.

Update: 2026-09-30T13:49:08.7123891+08:00 — Closeout fixes the two literal question-mark separators in workflow/goal labels to real middle dots. The existing result-component regression now asserts both labels; browser acceptance also asserts actual DOM separators rather than merely taking a screenshot. Current all-web checks: typecheck/generated API drift check passed; 88 tests across 16 files passed (31.47s); production build passed (2.17s). Logs: artifacts/parity/20260930/frontend-closeout-{typecheck,tests,build}.log. This supersedes the earlier partial title-toggle handoff. See verification.md for final browser evidence and unreleased/real-model boundaries.

Update: 2026-09-30T14:32:00+08:00 — GoClip workbench redesign.

- Replaced the marketing/import-first homepage with a searchable project library
  and explicit import dialog. Search survives copied URLs. Cancellation clears
  hidden video/SRT selections and restores focus; busy uploads cannot be dismissed.
- Removed repeated source/draft/export statistics and empty task/export sections.
  Clips lead the project page; completed tasks, old exports, workflow history,
  technical stages and source ranges are disclosures. Failures/active tasks remain
  visible. Project deletion and batch subtitle changes retain explicit dialogs.
- Confirmed plans now show a compact receipt and explicit new-round action, not
  an inactive wall of configuration. Unknown confirmations still use saved-revision
  replay; new rounds require fresh cost/image consent.
- Widened editor preview, collapsed title templates, added responsive review/model
  layouts, neutral/slate theme, compact controls, GoClip identity/favicon and a
  keyboard skip link. Removed the repeated footer; installation/shared-cost
  boundary remains in settings. No external UI/font dependencies were added.
- Updated full-workflow browser entry for the import dialog. Added permanent
  `workbench.test.tsx` behavioral coverage and `theme.test.ts` contrast checks.
  Existing import, consent, conflict/recovery, playback and export tests remain.
- Final web gate: **98/98 tests, 18 files, 37.31 seconds**; API drift/typecheck
  passed; production build passed (2.05 seconds), `index-BXDI7hbi.js` /
  `index-BIcEll7i.css`. Logs: `artifacts/frontend-redesign/{tests,typecheck,build}.log`.
  Five existing browser-harness tests also passed.
- Read-only Chrome review exercised 14 desktop/mobile/dark screenshots with real
  source media, explicit image/video readiness, overflow and Escape focus checks.
  Evidence `artifacts/frontend-redesign/browser-QSxRH9/summary.json` has zero
  writes and zero API/runtime errors. This screenshot run preceded only the final
  decorative plus-text → inline-icon change; final workflow validation is recorded
  separately in verification.md.
- Design/research, source skills and reproducible browser commands:
  `frontend-workbench.md`. This entry supersedes the old warm-stone/hero/card-grid
  styling description, not historical tests or backend contracts.
- No backend, production data, Docker image, deployment, commit or push changed.
