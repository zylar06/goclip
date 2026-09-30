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
	goals := []string{}
	reason := "未找到可用字幕，尚未判断内容质量；请选择制作类型。字幕分析将在确认后按需转写。"
	if p.SubtitleStatus == "available" {
		goals = []string{"content"}
		reason = "检测到可用字幕，建议按完整语义制作；这是本地检查，尚未调用模型或判断内容质量。"
	} else if p.HasAudio != nil && !*p.HasAudio {
		reason = "素材无音轨且没有可用字幕；可选择视觉高光，或手动剪辑。"
	}
	return ProductionPlan{Revision: revision, Status: "awaiting_confirmation",
		// Smart is safe by default: it remains local/subtitle-only until this
		// specific production confirms sampled-frame transmission.
		Options:        AnalysisOptions{Mode: "auto", Goals: goals, Aspect: "original"},
		SuggestedGoals: goals, Reason: reason}
}
