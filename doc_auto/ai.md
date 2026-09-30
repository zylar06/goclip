# AI adapters and analysis

Updated: 2026-09-30

## Ownership and public contract

`internal/ai` depends only on the standard library and `internal/domain`. It owns
model HTTP calls, analysis validation and task-directory JSON checkpoints, not
queue consent, credentials storage, video sampling/rendering or draft persistence.

```go
func New(settings domain.ModelSettings) *Client
func ValidateSettings(settings domain.ModelSettings) error
func (c *Client) Test(ctx context.Context, vision bool) error
func (c *Client) Complete(ctx context.Context, prompt string, frames []domain.Frame) (string, error)
func AnalyzeText(ctx context.Context, client *Client, cues []domain.Cue, opts domain.AnalysisOptions, checkpointDir string, progress domain.ProgressFunc) ([]domain.Draft, []domain.Candidate, error)
func AnalyzeVisual(ctx context.Context, client *Client, frames []domain.Frame, duration float64, opts domain.AnalysisOptions, checkpointDir string, progress domain.ProgressFunc) ([]domain.Draft, []domain.Candidate, error)
func AnalyzeVisualWithSampler(ctx context.Context, client *Client, frames []domain.Frame, duration float64, opts domain.AnalysisOptions, checkpointDir string, progress domain.ProgressFunc, sample func(context.Context, []float64) ([]domain.Frame, error)) ([]domain.Draft, []domain.Candidate, error)
func MakePromos(ctx context.Context, client *Client, candidates []domain.Candidate, opts domain.AnalysisOptions, checkpointDir string, progress domain.ProgressFunc) ([]domain.Draft, error)
func Screen(ctx context.Context, client *Client, frames []domain.Frame, duration float64) (Screening, error)
```

`New` retains validation errors for subsequent calls. A client is immutable and
safe for concurrent calls. Separate text/vision settings instantiate separate
clients. No network activity occurs during construction or settings validation.

## HTTP and errors

- HTTP(S) LAN, localhost, IPv6 and public bases are accepted. Root bases append
  `/v1/chat/completions`; a supplied API prefix such as `/v1`, `/api/v3` or
  `/compatible-mode/v1` appends only `/chat/completions`.
- Supply a **base**, not a completion URL. Full completion endpoints, duplicated
  segments/slashes, encoded/dot paths, native Responses/Anthropic/Gemini/Ollama
  action/resource paths, query strings, fragments and userinfo are rejected with local
  corrective messages. An empty API key supports unauthenticated local servers.
- Requests are nonstreaming Chat Completions POSTs with no redirect following
  and no replayable request body. `Complete` and smoke tests make one attempt.
  Analysis alone has a single bounded retry layer (see below). API keys are used only
  in Authorization. TLS certificate verification is not disabled.
- 90-second per-request cap for text and 300 seconds for requests carrying
  images, 10-second connect/TLS caps, 240-second response-header cap; parent
  context can shorten these. Responses: 2 MiB; headers: 64 KiB.
  Prompts: 512 KiB. Normal output budget: 8192 tokens; smoke: 32 tokens.
- Frames are local PNG/JPEG files, decoded/validated before use, encoded as
  `image_url` base64 data URLs, paired with source timestamps. File paths are
  never sent. Limit 60 images/request, 4 MiB/image, 16 million pixels/image,
  24 MiB aggregate image bytes/request. No remote image downloads.
- `Test(false)` checks a one-request `OK` response; `Test(true)` sends a genuine
  generated 64×64 red PNG and checks `red`. Tests are independent and have no
  fallback between modalities. Calling a smoke test can incur cost.
- Exported `*ai.Error`: `Code`, `Message`, `Stage`, `HTTPStatus`, `Retryable`.
  Codes: `auth`, `model`, `endpoint`, `timeout`, `rate_limit`, `invalid_response`,
  `no_highlights`.
  Provider status and recognized machine error labels drive classification;
  ambiguous 400/404 errors are endpoint errors rather than guesses from prose.
  Errors never include key, URL, server body, prompt, local path or raw cause.
  Context sentinels remain discoverable with `errors.Is`.
- `Retryable` is informational; it does not control the narrower analysis retry policy.
  Invalid inputs, stage schemas, checkpoints and quality failures use
  `invalid_response` with a local explanation. Progress callback failures stop
  work and are sanitized; cancellation/deadline identity is preserved.

## Text pipeline and quality

The embedded prompt ports cite their upstream files in `../autoclip/backend`.
The logical stages are `01-outline`, `02-timeline`, `03-scoring`, `04-titles`,
`05-drafts`. Outline/timeline process chunks of at most roughly
30 source minutes or 64 KiB subtitle text plus cue overhead, with at most 256
chunks/topics. Each outline chunk validates before the next paid call. Scoring and titles
use real subtitle excerpts, not only model summaries.

- Outline covers meaningful beginning/middle/end topics; no external facts.
- Timeline uses topic IDs and source cue bounds; numeric seconds and strict
  SRT/MM:SS timestamps are parsed. Boundaries snap within 3 seconds or to a
  containing cue. Hallucinated/out-of-chunk bounds fail explicitly.
