# Native media

Updated: 2026-09-29

## Boundary and dependencies

`internal/media` implements local subprocess execution, strict source URLs,
subtitles, whisper.cpp ASR, JPEG sampling, deterministic title PNGs and MP4
rendering. It imports only the shared domain, Go standard library and
`golang.org/x/image v0.31.0` (transitive `golang.org/x/text v0.29.0`).
No Python business/runtime code, shell invocation, cloud API, paid request,
telemetry or installed-desktop mutation is used.

Runtime tools: FFmpeg with libx264, AAC, libass, overlay and gblur; ffprobe;
yt-dlp; whisper.cpp CLI plus a compatible local ggml model. Docker's pins are
owned by the root build configuration. `Whisper` must be the whisper.cpp CLI,
not the unrelated Python Whisper command.

## Public API

```go
type Config struct {
    FFmpeg, FFprobe, YTDLP, Whisper, Model, FontDir string
    MaxDuration float64
    MaxBytes int64
}
type Info struct {
    Duration float64
    Width, Height int
    HasAudio bool
}
func New(Config) *Tools
func ValidateSourceURL(raw string) error
func OutputDimensions(info Info, aspect string) (int, int)
func (*Tools) Probe(ctx context.Context, path string) (Info, error)
func (*Tools) Download(ctx context.Context, url, directory, cookies string,
    progress domain.ProgressFunc) (video, subtitle string, err error)
func (*Tools) DownloadWithSubtitles(ctx context.Context, raw, directory, cookies string,
    includeSubtitles bool, progress domain.ProgressFunc) (video, subtitle string, err error)
func (*Tools) Transcribe(ctx context.Context, video, outDir string,
    progress domain.ProgressFunc) ([]domain.Cue, error)
func (*Tools) Sample(ctx context.Context, video, outDir string, duration float64,
    progress domain.ProgressFunc) ([]domain.Frame, error)
func (*Tools) SampleAt(ctx context.Context, video, outDir string, times []float64,
    progress domain.ProgressFunc) ([]domain.Frame, error)
func (*Tools) Thumbnail(ctx context.Context, video string, at float64) ([]byte, error)
func (*Tools) CompatiblePreview(ctx context.Context, source, outDir, output string,
    progress domain.ProgressFunc) (Info, error)
func (*Tools) Render(ctx context.Context, source, outDir, output string,
    draft domain.Draft, cues []domain.Cue, progress domain.ProgressFunc) (Info, error)
func (*Tools) TitlePNG(draft domain.Draft, width, height int) ([]byte, error)
func TitlePNG(draft domain.Draft, width, height int) ([]byte, error)
func ParseSRT([]byte) ([]domain.Cue, error)
func FormatSRT([]domain.Cue) []byte
```

Zero limits default to 7,200 seconds / 4 GiB. Negative/nonfinite limits fail
operations; `New` intentionally has no error return. Executable defaults are
`ffmpeg`, `ffprobe`, `yt-dlp`, `whisper-cli`. Set absolute paths in deployment.
`FontDir` defaults to `assets/fonts` found while walking up from the working
directory, or next to the executable. Explicit relative config paths resolve
when constructing `Tools`.
Executable names found on PATH are resolved so yt-dlp receives an actual
`--ffmpeg-location` path. Missing optional tools fail when invoked, not at
construction time.

`domain.ProgressFunc` percentages are 0–100; nil means unknown, not zero.
Callbacks are serialized within an operation and must return promptly.
Errors returned by callbacks cancel the native operation and propagate.
FFmpeg progress uses `out_time_us`, never a wall-clock estimate. Render caps
encoding progress at 98, validates at 99, and emits 100 only after publication.
Download reports **per-stream** known-byte progress under `download-stream`;
it can restart for a new stream, and unknown totals remain nil.
Whisper progress comes from its native callback log, capped below 100 until
SRT validation. Sampling reports completed frames / total requested frames.

## Source validation and download

