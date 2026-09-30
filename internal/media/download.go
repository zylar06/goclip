package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
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
var biliPath = regexp.MustCompile(`(?i)^/video/(BV[A-Za-z0-9]{10}|av[1-9][0-9]*)/?$`)
var pageNumber = regexp.MustCompile(`^[1-9][0-9]{0,3}$`)

// Bilibili's own share sheet emits b23.tv links, so rejecting them outright
// turned the most common way to share a video into an error. The destination is
// not knowable locally, so only the shape is checked here; resolveShortLink
// follows exactly one redirect and revalidates the target through sourceURL
// before anything is fetched, which keeps the host allowlist intact.
var shortCode = regexp.MustCompile(`^/[A-Za-z0-9]{4,24}/?$`)

// errShortLink marks a syntactically valid b23.tv link whose destination still
// has to be resolved over the network. sourceURL stays pure so the API can keep
// validating synchronously; only Download, which runs in the worker, resolves.
var errShortLink = errors.New("media: short link requires redirect resolution")

// ValidateSourceURL only accepts explicit YouTube/Bilibili HTTPS video pages,
// plus b23.tv share links, whose destination Download resolves and revalidates
// through the same rules. Playlists, other redirectors, IPs, credentials and
// even :443 are rejected. Download canonicalizes the accepted URL, dropping all
// unrelated query fields.
func ValidateSourceURL(raw string) error {
	_, err := sourceURL(raw)
	if errors.Is(err, errShortLink) {
		// A well-formed share link is accepted here; if it turns out not to point
		// at a supported video page, the import task fails with that reason.
		return nil
	}
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
	case "b23.tv":
		// Shape only. The caller cannot know the destination without a request,
		// so this returns errShortLink and Download resolves it. A query would
		// be dropped by canonicalization, so reject rather than silently ignore.
		if !shortCode.MatchString(u.Path) || u.RawQuery != "" || u.Fragment != "" {
			return "", reject
		}
		return "https://b23.tv" + strings.TrimSuffix(u.Path, "/"), errShortLink
	}
	return "", reject
}

// resolveShortLink follows exactly one redirect from b23.tv and revalidates the
// destination through sourceURL, so the host allowlist and every other rule
// still apply. It reads no body, follows no second hop and accepts no nested
// short link, which keeps this from becoming a general-purpose fetcher.
func (t *Tools) resolveShortLink(ctx context.Context, raw string) (string, error) {
	opaque := errors.New("media: could not resolve this b23.tv short link; paste the full bilibili.com video URL instead")
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, raw, nil)
	if err != nil {
		return "", opaque
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // Inspect the hop; never follow it.
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", opaque
	}
	defer resp.Body.Close()
	target, err := resp.Location()
	if err != nil || target == nil {
		return "", opaque
	}
	// A resolved target must itself be an accepted video page; a short link that
	// points at another short link is rejected rather than followed.
	resolved, err := sourceURL(target.String())
	if err != nil {
		return "", opaque
	}
	return resolved, nil
}

func (t *Tools) downloadArgs(raw, cookies string) []string {
	return t.downloadArgsWithSubtitles(raw, cookies, true)
}