- Duration zero means automatic complete semantic units; it is never normalized
  to 30 seconds for subtitles. Explicit content/highlight durations are preferred
  lengths, not hard cuts. The original 150/300/480-second tier maxima remain
  preferred ranges, separate from the source/domain `hard_max_seconds` budget.
  Valid complete spans beyond the preferred tier are preserved, counted in
  `quality.over_tier`, and reported as `02-timeline-over-tier`, including replay.
  Spans beyond the 1800-second safety budget fail explicitly rather than being
  blindly cut. Tiny sources remain supported.
- Overlap of at least 50% of the shorter segment merges; lesser overlap shifts
  to a later cue. Short neighbors can merge across at most 5 seconds.
  Unsupported short/silent candidates are counted in checkpoint quality reports.
  Retained spans require at least 50% union subtitle coverage, unique sequential
  IDs, nonoverlap, valid duration and verbatim cue-derived evidence.
- Scoring strictly maps every ID once to a finite score in [0,1]. Threshold
  0.7 selection is filled to the tier's minimum from existing valid scores, then
  capped at the tier maximum. Missing/model-failed scores are never fabricated.
- Titles map selected IDs exactly.
- Draft assembly is deterministic apart from new IDs/timestamps. It creates
  one draft per selected candidate; all use verified scenes. New subtitle drafts
  do not burn captions unless `AnalysisOptions.BurnSubtitles` is explicitly true.
  The final stage does not call a model or export video. All scored candidates are
  returned, including those not selected for drafts.

Malformed JSON, duplicate keys, extra top-level values, unknown schema fields,
excessive nesting, missing required semantic values and model truncation/refusal
are errors. One complete Markdown JSON fence is accepted, not arbitrary extraction
or JSON repair. Malformed output is never repaired or retried. Only a successfully decoded,
validated empty outline array may be skipped, with a progress notification.

## Visual pipeline and consent

Orchestration must require explicit consent. `AnalyzeVisual` additionally rejects
missing `AllowVisual` or `Confirmed`, and rejects subtitle mode. Text failure
never switches to vision. The media owner supplies immutable frame files and
the source duration; this package never launches FFmpeg.

Scan/review prompts cover general footage, including educational explanations,
interviews and demonstrations. Readable on-screen text is visual evidence;
non-game segments use `other`, and physical action is not required. Images alone
do not supply audio or the full ASR transcript. A static presenter cannot justify
inventing spoken arguments. Speech-led content should use explicit subtitle
analysis when the requested highlights depend on narration.

A valid empty result or exclusively filtered events yields `no_highlights`,
distinct from missing/null arrays (`invalid_response`). Scan arrays beyond twelve
are truncated before per-event validation, including an unusable thirteenth item. No events,
scores or drafts are fabricated. Worker records the explicit unsuccessful
result without a blind same-task retry; a new analysis with revised mode/options
still requires user consent. Raw provider output remains unlogged.

Up to 600 input frames (64 MiB total) are fingerprinted by image contents and
timestamps. Up to 60 evenly distributed frames form `01-visual-events`. Models
return up to 12 distinct events with exact supporting sample timestamps,
bounded evidence, event kind and score. All times and frame references validate.
An empty or whitespace label falls back to a default; empty evidence is accepted.
Length (120/1000 characters) and UTF-8 validity stay strictly enforced.
Pure menu/reward-screen/loading events are excluded by explicit kind, not labels.
Only the highest-scoring playable event receives `02-visual-refine-01`: review
window is the original event ±2 seconds within the source; at most 25
fresh images are requested through the optional sampler, at a step of
`max(1, window/24)` seconds (millisecond timestamps). Returned counts, timestamps
and image bytes are validated before a review request. The legacy wrapper with
a nil sampler uses nearby supplied frames instead. Identity, duration and local sample bounds
remain checked. A review can filter an event as non-play content; no supported
result is an error, not invented success. The remaining playable scan candidates
keep their scanned, trimmed and clamped bounds and still become drafts. An
unusable review is skipped only when another playable event remains, reported
as `02-visual-refine-skipped` in progress and durably checkpointed as a local
decision. Replay repeats the notification, not the paid review; every other
failure class still aborts. Independent events
are not merged. Stage names use local ranks, never model-returned text or IDs,
to prevent provider-controlled content from entering diagnostics or checkpoint
filenames.

`03-visual-titles` uses the verified evidence (no extra image request);
`04-visual-drafts` builds individual drafts with subtitles disabled.
Returned candidates include filtered scan events and reviewed replacements.
Static-frame classification/ranking/boundaries remain estimates requiring human
review. Fresh dense review is provided only through the injected media sampler; this
package does not run FFmpeg. Dense image fingerprints bind all downstream
checkpoints; replay re-samples locally but never repeats a verified model call.
Visual duration remains a hard upper bound, defaulting to 30 seconds only when
zero. Widely separated frame citations cannot override this cap. Promotional goals influence evidence-grounded hooks,
not unsupported performance claims or automatic paid variants.

## Checkpoints, progress and retry

`checkpointDir == ""` disables persistence. Otherwise each verified stage becomes
`<stage>.json`, written through a synced temporary file and same-directory rename.
New directories use 0700 and files 0600 (subject to OS semantics). Checkpoints
contain version, input/model/endpoint fingerprint (never the key), predecessor
chain digest, result digest, timestamp and typed stage JSON.