Only HTTPS video-page URLs on exact `youtube.com`, `www.youtube.com`,
`m.youtube.com`, `youtu.be`, `bilibili.com`, `www.bilibili.com`,
`m.bilibili.com` hosts are accepted. Supported paths are YouTube `/watch?v=...`,
`/shorts/<11-character-id>`, youtu.be `/<id>`, and Bilibili
`/video/BV...` or `/video/av...` with optional numeric `p`.
Credentials, every explicit port (including 443), IP literals, encoded paths,
backslashes, redirector links (including b23.tv), channel/playlist/embed/live
pages and arbitrary hosts are rejected. Accepted URLs are canonicalized and
unrelated query/fragment fields discarded before execution.

yt-dlp runs with no user configuration, plugin directories, cache, playlist or
shell; bounded socket retries, byte/duration filters and no-live filtering.
It merges/remuxes to MP4 and optionally converts Chinese/English subtitles to
SRT. Unsupported/unavailable/filtered videos are explicit failures even if
yt-dlp exits zero. Final media is probed and requested subtitles parsed before
success.

`Download` delegates to `DownloadWithSubtitles(..., true, ...)`, preserving its
existing platform subtitle behavior and errors. With `includeSubtitles=false`,
yt-dlp receives `--no-write-subs --no-write-auto-subs`, no subtitle language,
format or conversion options; no downloaded sidecar is discovered, read or
validated, and the returned subtitle path is empty. This permits callers with
an explicitly uploaded SRT to avoid unrelated platform subtitle failures.
Video validation, URL restrictions, progress errors, cookie isolation, native
deadlines and failure cleanup are unchanged. The enabled path still propagates
native conversion, sidecar read/size and SRT parse failures; it does not silently
fall back to omitting an invalid requested track.

`cookies` is a Netscape-cookie **file path**. A 0600 private copy prevents
yt-dlp cookie-jar writeback into the caller's file, and is deleted on every exit.
On success, returned files reside in a unique retained child of `directory`;
the caller owns their lifecycle. No available subtitle returns `subtitle=""`.
Failures remove the private download directory. `MaxBytes` applies to each
native download and the final video; intermediate split audio/video streams
can together consume more than the final-file limit.

## Probe, transcription and sampling

Probe accepts a nonempty regular local file within the byte limit, forbids
network input protocols, requires finite video duration and dimensions, and
accounts for 90/270-degree display rotation. An attached-picture primary video,
unbounded/live input, malformed JSON or native failure is an error.

ASR extracts mono 16 kHz signed 16-bit PCM, then validates the RIFF/WAV chunks.
Entire near-silence (all decoded samples have magnitude <=32, about -60 dBFS)
returns a non-nil empty cue slice **without invoking whisper or requiring a
model**. This prevents silence hallucinations; it is not a general speech/VAD
classifier. Non-silent audio requires a nonempty model and runs whisper.cpp with
`--language auto --output-srt --output-file transcript --print-progress`.
Empty SRT is valid; missing/malformed/out-of-range SRT is an error. No fake text,
placeholder transcription or paid fallback is generated. No audio track is an
explicit error. WAV/SRT work files are always cleaned.

Sample uses the first `duration` seconds (zero means the full source), rejects
out-of-range windows (1 ms source-end tolerance), and writes at most 60 JPEGs.
The upstream interval is `max(2, duration/60)`; seek targets are `i*interval`,
rounded to milliseconds, with the upstream 100 ms end guard. Tiny videos still
receive the first frame at zero. Returned timestamps are source-time seek
targets, not inferred wall-clock times or decoded frame PTS.

`SampleAt` accepts 1–60 finite, nonnegative source timestamps in caller order;
it never sorts, truncates or silently drops requests. It copies and rounds the
list to milliseconds. Duplicate times after rounding, or either original or
rounded times at/after the source duration, fail before creating work files.
This is the worker/AI dense second-pass sampling API. Successful sampling
directories are retained for the caller; failures (including progress callback
errors after a frame) clean the whole private directory.

