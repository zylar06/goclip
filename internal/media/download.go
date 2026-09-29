package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"autoclip-go/internal/domain"
)

var youtubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
var biliPath = regexp.MustCompile(`^/video/(BV[A-Za-z0-9]{10}|av[1-9][0-9]*)/?$`)
var pageNumber = regexp.MustCompile(`^[1-9][0-9]{0,3}$`)

// ValidateSourceURL only accepts explicit YouTube/Bilibili HTTPS video pages.
// Short-link redirectors, playlists, IPs, credentials and even :443 are rejected.
// Download canonicalizes the accepted URL, dropping all unrelated query fields.
func ValidateSourceURL(raw string) error {
	_, err := sourceURL(raw)
	return err
}

func sourceURL(raw string) (string, error) {
	reject := errors.New("media: expected an HTTPS YouTube or Bilibili video-page URL without credentials or ports")
	if len(raw) > 4096 || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\\\x00\r\n\t") {
		return "", reject
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Host == "" ||
		strings.Contains(u.Host, ":") || u.RawPath != "" || strings.Contains(u.EscapedPath(), "%") ||
		net.ParseIP(u.Hostname()) != nil {
		return "", reject
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", reject
	}
	host := strings.ToLower(u.Host)
	switch host {
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		var id string
		if u.Path == "/watch" && len(q["v"]) == 1 {
			id = q.Get("v")
		} else if strings.HasPrefix(u.Path, "/shorts/") {
			id = strings.TrimPrefix(u.Path, "/shorts/")
		}
		if !youtubeID.MatchString(id) {
			return "", reject
		}
		return "https://www.youtube.com/watch?v=" + id, nil
	case "youtu.be":
		id := strings.TrimPrefix(u.Path, "/")
		if !youtubeID.MatchString(id) {
			return "", reject
		}
		return "https://www.youtube.com/watch?v=" + id, nil
	case "bilibili.com", "www.bilibili.com", "m.bilibili.com":
		if !biliPath.MatchString(u.Path) {
			return "", reject
		}
		canonical := "https://www.bilibili.com" + strings.TrimSuffix(u.Path, "/")
		if p, ok := q["p"]; ok {
			if len(p) != 1 || !pageNumber.MatchString(p[0]) {
				return "", reject
			}
			canonical += "?p=" + p[0]
		}
		return canonical, nil
	}
	return "", reject
}

func (t *Tools) downloadArgs(raw, cookies string) []string {
	format := "bv*+ba/b"
	// Download validates/canonicalizes the source URL before constructing argv.
	if strings.HasPrefix(raw, "https://www.bilibili.com/") ||
		strings.HasPrefix(raw, "https://bilibili.com/") ||
		strings.HasPrefix(raw, "https://m.bilibili.com/") {
		// Avoid advertised peer/MCDN endpoints on unreachable high ports before
		// downloading either stream. If no regular-CDN combination exists,
		// yt-dlp must fail explicitly rather than retry an MCDN endpoint.
		format = "bv*[url!*=mcdn.bilivideo.cn]+ba[url!*=mcdn.bilivideo.cn]/b[url!*=mcdn.bilivideo.cn]"
	}
	args := []string{
		"--ignore-config", "--no-plugin-dirs", "--no-playlist", "--no-overwrites",
		"--no-cache-dir", "--no-colors", "--newline", "--progress",
		"--socket-timeout", "30", "--retries", "3", "--fragment-retries", "3",
		"--max-filesize", strconv.FormatInt(t.cfg.MaxBytes, 10),
		"--match-filters", fmt.Sprintf("!is_live & duration <= %.3f", t.cfg.MaxDuration),
		"--ffmpeg-location", t.cfg.FFmpeg,
		"--format", format, "--merge-output-format", "mp4", "--remux-video", "mp4",
		"--write-subs", "--write-auto-subs", "--sub-langs", "zh-Hans,zh-CN,zh,en",
		"--sub-format", "srt/vtt/best", "--convert-subs", "srt",
		"--output", "source.%(ext)s",
		"--progress-template", `download:MEDIA_DOWNLOAD %(progress)j`,
		"--print", "after_move:MEDIA_FILE %(filepath)j",
	}
	if cookies != "" {
		args = append(args, "--cookies", cookies)
	}
	return append(args, "--", raw)
}