Only validated results are saved; raw model responses are not diagnostic files.
Every replay checks schema, digests, stage identity, input/settings/options and
current semantic invariants. Corrupt or stale files fail explicitly without
silently making a replacement paid call. A fresh directory is required after
changing inputs, options or model/base URL. Changing only the key preserves
valid checkpoints. Explicit retry with unchanged input reuses completed stages.
Outline and timeline also save independently chained `01-outline-chunk-NNN` and
`02-timeline-chunk-NNN` checkpoints. A later chunk failure does not repeat earlier
verified chunks. Timeline chunks store validated, source-bounded candidates,
not unvalidated provider JSON. Aggregate replay remains deterministic regardless
of whether the chunk-producing closure ran.

Stage validators receive `*T`, so slice-header truncation and scalar/default
normalization reach the caller, serialized checkpoint, and replay. A cached
semantic failure is never treated as a skippable model refinement. Pipeline
version `autoclip-ai-2` and visual prompt/checkpoint version `general-visual-6` invalidate old rules
before spending.
Text profile version `semantic-tiers-2` and promo version `initial-promo-2`
independently invalidate earlier text/promo semantics without invalidating
otherwise current visual checkpoints.

`02-visual-refine-01` stores `{status,event_id,result?}`. `accepted` requires
one validated review result bound to the top scanned event; `skipped` requires
no result and another playable scan event. The skipped checkpoint contains no
rejected provider response or error prose. Both decisions advance the same
predecessor chain before downstream titles/drafts or the skip notification.
Thus a later failure/cancellation reuses the same decision and candidates even
if the provider would now return a different answer. Dense samples are still
reacquired and fingerprint-checked locally on replay. Callback, checkpoint,
auth, cancellation and explicit no-highlights failures cannot become a skip.

Version-5 visual checkpoints are intentionally rejected at the scan checkpoint
before new model calls: the old format did not record skips, so inferring or
silently replaying an omitted decision is unsafe. Files are not deleted or
rewritten. A new visual run requires a fresh task directory and explicit caller
consent; there is no automatic paid migration. Text and promo fingerprints and
their unchanged checkpoints are not bumped by this correction.

Analysis calls have at most three total transport attempts with 100/200 ms
cancellable delays, only for recognized network errors, HTTP 429 or 5xx.
Auth/model errors, cancellation, expired caller deadlines, structural/schema
failures and local validation failures do not retry. The loop exists only at
the shared analysis request boundary; neither stages nor chunk handling add a
second loop. Safe retry logs include only attempt, local error code and status.
No JSON-repair requests are made; a provider may still bill transient attempts.

SQLite remains task authority. The caller supplies a private task-scoped directory,
serializes retries and must not concurrently mutate input images/checkpoints.
Checkpoints can contain sensitive source/model text and must stay access-controlled.
Checksums detect accidental corruption, not hostile local file rewriting.
Progress callbacks receive stage name and percent **0–100**, including replay;
callback/context failure immediately stops subsequent model calls.

## Verification and limits

Tests use only loopback `httptest` servers and temporary generated images; no real
provider calls, API secrets or additional dependencies are needed. Run
`go test ./internal/ai`, `go vet ./internal/ai`, and (where a C toolchain is present)
`go test -race ./internal/ai`.

Verified locally on 2026-09-29:
- `go test -mod=readonly -timeout=60s -coverprofile=internal/ai/coverage.out ./internal/ai`
  passed; statement coverage **90.1%**.
- `go vet -mod=readonly ./internal/ai` passed.
- The AI test binary cross-compiled successfully for `linux/amd64`, `CGO_ENABLED=0`;
  that Linux binary was not executed on this Windows host.
- `go test -race` could not run: this host has `CGO_ENABLED=0` and no detected C
  compiler. Race-detector verification remains outstanding.
- Actual paid-provider smoke/analysis calls were intentionally not run. Tests use
  genuine generated PNG/JPEG fixtures, including the independent vision smoke.
- Repository-wide final tests/vet and worker/API integration review belong to
  main. This change edits only `internal/ai/**` and this document; shared domain,
  module configuration, worker, API, orchestration and root files are untouched.

Provider-specific native APIs, tool calls, SSE, model discovery, automatic JSON
repair, arbitrary image formats and provider-specific reasoning options are not
implemented. Very large aggregation prompts fail the explicit prompt budget
rather than truncate evidence. No claim of real-provider compatibility or
semantic quality is made by mock tests alone.

## Optional initial promos and screening

Worker routing owns goal products and should share one text/visual analysis per
run. The analysis functions do not start promo calls implicitly. Worker may use
fixed analysis goals (content for subtitles, highlight for vision) to reuse the
same checkpoints across its goal-product decisions.

`MakePromos` accepts one to six selected actual candidate scenes and makes one
logical text-model request. All supplied playable candidates remain available
for selection. Only opaque IDs and each candidate's label/evidence reach that
prompt as material. The model returns one to three
`{candidate_id,title,hook}` plans, never scenes/timestamps or rewrite variants.
Multiple plans may reference the same candidate with different openings; one
visual event can therefore yield three initial promos. Text plans may reference
one to three distinct candidates in any order. A hook (or title when no hook is
present) repeated after case/whitespace normalization is rejected, not copied
into multiple drafts. Sparse evidence may yield fewer plans, with no padding or
repair call. Single-scene drafts preserve bounds/evidence exactly; their origin
is promo. Each initial plan receives an independent draft ID.
Visual-mode input is supported using the caller's text model, with no image
request or subtitle burning. Source/options/model/version fingerprints protect
promo title and draft checkpoints. It requires confirmed analysis.