`Thumbnail` returns JPEG bytes only, without a browser-visible filesystem path
or scratch image. It rejects nonfinite, negative and end/out-of-range times.
FFmpeg accurate seek keeps the supplied target (six-decimal native argv); at a
between-frame time it returns the first decoded frame at/after that target.
Both thumbnail and sample extraction share bounded JPEG capture, header/pixel
validation, a 2 MiB output limit and aspect-preserving downscale to at most
640 pixels on either edge, without upscaling. Native failures, missing/corrupt
JPEGs, excessive dimensions/bytes and deadlines are explicit errors. Thumbnail's
30-second deadline covers probing plus extraction; callers may shorten it.

## Subtitle and render timeline

SRT supports UTF-8/BOM, CR/LF, optional numeric indices, multiline text and comma
or dot millisecond separators. Inputs are capped at 8 MiB, 100,000 cues and
16 KiB per cue; invalid timestamps, empty cues and invalid UTF-8 fail.
Overlap and cue order are preserved. `FormatSRT` rounds to milliseconds and
ensures positive sub-millisecond cues remain at least 1 ms long.
Its fixed no-error signature returns **nil for invalid cues**, distinct from
non-nil empty bytes for an empty valid list. Native operations validate first.

Scenes are independently sought, normalized to 30 fps and concatenated in draft
order (including repeats). Each duration is rounded once to a frame, within
1/60 second of the requested scene length. Audio is resampled/padded/trimmed to
the same frame-quantized durations. Subtitle intersections are clipped per scene
and offset by these exact rendered durations, preventing cumulative timing
drift; ASS text escaping prevents cue content from becoming filter syntax.

`OutputDimensions` is the single preview/export dimension contract:
- original: preserve aspect, no upscale, longest edge <=1920, even dimensions;
- portrait: 1080x1920;
- landscape: 1920x1080;
- invalid aspect/original dimensions: `(0, 0)`.

Fit letterboxes; crop fills using normalized `CropX`; blur uses a blurred fill
background plus centered fitted foreground. Original audio is retained only
when requested and present; otherwise the output has no audio stream.
User text/paths never enter filter syntax. The generated graph is a **single
`-filter_complex` argv entry**, compatible with Debian-era and new FFmpeg builds
that removed `-filter_complex_script`. Scratch filter asset names are fixed and
relative, avoiding Windows drive/quote escaping errors.

Source-end validation uses a 1 ms tolerance (matching the domain contract;
changes to `internal/domain` are owned by the main agent). When `Subtitles` is
false, Render neither validates nor remaps cues and creates no ASS/font assets.
Malformed, absent or valid cues cannot cause a second subtitle layer in that
mode; pre-existing hard subtitles are naturally retained in the source pixels.

## Browser-compatible source preview

`CompatiblePreview` is independent of draft rendering: it transcodes the full
source into H.264/yuv420p plus AAC when audio exists, without title rasterization,
subtitle burning, scene cuts, concat, frame-rate normalization or font access.
It selects only the first video and audio streams and drops soft subtitles/data,
chapters and metadata. Dimensions follow `OutputDimensions(info, "original")`.
`-copyts` preserves input timing and A/V offsets, including nonzero input origins;
there is no `-start_at_zero` or per-stream PTS reset. VFR frames use passthrough
with a numeric microsecond encoder time base. Sources whose encoded output
does not satisfy duration validation fail rather than publishing a misleading
timeline.

Source files are read-only. Preview reuses the subprocess limits, private work
directory, duration/dimension/audio/byte checks, sync, immutable target checks
and atomic publication used by Render. Both operations additionally verify the
actual MP4 container, exactly one H.264/yuv420p video, zero or one AAC audio, and
no extra streams before publication. Preview progress stages are `preview`
(0–98 encoding, 100 only after publication) and `preview-validate` (99).
Callback/cancellation/validation failure leaves no published preview.

## Titles and fonts

