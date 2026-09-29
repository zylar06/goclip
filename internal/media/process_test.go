package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as fake native tools. No shell, Python, network or
// platform-specific script is needed to exercise the orchestration.
func TestMain(m *testing.M) {
	if mode := os.Getenv("AUTOCLIP_MEDIA_FAKE"); mode != "" {
		if err := fakeTool(mode, os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(7)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeTool(mode string, args []string) error {
	if log := os.Getenv("AUTOCLIP_MEDIA_ARGV"); log != "" {
		f, err := os.OpenFile(log, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		err = json.NewEncoder(f).Encode(args)
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
	}
	switch mode {
	case "wait":
		fmt.Println("ready")
		time.Sleep(time.Minute)
		return nil
	case "fail":
		fmt.Fprint(os.Stderr, strings.Repeat("z", logLimit*3))
		return errors.New("specific native failure")
	case "stdout-overflow":
		fmt.Print(strings.Repeat("x", 4096))
		return nil
	}
	if hasArg(args, "-show_entries") {
		if mode == "bad-probe" {
			fmt.Print("not JSON")
			return nil
		}
		dur := "1.000"
		if mode == "nan-probe" {
			dur = "NaN"
		}
		width := 320
		if mode == "bad-render" && strings.HasSuffix(args[len(args)-1], "partial.mp4") {
			width = 100
		}
		rotation := ""
		if mode == "rotated" {
			rotation = `,"side_data_list":[{"rotation":90}]`
		}
		fmt.Printf(`{"streams":[{"codec_type":"video","width":%d,"height":180%s},{"codec_type":"audio"}],"format":{"duration":%q}}`, width, rotation, dur)
		return nil
	}
	if hasArg(args, "--ignore-config") {
		if mode == "filtered-download" {
			return nil
		}
		if mode == "download-limit" {
			fmt.Println(`MEDIA_DOWNLOAD {"downloaded_bytes":99999,"total_bytes":100000}`)
			return nil
		}
		if err := os.WriteFile("source.mp4", []byte("fake video"), 0600); err != nil {
			return err
		}
		fmt.Println(`MEDIA_DOWNLOAD {"downloaded_bytes":5,"total_bytes":10}`)
		fmt.Println(`MEDIA_DOWNLOAD {"downloaded_bytes":10,"total_bytes":10}`)
		fmt.Println(`MEDIA_FILE "source.mp4"`)
		if mode == "download-subtitles" {
			return os.WriteFile("source.en.srt", []byte("1\n00:00:00,000 --> 00:00:00,500\nHello\n"), 0600)
		}
		return nil
	}
	if hasArg(args, "--output-srt") {
		if mode == "missing-srt" {
			return nil
		}
		var data []byte
		switch mode {
		case "empty-srt":
			data = []byte{}
		case "bad-srt":
			data = []byte("garbage")
		case "overrun-srt":
			data = []byte("1\n00:00:00,000 --> 00:00:09,000\nimpossible\n")
		default:
			data = []byte("1\n00:00:00,000 --> 00:00:00,900\nHello 世界\n")
		}
		fmt.Fprintln(os.Stderr, "whisper_print_progress_callback: progress =  25%")
		fmt.Fprintln(os.Stderr, "whisper_print_progress_callback: progress = 100%")
		return os.WriteFile(valueAfter(args, "--output-file")+".srt", data, 0600)
	}
	if len(args) == 0 {
		return errors.New("fake tool: no args")
	}
	last := args[len(args)-1]
	if strings.HasSuffix(last, "audio.wav") {
		sample := int16(1000)
		if mode == "silence" {
			sample = 0
		}
		fmt.Println("out_time_us=1000000")
		return os.WriteFile(last, testWAV(sample), 0600)
	}
	if strings.HasSuffix(last, ".jpg") {
		f, err := os.Create(last)
		if err != nil {
			return err
		}
		return errors.Join(jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 32, 18)), nil), f.Close())
	}
	if last == "partial.mp4" {
		fmt.Println("out_time_us=500000")
		fmt.Println("out_time_us=1000000")
		return os.WriteFile(last, []byte("fake rendered video"), 0600)
	}
	return fmt.Errorf("unexpected fake-tool arguments: %q", args)
}

func testWAV(sample int16) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	write := func(v any) {
		if err := binary.Write(&b, binary.LittleEndian, v); err != nil {
			panic(err) // bytes.Buffer cannot fail; only fixed-size values are passed.
		}
	}
	write(uint32(36 + 32000))
	b.WriteString("WAVEfmt ")
	write(uint32(16))
	for _, v := range []uint16{1, 1} {
		write(v)
	}
	write(uint32(16000))
	write(uint32(32000))
	write(uint16(2))
	write(uint16(16))
	b.WriteString("data")
	write(uint32(32000))
	for i := 0; i < 16000; i++ {
		write(sample)
	}
	return b.Bytes()
}