`Screen` returns `Screening{Mode, Goals, Reason}` from at most four static
images. It enforces a strict schema, subtitle/visual mode, distinct supported
goals and bounded reason text. Every successful reason explicitly states that
speech/audio was not analyzed. It never changes modes, performs ASR, grants
consent or falls back on errors; orchestration must obtain consent before the
request. It shares the bounded transient-attempt policy, not a repair loop.

Update log:
- 2026-09-29T19:16:00+08:00: Investigated the reported educational-video failure
  without another model call. Saved task options show visual-only analysis;
  sampled frames show a presenter with on-screen subtitles. The former error
  combined empty, missing/null and oversized arrays, and no invalid raw response
  was persisted, so the exact returned array cannot be reconstructed. Broadened
  the game-biased scan/review instructions; separated no-highlights diagnostics
  from schema failures; added a visual-only prompt version to the input hash.
  Existing visual checkpoints from old prompts fail stale before billing;
  existing text checkpoint fingerprints are unaffected. Tests cover educational
  evidence through scan/review/drafts, distinct result errors, legacy checkpoint
  rejection, worker retry suppression, and browser consent/mode guidance.
  These mock tests do not establish real-model highlight quality.
- 2026-09-29T18:55:00+08:00: Fixed the vision connection-test fixture size from 8×8 to 64×64.
  A live DashScope qwen3-vl-plus diagnostic rejected 8×8 with HTTP 400
  `invalid_parameter_error` (both image sides must exceed 10 pixels). The generic
  endpoint error had obscured that input-size failure; no endpoint/key change
  is required for this defect. A loopback regression reproduces the provider's
  size restriction and verifies a bounded fixture and exactly one request.
  The test remains synthetic-only: no source video or user frames are uploaded.
  Verification: regression failed against the former fixture, then all AI tests
  and vet passed with the fix. The Docker build's full Go test suite passed.
  After local service recreation, the actual saved-vision test endpoint returned
  HTTP 200 with `ok:true` using qwen3-vl-plus. Two live diagnostic requests total
  (old synthetic fixture rejected, fixed app fixture accepted); no automatic
  retry, user media upload, or full visual-highlight analysis was performed.
- 2026-09-29: Initial Go AI adapter, six-stage text migration, sampled visual
  review, immutable editing and verified checkpoint contract.
- 2026-09-29: Added mock regression suites; hardened URL/resource validation,
  duplicate-key checks (including case aliases), strict subtitle timestamps and
  provider-independent visual checkpoint names.
- 2026-09-29: Added transport, image-budget, multichunk and replay regressions;
  validated outline chunks eagerly and allowed positive sub-100 ms subtitle cues.
- 2026-09-29T14:50:19+08:00: Reviewed ownership/consent/persistence boundaries,
  recorded passing package tests/vet and Linux compilation, and documented the
  race-detector and real-provider verification limitations.

Update: 2026-09-29T20:05:00+08:00 — Fixed the reported "extraction never
produces highlights" failure and reduced the analysis surface.

Root cause of the failure: sampled frame times were compared to model-returned
timestamps with a 1e-6 tolerance, but `Client.Complete` shows the model each
sample only as `Source timestamp %.3f seconds`. Media sampled at
`i*duration/count`, which for the reported 299.84-second upload yields values
such as 12.493333..., so a model that correctly echoed a supplied sample
returned 12.493 and was rejected with "cites a frame that was not supplied".
Every visual analysis of such a source failed at `01-visual-events`, after
paying for the request. Two coordinated changes: `internal/media` now rounds
sample times to milliseconds, so what is stored equals what is transmitted;
`internal/ai` matches within `frameTimeTolerance` (1e-3) and snaps accepted
timestamps to the exact supplied frame, so no provider rounding reaches scenes
or later stages. Bounds tolerate the same rounding and are then clamped to the
request window, so a value rounded just outside cannot become a negative or
overlong scene. Timestamps far from any supplied frame are still rejected, and
two values snapping to one frame are still duplicates.

`validateVisual` no longer reports fifteen distinct defects through one
message. Identity, text, score, kind, bounds, window, duration and frame-count
failures each name the rejected field. The combined message cost a live paid
run to attribute during this investigation.

Removed at the user's request: `Rewrite`, `Translate`, the `05-clustering`
collection stage and the per-draft output `Language`. The text pipeline is now
`01-outline`, `02-timeline`, `03-scoring`, `04-titles`, `05-drafts` — four paid
calls plus one deterministic local stage, one draft per selected candidate. The
`rewrite`, `translate` and `clustering` prompts, `internal/ai/editing.go` and
the render-time translation step in the worker are deleted. Draft language is
no longer part of the domain, API, checkpoint input or generated contract; the
visual prompt version moved to `general-visual-3`, so pre-existing visual
checkpoints fail stale before billing rather than replaying under old prompts.
Stored drafts from before this change still load; the obsolete `language` field
is ignored on read.

