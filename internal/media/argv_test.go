package media

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"autoclip-go/internal/domain"
)

func testDraft() domain.Draft {
	return domain.NewDraft("Hello 世界", []domain.Scene{{ID: "s1", Start: 0, End: 1}})
}

func TestValidateSourceURL(t *testing.T) {
	for _, raw := range []string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://youtube.com/watch?v=dQw4w9WgXcQ&list=PLignored&t=3",
		"https://m.youtube.com/shorts/dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?si=ignored",
		"https://www.bilibili.com/video/BV1xx411c7mD/?p=2",
		"https://bilibili.com/video/av123",
	} {
		if err := ValidateSourceURL(raw); err != nil {
			t.Errorf("reject %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"", "http://youtu.be/dQw4w9WgXcQ", "file:///etc/passwd", "https://127.0.0.1/watch?v=dQw4w9WgXcQ",
		"https://[::1]/", "https://youtube.com:443/watch?v=dQw4w9WgXcQ",
		"https://u:p@youtube.com/watch?v=dQw4w9WgXcQ", "https://youtube.com.evil.test/watch?v=dQw4w9WgXcQ",
		"https://youtube.com./watch?v=dQw4w9WgXcQ", "https://youtube.com/playlist?list=x",
		"https://youtube.com/watch?v=x", "https://youtube.com/watch?v=dQw4w9WgXcQ&v=dQw4w9WgXcQ",
		"https://youtube.com/%77atch?v=dQw4w9WgXcQ", "https://youtube.com/watch/../watch?v=dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ/extra", "https://youtube.com/redirect?q=https://example.org",
		"https://youtube.com/embed/dQw4w9WgXcQ", "https://b23.tv/abc", "https://bilibili.com/",
		"https://bilibili.com/video/BV1xx411c7mD?p=0", "https://bilibili.com/video/BV1xx411c7mD?p=1&p=2",
		"https://bilibili.com/video/BV1xx411c7mD?p=1;foo=2",
		"https://www.bilibili.com/video/av0", "https://youtube.com\\@evil.test/watch?v=dQw4w9WgXcQ",
		" https://youtu.be/dQw4w9WgXcQ", "https://youtu.be/dQw4w9WgXcQ\n",
	} {
		if err := ValidateSourceURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	canonical, err := sourceURL("https://youtu.be/dQw4w9WgXcQ?url=https://evil.test")
	if err != nil || canonical != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("canonical URL: %s %v", canonical, err)
	}
}

func TestDownloadArgv(t *testing.T) {
	tools := New(Config{FFmpeg: filepath.Join(t.TempDir(), "ffmpeg"), MaxBytes: 123, MaxDuration: 42})
	args := tools.downloadArgs("https://youtu.be/dQw4w9WgXcQ", "/private/cookies file.txt")
	for _, pair := range [][2]string{
		{"--max-filesize", "123"}, {"--match-filters", "!is_live & duration <= 42.000"},
		{"--cookies", "/private/cookies file.txt"}, {"--output", "source.%(ext)s"},
		{"--ffmpeg-location", tools.cfg.FFmpeg}, {"--print", "after_move:MEDIA_FILE %(filepath)j"},
	} {
		if valueAfter(args, pair[0]) != pair[1] {
			t.Errorf("missing argv pair %q: %#v", pair, args)
		}
	}
	for _, flag := range []string{"--ignore-config", "--no-plugin-dirs", "--no-playlist", "--no-cache-dir", "--continue", "--concurrent-fragments", "--extractor-retries", "--retry-sleep"} {
		if !hasArg(args, flag) {
			t.Errorf("missing %s", flag)
		}
	}
	if !reflect.DeepEqual(args[len(args)-2:], []string{"--", "https://youtu.be/dQw4w9WgXcQ"}) {
		t.Fatal("URL must be a separate final positional argument")
	}
}

func TestActionableBilibili412Error(t *testing.T) {
	base := errors.New("yt-dlp: HTTP Error 412: Precondition Failed")
	withoutCookies := actionableDownloadError("https://www.bilibili.com/video/BV1xx411c7mD", "", base)
	if !strings.Contains(withoutCookies.Error(), "设置 → 导入 Cookie") || !errors.Is(withoutCookies, base) {
		t.Fatalf("missing unauthenticated Bilibili guidance: %v", withoutCookies)
	}
	withCookies := actionableDownloadError("https://www.bilibili.com/video/BV1xx411c7mD", "cookies.txt", base)
	if !strings.Contains(withCookies.Error(), "可能过期") || !errors.Is(withCookies, base) {
		t.Fatalf("missing expired-cookie Bilibili guidance: %v", withCookies)
	}
	if got := actionableDownloadError("https://youtu.be/dQw4w9WgXcQ", "", base); got != base {
		t.Fatalf("unrelated URL changed: %v", got)
	}
}

func TestDownloadSubtitlePolicyArgv(t *testing.T) {
	tools := New(Config{})
	for _, raw := range []string{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "https://www.bilibili.com/video/BV1xx411c7mD"} {
		legacy := tools.downloadArgs(raw, "private cookies.txt")
		enabled := tools.downloadArgsWithSubtitles(raw, "private cookies.txt", true)
		if !reflect.DeepEqual(legacy, enabled) {
			t.Fatal("default subtitle policy changed legacy argv")
		}
		for _, flag := range []string{"--write-subs", "--write-auto-subs", "--sub-langs", "--sub-format", "--convert-subs"} {
			if !hasArg(enabled, flag) {
				t.Fatal("enabled subtitle option missing", flag)
			}
		}
		disabled := tools.downloadArgsWithSubtitles(raw, "private cookies.txt", false)
		for _, flag := range []string{"--write-subs", "--write-auto-subs", "--sub-langs", "--sub-format", "--convert-subs", "--embed-subs"} {
			if hasArg(disabled, flag) {
				t.Fatal("disabled subtitles still requested or converted", flag)
			}
		}
		for _, flag := range []string{"--no-write-subs", "--no-write-auto-subs", "--ignore-config", "--no-plugin-dirs", "--no-playlist"} {
			if !hasArg(disabled, flag) {
				t.Fatal("missing suppression or safety flag", flag)
			}
		}
		for _, flag := range []string{"--format", "--merge-output-format", "--remux-video", "--cookies", "--max-filesize", "--match-filters", "--output", "--ffmpeg-location"} {
			if valueAfter(disabled, flag) != valueAfter(enabled, flag) {
				t.Fatal("subtitle policy changed unrelated download option", flag)
			}
		}
		if !reflect.DeepEqual(disabled[len(disabled)-2:], []string{"--", raw}) {
			t.Fatal("subtitle policy changed positional URL boundary")
		}
	}
}

// The selector used to hard-exclude MCDN in every branch. Bilibili serves some
// videos' only audio streams from MCDN, so that made the selection
// unsatisfiable and the whole import failed with "Requested format is not
// available" — verified against a real video whose three audio streams were all
// MCDN. The preference must still lead with the regular CDN, but fall back.
func TestDownloadFormatPrefersRegularCDNButFallsBackToMCDN(t *testing.T) {
	tools := New(Config{})
	for _, raw := range []string{
		"https://www.bilibili.com/video/BV1TRhs6hEQp/",
		"https://bilibili.com/video/av123",
		"https://m.bilibili.com/video/BV1TRhs6hEQp",
	} {
		format := valueAfter(tools.downloadArgs(raw, ""), "--format")
		groups := strings.Split(format, "/")
		if len(groups) < 2 {
			t.Errorf("%s: selector has no fallback group: %q", raw, format)
			continue
		}
		// yt-dlp takes the first satisfiable group, so the all-regular-CDN pair
		// must come first for the preference to mean anything.
		if !strings.Contains(groups[0], "bv*[url!*=mcdn.bilivideo.cn]+ba[url!*=mcdn.bilivideo.cn]") {
			t.Errorf("%s: first choice is not an all-regular-CDN pair: %q", raw, groups[0])
		}
		// At least one later group must accept MCDN, or an MCDN-only audio track
		// still leaves the selection unsatisfiable.
		fallback := false
		for _, g := range groups[1:] {
			if !strings.Contains(g, "mcdn.bilivideo.cn") {
				fallback = true
			}
		}
		if !fallback {
			t.Errorf("%s: every group excludes MCDN, so MCDN-only audio cannot resolve: %q", raw, format)
		}
	}
	for _, raw := range []string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ",
		"https://www.bilibili.com.evil.test/video/BV1TRhs6hEQp",
		"url",
	} {
		if format := valueAfter(tools.downloadArgs(raw, ""), "--format"); format != "bv*+ba/b" {
			t.Errorf("unrelated host received Bilibili-specific selection: %s: %s", raw, format)
		}
	}
}

