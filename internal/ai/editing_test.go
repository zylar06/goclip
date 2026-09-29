package ai

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"autoclip-go/internal/domain"
)

func editDraft() domain.Draft {
	d := domain.NewDraft("Original", []domain.Scene{
		{ID: "scene-1", Label: "First", Start: 0, End: 10, Evidence: "Actual subtitle"},
		{ID: "scene-2", Label: "Second", Start: 20, End: 30, Evidence: "Another subtitle"},
	})
	d.ProjectID = "project"
	d.Revision = 7
	d.Hook = "Original hook"
	return d
}

func TestRewritePreservesImmutableDraftFields(t *testing.T) {
	model := &scriptedModel{replies: map[string]string{
		"rewrite": `{"title":"Rewritten","hook":"New hook","scenes":[{"id":"scene-2","label":"Second new"},{"id":"scene-1","label":"First new"}]}`,
	}}
	draft := editDraft()
	original := draft
	original.Scenes = append([]domain.Scene(nil), draft.Scenes...)
	out, err := Rewrite(context.Background(), model.client(t), draft, "Make concise")
	if err != nil {
		t.Fatal(err)
	}
	if out.Title != "Rewritten" || out.Hook != "New hook" || out.Scenes[0].Label != "First new" {
		t.Fatal("rewrite did not apply aligned text")
	}
	comparable := out
	comparable.Scenes = append([]domain.Scene(nil), out.Scenes...)
	comparable.Title, comparable.Hook = draft.Title, draft.Hook
	for i := range comparable.Scenes {
		comparable.Scenes[i].Label = draft.Scenes[i].Label
	}
	if !reflect.DeepEqual(comparable, draft) || !reflect.DeepEqual(draft, original) {
		t.Fatal("rewrite changed timing/evidence/settings/revision or mutated input")
	}
}

func TestEditingRejectsForgedScenesAndMalformedJSON(t *testing.T) {
	for _, reply := range []string{
		`{"title":"x","hook":"","scenes":[]}`,
		`{"title":"x","hook":"","scenes":[{"id":"scene-1","label":"a"},{"id":"scene-1","label":"b"}]}`,
		`{"title":"x","hook":"","scenes":[{"id":"scene-1","label":"a","start":10},{"id":"scene-2","label":"b"}]}`,
		`{"title":"x","hook":"","scenes":[{"id":"scene-1","label":"a"},{"id":"invented","label":"b"}]}`,
		`{"title":"","hook":"","scenes":[{"id":"scene-1","label":"a"},{"id":"scene-2","label":"b"}]}`,
	} {
		model := &scriptedModel{replies: map[string]string{"rewrite": reply}}
		_, err := Rewrite(context.Background(), model.client(t), editDraft(), "concise")
		assertCode(t, err, CodeInvalidResponse)
		if model.count() != 1 {
			t.Fatal("rewrite silently retried")
		}
	}
	_, err := Rewrite(context.Background(), nil, editDraft(), "")
	assertCode(t, err, CodeInvalidResponse)
	_, err = Rewrite(context.Background(), nil, domain.Draft{}, "concise")
	assertCode(t, err, CodeInvalidResponse)
}

func TestTranslateSourceDoesNotCallModel(t *testing.T) {
	draft, cues := editDraft(), fixtureCues()
	out, translated, err := Translate(context.Background(), nil, draft, cues)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, draft) || !reflect.DeepEqual(translated, cues) {
		t.Fatal("source should be unchanged")
	}
	out.Scenes[0].Label = "changed"
	translated[0].Text = "changed"
	if draft.Scenes[0].Label == "changed" || cues[0].Text == "changed" {
		t.Fatal("source copy aliased mutable input slices")
	}
}

func TestTranslateOnlySelectedCuesPreservingTimingAndRevision(t *testing.T) {
	model := &scriptedModel{replies: map[string]string{
		"translate": `{"title":"Translated","hook":"Translated hook","scenes":[{"id":"scene-2","label":"Two"},{"id":"scene-1","label":"One"}],"cues":[{"index":2,"text":"Translated two"},{"index":0,"text":"Translated one"}]}`,
	}}
	draft := editDraft()
	draft.Language = "en"
	cues := fixtureCues()
	out, translated, err := Translate(context.Background(), model.client(t), draft, cues)
	if err != nil {
		t.Fatal(err)
	}
	if out.Language != "en" || out.ID != draft.ID || out.Revision != draft.Revision || out.UpdatedAt != draft.UpdatedAt {
		t.Fatal("translation changed immutable metadata")
	}
	if translated[0].Text != "Translated one" || translated[2].Text != "Translated two" {
		t.Fatal("wrong cue alignment")
	}
	for i := range cues {
		if translated[i].Start != cues[i].Start || translated[i].End != cues[i].End {
			t.Fatal("translation changed source times")
		}
		if i != 0 && i != 2 && translated[i].Text != cues[i].Text {
			t.Fatal("translation modified unselected cues")
		}
	}
	if out.Scenes[0].Evidence != draft.Scenes[0].Evidence || cues[0].Text == translated[0].Text {
		t.Fatal("translation overwrote evidence or input")
	}
}

func TestTranslateRejectsCueIdentityChangesAndCancellation(t *testing.T) {
	for _, cueJSON := range []string{
		`[]`, `null`, `[{"index":0,"text":"x"},{"index":0,"text":"y"}]`,
		`[{"index":0,"text":"x"},{"index":8,"text":"y"}]`,
		`[{"text":"x"},{"index":2,"text":"y"}]`,
		`[{"index":0,"text":""},{"index":2,"text":"y"}]`,
		`[{"index":0,"text":"x","start":99},{"index":2,"text":"y"}]`,
	} {
		model := &scriptedModel{replies: map[string]string{"translate": `{"title":"x","hook":"","scenes":[{"id":"scene-1","label":"a"},{"id":"scene-2","label":"b"}],"cues":` + cueJSON + `}`}}
		draft := editDraft()
		draft.Language = "ja"
		_, _, err := Translate(context.Background(), model.client(t), draft, fixtureCues())
		assertCode(t, err, CodeInvalidResponse)
		if model.count() != 1 {
			t.Fatal("translation silently retried")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Translate(ctx, nil, editDraft(), fixtureCues())
	assertCode(t, err, CodeTimeout)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation")
	}
}

func TestTranslateVisualWithoutSubtitles(t *testing.T) {
	model := &scriptedModel{replies: map[string]string{
		"translate": `{"title":"x","hook":"","scenes":[{"id":"scene-1","label":"a"},{"id":"scene-2","label":"b"}],"cues":[]}`,
	}}
	draft := editDraft()
	draft.Language, draft.Subtitles = "zh", false
	out, cues, err := Translate(context.Background(), model.client(t), draft, nil)
	if err != nil || len(cues) != 0 || out.Subtitles {
		t.Fatal("visual translation failed", err)
	}
}