Verification: `go test ./...` passes (8/8 packages), `go vet ./...` clean, and
the frontend passes 40/40 tests, typecheck, generated-contract check and build.
New regressions reproduce the exact production failure and fail without their
fix: the rounded-timestamp acceptance test fails with the original
"cites a frame that was not supplied" error under the old tolerance, and the
sampling test reports 12.493333333333332 under the old sampler. A live
qwen3-vl-plus run against the user's 299.84-second upload confirmed the
frame-matching error is gone. That run then failed a *different* check, which
motivated the per-field diagnostics above; live highlight quality on this source
is still being confirmed and is recorded in verification.md, not claimed here.

Update: 2026-09-29T20:25:00+08:00 — Raised the transport budget for image
requests. Two consecutive live qwen3-vl-plus scans of the user's 300-second
source failed at `01-visual-events` with `ai timeout` after roughly 52 seconds,
each after the request had already been billed. A 60-image vision request needs
far longer to produce its first response byte than a text prompt, but both
shared a 45-second `ResponseHeaderTimeout`. Requests carrying images now use a
300-second deadline and the header cap is 240 seconds; text requests keep the
90-second budget. The parent context still shortens either, there is still no
automatic retry, and the client ceiling no longer cuts an image request short.
`TestImageRequestsGetALongerDeadlineThanText` pins the ordering of these
constants so a future edit cannot silently reintroduce a cap below the text
budget.

Update: 2026-09-29T21:10:00+08:00 — Restored upstream's tolerance for imperfect
model output in the visual pipeline. The port had converted three upstream
graceful fallbacks into hard failures, so a single imperfect field discarded an
entire already-billed paid analysis. A real 300-second educational video failed
repeatedly, each failure after the model call was paid for.

1. Empty label/evidence fallback. Upstream `Scene` in
   `../autoclip/backend/services/studio/models.py` declares
   `label: str = Field(default='片段', max_length=120)` and
   `evidence: str = Field(default='', max_length=1000)`, so a usable segment with
   no prose still yields an editable draft. `validateVisual` now substitutes
   `defaultVisualLabel` for an empty or whitespace label and accepts empty
   evidence. Length and UTF-8 limits stay strict and are now counted in
   characters, matching upstream `max_length` and `domain.Scene.Validate`; only
   emptiness falls back, never oversize. Label and evidence are rejected
   separately so each failure still names its own field.
2. Refine only the top candidate. Upstream refines exactly one event
   (`best = events[0]` in `../autoclip/backend/services/studio/intelligence.py`)
   in a single dense second pass. The port ran `02-visual-refine-NN` for up to
   six events, multiplying both cost and failure probability by six for a stage
   that only sharpens boundaries. Only the highest-scoring playable candidate is
   now reviewed, at the fixed locally authored stage name `02-visual-refine-01`
   spanning 40–80 percent. The remaining playable scan candidates keep their
   scanned (already trimmed and clamped) bounds and still become drafts.
3. Skip an unusable refinement instead of failing the run. Upstream drops a
   candidate whose dense review is unusable and continues (the `rejected` branch
   around `intelligence.py:207`). A new unexported `Error.refineRejected` tag,
   set only by `markRefineRejection` on an `invalid_response` originating in the
   refine stage's own decode/validate step, is the sole failure class
   `AnalyzeVisual` drops. Cancellation, deadline, auth, rate-limit, endpoint,
   checkpoint and `no_highlights` failures carry no tag and still abort, so
   `errors.Is(err, context.Canceled)` and `context.DeadlineExceeded` identity is
   preserved. A skip requires another playable candidate to fall back to, is
   surfaced through `r.notify` as the `02-visual-refine-skipped` progress stage
   rather than silently thinning the result, and the failed paid call is never
   repeated. Progress percentages stay monotonic within 0–100.

The visual prompt version moved to `general-visual-4`, so pre-existing visual
checkpoints fail stale before billing rather than replaying under the changed
refinement and validation semantics. Text checkpoint fingerprints are unaffected.
`prompts/visual.txt` and `prompts/refine.txt` now state that a short label and
empty evidence are acceptable and must not be padded with invented prose.

Verification: `go build ./...`, `go vet ./...` and `gofmt -l internal/ cmd/`
(empty) are clean; `go test -mod=readonly -timeout=600s -count=1 ./...` passes
all 8 packages. Negative controls were run for each change — the fix was
temporarily reverted, the new test was confirmed to fail, then the fix was
restored and the test confirmed to pass:
- Requiring nonempty label again failed `TestVisualAcceptsEmptyLabelAndEvidence`
  with `ai invalid_response (01-visual-events): Visual event label must be valid
  UTF-8 text within 120 characters.`
- Restoring the six-event refine loop failed
  `TestVisualRefinesOnlyTheTopCandidate` with
  `exactly one candidate may be refined, got 2 paid review calls`.
- Aborting on any refine failure failed
  `TestVisualSkipsUnusableRefinementInsteadOfFailing` with `an unusable dense
  review must not discard the billed scan: ai invalid_response
  (02-visual-refine-01): Boundary refinement must preserve exactly the reviewed
  event identity.`
- The inverse control also holds: widening the skip to tolerate any refine
  failure failed `TestVisualRefineSkipDoesNotSwallowOtherFailures/auth` with
  `want auth, got ai endpoint (03-visual-titles)`, proving the guard test
  catches an over-broad skip rather than only a missing one.

