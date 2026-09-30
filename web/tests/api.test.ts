import { describe, expect, it, vi } from 'vitest'
import { api, ApiError, errorText, request, urls } from '../src/api/client'
import { toWorkspace } from '../src/features/studio/api'
import { draftExportState } from '../src/features/studio/draftExportState'
import { draft, detail, task, mockHTTP, response } from './fixtures'

describe('Go HTTP adapter', () => {
  it('sends exact optimistic revision, duplicate and export bodies to documented routes', async () => {
    const fetch = mockHTTP(() => response(draft))
    await api.saveDraft('project1', draft)
    await api.duplicate('project1', 'draft1', 'Copy')
    await api.export('project1', 'draft1', 7)
    expect(fetch.mock.calls.map(([path]) => path)).toEqual([
      '/api/v1/projects/project1/drafts/draft1', '/api/v1/projects/project1/drafts/draft1/duplicate', '/api/v1/projects/project1/drafts/draft1/export',
    ])
    expect(JSON.parse(fetch.mock.calls[0][1]!.body as string)).toEqual(draft)
    expect(JSON.parse(fetch.mock.calls[1][1]!.body as string)).toEqual({ title: 'Copy' })
    expect(JSON.parse(fetch.mock.calls[2][1]!.body as string)).toEqual({ revision: 7 })
  })
  it('preserves errors and request IDs and does not automatically retry mutations', async () => {
    const fetch = mockHTTP(() => response({ code: 'conflict', message: 'Revision changed', retryable: false, request_id: 'req-123' }, 409))
    const error = await api.saveDraft('project1', draft).catch(e => e)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 409, code: 'conflict', retryable: false, requestId: 'req-123' })
    expect(errorText(error)).toBe('Revision changed (req-123)')
    expect(fetch).toHaveBeenCalledTimes(1)
  })
  it('normalizes Go nil slices without response-envelope compatibility', async () => {
    mockHTTP(() => response({ ...detail(), tasks: null, drafts: null, candidates: null, exports: null }))
    expect(await api.project('project1')).toMatchObject({ drafts: [], tasks: [], candidates: [], exports: [] })
  })
  it('times out explicitly and aborts the request', async () => {
    vi.useFakeTimers()
    mockHTTP((_url, init) => new Promise((_resolve, reject) => init.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))))
    const result = request('/tasks/task1', {}, 50)
    const assertion = expect(result).rejects.toThrow('Check task state before retrying')
    await vi.advanceTimersByTimeAsync(50)
    await assertion
  })
  it('never downloads incomplete tasks and matches immutable export revisions', () => {
    const workspace = toWorkspace(detail({
      tasks: [task({ kind: 'export', status: 'completed', payload: { draft } })],
      exports: [],
    }))
    expect(workspace.jobs).toEqual([])
    const withExport = toWorkspace(detail({
      exports: [{ task_id: 'render1', draft_id: draft.id, revision: 1, title: draft.title, created_at: draft.updated_at }],
      tasks: [task({ kind: 'export', progress: null, payload: { draft: { ...draft, revision: 2 } } })],
    }))
    expect(draftExportState(draft, withExport.jobs).status).toBe('ready')
    expect(draftExportState({ ...draft, revision: 2 }, withExport.jobs).status).toBe('rendering')
    expect(draftExportState({ ...draft, revision: 3 }, withExport.jobs).status).toBe('updated')
    expect(urls.video('project1', 'render1', true)).toBe('/api/v1/projects/project1/exports/render1/video?download=true')
  })
  it('encodes route IDs and deletion always requires explicit confirmation', async () => {
    const fetch = mockHTTP(() => response({ ok: true }))
    await api.removeProject('a/b')
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/projects/a%2Fb')
    expect(JSON.parse(fetch.mock.calls[0][1]!.body as string)).toEqual({ confirm: true })
  })
})
