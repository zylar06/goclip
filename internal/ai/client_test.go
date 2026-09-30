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
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"autoclip-go/internal/domain"
)

const testKey = "sk-PRIVATE-test-key-NEVER-output"
const serverSecret = "provider-private-body"

func modelServer(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(domain.ModelSettings{BaseURL: server.URL + "/v1", Model: "mock-model", APIKey: testKey}), server
}

func answer(t *testing.T, w http.ResponseWriter, content string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": content}}},
	}); err != nil {
		t.Errorf("encode mock answer: %v", err)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func tinyImage(t *testing.T, format string) string {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&buf, img, nil)
	} else {
		err = png.Encode(&buf, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sample."+format)
	mustWrite(t, path, buf.Bytes())
	return path
}

func assertCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error", code)
	}
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	data, marshalErr := json.Marshal(ae)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, text := range []string{err.Error(), fmt.Sprintf("%+v", err), string(data)} {
		for _, secret := range []string{testKey, serverSecret} {
			if strings.Contains(text, secret) {
				t.Fatalf("error leaked private data: %s", text)
			}
		}
	}
	return ae
}

func TestValidateSettings(t *testing.T) {
	for _, base := range []string{
		"http://localhost:11434", "https://example.com/", "http://192.168.1.10:8000/v1",
		"http://127.0.0.1/api/v3", "http://[::1]:8080/v1/", "https://api.example.com/compatible-mode/v1",
	} {
		t.Run("accept_"+base, func(t *testing.T) {
			if err := ValidateSettings(domain.ModelSettings{BaseURL: base, Model: "local/mock"}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, base := range []string{
		"", "localhost:1234", "ftp://host/v1", "file:///v1", "https://", " https://host/v1",
		"https://host/v1 ", "https://user:" + testKey + "@host/v1", "https://host/v1?api_key=" + testKey,
		"https://host/v1?", "https://host/v1#x", "https://host/v1#", "https://host:99999/v1",
		"https://host:0/v1", "https://host:/v1", "https://host:abc/v1", "https://host/v1/v1",
		"https://host/v1/chat/completions", "https://host/chat/completions", "https://host/v1/chat/completions/chat/completions",
		"https://host/v1/responses", "https://host/v1/messages", "https://host/api/chat",
		"https://host/api/generate", "https://host/v1beta/models/gemini:generateContent",
		"https://host/v1//", "https://host/v1//prefix", "https://host/a/../v1", "https://host/a/./v1",
		"https://host/%76%31", "https://host/v1%2fchat", "https://host/v1\\chat", "https://host/\n",
		"https://host//", "https://host/%00", "https://host/a b", "https://[not-ipv6]/v1",
		"https://host/v1/models", "https://host/api/tags", "https://host/v1/embeddings",
	} {
		t.Run("reject_"+base, func(t *testing.T) {
			assertCode(t, ValidateSettings(domain.ModelSettings{BaseURL: base, Model: "mock", APIKey: testKey}), CodeEndpoint)
		})
	}
	for _, model := range []string{"", " mock", "mock\n", strings.Repeat("m", 257)} {
		assertCode(t, ValidateSettings(domain.ModelSettings{BaseURL: "http://localhost", Model: model}), CodeModel)
	}
	for _, key := range []string{"has spaces", "line\nbreak", "non-ascii-密钥", strings.Repeat("s", 4097)} {
		assertCode(t, ValidateSettings(domain.ModelSettings{BaseURL: "http://localhost", Model: "mock", APIKey: key}), CodeAuth)
	}
}

func TestEndpointCanonicalization(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"http://localhost:8000", "http://localhost:8000/v1/chat/completions"},
		{"http://localhost:8000/", "http://localhost:8000/v1/chat/completions"},
		{"http://localhost:8000/v1/", "http://localhost:8000/v1/chat/completions"},
		{"http://localhost:8000/api/v3", "http://localhost:8000/api/v3/chat/completions"},
	} {
		value, err := endpointURL(tc.base)
		if err != nil || value != tc.want {
			t.Fatalf("%s: %s, %v", tc.base, value, err)
		}
	}
}

func decodeRequest(t *testing.T, r *http.Request) (string, []contentPart) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	if r.Header.Get("Authorization") != "Bearer "+testKey || r.Header.Get("Content-Type") != "application/json" {
		t.Error("missing request headers")
	}
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		MaxTokens int  `json:"max_tokens"`
		Stream    bool `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("invalid request: %v", err)
		return "", nil
	}
	if body.Model != "mock-model" || len(body.Messages) != 1 || body.Stream || body.MaxTokens <= 0 {
		t.Error("wrong model/messages/output limit")
		return "", nil
	}
	if body.Messages[0].Role != "user" {
		t.Error("unexpected role")
	}
	var prompt string
	if err := json.Unmarshal(body.Messages[0].Content, &prompt); err == nil {
		return prompt, nil
	}
	var parts []contentPart
	if err := json.Unmarshal(body.Messages[0].Content, &parts); err != nil || len(parts) == 0 {
		t.Errorf("invalid multimodal content: %v", err)
		return "", nil
	}
	return parts[0].Text, parts
}

func assertImages(t *testing.T, parts []contentPart, count int) {
	t.Helper()
	found := 0
	for _, p := range parts {
		if p.Type != "image_url" {
			continue
		}
		found++
		if p.ImageURL == nil {
			t.Error("missing image_url")
			continue
		}
		split := strings.SplitN(p.ImageURL.URL, ";base64,", 2)
		if len(split) != 2 || !strings.HasPrefix(split[0], "data:image/") {
			t.Error("image must be embedded")
			continue
		}
		data, err := base64.StdEncoding.DecodeString(split[1])
		if err != nil {
			t.Error(err)
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Error("not a real decodable image", err)
			continue
		}
		r, g, b, _ := img.At(0, 0).RGBA()
		if r <= g || r <= b {
			t.Error("expected genuine red image fixture")
		}
	}
	if found != count {
		t.Errorf("want %d images, got %d", count, found)
	}
}

func TestIndependentSmokeTests(t *testing.T) {
	var textCalls, imageCalls atomic.Int32
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, parts := decodeRequest(t, r)
		if len(parts) == 0 {
			textCalls.Add(1)
			answer(t, w, "OK")
		} else {
			imageCalls.Add(1)
			assertImages(t, parts, 1)
			answer(t, w, "red")
		}
	})
	if err := client.Test(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if imageCalls.Load() != 1 || textCalls.Load() != 0 {
		t.Fatal("vision smoke must not perform a text prerequisite")
	}
	if err := client.Test(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if imageCalls.Load() != 1 || textCalls.Load() != 1 {
		t.Fatal("smoke tests are not independent")
	}
}

func TestVisionSmokeMeetsProviderMinimumImageSize(t *testing.T) {
	var calls atomic.Int32
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, parts := decodeRequest(t, r)
		assertImages(t, parts, 1)
		for _, part := range parts {
			if part.Type != "image_url" || part.ImageURL == nil {
				continue
			}
			encoded := strings.SplitN(part.ImageURL.URL, ";base64,", 2)
			if len(encoded) != 2 {
				t.Error("missing embedded image")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			data, err := base64.StdEncoding.DecodeString(encoded[1])
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			// DashScope rejects the former 8x8 fixture: each side must exceed 10.
			if cfg.Width <= 10 || cfg.Height <= 10 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"code":"invalid_parameter_error","message":"image sides must exceed 10"}}`)
				return
			}
			if cfg.Width != 64 || cfg.Height != 64 {
				t.Errorf("expected bounded 64x64 smoke fixture, got %dx%d", cfg.Width, cfg.Height)
			}
		}
		answer(t, w, "red")
	})
	if err := client.Test(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected exactly one vision request, got %d", calls.Load())
	}
}

