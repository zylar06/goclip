package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"autoclip-go/internal/httpapi"
	"autoclip-go/internal/media"
	"autoclip-go/internal/store"
	"autoclip-go/internal/worker"
)

var version = "0.1.0"

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func positiveEnv(key string, fallback int64) (int64, error) {
	s := os.Getenv(key)
	if s == "" {
		return fallback, nil
	}
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil || v <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return v, nil
}
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if e := run(); e != nil {
		slog.Error("fatal", "error", e)
		os.Exit(1)
	}
}
func run() error {
	mode := "web"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	data := env("AUTOCLIP_DATA_DIR", "data")
	if mode == "healthcheck" {
		if len(os.Args) > 2 && os.Args[2] == "worker" {
			b, e := os.ReadFile(filepath.Join(data, "worker.heartbeat"))
			if e != nil {
				return e
			}
			t, e := time.Parse(time.RFC3339Nano, string(b))
			if e != nil {
				return e
			}
			if time.Since(t) > 30*time.Second {
				return errors.New("worker heartbeat expired")
			}
			return nil
		}
		client := http.Client{Timeout: 3 * time.Second}
		target, e := healthURL(env("AUTOCLIP_ADDR", "127.0.0.1:8080"))
		if e != nil {
			return e
		}
		res, e := client.Get(target)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return errors.New("API not healthy")
		}
		return nil
	}
	if mode != "web" && mode != "worker" {
		return errors.New("usage: autoclip [web|worker|healthcheck [worker]]")
	}
	maxBytes, e := positiveEnv("MAX_UPLOAD_BYTES", 4<<30)
	if e != nil {
		return e
	}
	maxDuration, e := positiveEnv("MAX_VIDEO_SECONDS", 7200)
	if e != nil {
		return e
	}
	taskSeconds, e := positiveEnv("TASK_TIMEOUT_SECONDS", 21600)
	if e != nil {
		return e
	}
	s, e := store.Open(data)
	if e != nil {
		return e
	}
	defer func() {
		if e := s.Close(); e != nil {
			slog.Error("database close", "error", e)
		}
	}()
	if e := applyModelEnvironment(s, mode, os.Getenv); e != nil {
		return e
	}
	m := media.New(media.Config{FFmpeg: env("FFMPEG_PATH", "ffmpeg"), FFprobe: env("FFPROBE_PATH", "ffprobe"), YTDLP: env("YTDLP_PATH", "yt-dlp"), Whisper: env("WHISPER_PATH", "whisper-cli"), Model: env("WHISPER_MODEL", "models/ggml-base.bin"), FontDir: env("FONT_DIR", "assets/fonts"), MaxBytes: maxBytes, MaxDuration: float64(maxDuration)})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if mode == "worker" {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		heartbeatErr := make(chan error, 1)
		go func() {
			tick := time.NewTicker(5 * time.Second)
			defer tick.Stop()
			for {
				if err := os.WriteFile(filepath.Join(s.Dir, "worker.heartbeat"), []byte(domainTime()), 0600); err != nil {
					heartbeatErr <- err
					cancel()
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
			}
		}()
		w := &worker.Worker{Store: s, Media: m, MaxBytes: maxBytes, MaxDuration: float64(maxDuration), TaskTimeout: time.Duration(taskSeconds) * time.Second}
		err := w.Run(ctx)
		select {
		case e := <-heartbeatErr:
			return e
		default:
			return err
		}
	}
	a := &httpapi.API{Store: s, Media: m, Config: httpapi.Config{WebDir: env("AUTOCLIP_WEB_DIR", "web/dist"), Version: version, MaxBytes: maxBytes}}
	addr := env("AUTOCLIP_ADDR", "127.0.0.1:8080")
	if !strings.Contains(addr, ":") {
		return errors.New("AUTOCLIP_ADDR must include host:port")
	}
	server := &http.Server{Addr: addr, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("web listening", "address", addr, "version", version)
		serveErr <- server.ListenAndServe()
	}()
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return err
		}
		return nil
	}
}
func domainTime() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func healthURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/api/v1/health", nil
}