func fakeTools(t *testing.T, mode string) (*Tools, string, string) {
	t.Helper()
	t.Setenv("AUTOCLIP_MEDIA_FAKE", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	model := filepath.Join(dir, "model.bin")
	for _, path := range []string{source, model} {
		if err := os.WriteFile(path, []byte("test input"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return New(Config{FFmpeg: exe, FFprobe: exe, YTDLP: exe, Whisper: exe, Model: model}), source, dir
}

func TestRunTimeoutCancellationAndCallbackErrors(t *testing.T) {
	tools, _, _ := fakeTools(t, "wait")
	start := time.Now()
	_, err := run(context.Background(), command{exe: tools.cfg.FFmpeg, timeout: 150 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 8*time.Second {
		t.Fatalf("timeout not enforced: %v in %s", err, time.Since(start))
	}
	sentinel := errors.New("callback stopped operation")
	_, err = run(context.Background(), command{exe: tools.cfg.FFmpeg, timeout: 10 * time.Second,
		line: func(s string) error { return sentinel }})
	if !errors.Is(err, sentinel) {
		t.Fatalf("lost callback error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = run(ctx, command{exe: tools.cfg.FFmpeg, timeout: time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestRunBoundedLogsAndExplicitFailure(t *testing.T) {
	tools, _, _ := fakeTools(t, "fail")
	_, err := run(context.Background(), command{exe: tools.cfg.FFmpeg, timeout: time.Second})
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || len(toolErr.Output) > logLimit ||
		!strings.Contains(toolErr.Output, "specific native failure") {
		t.Fatalf("native failure/log bound: %v", err)
	}
	t.Setenv("AUTOCLIP_MEDIA_FAKE", "stdout-overflow")
	_, err = run(context.Background(), command{exe: tools.cfg.FFmpeg, timeout: time.Second, stdoutLimit: 32})
	if err == nil || !strings.Contains(err.Error(), "capture limit") {
		t.Fatalf("stdout overflow hidden: %v", err)
	}
	_, err = run(context.Background(), command{exe: filepath.Join(t.TempDir(), "absent"), timeout: time.Second})
	if err == nil {
		t.Fatal("missing native tool must fail explicitly")
	}
}

func TestFFmpegProgress(t *testing.T) {
	var got []float64
	fn := ffmpegProgress(func(stage string, p *float64) error {
		if stage != "test" || p == nil {
			t.Fatal("wrong progress contract")
		}
		got = append(got, *p)
		return nil
	}, "test", 10, 98)
	for _, line := range []string{"out_time_us=N/A", "out_time_us=0", "out_time_us=5000000", "out_time_us=4000000", "out_time_us=20000000"} {
		if err := fn(line); err != nil {
			t.Fatal(err)
		}
	}
	if fmt.Sprint(got) != "[0 49 98]" {
		t.Fatalf("timestamp progress, monotonic/capped: %v", got)
	}
}