func (t *Tools) downloadArgsWithSubtitles(raw, cookies string, includeSubtitles bool) []string {
	format := "bv*+ba/b"
	// Download validates/canonicalizes the source URL before constructing argv.
	if strings.HasPrefix(raw, "https://www.bilibili.com/") ||
		strings.HasPrefix(raw, "https://bilibili.com/") ||
		strings.HasPrefix(raw, "https://m.bilibili.com/") {
		// Prefer the regular CDN, but fall back rather than fail. Bilibili serves
		// some videos' *only* audio streams from MCDN, so demanding non-MCDN audio
		// made the selection unsatisfiable and yt-dlp reported "Requested format
		// is not available" for the whole import. yt-dlp takes the first
		// satisfiable group left to right, so a regular-CDN pair still wins
		// whenever one exists. MCDN's high port can be slow; the socket timeout
		// and retries below cover that, and a slow import beats no import.
		format = "bv*[url!*=mcdn.bilivideo.cn]+ba[url!*=mcdn.bilivideo.cn]" +
			"/bv*[url!*=mcdn.bilivideo.cn]+ba" +
			"/bv*+ba" +
			"/b[url!*=mcdn.bilivideo.cn]/b"
	}
	args := []string{
		"--ignore-config", "--no-plugin-dirs", "--no-playlist", "--no-overwrites",
		"--no-cache-dir", "--no-colors", "--newline", "--progress",
		"--socket-timeout", "30", "--retries", "3", "--fragment-retries", "3",
		"--max-filesize", strconv.FormatInt(t.cfg.MaxBytes, 10),
		// yt-dlp 2026.08.19 rejects the documented `duration?<=N` optional-field
		// form outright ("Invalid filter part"), in every spacing and grouping
		// variant tried, so a missing-duration extractor still filters the video
		// out. Keeping the working form rather than shipping a filter that fails
		// every import; revisit if the pinned yt-dlp gains real `?` support.
		"--match-filters", fmt.Sprintf("!is_live & duration <= %.3f", t.cfg.MaxDuration),
		"--ffmpeg-location", t.cfg.FFmpeg,
		"--format", format, "--merge-output-format", "mp4", "--remux-video", "mp4",
	}
	if includeSubtitles {
		// ai-zh is Bilibili's machine-generated Chinese track and the only
		// subtitle most Bilibili videos carry. "zh" does not match it: yt-dlp
		// expands a request to lang and lang-suffix, never prefix-lang. Upstream
		// bilibili_downloader.py lists it first for exactly this reason.
		args = append(args, "--write-subs", "--write-auto-subs", "--sub-langs", "ai-zh,zh-Hans,zh-CN,zh,en",
			"--sub-format", "srt/vtt/best", "--convert-subs", "srt")
	} else {
		args = append(args, "--no-write-subs", "--no-write-auto-subs")
	}
	args = append(args,
		"--output", "source.%(ext)s",
		"--progress-template", `download:MEDIA_DOWNLOAD %(progress)j`,
		"--print", "after_move:MEDIA_FILE %(filepath)j",
	)
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
	return t.DownloadWithSubtitles(ctx, raw, directory, cookies, true, progress)
}

// DownloadWithSubtitles has the same ownership and safety contract as Download.
// When includeSubtitles is false, platform subtitles are neither requested,
// converted nor inspected, and subtitle is always empty. This lets callers use
// explicit uploaded subtitles without depending on unrelated platform tracks.
func (t *Tools) DownloadWithSubtitles(ctx context.Context, raw, directory, cookies string, includeSubtitles bool, progress domain.ProgressFunc) (video, subtitle string, err error) {
	if t.cfgErr != nil {
		return "", "", t.cfgErr
	}
	raw, err = sourceURL(raw)
	if errors.Is(err, errShortLink) {
		// Resolution needs the network, so it happens here in the worker rather
		// than in sourceURL, which the API calls synchronously on request input.
		if raw, err = t.resolveShortLink(ctx, raw); err != nil {
			return "", "", err
		}
	}
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
		args: t.downloadArgsWithSubtitles(raw, cookies, includeSubtitles), line: func(line string) error {
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
	if includeSubtitles {
		files, e := filepath.Glob(filepath.Join(dir, "source.*.srt"))
		if e != nil {
			return "", "", e
		}
		// Prefer Chinese, then English, deterministically. ai-zh leads because it is
		// Bilibili's machine-generated Chinese track, usually the only one present.
		for _, lang := range []string{"ai-zh", "zh-Hans", "zh-CN", "zh", "en"} {
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
	}
	if err = report(progress, "download", percent(100)); err != nil {
		return "", "", err
	}
	keep = true
	return video, subtitle, nil
}