Preview and export call the same Go `x/image/opentype` PNG rasterizer. Hook is
used when nonblank, otherwise title. The nine deterministic native styles are
plain, impact, card, comic, neon, arena, editorial, pixel and frosted.
Scale, position, accent, Unicode glyph fallback and explicit/automatic wrapping
are honored. Missing fonts/glyphs and text that cannot fit fail explicitly.
Transparent canvases are limited to 16 megapixels.

Legacy `TitleTemplateVersion` 0–6 is accepted for saved-draft compatibility;
native style selection is not pixel-identical to individual Python template
revisions. Frosted is a translucent PNG panel, not a blur of source video.
Optional title motion is a bounded 180 ms entrance; pixel/frosted stay static.
Titles are visible only while output time is strictly less than 4 seconds.

Fonts and all upstream OFL licenses are copied in `assets/fonts`. The original
Noto variable font is retained for provenance but rejected by the Go loader.
The static Bold instance has no fvar/gvar/CFF2 tables and is used for CJK title
fallback and libass subtitles. `static-manifest.json` records source/output
hashes, the one-time FontTools 4.60.1 conversion and fixed weight 700. No
FontTools/Python runtime is needed.
License checksum tests allow only LF/CRLF normalization against the upstream
manifest's mixed line endings; binary font checksums are exact.

## Process safety and publication

Every command has a context deadline. Maximum/default operation budgets:
probe 30 seconds; download 2 hours; audio extraction 1 minute + 2x source
duration (cap 30 minutes); ASR 10 minutes + 20x duration (cap 4 hours);
each sampled frame 30 seconds; thumbnail 30 seconds including probe;
render/compatible preview 5 minutes + 30x duration (cap 2 hours).
Caller deadlines can shorten all of these.

Linux/Unix starts a separate process group and kills the group on cancellation.
Windows hides tool windows and uses bounded `taskkill /T /F`, with explicit
fallback kill/error propagation. Pipe wait delay is 2 seconds. Diagnostic tails
are capped at 64 KiB and parsed lines at 64 KiB; oversized captures fail rather
than silently truncating JSON. `ToolError` exposes `Tool`, `Output`, `Err` and
unwraps the underlying failure/cancellation without embedding the argv.

Render validates the draft and writes `partial.mp4` in a unique private child
of outDir. H.264/yuv420p plus optional AAC is checked with ffprobe for size,
duration (within 120 ms), dimensions and audio presence; file contents are
synced before same-filesystem rename. `output` must be an MP4 filename or
absolute path **directly inside outDir**. Existing outputs are rejected; callers
must serialize identical targets. Cancellation/failure does not publish partial
media, and completion-callback failure removes the just-published file.
Cleanup errors remain visible. Atomic visibility is provided; directory fsync
and crash-durable publication are not guaranteed by this package.

## Tests and local verification

Run `go test -mod=readonly ./internal/media -count=1 -timeout=120s` and
`go vet -mod=readonly ./internal/media`. Unit tests cover subtitle parsing/
rounding, reordered/repeated scene timing, ASS escaping, all nine title styles,
font validity/licenses/checksums, URL allowlists, argv construction, output
dimensions, native errors/cancellation/log caps, callback failure, byte/duration
limits, sample cleanup, ASR silence/empty/error paths, and atomic publication.

Real FFmpeg tests auto-discover PATH or the read-only Windows desktop tool
directory; `MEDIA_FFMPEG` and `MEDIA_FFPROBE` override discovery. They generate
short clips and check output pixels for reordered scene colors, subtitle windows
and title removal after 4 seconds; all layouts, audio choices, real silence,
sampling and paths containing spaces/quotes/CJK are exercised.

Real speech ASR is opt-in using `MEDIA_WHISPER`, `MEDIA_WHISPER_MODEL` and
`MEDIA_WHISPER_FIXTURE` (a short speech video). Missing tools explicitly skip
that test. Optional `MEDIA_WHISPER_EXPECT` asserts an expected transcript phrase.
No network download or paid call is performed by the test suite.

