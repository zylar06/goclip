import { vi } from 'vitest'
import type { Draft, ProjectDetail, Task } from '../src/api/contracts'
import { newDraft } from '../src/features/studio/types'

export const draft: Draft = {
  ...newDraft('First draft', [{ id: 'scene1', label: 'Opening', start: 1, end: 6, evidence: 'Spoken introduction' }]),
  id: 'draft1', project_id: 'project1', updated_at: '2026-09-29T00:00:00Z',
}
export const task = (patch: Partial<Task> = {}): Task => ({
  id: 'task1', project_id: 'project1', kind: 'analyze', status: 'running', stage: 'custom-ai-stage',
  progress: null, completed_steps: ['prepared'], heartbeat: '2026-09-29T00:00:00Z',
  created_at: '2026-09-29T00:00:00Z', updated_at: '2026-09-29T00:00:00Z', retryable: false, ...patch,
})
export const detail = (patch: Partial<ProjectDetail> = {}): ProjectDetail => ({
  project: { id: 'project1', name: 'My video', status: 'ready', duration: 120, width: 1920, height: 1080,
    created_at: '2026-09-29T00:00:00Z', updated_at: '2026-09-29T00:00:00Z' },
  drafts: [draft], tasks: [], candidates: [{ id: 'candidate1', label: 'Highlight', start: 20, end: 25, evidence: 'Goal scored', score: .9, kind: 'visual' }],
  exports: [], ...patch,
})
export const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
export function mockHTTP(handler: (path: string, init: RequestInit) => Response | Promise<Response>) {
  const mock = vi.fn((path: string, init: RequestInit = {}) => Promise.resolve(handler(path, init)))
  vi.stubGlobal('fetch', mock)
  return mock
}