func TestCompleteEmbedsImages(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			path := tinyImage(t, format)
			client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				prompt, parts := decodeRequest(t, r)
				assertImages(t, parts, 1)
				if prompt != "analyze" || !strings.Contains(parts[1].Text, "12.500") {
					t.Error("missing prompt or source timestamp")
				}
				raw, err := json.Marshal(parts)
				if err != nil {
					t.Error(err)
				}
				if strings.Contains(string(raw), filepath.Base(path)) || strings.Contains(string(raw), testKey) {
					t.Error("file path or key entered model material")
				}
				answer(t, w, "done")
			})
			got, err := client.Complete(context.Background(), "analyze", []domain.Frame{{Time: 12.5, Path: path}})
			if err != nil || got != "done" {
				t.Fatalf("%s %v", got, err)
			}
		})
	}
}

func TestHTTPFailuresAreClassifiedRedactedAndNeverRetried(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, code string
		retry      bool
	}{
		{401, "", CodeAuth, false}, {403, "", CodeAuth, false},
		{429, "", CodeRateLimit, true}, {408, "", CodeTimeout, true}, {504, "", CodeTimeout, true},
		{404, `{"error":{"code":"model_not_found"}}`, CodeModel, false},
		{400, `{"error":{"param":"model"}}`, CodeModel, false},
		{400, `{"error":{"code":"invalid_api_key"}}`, CodeAuth, false},
		{400, `{"error":{"type":"insufficient_quota"}}`, CodeRateLimit, true},
		{404, "", CodeEndpoint, false}, {500, "", CodeEndpoint, true}, {503, "", CodeEndpoint, true},
		{400, `{"error":{"code":"unknown"}}`, CodeEndpoint, false},
	} {
		t.Run(fmt.Sprintf("%d_%s", tc.status, tc.code), func(t *testing.T) {
			var calls atomic.Int32
			client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				body := tc.body
				if body == "" {
					body = `{"error":{"message":"` + testKey + " " + serverSecret + `"}}`
				}
				if _, err := io.WriteString(w, body); err != nil {
					t.Error(err)
				}
			})
			_, err := client.Complete(context.Background(), "test", nil)
			ae := assertCode(t, err, tc.code)
			if ae.HTTPStatus != tc.status || ae.Retryable != tc.retry || calls.Load() != 1 {
				t.Fatalf("wrong status/retry behavior: %+v / calls=%d", ae, calls.Load())
			}
		})
	}
}

