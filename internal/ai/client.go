package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"autoclip-go/internal/domain"
)

const (
	requestTimeout   = 90 * time.Second
	maxResponseBytes = 2 << 20
	maxPromptBytes   = 512 << 10
	maxImageBytes    = 4 << 20
	maxImagesBytes   = 24 << 20
	maxFrames        = 60
)

// Client is immutable after New and safe for concurrent requests. Invalid
// settings are retained as a safe error because the public constructor has no
// error return. Local unauthenticated compatible servers may use an empty key.
type Client struct {
	settings domain.ModelSettings
	endpoint string
	http     *http.Client
	err      error
}

func New(settings domain.ModelSettings) *Client {
	c := &Client{settings: settings}
	c.err = ValidateSettings(settings)
	if c.err == nil {
		c.endpoint, c.err = endpointURL(settings.BaseURL)
	}
	c.http = &http.Client{
		Timeout: requestTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                  http.ProxyFromEnvironment,
			DialContext:            (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:    10 * time.Second,
			ResponseHeaderTimeout:  45 * time.Second,
			IdleConnTimeout:        60 * time.Second,
			MaxIdleConns:           8,
			MaxConnsPerHost:        4,
			MaxResponseHeaderBytes: 64 << 10,
		},
	}
	return c
}

func ValidateSettings(settings domain.ModelSettings) error {
	if _, err := endpointURL(settings.BaseURL); err != nil {
		return err
	}
	if strings.TrimSpace(settings.Model) != settings.Model || settings.Model == "" ||
		len(settings.Model) > 256 || !utf8.ValidString(settings.Model) || hasControl(settings.Model) {
		return failure(CodeModel, "Set a nonempty model identifier without surrounding whitespace or control characters.")
	}
	if len(settings.APIKey) > 4096 {
		return failure(CodeAuth, "API key is too long.")
	}
	for _, ch := range settings.APIKey {
		if ch < 33 || ch > 126 {
			return failure(CodeAuth, "API key must contain only printable non-space ASCII characters; remove whitespace.")
		}
	}
	return nil
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

func endpointURL(raw string) (string, error) {
	bad := func(message string) (string, error) { return "", failure(CodeEndpoint, message) }
	if raw == "" || len(raw) > 2048 || strings.TrimSpace(raw) != raw || hasControl(raw) {
		return bad("Set an absolute http:// or https:// API base URL, without whitespace.")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return bad("Use an absolute HTTP(S) API base URL; LAN, localhost and IPv6 hosts are supported.")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return bad("Remove URL userinfo, query parameters and fragments; put credentials in API key, not the URL.")
	}
	if strings.ContainsAny(u.Hostname(), " /\\%") || strings.HasSuffix(u.Host, ":") {
		return bad("API base URL has an invalid host or port.")
	}
	if (strings.HasPrefix(u.Host, "[") || strings.Contains(u.Hostname(), ":")) && net.ParseIP(u.Hostname()) == nil {
		return bad("Bracketed or colon-containing hosts must be valid IPv6 addresses.")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return bad("API port must be between 1 and 65535.")
		}
	}
	p := strings.TrimSuffix(u.Path, "/")
	if u.RawPath != "" || strings.Contains(raw, "%") || strings.ContainsAny(p, "\\") ||
		strings.Contains(u.Path, "//") || strings.ContainsFunc(p, unicode.IsSpace) {
		return bad("Remove encoded paths, backslashes or duplicate slashes from the API base URL.")
	}
	parts := strings.Split(strings.TrimPrefix(strings.ToLower(p), "/"), "/")
	for i, part := range parts {
		if part == "." || part == ".." || (part == "" && len(parts) > 1) {
			return bad("API base URL must not contain empty or dot path segments.")
		}
		if i > 0 && part == parts[i-1] {
			return bad("Remove duplicated API path segments (for example /v1/v1).")
		}
		switch part {
		case "chat", "completions":
			return bad("Supply the API base URL, not /chat/completions; that suffix is added exactly once.")
		case "responses", "messages", "generate", "generatecontent", "streamgeneratecontent", "v1beta", "v1beta1",
			"models", "embeddings", "images", "audio", "tags", "show":
			return bad("Native Responses, Anthropic, Gemini and Ollama paths are unsupported; use their OpenAI-compatible base URL.")
		}
		if strings.Contains(part, ":") {
			return bad("Native model action paths are unsupported; use an OpenAI-compatible API base URL.")
		}
	}
	if p == "" {
		p = "/v1"
	}
	u.Path = p + "/chat/completions"
	return u.String(), nil
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

// Test performs exactly one independent smoke request. The vision test sends
// an actual in-memory 8x8 red PNG and checks the answer, not just text support.
func (c *Client) Test(ctx context.Context, vision bool) error {
	prompt := "Connection test. Reply with exactly OK."
	var parts []contentPart
	if vision {
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return invalid("Could not construct the vision smoke image.")
		}
		prompt = "Inspect this image. Reply with only the English name of its dominant color."
		parts = []contentPart{{Type: "text", Text: prompt}, imagePart(buf.Bytes(), "png")}
	}
	value, err := c.complete(ctx, prompt, parts, 32)
	if err != nil {
		return err
	}
	want := "ok"
	if vision {
		want = "red"
	}
	if strings.ToLower(strings.Trim(strings.TrimSpace(value), ".!\"'`")) != want {
		return invalid("Smoke test returned an unexpected answer; check model compatibility and image support.")
	}
	return nil
}

