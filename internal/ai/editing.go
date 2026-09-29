package ai

import (
	"context"
	"math"
	"strings"

	"autoclip-go/internal/domain"
)

type editedScene struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type editResult struct {
	Title  string        `json:"title"`
	Hook   string        `json:"hook"`
	Scenes []editedScene `json:"scenes"`
}

type translatedCue struct {
	Index *int   `json:"index"`
	Text  string `json:"text"`
}

type translationResult struct {
	Title  string          `json:"title"`
	Hook   string          `json:"hook"`
	Scenes []editedScene   `json:"scenes"`
	Cues   []translatedCue `json:"cues"`
}

func draftDuration(draft domain.Draft) (float64, error) {
	duration := 0.0
	for _, scene := range draft.Scenes {
		duration = math.Max(duration, scene.End)
	}
	if !finite(duration) || duration > 7200 || draft.Validate(duration) != nil {
		return 0, invalid("Provide a valid source-bounded draft for editing.")
	}
	return duration, nil
}

func applyEdit(draft domain.Draft, result editResult, duration float64) (domain.Draft, error) {
	if !textOK(result.Title, 200, true) || !textOK(result.Hook, 120, false) || len(result.Scenes) != len(draft.Scenes) {
		return domain.Draft{}, invalid("Edited title, hook or scene count is invalid.")
	}
	labels := map[string]string{}
	for _, scene := range result.Scenes {
		if _, exists := labels[scene.ID]; exists || !textOK(scene.Label, 120, false) {
			return domain.Draft{}, invalid("Edited scene IDs must be unique and labels bounded.")
		}
		labels[scene.ID] = scene.Label
	}
	out := draft
	out.Scenes = append([]domain.Scene(nil), draft.Scenes...)
	out.Title, out.Hook = result.Title, result.Hook
	for i := range out.Scenes {
		label, exists := labels[out.Scenes[i].ID]
		if !exists {
			return domain.Draft{}, invalid("Edited scene IDs must exactly match the original draft.")
		}
		out.Scenes[i].Label = label
	}
	if out.Validate(duration) != nil {
		return domain.Draft{}, invalid("Edited draft failed domain validation.")
	}
	return out, nil
}

// Rewrite returns an unsaved edit, preserving revision, IDs, source evidence,
// timestamps and rendering settings. Persistence/version changes belong to store.
func Rewrite(ctx context.Context, client *Client, draft domain.Draft, instruction string) (domain.Draft, error) {
	duration, err := draftDuration(draft)
	if err != nil {
		return domain.Draft{}, err
	}
	if strings.TrimSpace(instruction) == "" || !textOK(instruction, 4000, true) {
		return domain.Draft{}, invalid("Rewrite instruction must contain 1–4000 characters.")
	}
	result, err := ask[editResult](ctx, client, "rewrite", map[string]any{
		"title": draft.Title, "hook": draft.Hook, "scenes": draft.Scenes, "language": draft.Language, "instruction": instruction,
	}, nil)
	if err != nil {
		return domain.Draft{}, atStage("rewrite", err)
	}
	if err := contextError(ctx); err != nil {
		return domain.Draft{}, err
	}
	out, err := applyEdit(draft, result, duration)
	return out, atStage("rewrite", err)
}

// Translate is a zero-request copy when Language == "source". Otherwise it
// translates only subtitles intersecting selected scenes, preserving the entire
// supplied cue list and all original timings/order. Scene evidence stays verbatim.
func Translate(ctx context.Context, client *Client, draft domain.Draft, cues []domain.Cue) (domain.Draft, []domain.Cue, error) {
	if err := contextError(ctx); err != nil {
		return domain.Draft{}, nil, err
	}
	duration, err := draftDuration(draft)
	if err != nil {
		return domain.Draft{}, nil, err
	}
	copyDraft := draft
	copyDraft.Scenes = append([]domain.Scene(nil), draft.Scenes...)
	copyCues := append([]domain.Cue(nil), cues...)
	if draft.Language == "source" {
		return copyDraft, copyCues, nil
	}
	if len(cues) > 0 {
		if _, _, err := normalizeCues(cues); err != nil {
			return domain.Draft{}, nil, err
		}
	}
	selected := make([]translatedCue, 0)
	for i, cue := range cues {
		for _, scene := range draft.Scenes {
			if cue.Start < scene.End && cue.End > scene.Start {
				index := i
				selected = append(selected, translatedCue{&index, cue.Text})
				break
			}
		}
	}
	result, err := ask[translationResult](ctx, client, "translate", map[string]any{
		"title": draft.Title, "hook": draft.Hook, "scenes": draft.Scenes,
		"target_language": draft.Language, "cues": selected,
	}, nil)
	if err != nil {
		return domain.Draft{}, nil, atStage("translate", err)
	}
	if err := contextError(ctx); err != nil {
		return domain.Draft{}, nil, err
	}
	out, err := applyEdit(draft, editResult{result.Title, result.Hook, result.Scenes}, duration)
	if err != nil {
		return domain.Draft{}, nil, atStage("translate", err)
	}
	if result.Cues == nil || len(result.Cues) != len(selected) {
		return domain.Draft{}, nil, atStage("translate", invalid("Translation must return every requested cue exactly once."))
	}
	expected, seen := map[int]bool{}, map[int]bool{}
	for _, cue := range selected {
		expected[*cue.Index] = true
	}
	for _, cue := range result.Cues {
		if cue.Index == nil || !expected[*cue.Index] || seen[*cue.Index] || !textOK(cue.Text, 8000, true) {
			return domain.Draft{}, nil, atStage("translate", invalid("Translation changed cue identity or returned empty/oversized text."))
		}
		copyCues[*cue.Index].Text = cue.Text
		seen[*cue.Index] = true
	}
	return out, copyCues, nil
}
