import { api, urls } from '../../api/client'
import type { ProjectDetail } from '../../api/contracts'
import type { RenderJob, Workspace } from './types'
export { errorText } from '../../api/client'

/** Project detail is authoritative; downloadable jobs require an Export record.
 * ExportPayload.draft links active tasks to editor revisions when supplied.
 */
export function toWorkspace(detail: ProjectDetail): Workspace {
  const jobs: RenderJob[] = detail.exports.map(e => ({
    job_id: e.task_id, draft_id: e.draft_id, revision: e.revision, title: e.title,
    created_at: e.created_at, status: 'completed', percent: 100,
  }))
  for (const task of detail.tasks) {
    if (task.kind !== 'export' || task.status === 'completed' || jobs.some(j => j.job_id === task.id)) continue
    const payload = task.payload
    if (!payload || typeof payload !== 'object' || !('draft' in payload)) continue
    const draft = payload.draft
    if (!draft || typeof draft !== 'object' || !('id' in draft) || !('revision' in draft) ||
      typeof draft.id !== 'string' || typeof draft.revision !== 'number') continue
    jobs.push({
      job_id: task.id, draft_id: draft.id, revision: draft.revision,
      title: 'title' in draft && typeof draft.title === 'string' ? draft.title : '',
      created_at: task.created_at, status: task.status, percent: task.progress, error: task.error,
    })
  }
  return { ...detail, jobs }
}
export const studioApi = {
  source: urls.source,
  video: urls.video,
  get: async (id: string, signal?: AbortSignal) => toWorkspace(await api.project(id, signal)),
  candidates: async (id: string, signal?: AbortSignal) => {
    const detail = await api.project(id, signal)
    return { duration: detail.project.duration, candidates: detail.candidates, warnings: [] }
  },
  save: api.saveDraft,
  duplicate: api.duplicate,
  rewrite: api.rewrite,
  titlePreview: api.titlePreview,
  export: api.export,
}