Two pre-existing assertions were updated because the mandated behavior changed,
not because they were wrong: the `label` subcase of
`TestVisualValidationNamesTheFailedField` now uses an oversize label instead of
an empty one (an empty label is valid by design as of this change) and gained an
`evidence` subcase; `TestVisualIndependentEventsAreNotMerged` now expects three
model calls instead of four, since only one candidate is reviewed. The
frame-timestamp tolerance, the longer vision transport budgets and
`trimVisualEvent` are untouched, and their regressions still pass.

Not verified: no real-provider call was made during this change, so live
highlight quality on the reported educational source is not established, and
neither is real-model behavior under the relaxed label/evidence rules. Tests use
only loopback `httptest` servers and generated image fixtures. Whether one dense
review instead of six measurably degrades boundary accuracy on real footage is
unmeasured; it matches upstream, which is the only evidence offered. The
race detector still could not run on this host (`CGO_ENABLED=0`, no C compiler).
This change edits only `internal/ai/**` and this document.

Update: 2026-09-30T11:20:00+08:00 — Fixed a `profileFor` defect that made
subtitle analysis fail outright for whole classes of source, and added the
matrix test that would have caught it.

`profileFor` clamps the profile's Max to the user's requested clip length, then
clamped Min to that same value. The window therefore collapsed to a single
point whenever the request was at or below the tier's natural minimum.
`validateTimeline` then required a span of exactly Max seconds while
`refineTimeline`'s extension loop can only extend to a cue edge strictly below
Max, so every candidate was dropped and the stage ended with "No timeline
survived cue-boundary, speech-coverage and duration safeguards" — after the
outline and timeline calls had already been billed.

Measured before the fix, the collapse covered more than first suspected: every
source at target 15 or below (including short ones), every source past eight
minutes at the default target of 30, and every source past forty minutes at 60.
Only the short tier above target 20 escaped, which is why the reported
299.84-second video worked and a ten-minute one would not have.

Min now yields to `Max/2` instead of Max. Upstream `pipeline/quality.py`
`profile_for(total_sec)` takes only the source duration and never constrains
clip length by the requested target at all; keeping the target as an upper
bound is a deliberate divergence, since it is what the user asked for, so the
lower bound is what gives way.

The existing tests only exercised `profileFor` at durations 3/60/100/120, all
in the short tier above target 20, so none of them reached the collapse. The
new `TestProfileAlwaysLeavesAUsableDurationWindow` sweeps ten durations across
all three tiers against five targets and asserts a non-degenerate window, plus
that Max never exceeds the request or the source.
`TestTenMinuteSourceSurvivesTimelineAtDefaultTarget` proves the end-to-end case
with real cue-aligned bounds. Negative control: restoring `p.Min =
math.Min(p.Min, p.Max)` fails the matrix test with `window collapsed to
[30,30]` and thirteen further combinations.

Not verified: no real-provider call was made for this change. A live subtitle
analysis of a source longer than eight minutes has not yet been run, so the
end-to-end claim rests on the unit-level timeline test, not on a billed run.

Update: 2026-09-30T12:30:00+08:00 — Aligned the remaining validation tolerance
with upstream and closed two clip-quality gaps. The recurring defect class was
that upstream treats a model deviation as a per-item problem (skip it, clamp it,
default it) while the port treated almost all of them as whole-run failures,
discarding analyses whose model calls were already billed.

Text pipeline. Scoring gained `normalizeScore`, porting `quality.py _to_score`,
which rescales a 0-10 or 0-100 answer, and `alignScores`, porting `align_scores`
and `step3_scoring.py`, which back-fills a skipped candidate at a neutral 0.5
rather than failing. A duplicated ID discards both copies rather than picking one
arbitrarily, and a fabricated ID is dropped. Outline chunks now follow
`step1_outline.py`: a chunk with nothing to outline is skipped and reported
through progress as `01-outline-partial-<n>`, a duplicate title keeps its first
occurrence, and a topic with no bullet points is still a topic; only a globally
empty outline fails. The deliberate cost protection is preserved and was the
reason for splitting the two cases — a *structurally malformed* chunk still stops
before the next paid call, because a malformed first response usually means every
later one will be too, and `TestBadOutlineChunkStopsBeforeNextPaidCall` still
passes. Skipping an outline chunk exposed a bug this change introduced: the
timeline stage would then pay for a chunk with zero topics, so it now skips those.
Timeline items are clamped back into their chunk and individually skipped rather
than failing the stage, per `step2_timeline.py`; a timeline with nothing placeable
still fails. `boundary` can no longer return a non-cue second: it falls back to
the first cue reaching past the request and then to the last cue, as
`_snap_start`/`_snap_end` do, which removes an abort whenever a bound landed in a
silent gap wider than the three-second window. Titles gained `alignTitles`,
porting `step4_title.py`, falling back to the clip's own verified label.

Visual pipeline. A missing `kind` defaults to `unknown` (upstream's
`HighlightCandidate` default, with its "Missing annotations remain usable" note);
a null or omitted `score` defaults and is rescaled by the same
`normalizeScore`; more than twelve events truncates to the first twelve as
`raw_events[:12]` does; and two cited timestamps that snap to one supplied frame
are deduplicated instead of rejected, which is routine when sampling is coarse.
An event citing no in-bounds frame at all is still rejected — tolerance must not
become fabrication. `validateVisual` now takes a pointer, since truncation and
rescaling have to reach the caller.