func TestInvalidResponses(t *testing.T) {
	for name, body := range map[string]string{
		"html":            "<html>" + serverSecret + "</html>",
		"null":            "null",
		"missing":         `{}`,
		"duplicate_keys":  `{"choices":[],"choices":[{"message":{"content":"ok"}}]}`,
		"case_alias_keys": `{"choices":[],"CHOICES":[{"message":{"content":"ok"}}]}`,
		"empty_choices":   `{"choices":[]}`,
		"missing_content": `{"choices":[{"message":{}}]}`,
		"empty_content":   `{"choices":[{"message":{"content":" "}}]}`,
		"wrong_content":   `{"choices":[{"message":{"content":[]}}]}`,
		"truncated":       `{"choices":[{"finish_reason":"length","message":{"content":"secret"}}]}`,
		"filtered":        `{"choices":[{"finish_reason":"content_filter","message":{"content":"secret"}}]}`,
		"refusal":         `{"choices":[{"message":{"refusal":"private","content":"secret"}}]}`,
		"tools":           `{"choices":[{"finish_reason":"tool_calls","message":{"content":"secret"}}]}`,
		"error":           `{"error":{"message":"private"},"choices":[{"message":{"content":"ok"}}]}`,
		"trailing":        `{"choices":[{"message":{"content":"ok"}}]} {}`,
		"too_large":       strings.Repeat("x", maxResponseBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				// An oversized response can be cut off by the client by design.
				_, err := io.WriteString(w, body)
				if err != nil && name != "too_large" {
					t.Error(err)
				}
			})
			_, err := client.Complete(context.Background(), "test", nil)
			assertCode(t, err, CodeInvalidResponse)
		})
	}
}

func TestRedirectDoesNotForwardCredentials(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer target.Close()
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	_, err := client.Complete(context.Background(), "test", nil)
	assertCode(t, err, CodeEndpoint)
	if targetCalls.Load() != 0 {
		t.Fatal("followed a paid redirect")
	}
}

func TestTimeoutAndCancellation(t *testing.T) {
	var calls atomic.Int32
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.Complete(ctx, "test", nil)
	assertCode(t, err, CodeTimeout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lost deadline sentinel")
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	_, err = client.Complete(ctx2, "test", nil)
	ae := assertCode(t, err, CodeTimeout)
	if !errors.Is(err, context.Canceled) || ae.Retryable || calls.Load() != 1 {
		t.Fatal("pre-cancelled request performed work or lost cancellation")
	}
}

func TestBodyReadDeadline(t *testing.T) {
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.Complete(ctx, "test", nil)
	assertCode(t, err, CodeTimeout)
}

func TestBadImagesAndPromptsNeverCallProvider(t *testing.T) {
	var calls atomic.Int32
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		answer(t, w, "unexpected")
	})
	bad := filepath.Join(t.TempDir(), "private-"+testKey+".png")
	mustWrite(t, bad, []byte("not an image "+serverSecret))
	big := filepath.Join(t.TempDir(), "big.png")
	mustWrite(t, big, make([]byte, maxImageBytes+1))
	for _, frames := range [][]domain.Frame{
		{{Path: bad}}, {{Path: bad + "missing"}}, {{Path: big}}, {{Path: t.TempDir()}},
		{{Path: bad, Time: -1}}, make([]domain.Frame, 61),
	} {
		_, err := client.Complete(context.Background(), "test", frames)
		assertCode(t, err, CodeInvalidResponse)
	}
	for _, prompt := range []string{"", " ", strings.Repeat("x", maxPromptBytes+1), string([]byte{0xff})} {
		_, err := client.Complete(context.Background(), prompt, nil)
		assertCode(t, err, CodeInvalidResponse)
	}
	if calls.Load() != 0 {
		t.Fatal("bad local input should not cost a request")
	}
}