Update: 2026-09-29 — native package implemented; all media unit/fake-tool tests
and Windows FFmpeg integration tests passed. Installed read-only FFmpeg build:
`N-126889-gb139ba11d8-20260926`, with ffprobe in the same directory.
`go vet` passed; Linux/amd64 test binary cross-compiled, including the Linux
process-group regression test. Linux execution and race-detector execution are
not yet validated locally (no Linux runtime/C toolchain). Real speech ASR
acceptance is tracked separately below when a CLI/model/fixture is available.

Update: 2026-09-29 — exported and tested the shared `OutputDimensions` preview/
render contract; added static-font and original-license provenance checks and
PATH resolution for yt-dlp's FFmpeg location. Complete local suite passed with
85.7% statement coverage before the optional real-speech acceptance run.

Update: 2026-09-29 — real CPU speech acceptance passed with the official b5130
Windows x64 build linked by the v1.9.4 release and the same ggml-base model hash
as Docker. Offline Windows Zira/Huihui voices produced short English/Chinese
fixtures, then `Tools.Transcribe` extracted PCM and ran the actual native CLI.
English passed in 11.07 seconds and contained the full expected sentence.
Chinese passed in 12.75 seconds, including the expected phrase `本地`; output
was Traditional Chinese and included one recognition error (`時別` vs `識別`).
This verifies native orchestration, not model accuracy on general speech.
Tests do not normalize Chinese script or synthesize placeholder transcripts.

Local acceptance files/logs are in ignored `artifacts/media-native-whisper`;
no binary/model is required to be committed or installed globally:
- `whisper-bin-x64.zip`, b5130 SHA-256:
  `f9ec6c52a2e949b62ab51fa21d0d497958f9e41c3010c157c4e42932d5316f3c`
- `ggml-base.bin`, SHA-256:
  `60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe`
- `english-asr-test.log`, `chinese-asr-test.log` retain actual transcripts.

The model transfer used per-command Schannel revocation best-effort because
the revocation service was offline; TLS certificate-chain validation remained
enabled and the fixed model hash was verified. No system configuration changed.
Actual remote yt-dlp downloading, Docker execution, Linux runtime cancellation
and race-detector execution remain unverified locally; downloader orchestration
is covered by deterministic fake-tool tests and Linux cancellation tests compile.

Update: 2026-09-29T17:31:00+08:00 — Real Bilibili acceptance of
`BV1TRhs6hEQp` reproduced an audio-download failure after the video stream
completed: an advertised `mcdn.bilivideo.cn:8082` peer refused the connection.
The pinned yt-dlp extractor exposes primary format URLs, not backup URL choices.
Bilibili downloads now select video/audio/combined formats excluding that MCDN
domain before downloading either stream. Selection stays within the extractor's
advertised formats and retains ordinary TLS/signature checks; it does not rewrite
signed URLs, extract browser cookies, change the installed desktop or retry a
failed paid task. Available codec/bitrate/quality may differ, and no eligible
non-MCDN video/audio combination is an explicit failure. YouTube selection is
unchanged. A regression first reproduced the old unsafe selection; the completed
real-video outcome is recorded in verification.md after rerunning acceptance.

Update: 2026-09-29T17:50:00+08:00 — Frame inspection of the real Bilibili MP4
found boxed/missing Chinese subtitles despite successful rendering. The bundled
weight-700 static font retains the legacy family `Noto Sans SC Thin`; libass
did not match the typographic family `Noto Sans SC` for its private embedded
font. Linux then fell back to DejaVu (missing CJK), while installed Windows CJK
fonts masked the defect. ASS now requests the exact legacy family without
changing any font binary, license or checksum. New metadata/glyph and native
FFmpeg font-selection regressions reproduced the old behavior before the fix;
the native test rejects missing-glyph/system-fallback diagnostics.

Update: 2026-09-29T17:56:00+08:00 — The same sample exposed long unspaced CJK
cues overflowing the video width in Debian's libass. Subtitle preparation now
wraps with the bundled static font's measured glyph advances (preserving Latin
words where possible), before ASS escaping. Cue times and stored ASR text are
unchanged; only render-time line breaks and whitespace normalization change.
Missing glyphs/fonts and a cue too tall for the frame fail explicitly instead
of publishing unreadable text. Unit checks cover landscape/portrait sizing,
timing/text preservation, tabs/newlines and rejection; native FFmpeg checks
also verify glyph selection and rendered pixels stay inside frame margins.
This is layout correction, not speech-recognition accuracy improvement.

