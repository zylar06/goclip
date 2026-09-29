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
func (*Tools) Transcribe(ctx context.Context, video, outDir string,
    progress domain.ProgressFunc) ([]domain.Cue, error)
func (*Tools) Sample(ctx context.Context, video, outDir string, duration float64,
    progress domain.ProgressFunc) ([]domain.Frame, error)
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
yt-dlp exits zero. Final media is probed and subtitles parsed before success.

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
out-of-range windows, and writes at most 24 JPEGs. Count is
`min(24, max(1, ceil(duration/10)))`; requested seek times are
`i*duration/count`, starting at zero. Returned timestamps are these source-time
seek targets, not inferred wall-clock times. JPEGs are at most 640 pixels wide.
Successful sampling directories are retained for the caller; failures clean up.

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
each sampled frame 1 minute; render 5 minutes + 30x duration (cap 2 hours).
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