Client. `len(Choices) != 1` became `== 0`, the finish-reason allowlist became a
denylist of the reasons that actually mean incomplete or withheld output
(`length`, `content_filter`, `max_tokens`, `tool_calls`, `function_call`,
case-insensitively), and the top-level `error` key is judged by `reportsError`
rather than by being absent. Several compatible gateways emit `"error":{}`
alongside a valid 200, or report `eos`/`end_turn`/`STOP`, and were unusable here
while working in the Python app. A populated error object still wins over any
choice.

Clip quality. `addContext` ports `assemble_sequences`, giving every visual clip a
lead of up to 1.5 seconds — only from slack the requested duration leaves — and a
tail of up to 2 seconds, bounded by the source and the target. The port had no
equivalent, so clips opened mid-action and cut on the final frame of evidence.
It runs after the scan and review have verified bounds and before `draftPlans`,
which is upstream's order, and it can only widen a span, never narrow the
verified evidence.

Category prompts. `internal/ai/prompts/<category>/` now holds outline and
timeline prompts for the seven upstream genres, resolved by `promptFile` with
per-stage fallback to the shared prompt exactly as `get_prompt_files` does. The
path is assembled only from a fixed allowlist plus the caller's literal stage
name, so provider text can never reach the embedded filesystem; a traversal
attempt in the category name resolves to the shared prompt. `AnalysisOptions`
gained `Category`, validated at both the API boundary and in `normalizeOptions`,
and an unknown value is rejected before any paid call rather than silently
falling back. The profile also gained `Tier`, `TopicsLow/High` and `Guidance`,
porting `prompt_hint` — including its assertion that these parameters outrank any
figure in the prompt body, which is what kept the model returning counts and
durations that then collided with the strict downstream checks.

Verification. All 8 packages pass, vet and gofmt clean, frontend 40/40 with
typecheck, contract check and build. Six negative controls were confirmed by
reverting each fix and observing the specific new test fail: the kind default,
the twelve-event truncation, the title fallback, the client's single-choice
requirement, the context buffer (`draft lost its context buffer: [10,24]`) and
the guidance priority assertion. Eleven pre-existing assertions that encoded the
rejected-by-design behavior were rewritten to assert the new guarantee rather
than retargeted, and each is noted at its call site.

Live paid verification: a `knowledge`-category subtitle analysis of the
299.84-second Bilibili import completed and produced six drafts, one of which
carries the 0.5 neutral score, confirming the scoring fallback on a real
provider response rather than only in mocks.

Update: 2026-09-30T12:49:00+08:00 ? Completed the AI-only analysis subtask.

- Fixed generic stage validation to accept a pointer, preserving canonical slice
  lengths and scalar fields through the return value, checkpoint and replay.
  End-to-end regressions run AnalyzeVisual with thirteen valid events and with
  a semantically broken thirteenth event (nil bounds/score): twelve audited
  candidates, six selected drafts, exactly one review and three model requests;
  replay makes none. Cached refine corruption cannot be downgraded to a skip.
- Malformed outline JSON/schema now stops on the failing chunk, including
  failures after an earlier successful chunk. Only validated empty arrays can
  degrade; null is invalid. Independently chained chunk checkpoints preserve
  completed outline/timeline work after later failures. Model, source, version,
  corruption and full-replay cost regressions cover partial checkpoints.
- Duration zero remains automatic for text. Explicit subtitle content/highlight
  targets preserve a 200-second semantic unit in regressions, rather than
  truncating it to 15/30 seconds. Source and domain's 1800-second budget remain
  safety limits. New subtitle drafts default to no burning; explicit
  BurnSubtitles=true and replay are tested. Visual zero defaults to a hard
  30-second cap, and explicit caps cannot be overridden by wide frame citations.
- Analysis retries exist only at the model-request boundary: three total
  attempts for recognized network/429/5xx failures, safe logs, cancellable
  100/200 ms delays, no authentication/model/schema/cancellation retries and no
  nested stage loop. Direct Complete and smoke retain one-attempt semantics.
- Added AnalyzeVisualWithSampler; the old wrapper remains. Fresh sampling is
  only for the best candidate's +/-2-second window, at most 25 images, step
  max(1, window/24). Tests prove the review receives new image bytes and new
  timestamps absent from the coarse scan, and changed dense image bytes fail
  checkpoint replay before another paid call. Sampler failures are explicit.
- Added MakePromos with one-to-six verified candidate inputs and at most three
  initial single-scene plans. Only label/evidence plus opaque mapping IDs go to
  the text model, including for Mode=visual; verified scene bounds stay fixed.
  Added Screen with at most four stills, strict recommendation schema and an
  explicit no-speech-analysis disclaimer. Neither API grants authorization or
  implicitly starts another goal workflow. No translation/rewrite/collection
  entry point was restored. Worker/media/ASR/export routing remains outside AI.

Verification (local mocks only; GOPROXY=off and GOSUMDB=off):
- go test -mod=readonly -count=1 -timeout=90s
  -coverprofile=internal/ai/coverage.out ./internal/ai: passed in 7.311s,
  statement coverage 91.2%.