func TestNewResolvesPATHForDownloaderFFmpegLocation(t *testing.T) {
	dir := t.TempDir()
	name := "test-media-tool"
	filename := name
	if runtime.GOOS == "windows" {
		filename += ".exe"
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte("tool placeholder"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	tools := New(Config{FFmpeg: name, Whisper: "unused-absent-native-tool"})
	if tools.cfg.FFmpeg != path || tools.cfgErr != nil {
		t.Fatalf("PATH resolution: %s %v", tools.cfg.FFmpeg, tools.cfgErr)
	}
	if got := valueAfter(tools.downloadArgs("url", ""), "--ffmpeg-location"); got != path {
		t.Fatalf("yt-dlp was not given resolved FFmpeg path: %s", got)
	}
}

func TestRenderArgvAndLayouts(t *testing.T) {
	tools := New(Config{})
	d := testDraft()
	d.Title, d.Hook = "x;[evil]", "'\\:; injected"
	d.Scenes = append(d.Scenes, domain.Scene{ID: "s2", Start: 4, End: 5})
	source := filepath.Join(t.TempDir(), "spaces ; quote' 输入.mp4")
	for _, layout := range []string{"fit", "crop", "blur"} {
		d.Layout = layout
		p := tools.planRender(source, Info{Width: 320, Height: 180, HasAudio: true}, d, true)
		if !strings.Contains(p.graph, "concat=n=2:v=1:a=1") || !strings.Contains(p.graph, "enable='lt(t,4)'") ||
			!strings.Contains(p.graph, "ass=filename=captions.ass:fontsdir=fonts") {
			t.Fatalf("bad graph: %s", p.graph)
		}
		for _, forbidden := range []string{source, d.Title, d.Hook} {
			if strings.Contains(p.graph, forbidden) {
				t.Fatalf("untrusted string in filter graph: %q", forbidden)
			}
		}
		if valueAfter(p.args, "-i") != source || valueAfter(p.args, "-filter_complex") != p.graph ||
			p.args[len(p.args)-1] != "partial.mp4" {
			t.Fatalf("bad argv: %#v", p.args)
		}
		switch layout {
		case "fit":
			if !strings.Contains(p.graph, "pad=320:180") {
				t.Fatal("fit must letterbox")
			}
		case "crop":
			if !strings.Contains(p.graph, "crop=320:180:x='(iw-ow)*0.500000'") {
				t.Fatal("crop must use normalized crop_x")
			}
		case "blur":
			if !strings.Contains(p.graph, "gblur=sigma=24") || !strings.Contains(p.graph, "split=2") {
				t.Fatal("blur must use separate background and fitted foreground")
			}
		}
	}
	d.OriginalAudio = false
	p := tools.planRender(source, Info{Width: 321, Height: 181, HasAudio: true}, d, false)
	if p.audio || strings.Contains(p.graph, "[audio]") || !hasArg(p.args, "-an") || p.width%2 != 0 || p.height%2 != 0 {
		t.Fatal("silent/even-sized output not respected")
	}
}

func TestOutputDimensionsSharedWithRender(t *testing.T) {
	for _, tc := range []struct {
		info   Info
		aspect string
		w, h   int
	}{
		{Info{Width: 3840, Height: 2160}, "original", 1920, 1080},
		{Info{Width: 2160, Height: 3840}, "original", 1080, 1920},
		{Info{Width: 321, Height: 181}, "original", 320, 180},
		{Info{Width: 640, Height: 480}, "original", 640, 480},
		{Info{Width: 320, Height: 180}, "portrait", 1080, 1920},
		{Info{Width: 320, Height: 180}, "landscape", 1920, 1080},
		{Info{}, "original", 0, 0},
		{Info{Width: 320, Height: 180}, "unknown", 0, 0},
	} {
		w, h := OutputDimensions(tc.info, tc.aspect)
		if w != tc.w || h != tc.h {
			t.Errorf("dimensions %+v %s: %dx%d, want %dx%d", tc.info, tc.aspect, w, h, tc.w, tc.h)
		}
		if w > 0 {
			d := testDraft()
			d.Aspect = tc.aspect
			p := New(Config{}).planRender("source.mp4", tc.info, d, false)
			if p.width != w || p.height != h {
				t.Fatal("preview/render dimensions diverged")
			}
		}
	}
}

func hasArg(args []string, arg string) bool {
	for _, a := range args {
		if a == arg {
			return true
		}
	}
	return false
}

func valueAfter(args []string, arg string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == arg {
			return args[i+1]
		}
	}
	return ""
}

// Bilibili's share sheet emits b23.tv links, which used to be rejected outright
// — turning the most common way to share a video into an error. A well-formed
// share code is now accepted at validation time; Download resolves the redirect
// and revalidates the destination through the same rules, so the host allowlist
// is unchanged and a short link pointing anywhere unsupported still fails.
func TestShortLinkShapeAcceptedAndResolutionDeferred(t *testing.T) {
	for _, raw := range []string{
		"https://b23.tv/abcdefg",
		"https://b23.tv/BV1P5h16JE8n",
		"https://b23.tv/abcdefg/",
	} {
		if err := ValidateSourceURL(raw); err != nil {
			t.Errorf("a well-formed share link must validate: %q: %v", raw, err)
		}
		// sourceURL stays pure: it reports that resolution is still required
		// rather than performing a request, so the API can validate synchronously.
		canonical, err := sourceURL(raw)
		if !errors.Is(err, errShortLink) {
			t.Errorf("%q: want errShortLink, got %v", raw, err)
		}
		if strings.HasSuffix(canonical, "/") || !strings.HasPrefix(canonical, "https://b23.tv/") {
			t.Errorf("%q: unexpected canonical form %q", raw, canonical)
		}
	}
	// Shapes that are not share codes stay rejected, including a nested path and
	// any query, so this does not become a general redirect follower.
	for _, raw := range []string{
		"https://b23.tv/", "https://b23.tv/abc", "https://b23.tv/a/b",
		"https://b23.tv/abcdefg?x=1", "https://b23.tv/" + strings.Repeat("a", 25),
		"https://b23.tv/abc-def",
	} {
		if err := ValidateSourceURL(raw); err == nil {
			t.Errorf("accepted malformed short link %q", raw)
		}
	}
}

// Resolution must revalidate: a short link that lands on a non-video page, a
// foreign host, or another short link is rejected rather than followed.
func TestShortLinkResolutionRevalidatesDestination(t *testing.T) {
	tools := New(Config{FFmpeg: "ffmpeg", FFprobe: "ffprobe", YTDLP: "yt-dlp"})
	for _, target := range []string{
		"https://evil.test/video/BV1P5h16JE8n",
		"https://www.bilibili.com/space/12345",
		"https://b23.tv/nested1",
		"",
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead {
				t.Errorf("expected HEAD, got %s", r.Method)
			}
			if target != "" {
				w.Header().Set("Location", target)
			}
			w.WriteHeader(http.StatusFound)
		}))
		_, err := tools.resolveShortLink(context.Background(), server.URL)
		server.Close()
		if err == nil {
			t.Errorf("destination %q must be rejected", target)
		}
		// The message must not leak the resolved destination.
		if err != nil && target != "" && strings.Contains(err.Error(), target) {
			t.Errorf("error leaked the redirect target: %v", err)
		}
	}
}

// A share link that resolves to a real video page is accepted and canonicalized
// exactly as if the user had pasted the full URL.
func TestShortLinkResolvesToCanonicalVideoURL(t *testing.T) {
	tools := New(Config{FFmpeg: "ffmpeg", FFprobe: "ffprobe", YTDLP: "yt-dlp"})
	var hops int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hops++
		w.Header().Set("Location", "https://www.bilibili.com/video/BV1P5h16JE8n/?spm_id_from=333.1391.0.0")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	got, err := tools.resolveShortLink(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("a share link to a real video page must resolve: %v", err)
	}
	if got != "https://www.bilibili.com/video/BV1P5h16JE8n" {
		t.Fatalf("resolved to %q, want the canonical video URL with tracking query dropped", got)
	}
	// Exactly one hop: the resolver inspects the redirect and never follows it.
	if hops != 1 {
		t.Fatalf("expected exactly one request, got %d", hops)
	}
}