// Download creates a unique subdirectory under directory, retained on success.
// cookies is a path to a Netscape cookie file, not cookie contents. A private copy
// prevents yt-dlp's cookie-jar writeback from modifying the caller's file.
// subtitle is empty only if the platform supplied no supported subtitles.
func (t *Tools) Download(ctx context.Context, raw, directory, cookies string, progress domain.ProgressFunc) (video, subtitle string, err error) {
	if t.cfgErr != nil {
		return "", "", t.cfgErr
	}
	raw, err = sourceURL(raw)
	if err != nil {
		return "", "", err
	}
	if err = report(progress, "download", nil); err != nil {
		return "", "", err
	}
	dir, err := workspace(directory, ".download-")
	if err != nil {
		return "", "", err
	}
	keep := false
	defer func() {
		if !keep {
			cleanup(dir, &err)
		}
	}()
	if cookies != "" {
		var data []byte
		data, err = readLimited(cookies, 1<<20)
		if err != nil {
			return "", "", fmt.Errorf("cookies: %w", err)
		}
		cookies = filepath.Join(dir, "cookies.txt")
		if err = os.WriteFile(cookies, data, 0600); err != nil {
			return "", "", err
		}
		// Cookie deletion also happens on failure, before deleting the workspace.
		defer func() {
			if e := os.Remove(cookies); e != nil {
				err = errors.Join(err, fmt.Errorf("remove private cookies: %w", e))
			}
		}()
	}
	_, err = run(ctx, command{exe: t.cfg.YTDLP, dir: dir, timeout: 2 * time.Hour,
		args: t.downloadArgs(raw, cookies), line: func(line string) error {
			if strings.HasPrefix(line, "MEDIA_FILE ") {
				var output string
				if e := json.Unmarshal([]byte(strings.TrimPrefix(line, "MEDIA_FILE ")), &output); e != nil {
					return fmt.Errorf("yt-dlp output path: %w", e)
				}
				if !filepath.IsAbs(output) {
					output = filepath.Join(dir, output)
				}
				if filepath.Clean(output) != filepath.Join(dir, "source.mp4") {
					return errors.New("yt-dlp returned an unexpected output path")
				}
				video = output
			}
			if !strings.HasPrefix(line, "MEDIA_DOWNLOAD ") {
				return nil
			}
			var p struct {
				Downloaded float64 `json:"downloaded_bytes"`
				Total      float64 `json:"total_bytes"`
			}
			if e := json.Unmarshal([]byte(strings.TrimPrefix(line, "MEDIA_DOWNLOAD ")), &p); e != nil {
				return fmt.Errorf("yt-dlp progress: %w", e)
			}
			if p.Downloaded > float64(t.cfg.MaxBytes) {
				return errors.New("download exceeds MaxBytes")
			}
			// yt-dlp downloads independent video/audio/subtitle streams. This is
			// per-stream progress, never a fabricated whole-operation percentage.
			if p.Total > 0 {
				return report(progress, "download-stream", percent(min(99, 100*p.Downloaded/p.Total)))
			}
			return report(progress, "download-stream", nil)
		}})
	if err != nil {
		return "", "", err
	}
	if video == "" {
		return "", "", errors.New("yt-dlp completed without a video (filtered, unavailable or unsupported)")
	}
	if _, err = t.Probe(ctx, video); err != nil {
		return "", "", fmt.Errorf("download validation: %w", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "source.*.srt"))
	if err != nil {
		return "", "", err
	}
	// Prefer Chinese, then English, deterministically.
	for _, lang := range []string{"zh-Hans", "zh-CN", "zh", "en"} {
		for _, file := range files {
			if filepath.Base(file) == "source."+lang+".srt" {
				subtitle = file
				break
			}
		}
		if subtitle != "" {
			break
		}
	}
	if subtitle == "" && len(files) > 0 {
		subtitle = files[0]
	}
	if subtitle != "" {
		data, e := readLimited(subtitle, subtitleLimit)
		if e != nil {
			return "", "", e
		}
		cues, e := ParseSRT(data)
		if e != nil {
			return "", "", fmt.Errorf("download subtitles: %w", e)
		}
		if len(cues) == 0 {
			subtitle = ""
		}
	}
	if err = report(progress, "download", percent(100)); err != nil {
		return "", "", err
	}
	keep = true
	return video, subtitle, nil
}
