import type { Candidate, Draft, ProjectDetail, Scene, TaskStatus } from '../../api/contracts'
export type { Candidate, Draft, Scene, Language } from '../../api/contracts'
export interface CandidateList { duration: number; candidates: Candidate[]; warnings: string[] }
/** View model only; never sent as a backend job. */
export interface RenderJob {
  job_id: string; draft_id: string; revision: number; title: string; created_at: string
  status: TaskStatus; percent: number | null; error?: string
}
export interface Workspace extends Omit<ProjectDetail, 'project'> { project: ProjectDetail['project'] | null; jobs: RenderJob[] }
export const languages = [
  { value: 'source', label: '原语言' }, { value: 'zh', label: '简体中文' },
  { value: 'en', label: 'English' }, { value: 'ja', label: '日本語' },
] as const

// getRandomValues also works on trusted-LAN HTTP, unlike randomUUID.
export function newID() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), n => n.toString(16).padStart(2, '0')).join('')
}
export function newDraft(title: string, scenes: Scene[], origin = 'manual'): Draft {
  return {
    id: newID(), title, hook: '', scenes, language: 'source', aspect: 'original', layout: 'fit',
    crop_x: .5, title_style: 'plain', title_template_version: 1, title_motion: true,
    title_scale: 1, title_y: .12, title_accent: null, subtitles: true, original_audio: true,
    revision: 1, origin, updated_at: new Date().toISOString(),
  }
}
export const draftDuration = (draft: Draft) => draft.scenes.reduce((sum, s) => sum + s.end - s.start, 0)
const validID = (value: string) => /^[a-zA-Z0-9_-]{1,100}$/.test(value)
const length = (value: string) => [...value].length
export function draftError(draft: Draft, sourceDuration?: number): string | null {
  if (!validID(draft.id) || !draft.title.trim() || length(draft.title) > 200 || length(draft.hook) > 120) return 'Invalid draft title or hook.'
  if (!Array.isArray(draft.scenes) || !draft.scenes.length || draft.scenes.length > 30) return 'A draft needs 1–30 scenes.'
  if (new Set(draft.scenes.map(s => s.id)).size !== draft.scenes.length) return 'Scene IDs must be unique.'
  if (draft.scenes.some(s => !validID(s.id) || length(s.label) > 120 || length(s.evidence) > 1000 ||
    !Number.isFinite(s.start) || !Number.isFinite(s.end) || s.start < 0 || s.end - s.start < .1)) return 'Each scene must have valid times and last at least 0.1 seconds.'
  if (sourceDuration !== undefined && draft.scenes.some(s => s.end > sourceDuration + .05)) return 'Scene exceeds source duration.'
  if (draftDuration(draft) > 1800) return 'A draft cannot exceed 30 minutes.'
  if (!['source', 'zh', 'en', 'ja'].includes(draft.language) || !['original', 'portrait', 'landscape'].includes(draft.aspect) ||
    !['fit', 'crop', 'blur'].includes(draft.layout) ||
    !['plain', 'impact', 'card', 'comic', 'neon', 'arena', 'editorial', 'pixel', 'frosted'].includes(draft.title_style)) return 'Invalid draft options.'
  if (!Number.isFinite(draft.crop_x) || draft.crop_x < 0 || draft.crop_x > 1 ||
    !Number.isFinite(draft.title_scale) || draft.title_scale < .75 || draft.title_scale > 1.2 ||
    !Number.isFinite(draft.title_y) || draft.title_y < .06 || draft.title_y > .70) return 'Invalid title or crop placement.'
  if (draft.title_accent !== null && !/^#[0-9a-fA-F]{6}$/.test(draft.title_accent)) return 'Invalid accent color.'
  if (!Number.isInteger(draft.revision) || draft.revision < 1 || !Number.isInteger(draft.title_template_version)) return 'Invalid draft revision or template.'
  if (![1, 6].includes(draft.title_template_version)) return 'Historical title templates are not supported.'
  return null
}
export function moveScene(draft: Draft, index: number, delta: number): Draft {
  const next = index + delta
  if (index < 0 || index >= draft.scenes.length || next < 0 || next >= draft.scenes.length) return draft
  const scenes = [...draft.scenes]
  ;[scenes[index], scenes[next]] = [scenes[next], scenes[index]]
  return { ...draft, scenes }
}
export function applyCandidate(draft: Draft, candidate: Candidate, target: number | 'append', id: string): Draft {
  const scene: Scene = { id, label: candidate.label, start: candidate.start, end: candidate.end, evidence: candidate.evidence }
  if (target !== 'append' && (target < 0 || target >= draft.scenes.length)) throw new Error('Scene no longer exists.')
  const result = { ...draft, scenes: target === 'append' ? [...draft.scenes, scene] : draft.scenes.map((s, i) => i === target ? scene : s) }
  const invalid = draftError(result)
  if (invalid) throw new Error(invalid)
  return result
}
export function portraitDesign(draft: Draft): Draft {
  return { ...draft, aspect: 'portrait', layout: 'crop', title_style: 'comic', title_template_version: 6 }
}
/** Rewrite is a suggestion, not permission to replace timing, identity or revision. */
export const applyRewrite = (draft: Draft, suggestion: Draft): Draft => ({ ...draft, title: suggestion.title, hook: suggestion.hook })
