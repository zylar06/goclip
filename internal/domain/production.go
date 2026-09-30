package domain

// A production plan is durable, unconfirmed until a particular revision is
// explicitly confirmed. Its recommendation is not a claim of content quality.
type ProductionPlan struct {
	Revision       int             `json:"revision"`
	Status         string          `json:"status"`
	Options        AnalysisOptions `json:"options"`
	AutoExport     bool            `json:"auto_export"`
	SuggestedGoals []string        `json:"suggested_goals"`
	Reason         string          `json:"reason"`
}

type PlanUpdate struct {
	Revision   int             `json:"revision"`
	Options    AnalysisOptions `json:"options"`
	AutoExport bool            `json:"auto_export"`
}

type ConfirmProduction struct {
	PlanRevision int  `json:"plan_revision"`
	Confirmed    bool `json:"confirmed"`
}

type InspectOptions struct {
	AllowVisual bool `json:"allow_visual"`
	Confirmed   bool `json:"confirmed"`
}

type GoalResult struct {
	Goal          string   `json:"goal"`
	Status        string   `json:"status"`
	Error         string   `json:"error,omitempty"`
	DraftIDs      []string `json:"draft_ids"`
	ExportTaskIDs []string `json:"export_task_ids"`
}

// A workflow is not a runnable task. Only its children take the single worker
// lease, so orchestration never waits while occupying its own execution slot.
type Workflow struct {
	ID           string          `json:"id"`
	ProjectID    string          `json:"project_id"`
	PlanRevision int             `json:"plan_revision"`
	Options      AnalysisOptions `json:"options"`
	AutoExport   bool            `json:"auto_export"`
	Status       string          `json:"status"`
	Goals        []GoalResult    `json:"goals"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}

type PreviewStatus struct {
	Status string `json:"status"`
	Task   *Task  `json:"task,omitempty"`
}

func LocalPlan(p Project, revision int) ProductionPlan {
	goals := []string{"highlight"}
	reason := "将结合字幕和抽样画面寻找高光片段；没有字幕时会尝试本地转写，仍可使用画面证据。"
	if p.SubtitleStatus == "available" {
		reason = "检测到可用字幕；默认同时使用字幕和抽样画面寻找高光，这是本地检查，尚未调用模型。"
	} else if p.HasAudio != nil && !*p.HasAudio {
		reason = "素材无音轨且没有可用字幕；默认使用抽样画面寻找高光。"
	}
	return ProductionPlan{Revision: revision, Status: "awaiting_confirmation",
		Options:        AnalysisOptions{Mode: "fused", Goals: goals, Aspect: "original"},
		SuggestedGoals: goals, Reason: reason}
}
