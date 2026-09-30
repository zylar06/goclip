package ai

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"autoclip-go/internal/domain"
)

// FuseCandidates combines independent subtitle and visual evidence into one
// ordered highlight list. The fusion is deliberately local and deterministic:
// providers are used to observe evidence, while overlap, weighting and
// deduplication stay auditable and do not create another paid model call.
func FuseCandidates(text, visual []domain.Candidate) []domain.Candidate {
	type evidence struct {
		text, visual *domain.Candidate
	}
	groups := make([]evidence, 0, len(text)+len(visual))
	add := func(items []domain.Candidate, isVisual bool) {
		for i := range items {
			item := items[i]
			placed := -1
			for j := range groups {
				g := &groups[j]
				var other *domain.Candidate
				if isVisual {
					other = g.text
				} else {
					other = g.visual
				}
				if other == nil || !sameHighlight(item.Scene, other.Scene) {
					continue
				}
				placed = j
				break
			}
			if placed < 0 {
				g := evidence{}
				if isVisual {
					g.visual = &item
				} else {
					g.text = &item
				}
				groups = append(groups, g)
			} else if isVisual {
				groups[placed].visual = &item
			} else {
				groups[placed].text = &item
			}
		}
	}
	add(text, false)
	add(visual, true)

	out := make([]domain.Candidate, 0, len(groups))
	for i, g := range groups {
		base := g.visual
		if base == nil {
			base = g.text
		}
		scene := base.Scene
		if g.text != nil && g.visual != nil {
			scene.Start = math.Min(g.text.Start, g.visual.Start)
			scene.End = math.Max(g.text.End, g.visual.End)
		}
		textScore, visualScore, count := 0.0, 0.0, 0
		parts := make([]string, 0, 2)
		if g.text != nil {
			textScore, count = g.text.Score, count+1
			if strings.TrimSpace(g.text.Evidence) != "" {
				parts = append(parts, "字幕："+g.text.Evidence)
			}
		}
		if g.visual != nil {
			visualScore, count = g.visual.Score, count+1
			if strings.TrimSpace(g.visual.Evidence) != "" {
				parts = append(parts, "画面："+g.visual.Evidence)
			}
		}
		score := (textScore + visualScore) / float64(count)
		if g.text != nil && g.visual != nil {
			score = math.Min(1, score+0.1)
		}
		kind, reason := base.Kind, "单侧证据"
		if g.text != nil && g.visual != nil {
			kind, reason = "joint", fmt.Sprintf("字幕 %.2f + 画面 %.2f，时间段重叠并合并", textScore, visualScore)
		}
		if g.text != nil && g.visual == nil {
			kind, reason = "subtitle-only", "仅有字幕证据，未发现对应画面证据"
		}
		if g.text == nil && g.visual != nil {
			kind, reason = "visual-only", "仅有画面证据，未发现可用字幕证据"
		}
		scene.ID = fmt.Sprintf("fused-%03d", i+1)
		scene.Evidence = strings.Join(parts, " ")
		out = append(out, domain.Candidate{Scene: scene, Score: score, Kind: kind, SelectionReason: reason})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func sameHighlight(a, b domain.Scene) bool {
	short := math.Min(a.End-a.Start, b.End-b.Start)
	if short <= 0 {
		return false
	}
	overlap := math.Min(a.End, b.End) - math.Max(a.Start, b.Start)
	return overlap > 0 && overlap/short >= .5 || (a.Start <= b.End+2 && b.Start <= a.End+2)
}