Update: 2026-09-29T17:59:00+08:00 — Final Windows full suite passed **252 events**
with real Chinese Whisper enabled, no failures/skips, plus `go vet`. The final
runtime Linux image also passed **66 media test events**, including the new
private CJK-font selection/pixel-bounds check, real Whisper and process-group
cancellation. The native test container had no network or application data
volumes and was removed afterward. Real `BV1TRhs6hEQp` import yielded 56 cues;
its final 30–60s manual slice exported and independently verified as a 30s
H.264/AAC MP4. See verification.md for the exact artifact/hash and final frames.
ASR word errors and overlapping pre-existing hard captions remain content
review issues; no paid AI highlight selection or transcript correction was run.

Update: 2026-09-29T20:05:00+08:00 — `Sample` now rounds each frame timestamp to
milliseconds (`math.Round(t*1000)/1000`). The AI layer transmits sample times to
vision models as `%.3f`, so a time carrying more precision than that could not be
matched back to its own frame when a model echoed it, which failed every visual
analysis whose duration did not divide evenly. Frame count, spacing, the FFmpeg
argument vector and output files are otherwise unchanged; `-ss` already received
6-decimal seconds and still does. Covered by
`TestSampleTimesAreMillisecondExact`, which fails against the previous sampler
with 12.493333333333332.

Update: 2026-09-30T11:10:00+08:00 — Bilibili URL import repaired in three ways
after a user reported that a link the upstream Python app downloads fine failed
here.

The format selector hard-excluded `mcdn.bilivideo.cn` in every branch. Bilibili
serves some videos' *only* audio streams from MCDN, so the selection became
unsatisfiable and yt-dlp reported `Requested format is not available` for the
whole import. Verified against the reported video: all three of its audio
streams (30216/30232/30280) resolve to `mcdn.bilivideo.cn:8082` while its video
streams come from `upos-sz-mirror*.bilivideo.com`. The selector is now tiered —
all-regular-CDN, then video-regular with audio free, then both free, then the
merged stream — so a regular-CDN pair still wins whenever one exists but an
MCDN-only audio track no longer blocks the import. Measured on the same
endpoint the exclusion was added to avoid: 7.61 MiB in 1 second at 3.9 MB/s, so
the exclusion was guarding a transient failure that no longer reproduces. The
existing `--socket-timeout 30 --retries 3` covers a genuinely slow MCDN host,
and a slow import beats no import. The tiered selector resolves this video to
`30080+30280` (avc1), which is also more edit-friendly than upstream's `100026`
(av01).

`--sub-langs` gained `ai-zh` in first position, matching upstream
`bilibili_downloader.py:155`. That is Bilibili's machine-generated Chinese track
and the only subtitle most Bilibili videos carry; `zh` does not match it,
because yt-dlp expands a requested language to `lang` and `lang-suffix`, never
`prefix-lang`. The post-download preference order was updated to match. Without
this, Bilibili imports returned no subtitle and fell through to local ASR, which
hard-fails wherever whisper.cpp is not installed.

`--match-filters` was left unchanged. The intended fix was the documented
`duration?<=N` optional-field form, so a video whose extractor reports no
duration would not be silently filtered out. The pinned yt-dlp 2026.08.19
rejects that syntax outright with `Invalid filter part`, in every spacing and
grouping variant tried (`duration?<=N`, `duration ?<= N`, split across two
`--match-filters` flags, and `!(duration>N)`), and shipping it broke every
import in a live test. The working form is retained and the gap is still open:
a missing-duration extractor is still filtered out and still surfaces only as
`yt-dlp completed without a video`. Revisit when the pinned yt-dlp supports it.