// Complete encodes local Frame.Path image bytes as data URLs; filesystem paths
// and externally fetched image URLs are never sent to the provider.
func (c *Client) Complete(ctx context.Context, prompt string, frames []domain.Frame) (string, error) {
	if err := c.ready(ctx); err != nil {
		return "", err
	}
	if strings.TrimSpace(prompt) == "" || len(prompt) > maxPromptBytes || !utf8.ValidString(prompt) {
		return "", invalid("Prompt must be valid nonempty UTF-8 within 512 KiB; split the input.")
	}
	if len(frames) > maxFrames {
		return "", invalid("At most 60 sampled images may be sent per request.")
	}
	var parts []contentPart
	if len(frames) > 0 {
		parts = append(parts, contentPart{Type: "text", Text: prompt})
	}
	total := 0
	for _, frame := range frames {
		if err := contextError(ctx); err != nil {
			return "", err
		}
		if !finite(frame.Time) || frame.Time < 0 {
			return "", invalid("Image timestamps must be finite and nonnegative.")
		}
		data, format, err := readImage(frame.Path)
		if err != nil {
			return "", err
		}
		total += len(data)
		if total > maxImagesBytes {
			return "", invalid("Sampled images exceed the 24 MiB request budget; use smaller images.")
		}
		parts = append(parts, contentPart{Type: "text", Text: fmt.Sprintf("Source timestamp %.3f seconds", frame.Time)}, imagePart(data, format))
	}
	return c.complete(ctx, prompt, parts, 8192)
}