- go vet -mod=readonly ./internal/ai: passed (exit 0, no findings).
- gofmt applied to the touched Go sources/tests.
- Sandbox Go-cache access was denied; the same offline test/vet command was
  rerun with explicit escalation. No paid or external-network requests were
  made; all HTTP test requests use loopback or an in-process transport.
- Scope: only internal/ai/** and this file were edited. Existing uncommitted
  changes and deleted editing/prompts remain intact. Repository-wide worker,
  media, API and UI verification belongs to the main agent. Race detection and
  real-model highlight/promo quality were not verified in this subtask.

Update: 2026-09-30T12:53:54+08:00 -- Integration feedback: distinct initial
promo openings and advisory subtitle tier limits.

- Replaced the one-title-per-candidate promo schema with strict
  {candidate_id,title,hook} plans, one to three total. A single event may have
  three genuinely different initial openings; different text candidates can
  also be selected from the full six-input budget. Duplicate normalized
  openings, unknown IDs, scene edits and oversized arrays fail without repair
  or another model call. Scenes remain exactly the verified input scenes.
- Restored profileFor's original 150/300/480-second preferred maxima, capped
  by source duration. User duration is still only a target. A separate hard
  safety budget remains at most 1800 seconds and is explicitly not a desired
  clip length. Valid over-tier spans are not trimmed: quality.over_tier and
  02-timeline-over-tier surface them on initial execution and replay. Unsafe
  over-budget spans fail with a request for shorter complete topics.
- Added scoped text/promo fingerprint versions; visual cache identity stays
  unchanged. Exported signatures, screening and sampler behavior are unchanged.
- Regression coverage includes one scene/three unique openings with stable
  replay, one-to-three arbitrary text candidate references, duplicate-opening
  and schema rejection, legacy promo checkpoint rejection without spending,
  tier boundaries, preserved over-tier spans and warning replay, and hard
  safety/coverage failures. Updated the former low-level hard-trim assertion
  to require preservation plus an over-tier quality count.

Verification with GOPROXY=off and GOSUMDB=off:
- go test -mod=readonly -count=1 -timeout=90s
  -coverprofile=internal/ai/coverage.out ./internal/ai: PASS, 6.417s, 91.6%
  statement coverage.
- go vet -mod=readonly ./internal/ai: PASS.
- gofmt -l internal/ai: no output; git diff --check for the owned paths: clean.
- Go-cache access denial required an escalated rerun. Tests remain local mock
  only, with no paid calls, external networking, rewrite, translation or
  collection entry point. Semantic diversity and completeness on real model
  output remain unmeasured; tests enforce bounds, mapping and obvious duplicate
  rejection, not a proof that differently worded hooks differ in meaning.

Update: 2026-09-30T13:15:00+08:00 -- R2-03 closed: durable visual refinement
decisions and deterministic replay after degradation.

- Reproduced the review failure before changing behavior: a first-run invalid
  refine ID was skipped, but the next run's valid answer caused an extra paid
  request and a stale `03-visual-titles` predecessor. When titles had failed
  instead, retry changed the selected draft set rather than preserving it.
- `visual.go` now checkpoints `refineDecision{status,event_id,result?}` through
  the existing synced, atomic stage writer. Both accepted and skipped decisions
  are semantically validated and advance the chain. Only fresh, tolerated
  refine-output rejection with another playable event may create a skip;
  rejected response/error prose is never saved. Checkpoint validation errors
  are never converted into skips.
- Skip progress remains explicit, including on replay. The checkpoint is
  committed before notifying the skip or calling titles, so downstream failure
  and cancellation at the skip callback do not repeat the review. Dense
  sampling/fingerprint checks, accepted review validation, and the bounded
  transport retry policy are unchanged. Public signatures are unchanged.
- Visual fingerprint version is `general-visual-6`. Existing version-5 visual
  directories fail before new paid calls, with files preserved; they need a
  fresh explicitly consented run, not an automatic migration or replacement.
  This does not bump text/promo versions or fix worker attempt ownership.
- New `refine_replay_test.go` covers supplied/dense-frame replay with a provider
  that would now succeed, downstream titles failure, callback cancellation,
  corrupt JSON and checksum-valid semantic corruption, no-fallback/auth/empty
  review failure guards, and version-5 rejection before spending. The old
  skip replay assertion now requires zero extra requests instead of one.

Verification (GOPROXY=off, GOSUMDB=off, GOTOOLCHAIN=local; loopback only):
- Before fix: targeted new regressions failed, log
  `internal/ai/.r2-03-before.log`.
- `go test -mod=readonly -count=1 -timeout=90s -v ./internal/ai`: PASS,
  6.540s, 107 top-level tests / 359 passing entries including subtests;
  log `internal/ai/.r2-03-after.log`.
- The original independent review probe
  `TestReview2SkippedReviewThenSuccessfulReviewMustReplay`, loaded through
  its artifacts overlay, now PASS; log
  `internal/ai/.r2-03-review-regression.log`.
- `go vet -mod=readonly ./internal/ai`: PASS, log
  `internal/ai/.r2-03-vet.log`; touched Go files are gofmt-clean and scoped
  `git diff --check` reports no whitespace errors.
- Scope: `internal/ai/visual.go`, `visual_test.go`, new
  `refine_replay_test.go`, this document, and the requested review-2 closure.
  No domain/store/worker/httpapi/frontend/media changes, real provider calls,
  existing database operations, or race-detector verification in this fix.