`b23.tv` share links are now supported, which is what Bilibili's own share sheet
emits. The host allowlist is unchanged: `sourceURL` stays a pure function and
only shape-checks the share code, returning the `errShortLink` sentinel;
`Download` resolves exactly one redirect with a HEAD request and revalidates the
destination through `sourceURL` before anything is fetched. Resolution lives in
the worker, not in the web process, so the public API does not gain a
caller-driven outbound request. A short link that resolves to a foreign host, a
non-video page, or another short link is rejected rather than followed, and the
error does not echo the resolved target. The cost is that an unresolvable share
link fails inside the import task rather than at paste time. The BV-number
pattern is now case-insensitive, matching upstream `[Bb][Vv]`.

Tests: the `all-MCDN rejection` case asserted the removed behavior and was
rewritten rather than retargeted — it now asserts the *ordering* guarantee (a
regular-CDN pair first, at least one MCDN-tolerant fallback) instead of an exact
string, so it survives future selector edits. New coverage for short-link shape
acceptance, redirect revalidation against four bad destinations, single-hop
resolution to a canonical URL, and query rejection on a short link. Negative
controls: restoring the MCDN exclusion fails
`TestDownloadFormatPrefersRegularCDNButFallsBackToMCDN` with `every group
excludes MCDN, so MCDN-only audio cannot resolve`; disabling the `b23.tv` case
fails `TestShortLinkShapeAcceptedAndResolutionDeferred`.

Not fixed here, because it is a Bilibili account limitation rather than a code
defect: without cookies the 1080P high-bitrate stream and the official/AI
subtitle tracks are unavailable (`--list-subs` returns only `danmaku`).
`cookies-from-browser` remains unsupported deliberately — the services run in a
container and cannot read the host browser's cookie store; the cookies.txt
upload path is the supported route.

Update: 2026-09-30T12:47:56+08:00 — Added the worker-facing `SampleAt`,
byte-returning `Thumbnail`, and clean-source `CompatiblePreview` APIs without
changing their requested signatures or touching worker/HTTP/domain code.
Sparse sampling now matches upstream's 60-frame / `max(2,duration/60)` scan,
millisecond labels and 100 ms tail guard. Dense requests are validated as a
whole and retained in caller order. JPEG extraction now validates complete JPEG
decoding as well as dimensions and bounded stdout. Disabled render subtitles
ignore all cues; source-end tolerance is 1 ms. Render and preview share verified,
synced atomic MP4 publication with real codec checks.

Offline native acceptance used the read-only installed Windows FFmpeg/ffprobe
`N-126889-gb139ba11d8-20260926`. The controlled MPEG4/PCM fixture carries Chinese
text at the top, 77 variable-rate video frames and a 440 Hz tone delayed 400 ms.
Its H.264/AAC preview preserved every frame timestamp within 1 ms, produced
6.011 seconds from the 6-second source, and retained the audio onset at 0.4000s
and tone at 440 Hz. Source bytes were SHA-256 identical before/after. Top Chinese
pixels remained one layer; three subtitle-off renders (nil, invalid and valid
cues) each had zero added bottom subtitle pixels. The explicit subtitle-on
positive control had 135 bright bottom pixels. A separate nonzero-origin
fixture retained the first PTS at 5.000s and the full 6.000s duration.

The nonzero-origin test caught truncation from combining `-copyts` with
output `-t duration`; previews therefore encode the full validated source under
native timeout/byte bounds instead of adding an absolute timestamp cutoff.
The current FFmpeg build also rejected the historical `-enc_time_base -1`
alias; a numeric `1:1000000` time base passes the actual VFR regression.
Other new checks cover no-audio output, portrait JPEG bounds, exact seeks,
60-frame acceptance, invalid/duplicate/end timestamps, corrupt/oversized JPEGs,
codec/container/stream rejection, immutable targets, caller/native deadlines,
callback cancellation and private-directory cleanup.

