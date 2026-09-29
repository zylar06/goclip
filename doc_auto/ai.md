# AI adapters and analysis

Updated: 2026-09-29

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
func Rewrite(ctx context.Context, client *Client, draft domain.Draft, instruction string) (domain.Draft, error)
func Translate(ctx context.Context, client *Client, draft domain.Draft, cues []domain.Cue) (domain.Draft, []domain.Cue, error)
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
- Requests are nonstreaming Chat Completions POSTs, with no automatic retries,
  no redirect following and no replayable request body. API keys are used only
  in Authorization. TLS certificate verification is not disabled.
- 90-second per-request cap, 10-second connect/TLS caps, 45-second response-header
  cap; parent context can shorten these. Responses: 2 MiB; headers: 64 KiB.
  Prompts: 512 KiB. Normal output budget: 8192 tokens; smoke: 32 tokens.
- Frames are local PNG/JPEG files, decoded/validated before use, encoded as
  `image_url` base64 data URLs, paired with source timestamps. File paths are
  never sent. Limit 60 images/request, 4 MiB/image, 16 million pixels/image,
  24 MiB aggregate image bytes/request. No remote image downloads.
- `Test(false)` checks a one-request `OK` response; `Test(true)` sends a genuine
  generated 8×8 red PNG and checks `red`. Tests are independent and have no
  fallback between modalities. Calling a smoke test can incur cost.
- Exported `*ai.Error`: `Code`, `Message`, `Stage`, `HTTPStatus`, `Retryable`.
  Codes: `auth`, `model`, `endpoint`, `timeout`, `rate_limit`, `invalid_response`.
  Provider status and recognized machine error labels drive classification;
  ambiguous 400/404 errors are endpoint errors rather than guesses from prose.
  Errors never include key, URL, server body, prompt, local path or raw cause.
  Context sentinels remain discoverable with `errors.Is`.
- `Retryable` is informational, never permission to launch another request.
  Invalid inputs, stage schemas, checkpoints and quality failures use
  `invalid_response` with a local explanation. Progress callback failures stop
  work and are sanitized; cancellation/deadline identity is preserved.

## Text pipeline and quality

The embedded prompt ports cite their upstream files in `../autoclip/backend`.
The logical stages are `01-outline`, `02-timeline`, `03-scoring`, `04-titles`,
`05-clustering`, `06-drafts`. Outline/timeline process chunks of at most roughly
30 source minutes or 64 KiB subtitle text plus cue overhead, with at most 256
chunks/topics. Each outline chunk validates before the next paid call. Scoring and titles
use real subtitle excerpts, not only model summaries.

- Outline covers meaningful beginning/middle/end topics; no external facts.
- Timeline uses topic IDs and source cue bounds; numeric seconds and strict
  SRT/MM:SS timestamps are parsed. Boundaries snap within 3 seconds or to a
  containing cue. Hallucinated/out-of-chunk bounds fail explicitly.
- Upstream duration tiers (20/45/90-second minima, 150/300/480-second maxima)
  are capped by requested duration and actual source duration. Tiny sources
  are supported. Bounds extend/trim at cue edges, not in the middle of speech.
- Overlap of at least 50% of the shorter segment merges; lesser overlap shifts
  to a later cue. Short neighbors can merge across at most 5 seconds.
  Unsupported short/silent candidates are counted in checkpoint quality reports.
  Retained spans require at least 50% union subtitle coverage, unique sequential
  IDs, nonoverlap, valid duration and verbatim cue-derived evidence.
- Scoring strictly maps every ID once to a finite score in [0,1]. Threshold
  0.7 selection is filled to the tier's minimum from existing valid scores, then
  capped at the tier maximum. Missing/model-failed scores are never fabricated.
- Titles map selected IDs exactly. Clusters use 2–5 verified IDs, may be empty,
  and cannot duplicate membership or exceed 30 minutes.
- Draft assembly is deterministic apart from new IDs/timestamps. It creates
  individual drafts and optional collection drafts; all use verified scenes.
  Stage six does not call a model or export video. All scored candidates are
  returned, including those not selected for drafts.

Malformed JSON, duplicate keys, extra top-level values, unknown schema fields,
excessive nesting, missing required semantic values and model truncation/refusal
are errors. One complete Markdown JSON fence is accepted, not arbitrary extraction
or JSON repair. There are no upstream silent catch/fallback or paid retry loops.

## Visual pipeline and consent

Orchestration must require explicit consent. `AnalyzeVisual` additionally rejects
missing `AllowVisual` or `Confirmed`, and rejects subtitle mode. Text failure
never switches to vision. The media owner supplies immutable frame files and
the source duration; this package never launches FFmpeg.

Up to 600 input frames (64 MiB total) are fingerprinted by image contents and
timestamps. Up to 60 evenly distributed frames form `01-visual-events`. Models
return up to 12 distinct events with exact supporting sample timestamps,
bounded evidence, event kind and score. All times and frame references validate.
Pure menu/reward-screen/loading events are excluded by explicit kind, not labels.
Up to six strongest usable events each receive `02-visual-refine-<rank>`:
review window is the original event ±2 seconds within the source; at most 25
supplied nearby frames are used. Identity, duration and local sample bounds
remain checked. A review can filter an event as non-play content; no supported
result is an error, not invented success. Independent events are not merged.
Stage names use local ranks, never model-returned text or IDs, to prevent
provider-controlled content from entering diagnostics or checkpoint filenames.

`03-visual-titles` uses the verified evidence (no extra image request);
`04-visual-drafts` builds individual drafts with subtitles disabled.
Returned candidates include filtered scan events and reviewed replacements.
Static-frame classification/ranking/boundaries remain estimates requiring human
review. Dense review is possible only when supplied frames are dense; the adapter
cannot acquire new frames. Promotional goals influence evidence-grounded hooks,
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
Outline/timeline chunks share one stage checkpoint: a failure before that stage
completes can require redoing that stage's earlier chunks on explicit retry.

SQLite remains task authority. The caller supplies a private task-scoped directory,
serializes retries and must not concurrently mutate input images/checkpoints.
Checkpoints can contain sensitive source/model text and must stay access-controlled.
Checksums detect accidental corruption, not hostile local file rewriting.
Progress callbacks receive stage name and percent **0–100**, including replay;
callback/context failure immediately stops subsequent model calls.

## Editing and translation

Rewrite changes title/hook/scene labels only, preserving source evidence, bounds,
order, identity, revision and rendering settings. Translate with `source` is a
zero-request copy; other supported languages (`zh`, `en`, `ja`) translate
title/hook/labels and only cues intersecting draft scenes. The full cue list is
returned with original indexes, order and timestamps; unrelated cues are unchanged.
These functions do not persist or increment revisions; the store owns that.

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

Update log:
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
