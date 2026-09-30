import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import HomePage from '../src/pages/HomePage'
import AnalysisPanel from '../src/components/AnalysisPanel'
import TaskPanel from '../src/components/TaskPanel'
import StudioResults from '../src/features/studio/StudioResults'
import { toWorkspace } from '../src/features/studio/api'
import { t } from '../src/i18n'
import { detail, draft, mockHTTP, response, task } from './fixtures'

describe('import workflows against the actual web adapter', () => {
  it('uploads video and optional SRT with exact multipart field names, without analysis', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP((path, init) => {
      if (path === '/api/v1/projects') return response(init.method === 'POST' ? detail().project : [])
      throw new Error(`Unexpected request ${path}`)
    })
    const router = createMemoryRouter([{ path: '/', element: <HomePage /> }, { path: '/import/:id', element: <p>Imported project</p> }])
    render(<RouterProvider router={router} />)
    await user.upload(screen.getByLabelText('Video file'), new File(['video'], 'demo.mp4', { type: 'video/mp4' }))
    await user.upload(screen.getByLabelText('SRT subtitles (optional)'), new File(['1\n00:00:00,000 --> 00:00:01,000\nhello\n'], 'demo.srt'))
    await user.click(screen.getByRole('button', { name: 'Create project' }))
    expect(await screen.findByText('Imported project')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/import/project1')
    const post = fetch.mock.calls.find(([, init]) => init?.method === 'POST')!
    expect(post[1]!.body).toBeInstanceOf(FormData)
    expect(Array.from((post[1]!.body as FormData).keys())).toEqual(['name', 'video', 'subtitle'])
    expect(post[1]!.headers).toBeUndefined() // Browser supplies the multipart boundary.
    expect(fetch.mock.calls.filter(([path]) => path.includes('analyze'))).toHaveLength(0)
  })
  it('imports a canonical URL and exposes server import errors', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP((_path, init) => init.method === 'POST'
      ? response({ code: 'size_limit', message: 'Video too long', retryable: false, request_id: 'import-1' }, 413) : response([]))
    render(<RouterProvider router={createMemoryRouter([{ path: '/', element: <HomePage /> }])} />)
    await user.click(screen.getByRole('button', { name: 'Bilibili / YouTube URL' }))
    await user.type(screen.getByLabelText('Video URL'), 'https://youtu.be/abcdefghijk?si=tracking')
    await user.click(screen.getByRole('button', { name: 'Create project' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Video too long (import-1)')
    const post = fetch.mock.calls.find(([, init]) => init?.method === 'POST')!
    expect(JSON.parse(post[1]!.body as string)).toEqual({ name: '', url: 'https://www.youtube.com/watch?v=abcdefghijk' })
  })
  it('does not retain invisible file selections after switching import modes', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP(() => response([]))
    render(<RouterProvider router={createMemoryRouter([{ path: '/', element: <HomePage /> }])} />)
    await user.upload(screen.getByLabelText('Video file'), new File(['video'], 'previous.mp4', { type: 'video/mp4' }))
    await user.click(screen.getByRole('button', { name: 'Bilibili / YouTube URL' }))
    await user.click(screen.getByRole('button', { name: 'Upload video + SRT' }))
    await user.click(screen.getByRole('button', { name: 'Create project' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Choose a non-empty video up to 4 GiB.')
    expect(fetch.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(false)
  })
  it('imports a URL with optional SRT and persists instructions on the import request', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP((_path, init) => response(init.method === 'POST' ? detail().project : []))
    const router = createMemoryRouter([{ path: '/', element: <HomePage /> }, { path: '/import/:id', element: <p>Review</p> }])
    render(<RouterProvider router={router} />)
    await user.click(screen.getByRole('button', { name: 'Bilibili / YouTube URL' }))
    await user.type(screen.getByLabelText('Video URL'), 'https://youtu.be/abcdefghijk')
    await user.upload(screen.getByLabelText('SRT subtitles (optional)'), new File(['subtitles'], 'input.srt'))
    await user.click(screen.getByText('Special requirements (optional)'))
    await user.type(screen.getByLabelText('Production instructions'), 'Keep complete explanations')
    await user.click(screen.getByRole('button', { name: 'Create project' }))
    await screen.findByText('Review')
    const form = fetch.mock.calls.find(([, i]) => i?.method === 'POST')![1]!.body as FormData
    expect(form.get('url')).toBe('https://www.youtube.com/watch?v=abcdefghijk')
    expect(form.get('video')).toBeNull()
    expect(form.get('subtitle')).toBeInstanceOf(File)
    expect(form.get('instruction')).toBe('Keep complete explanations')
  })
})
describe('explicit cost and image consent', () => {
  it('shows no-highlight guidance without offering a blind task retry', () => {
    const fetch = mockHTTP(() => response(task()))
    render(<TaskPanel tasks={[task({
      kind: 'analyze', status: 'failed', retryable: false, stage: '01-visual-events',
      error: 'ai no_highlights: No supported highlights; choose Text / subtitles analysis.',
    })]} onRefresh={vi.fn()} />)
    expect(screen.getByRole('alert')).toHaveTextContent('choose Text / subtitles analysis')
    expect(screen.queryByRole('button', { name: 'Retry task…' })).not.toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalled()
  })
  it('requires both image opt-in and paid confirmation for visual analysis', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP(() => response(task()))
    const started = vi.fn()
    render(<AnalysisPanel projectId="project1" ready active={false} onStarted={started} />)
    expect(fetch).not.toHaveBeenCalled()
    await user.selectOptions(screen.getByLabelText('Analysis mode'), 'visual')
    expect(screen.getByText(/Vision uses sampled images only, not audio or the full transcript/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review analysis…' })).toBeDisabled()
    await user.click(screen.getByLabelText('I allow sampled video images to be uploaded to the saved vision model provider.'))
    await user.click(screen.getByRole('button', { name: 'Review analysis…' }))
    expect(screen.getByRole('button', { name: 'Start confirmed analysis' })).toBeDisabled()
    await user.click(screen.getByLabelText('I understand and approve possible additional charges.'))
    await user.click(screen.getByRole('button', { name: 'Start confirmed analysis' }))
    await waitFor(() => expect(started).toHaveBeenCalledOnce())
    expect(JSON.parse(fetch.mock.calls[0][1]!.body as string)).toEqual({
      mode: 'visual', allow_visual: true, confirmed: true, goals: ['content'], duration: 30, aspect: 'original', category: '', instruction: '',
    })
  })
  it('does not retain image consent after switching modes and text never sends images', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP(() => response(task()))
    render(<AnalysisPanel projectId="project1" ready active={false} onStarted={vi.fn()} />)
    await user.selectOptions(screen.getByLabelText('Analysis mode'), 'visual')
    await user.click(screen.getByLabelText('I allow sampled video images to be uploaded to the saved vision model provider.'))
    await user.selectOptions(screen.getByLabelText('Analysis mode'), 'subtitle')
    await user.click(screen.getByRole('button', { name: 'Review analysis…' }))
    await user.click(screen.getByLabelText('I understand and approve possible additional charges.'))
    await user.click(screen.getByRole('button', { name: 'Start confirmed analysis' }))
    expect(JSON.parse(fetch.mock.calls[0][1]!.body as string)).toMatchObject({ mode: 'subtitle', allow_visual: false, confirmed: true })
  })
  it('never retries automatically or before consent, and shows arbitrary stages and null progress', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP(() => response(task({ status: 'queued' })))
    render(<TaskPanel tasks={[task({ status: 'interrupted', retryable: true, error: 'Worker restarted' })]} onRefresh={vi.fn()} />)
    expect(screen.getByText('Stage: custom-ai-stage')).toBeInTheDocument()
    expect(screen.getByText('Progress unavailable')).toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Retry task…' }))
    expect(screen.getByRole('button', { name: 'Confirm retry' })).toBeDisabled()
    await user.click(screen.getByLabelText('I understand and approve possible additional charges.'))
    await user.click(screen.getByRole('button', { name: 'Confirm retry' }))
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/tasks/task1/retry')
    expect(fetch.mock.calls[0][1]!.body).toBeUndefined()
    expect(await screen.findByText('Retry queued.')).toBeInTheDocument()
  })
  it('cancellation remains running while cancel_requested is true', () => {
    render(<TaskPanel tasks={[task({ cancel_requested: true })]} onRefresh={vi.fn()} />)
    expect(screen.getByText('Cancellation requested. Waiting for the worker to stop.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancel task' })).toBeDisabled()
    expect(screen.getByText('analyze · running')).toBeInTheDocument()
    expect(screen.getByRole('progressbar')).not.toHaveAttribute('aria-valuenow')
  })
})
describe('result history', () => {
  it('shows revisioned downloads only for published export records, never active jobs', () => {
    const workspace = toWorkspace(detail({
      tasks: [task({ kind: 'export', payload: { draft }, status: 'running' })],
      exports: [{ task_id: 'finished1', draft_id: draft.id, revision: 1, title: draft.title, created_at: draft.updated_at }],
    }))
    render(<RouterProvider router={createMemoryRouter([{ path: '/', element: <StudioResults projectId="project1" workspace={workspace}
      onRefresh={vi.fn()} onManual={vi.fn()} busy={false} /> }])} />)
    const links = screen.getAllByRole('link', { name: t('下载成片') })
    expect(links.length).toBeGreaterThan(0)
    for (const link of links) expect(link).toHaveAttribute('href', '/api/v1/projects/project1/exports/finished1/video?download=true')
  })
})