Verification: `go test -mod=readonly ./internal/media -count=1 -timeout=120s -v
-coverprofile=internal/media/.coverage.log` passed in **17.091s**, with
**55 top-level tests / 97 including subtests**, zero failures, and **87.9%**
statement coverage (`SampleAt`, sparse scheduling and JPEG extraction: 100%).
`go vet -mod=readonly ./internal/media` and scoped `git diff --check` passed.
All new tests, including real FFmpeg tests, ran; the sole skip is the existing
opt-in `TestIntegrationWhisperOptional`, because CLI/model/speech-fixture
environment variables are absent. No new ASR behavior or ASR test debt was
introduced. No downloads, paid requests, keys or original-application writes.
Go cache access-denied failures were retried with explicit escalation.

Logs/artifacts (all inside the assigned media directory):
`internal/media/.full-tests.log`, `.native-api-tests.log`, `.timeline-tests.log`,
`.vet.log`, `.coverage.log`, and `.coverage-summary.log`. Native fixtures are
generated under per-test temporary directories and cleaned by the Go test runner.
Existing uncommitted downloader/URL fixes were preserved, not modified here.

Update: 2026-09-30T13:21:42.6005111+08:00 — Review integration adds Draft.title_enabled: an explicit false produces a transparent opening-title PNG rather than falling back from empty Hook to Title. Missing/null preserves old drafts. Worker preview retry verifies an already-published checkpoint through VerifyPreview (source duration, output dimensions/audio and actual MP4 H264/yuv420p/AAC streams), then registers it without overwriting or re-encoding. Review1 permanent tests cover disabled-title rendering and valid orphan-preview recovery; final verification is tracked in verification.md.

Update: 2026-09-30T13:34:02+08:00 — R2-09 media-only platform-subtitle opt-out.

Added `DownloadWithSubtitles(ctx, raw, directory, cookies, includeSubtitles,
progress)`; existing `Download` delegates with true. False explicitly disables
both ordinary and automatic platform subtitles, omits all subtitle conversion
options and skips sidecar discovery/read/validation. True retains deterministic
language selection and propagates native conversion and subtitle validation
failures. No worker/store/HTTP changes were made; worker selection based on an
explicit uploaded subtitle and its integration regression remain main-owned.

Permanent tests in `argv_test.go`, `download_subtitles_test.go` and the existing
native test-binary helper in `process_test.go` cover legacy/true/false policies
against missing, valid, empty, malformed, unreadable and oversized sidecars,
plus simulated native conversion failure. Actual subprocess argv is checked.
False still fails on filtered download, byte limit, invalid probe, native failure
and progress rejection, with private-workspace cleanup. A real FFmpeg six-second
audio/video fixture is copied by the fake native downloader and validated by
real ffprobe: false succeeds without modifying its invalid unrelated SRT; true
rejects that same SRT. This is offline orchestration coverage, not a live yt-dlp
platform download or model call.

Windows verification using the read-only installed FFmpeg/ffprobe, GOPROXY=off
and GOSUMDB=off:
- `go test -mod=readonly ./internal/media -run 'Test(Download|IntegrationDownload)'
  -count=1 -timeout=60s -v`: 8 top-level tests + 26 subtests passed in 1.727s,
  zero failures/skips. Log: `internal/media/.r2-09-targeted.log`.
- `go test -mod=readonly ./internal/media -count=1 -timeout=120s -v`: 63 top-level
  tests + 83 subtests passed in 26.678s, zero failures. Only the existing optional
  real Whisper test skipped (speech/model environment not configured); no new
  test skipped. Log: `internal/media/.r2-09-full.log`.
- `go vet -mod=readonly ./internal/media`: exit 0; log
  `internal/media/.r2-09-vet.log` (empty success output). Owned files gofmt clean;
  scoped git diff --check passed. All preceding uncommitted edits preserved.

Final R2-09 write scope: `internal/media/download.go`, `argv_test.go`,
`process_test.go`, new `download_subtitles_test.go`, the three media-local logs
above, and this document. Media implementation/testing is frozen for handoff;
this does not claim acceptance of main's worker integration or review-2 overall.
