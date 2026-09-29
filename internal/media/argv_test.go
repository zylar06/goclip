package media

import (
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
	for _, flag := range []string{"--ignore-config", "--no-plugin-dirs", "--no-playlist", "--no-cache-dir"} {
		if !hasArg(args, flag) {
			t.Errorf("missing %s", flag)
		}
	}
	if !reflect.DeepEqual(args[len(args)-2:], []string{"--", "https://youtu.be/dQw4w9WgXcQ"}) {
		t.Fatal("URL must be a separate final positional argument")
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
