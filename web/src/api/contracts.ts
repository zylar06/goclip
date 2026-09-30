import type * as Wire from '../generated/api'
/** Generated OpenAPI schemas, narrowed to domain enum values for editor controls.
 * Go nil slices and an omitted cancel_requested are handled at the web boundary.
 */
export type Project = Wire.Project
export type Cue = Wire.Cue
export type Scene = Wire.Scene
export type Candidate = Wire.Candidate
export type TitleStyle = 'plain' | 'impact' | 'card' | 'comic' | 'neon' | 'arena' | 'editorial' | 'pixel' | 'frosted'
export type Draft = Omit<Wire.Draft, 'aspect' | 'layout' | 'title_style'> & {
  aspect: 'original' | 'portrait' | 'landscape'; layout: 'fit' | 'crop' | 'blur'
  title_style: TitleStyle
}
export type TaskStatus = 'queued' | 'running' | 'completed' | 'failed' | 'interrupted' | 'cancelled'
export type Task = Omit<Wire.Task, 'kind' | 'status' | 'completed_steps' | 'cancel_requested'> & {
  kind: 'import' | 'analyze' | 'export' | 'inspect' | 'preview'; status: TaskStatus
  workflow_id?: string; goal?: string
  completed_steps: string[] | null
  cancel_requested?: boolean
}
export type Export = Wire.Export
export interface ProjectDetail {
  project: Project; drafts: Draft[]; tasks: Task[]; candidates: Candidate[]; exports: Export[]
  workflows?: Workflow[]
}
export type AnalysisOptions = Omit<Wire.AnalysisOptions, 'mode' | 'confirmed' | 'goals' | 'aspect'> & {
  mode: 'subtitle' | 'auto' | 'visual' | 'fused'; allow_visual: boolean; confirmed: true
  goals: ('content' | 'highlight' | 'promo')[]; duration: number; aspect: Draft['aspect']
  instruction: string
  burn_subtitles?: boolean
}
export type PlanOptions = Omit<AnalysisOptions, 'confirmed'> & { confirmed: boolean }
export type ProductionPlan = Omit<Wire.ProductionPlan, 'options'> & { options: PlanOptions }
export type Workflow = Omit<Wire.Workflow, 'options'> & { options: PlanOptions }
export type PreviewStatus = Omit<Wire.PreviewStatus, 'task'> & { task?: Task | null }
export type ModelStatus = Wire.ModelStatus
export type ModelSettings = Wire.ModelSettings
export interface Settings { text: ModelStatus; vision: ModelStatus; cookies_configured: boolean }
export type ModelKind = 'text' | 'vision'
export const terminal = (task: Pick<Task, 'status'>) =>
  ['completed', 'failed', 'interrupted', 'cancelled'].includes(task.status)
