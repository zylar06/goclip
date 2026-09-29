package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"autoclip-go/internal/domain"
	"autoclip-go/internal/media"
	"autoclip-go/internal/store"
)

// This test does not call a model or access the internet. Require native tools in
// acceptance/CI by setting REQUIRE_MEDIA_TESTS=1; local unit runs may skip.
func TestRealMediaImportAndExport(t *testing.T) {
	find := func(key, fallback string) string {
		v := os.Getenv(key)
		if v == "" {
			v = fallback
		}
		path, err := exec.LookPath(v)
		if err != nil {
			if os.Getenv("REQUIRE_MEDIA_TESTS") == "1" {
				t.Fatalf("%s required: %v", key, err)
			}
			t.Skipf("native integration unavailable: %s", key)
		}
		return path
	}
	ffmpeg, ffprobe := find("FFMPEG_PATH", "ffmpeg"), find("FFPROBE_PATH", "ffprobe")
	s, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := domain.Project{ID: domain.ID(), Name: "中文路径集成", CreatedAt: domain.Now(), UpdatedAt: domain.Now(), Status: "importing"}
	dir, e := s.ProjectDir(p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(dir, "中文原片.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo", "-t", "5", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", source}
	if out, e := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); e != nil {
		t.Fatalf("fixture: %v %s", e, out)
	}
	if e = os.WriteFile(filepath.Join(dir, "input.srt"), []byte("1\n00:00:00,500 --> 00:00:01,500\n你好，第一段。\n\n2\n00:00:03,000 --> 00:00:04,000\nSecond scene.\n"), 0600); e != nil {
		t.Fatal(e)
	}
	job, e := s.CreateProject(p, domain.ImportPayload{Video: "中文原片.mp4", Subtitle: "input.srt"})
	if e != nil {
		t.Fatal(e)
	}
	m := media.New(media.Config{FFmpeg: ffmpeg, FFprobe: ffprobe})
	w := Worker{Store: s, Media: m, TaskTimeout: 90 * time.Second}
	job, e = s.Claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = w.runTask(ctx, job); e != nil {
		t.Fatal(e)
	}
	got, e := s.Task(job.ID)
	if e != nil || got.Status != "completed" {
		t.Fatal("import", got, e)
	}
	cues, e := w.readCues(p.ID)
	if e != nil || len(cues) != 2 {
		t.Fatal(cues, e)
	}
	d := domain.NewDraft("自动集成测试", []domain.Scene{{ID: domain.ID(), Start: .5, End: 1.5}, {ID: domain.ID(), Start: 3, End: 4}})
	d.Hook = "GO 自动剪辑"
	d.TitleStyle = "comic"
	d.TitleTemplateVersion = 6
	d, e = s.SaveDraft(p.ID, d, true)
	if e != nil {
		t.Fatal(e)
	}
	job, e = s.QueueExport(p.ID, d.ID, d.Revision)
	if e != nil {
		t.Fatal(e)
	}
	job, e = s.Claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = w.runTask(ctx, job); e != nil {
		t.Fatal(e)
	}
	got, e = s.Task(job.ID)
	if e != nil || got.Status != "completed" {
		t.Fatal("export", got, e)
	}
	result, e := m.Probe(ctx, filepath.Join(dir, "exports", job.ID, "output.mp4"))
	if e != nil {
		t.Fatal(e)
	}
	if result.Duration < 1.9 || result.Duration > 2.2 || !result.HasAudio {
		t.Fatal(result)
	}
	exports, e := s.Exports(p.ID)
	if e != nil || len(exports) != 1 || exports[0].Revision != 1 {
		t.Fatal(exports, e)
	}
	if _, e = os.Stat(source); errors.Is(e, os.ErrNotExist) {
		t.Fatal("original was removed")
	}
}