func TestClientConfigurationAndConcurrentUse(t *testing.T) {
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) { answer(t, w, "OK") })
	if client.http.Timeout != visionRequestTimeout {
		t.Fatal("unbounded client")
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok || transport.TLSHandshakeTimeout == 0 || transport.ResponseHeaderTimeout == 0 || transport.MaxResponseHeaderBytes == 0 {
		t.Fatal("unbounded transport")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := client.Test(context.Background(), false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestNoAuthenticationForLocalAnonymousServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected authorization")
		}
		answer(t, w, "OK")
	}))
	defer server.Close()
	client := New(domain.ModelSettings{BaseURL: server.URL, Model: "local"})
	if err := client.Test(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}

func TestSmokeWrongAnswerAndInvalidSettings(t *testing.T) {
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) { answer(t, w, "incorrect") })
	assertCode(t, client.Test(context.Background(), true), CodeInvalidResponse)
	assertCode(t, client.Test(context.Background(), false), CodeInvalidResponse)
	assertCode(t, New(domain.ModelSettings{}).Test(context.Background(), false), CodeEndpoint)
	var absent *Client
	_, err := absent.Complete(context.Background(), "test", nil)
	assertCode(t, err, CodeModel)
}

// Live qwen3-vl-plus vision scans of a 300-second source exceeded the former
// 45-second response-header cap and failed as timeouts after the request had
// already been billed. Image requests must get a materially longer budget than
// text ones, and the header cap must not undercut it.
func TestImageRequestsGetALongerDeadlineThanText(t *testing.T) {
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) { answer(t, w, "OK") })
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected a configured transport")
	}
	if visionRequestTimeout <= requestTimeout {
		t.Fatalf("image budget %v must exceed the text budget %v", visionRequestTimeout, requestTimeout)
	}
	if transport.ResponseHeaderTimeout < requestTimeout {
		t.Fatalf("header cap %v undercuts the text request budget %v", transport.ResponseHeaderTimeout, requestTimeout)
	}
	if client.http.Timeout < visionRequestTimeout {
		t.Fatalf("client ceiling %v cuts off image requests early", client.http.Timeout)
	}
}

// Upstream intelligence.py indexes choices[0] and ignores extras. Requiring
// exactly one choice, allowlisting finish reasons, and failing on any non-null
// `error` key made whole families of OpenAI-compatible gateways unusable here
// while working in the Python app.
func TestClientAcceptsCommonCompatibleGatewayShapes(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"extra_choices", `{"choices":[{"message":{"content":"ok"}},{"message":{"content":"ignored"}}]}`},
		{"empty_error_object", `{"error":{},"choices":[{"message":{"content":"ok"}}]}`},
		{"null_error", `{"error":null,"choices":[{"message":{"content":"ok"}}]}`},
		{"finish_eos", `{"choices":[{"finish_reason":"eos","message":{"content":"ok"}}]}`},
		{"finish_end_turn", `{"choices":[{"finish_reason":"end_turn","message":{"content":"ok"}}]}`},
		{"finish_uppercase_stop", `{"choices":[{"finish_reason":"STOP","message":{"content":"ok"}}]}`},
		{"finish_complete", `{"choices":[{"finish_reason":"complete","message":{"content":"ok"}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := modelServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Error(err)
				}
			})
			got, err := client.Complete(context.Background(), "prompt", nil)
			if err != nil {
				t.Fatalf("%s must be accepted: %v", tc.name, err)
			}
			if got != "ok" {
				t.Fatalf("expected the first choice's content, got %q", got)
			}
		})
	}
}

// Tolerance must not swallow a gateway that genuinely reports a failure, nor a
// finish reason that means the text is incomplete or withheld.
func TestClientStillRejectsRealErrorsAndIncompleteOutput(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"populated_error", `{"error":{"message":"private"},"choices":[{"message":{"content":"ok"}}]}`},
		{"truncated", `{"choices":[{"finish_reason":"length","message":{"content":"secret"}}]}`},
		{"filtered", `{"choices":[{"finish_reason":"content_filter","message":{"content":"secret"}}]}`},
		{"max_tokens", `{"choices":[{"finish_reason":"max_tokens","message":{"content":"secret"}}]}`},
		{"tool_calls", `{"choices":[{"finish_reason":"tool_calls","message":{"content":"secret"}}]}`},
		{"no_choices", `{"choices":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := modelServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Error(err)
				}
			})
			_, err := client.Complete(context.Background(), "prompt", nil)
			var e *Error
			if !errors.As(err, &e) || e.Code != CodeInvalidResponse {
				t.Fatalf("%s must stay invalid_response, got %v", tc.name, err)
			}
			// Provider text must never leak into the error message.
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked provider content: %v", err)
			}
		})
	}
}
