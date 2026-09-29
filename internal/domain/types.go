package domain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
)

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

func ValidID(id string) bool { return idPattern.MatchString(id) }

type Project struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	URL       string  `json:"url,omitempty"`
	Status    string  `json:"status"`
	Duration  float64 `json:"duration"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	Error     string  `json:"error,omitempty"`
}
type Cue struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}
type Scene struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Evidence string  `json:"evidence"`
}
type Candidate struct {
	Scene
	Score float64 `json:"score"`
	Kind  string  `json:"kind"`
}
type Draft struct {
	ID                   string  `json:"id"`
	ProjectID            string  `json:"project_id,omitempty"`
	Title                string  `json:"title"`
	Hook                 string  `json:"hook"`
	Scenes               []Scene `json:"scenes"`
	Language             string  `json:"language"`
	Aspect               string  `json:"aspect"`
	Layout               string  `json:"layout"`
	CropX                float64 `json:"crop_x"`
	TitleStyle           string  `json:"title_style"`
	TitleTemplateVersion int     `json:"title_template_version"`
	TitleMotion          bool    `json:"title_motion"`
	TitleScale           float64 `json:"title_scale"`
	TitleY               float64 `json:"title_y"`
	TitleAccent          *string `json:"title_accent"`
	Subtitles            bool    `json:"subtitles"`
	OriginalAudio        bool    `json:"original_audio"`
	Revision             int     `json:"revision"`
	UpdatedAt            string  `json:"updated_at"`
	Origin               string  `json:"origin"`
	ParentDraftID        *string `json:"parent_draft_id,omitempty"`
	ParentRevision       *int    `json:"parent_revision,omitempty"`
}

var Styles = []string{"plain", "impact", "card", "comic", "neon", "arena", "editorial", "pixel", "frosted"}

func NewDraft(title string, scenes []Scene) Draft {
	return Draft{ID: ID(), Title: title, Scenes: scenes, Language: "source", Aspect: "original", Layout: "fit", CropX: .5, TitleStyle: "plain", TitleTemplateVersion: 1, TitleMotion: true, TitleScale: 1, TitleY: .12, Subtitles: true, OriginalAudio: true, Revision: 1, Origin: "manual", UpdatedAt: Now()}
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func (d Draft) Validate(duration float64) error {
	if !ValidID(d.ID) || strings.TrimSpace(d.Title) == "" || len([]rune(d.Title)) > 200 || len([]rune(d.Hook)) > 120 {
		return errors.New("invalid draft id, title or hook")
	}
	if len(d.Scenes) < 1 || len(d.Scenes) > 30 {
		return errors.New("draft requires 1–30 scenes")
	}
	total := 0.0
	ids := map[string]bool{}
	for _, s := range d.Scenes {
		if !ValidID(s.ID) || ids[s.ID] || len([]rune(s.Label)) > 120 || len([]rune(s.Evidence)) > 1000 || !finite(s.Start) || !finite(s.End) || s.Start < 0 || s.End-s.Start < .1 || s.End > duration+.05 {
			return errors.New("invalid or out-of-range scene")
		}
		ids[s.ID] = true
		total += s.End - s.Start
	}
	if total > 1800 {
		return errors.New("draft exceeds 30 minutes")
	}
	if !slices.Contains([]string{"source", "zh", "en", "ja"}, d.Language) || !slices.Contains([]string{"original", "portrait", "landscape"}, d.Aspect) || !slices.Contains([]string{"fit", "crop", "blur"}, d.Layout) || !slices.Contains(Styles, d.TitleStyle) {
		return errors.New("invalid draft options")
	}
	if !finite(d.CropX) || d.CropX < 0 || d.CropX > 1 || !finite(d.TitleScale) || d.TitleScale < .75 || d.TitleScale > 1.2 || !finite(d.TitleY) || d.TitleY < .06 || d.TitleY > .70 {
		return errors.New("invalid title/crop placement")
	}
	if d.TitleAccent != nil && !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(*d.TitleAccent) {
		return errors.New("invalid accent color")
	}
	if d.Revision < 1 {
		return errors.New("invalid revision")
	}
	if d.TitleTemplateVersion != 1 && d.TitleTemplateVersion != 6 {
		return errors.New("historical title template versions are not supported")
	}
	return nil
}

type Task struct {
	ID              string          `json:"id"`
	ProjectID       string          `json:"project_id"`
	Kind            string          `json:"kind"`
	Status          string          `json:"status"`
	Stage           string          `json:"stage"`
	Progress        *float64        `json:"progress"`
	CompletedSteps  []string        `json:"completed_steps"`
	Heartbeat       string          `json:"heartbeat"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
	Error           string          `json:"error,omitempty"`
	Retryable       bool            `json:"retryable"`
	CancelRequested bool            `json:"cancel_requested"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

func (t Task) Terminal() bool {
	return slices.Contains([]string{"completed", "failed", "interrupted", "cancelled"}, t.Status)
}

type Export struct {
	TaskID    string `json:"task_id"`
	DraftID   string `json:"draft_id"`
	Revision  int    `json:"revision"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
}
type AnalysisOptions struct {
	Mode        string   `json:"mode"`
	AllowVisual bool     `json:"allow_visual"`
	Confirmed   bool     `json:"confirmed"`
	Goals       []string `json:"goals"`
	Duration    int      `json:"duration"`
	Aspect      string   `json:"aspect"`
	Language    string   `json:"language"`
	Instruction string   `json:"instruction"`
}
type ModelSettings struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	APIKey  string `json:"api_key"`
}
type ModelStatus struct {
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	Configured bool   `json:"configured"`
}
type ExportPayload struct {
	Draft Draft `json:"draft"`
}
type ImportPayload struct {
	Video    string `json:"video"`
	Subtitle string `json:"subtitle"`
	URL      string `json:"url"`
}
type ProgressFunc func(stage string, percent *float64) error
type Frame struct {
	Time float64 `json:"time"`
	Path string  `json:"path"`
}