func imagePart(data []byte, format string) contentPart {
	return contentPart{Type: "image_url", ImageURL: &imageURL{URL: "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(data)}}
}

func readImage(path string) ([]byte, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", invalid("Cannot read sampled image; check that the local image file exists and is readable.")
	}
	info, statErr := f.Stat()
	var data []byte
	var readErr error
	if statErr == nil && info.Mode().IsRegular() && info.Size() <= maxImageBytes {
		data, readErr = io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	}
	closeErr := f.Close()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxImageBytes || readErr != nil || closeErr != nil || len(data) > maxImageBytes {
		return nil, "", invalid("Sampled image must be a readable regular file no larger than 4 MiB.")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return nil, "", invalid("Sampled image must be a valid PNG or JPEG with at most 16 million pixels.")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, "", invalid("Sampled image is truncated or invalid.")
	}
	return data, format, nil
}

func (c *Client) ready(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if c == nil {
		return failure(CodeModel, "Configure a model before making a request.")
	}
	if c.err != nil {
		return c.err
	}
	if c.http == nil || c.endpoint == "" {
		return failure(CodeModel, "Initialize the model client with New before making a request.")
	}
	return nil
}

func (c *Client) complete(ctx context.Context, prompt string, parts []contentPart, maxTokens int) (string, error) {
	if err := c.ready(ctx); err != nil {
		return "", err
	}
	var content any = prompt
	if len(parts) > 0 {
		content = parts
	}
	body, err := json.Marshal(struct {
		Model     string           `json:"model"`
		Messages  []map[string]any `json:"messages"`
		MaxTokens int              `json:"max_tokens"`
		Stream    bool             `json:"stream"`
	}{c.settings.Model, []map[string]any{{"role": "user", "content": content}}, maxTokens, false})
	if err != nil {
		return "", invalid("Could not encode model request.")
	}
	bounded, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(bounded, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", failure(CodeEndpoint, "Could not construct the compatible API request.")
	}
	req.GetBody = nil // Do not make a paid POST replayable.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.settings.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.settings.APIKey)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return "", transportError(bounded, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	closeErr := res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", statusError(res.StatusCode, data)
	}
	if readErr != nil || closeErr != nil {
		return "", transportError(bounded, errors.Join(readErr, closeErr))
	}
	if len(data) > maxResponseBytes {
		return "", invalid("Provider response exceeded the 2 MiB safety limit.")
	}
	if err := contextError(bounded); err != nil {
		return "", err
	}
	var response struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content *string `json:"content"`
				Refusal string  `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
		Error json.RawMessage `json:"error"`
	}
	if !utf8.Valid(data) || checkJSON(data) != nil || json.Unmarshal(data, &response) != nil || len(response.Choices) != 1 || (len(response.Error) > 0 && string(response.Error) != "null") {
		return "", invalid("Provider did not return one valid Chat Completions choice; verify the compatible endpoint.")
	}
	choice := response.Choices[0]
	if choice.FinishReason != "" && choice.FinishReason != "stop" {
		return "", invalid("Provider output was truncated, filtered, or not a completed text answer; no retry was made.")
	}
	if choice.Message.Refusal != "" || choice.Message.Content == nil || strings.TrimSpace(*choice.Message.Content) == "" {
		return "", invalid("Provider returned an empty answer or refusal.")
	}
	return *choice.Message.Content, nil
}

func transportError(ctx context.Context, err error) error {
	if e := contextError(ctx); e != nil {
		return e
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		e := failure(CodeTimeout, "Model request timed out; retry only with explicit consent.")
		e.cause = context.DeadlineExceeded
		return e
	}
	return failure(CodeEndpoint, "Cannot reach the compatible API or its connection was interrupted; check address, TLS and network.")
}

func statusError(status int, data []byte) error {
	var body struct {
		Error struct {
			Code  string `json:"code"`
			Type  string `json:"type"`
			Param string `json:"param"`
		} `json:"error"`
	}
	// Parse only machine labels for classification; never copy server messages.
	if len(data) <= maxResponseBytes {
		if err := json.Unmarshal(data, &body); err != nil {
			body.Error.Code = ""
			body.Error.Type = ""
			body.Error.Param = ""
		}
	}
	modelError := body.Error.Param == "model"
	authError, rateError := false, false
	for _, label := range []string{body.Error.Code, body.Error.Type} {
		switch label {
		case "model_not_found", "invalid_model", "model_not_supported", "unsupported_model", "model_not_available":
			modelError = true
		case "invalid_api_key", "authentication_error", "invalid_authentication":
			authError = true
		case "rate_limit_exceeded", "insufficient_quota":
			rateError = true
		}
	}
	var e *Error
	switch {
	case status == 401 || status == 403 || authError:
		e = failure(CodeAuth, "Authentication failed; check API key and model access permissions.")
	case status == 429 || rateError:
		e = failure(CodeRateLimit, "Provider rate or quota limit reached; check quota and explicitly retry later.")
	case status == 408 || status == 504:
		e = failure(CodeTimeout, "Provider timed out; retry only with explicit consent.")
	case modelError:
		e = failure(CodeModel, "Provider rejected the model identifier; select a model available on this compatible endpoint.")
	default:
		e = failure(CodeEndpoint, "Compatible API request failed; check base URL, API compatibility and provider availability.")
		e.Retryable = status >= 500
	}
	e.HTTPStatus = status
	return e
}
