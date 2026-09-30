import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import HomePage from '../src/pages/HomePage'
import TaskPanel from '../src/components/TaskPanel'
import StudioResults from '../src/features/studio/StudioResults'
import ProjectPage from '../src/pages/ProjectPage'
import StudioEditor from '../src/features/studio/StudioEditor'
import { t } from '../src/i18n'
import { toWorkspace } from '../src/features/studio/api'
import { defaultPlanOptions } from '../src/features/studio/PlanSummary'
import { detail, draft, mockHTTP, response, task } from './fixtures'

function home(entry = '/') {
  const router = createMemoryRouter([{ path: '/', element: <HomePage /> }], { initialEntries: [entry] })
  render(<RouterProvider router={router} />)
  return router
}
describe('project workbench', () => {
  it('starts with the library, not a marketing hero or upload form; filters in the URL', async () => {
    const user = userEvent.setup()
    const project = detail().project
    const fetch = mockHTTP(() => response([project, { ...project, id: 'other', name: 'Interview', updated_at: '2026-09-30T00:00:00Z' }]))
    const router = home()
    await screen.findByRole('heading', { name: 'My video' })
    expect(screen.queryByLabelText('Video file')).not.toBeInTheDocument()
    expect(screen.queryByText('Your source. Your edit.')).not.toBeInTheDocument()
    expect(screen.getAllByRole('link').map(link => link.textContent)).toEqual([expect.stringContaining('Interview'), expect.stringContaining('My video')])
    await user.type(screen.getByRole('searchbox'), 'interview')
    expect(router.state.location.search).toBe('?q=interview')
    expect(screen.queryByRole('heading', { name: 'My video' })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Interview/ })).toHaveAttribute('href', '/project/other')
    await user.clear(screen.getByRole('searchbox'))
    await user.type(screen.getByRole('searchbox'), 'not found')
    expect(screen.getByText('No matching projects. Try another name.')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledTimes(1)
  })
  it('restores search from a copied URL', async () => {
    mockHTTP(() => response([detail().project]))
    home('/?q=video')
    await screen.findByRole('heading', { name: 'My video' })
    expect(screen.getByRole('searchbox')).toHaveValue('video')
  })
  it('restores focus and clears hidden file selections after cancelling import', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP(() => response([]))
    home()
    const trigger = screen.getByRole('button', { name: 'Import video' })
    await user.click(trigger)
    const dialog = screen.getByRole('dialog', { name: 'Import video' })
    await user.upload(within(dialog).getByLabelText('Video file'), new File(['video'], 'old.mp4', { type: 'video/mp4' }))
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
    await user.click(trigger)
    expect((screen.getByLabelText('Video file') as HTMLInputElement).files).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: 'Create project' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Choose a non-empty video')
    expect(fetch.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(false)
    await user.keyboard('{Escape}')
    expect(trigger).toHaveFocus()
  })
  it('keeps keyboard focus inside import, skipping inputs inside closed details', async () => {
    const user = userEvent.setup()
    mockHTTP(() => response([]))
    home()
    await user.click(screen.getByRole('button', { name: 'Import video' }))
    await user.tab({ shift: true })
    expect(screen.getByRole('button', { name: 'Create project' })).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'Upload video + SRT' })).toHaveFocus()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
  it('does not let cancel or Escape dismiss an in-flight upload', async () => {
    const user = userEvent.setup()
    let resolve!: (result: Response) => void
    mockHTTP((_path, init) => init.method === 'POST' ? new Promise<Response>(r => { resolve = r }) : response([]))
    home()
    await user.click(screen.getByRole('button', { name: 'Import video' }))
    await user.upload(screen.getByLabelText('Video file'), new File(['video'], 'source.mp4', { type: 'video/mp4' }))
    await user.click(screen.getByRole('button', { name: 'Create project' }))
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    await user.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    resolve(response({ code: 'failed', message: 'Import failed', retryable: false }, 500))
    await screen.findByRole('alert')
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled()
  })
})
describe('progressive disclosure without hiding failures', () => {
  it('keeps title templates out of the editor until requested without losing save behavior', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP((path, init) => init.method === 'PUT' ? response({ ...JSON.parse(init.body as string), revision: 2 }) :
      path.endsWith('/source-preview') ? response({ status: 'idle' }) : response(detail()))
    render(<RouterProvider router={createMemoryRouter([{ path: '/project/:id/studio/:draftId', element: <StudioEditor /> }], { initialEntries: ['/project/project1/studio/draft1'] })} />)
    await screen.findByRole('heading', { name: 'First draft' })
    const template = screen.getByRole('button', { name: t('简洁标题卡') })
    expect(template).not.toBeVisible()
    await user.click(screen.getByText(t('标题模板')))
    expect(template).toBeVisible()
    await user.click(template)
    expect(template).toHaveAttribute('aria-pressed', 'true')
    await user.click(screen.getByRole('button', { name: t('保存草稿') }))
    await waitFor(() => expect(fetch.mock.calls.some(([, init]) => init?.method === 'PUT')).toBe(true))
    const body = JSON.parse(fetch.mock.calls.find(([, init]) => init?.method === 'PUT')![1]!.body as string)
    expect(body).toMatchObject({ title_style: 'card', scenes: draft.scenes })
  })
  it('omits empty task chrome, folds success history, keeps failures and active controls visible', async () => {
    const user = userEvent.setup()
    const view = render(<TaskPanel tasks={[]} onRefresh={vi.fn()} />)
    expect(screen.queryByRole('heading', { name: /Tasks/ })).not.toBeInTheDocument()
    view.rerender(<TaskPanel tasks={[
      task({ id: 'done', status: 'completed', progress: 100 }),
      task({ id: 'failed', status: 'failed', error: 'Provider unavailable', retryable: true }),
      task({ id: 'active', status: 'running' }),
    ]} onRefresh={vi.fn()} />)
    expect(screen.getByRole('alert')).toHaveTextContent('Provider unavailable')
    expect(screen.getByRole('button', { name: 'Retry task…' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Cancel task' })).toBeVisible()
    expect(screen.getByRole('article', { name: 'analyze done' })).not.toBeVisible()
    await user.click(screen.getByText('Completed tasks (1)'))
    expect(screen.getByRole('article', { name: 'analyze done' })).toBeVisible()
  })
  it('shows one download per draft; history is available only on request', async () => {
    const user = userEvent.setup()
    const workspace = toWorkspace(detail({ exports: [{ task_id: 'export1', draft_id: draft.id, revision: draft.revision, title: draft.title, created_at: draft.updated_at }] }))
    render(<RouterProvider router={createMemoryRouter([{ path: '/', element: <StudioResults projectId="project1" workspace={workspace} onRefresh={vi.fn()} onManual={vi.fn()} busy={false} /> }])} />)
    let downloads = screen.getAllByRole('link').filter(link => link.getAttribute('href')?.includes('download=true'))
    expect(downloads).toHaveLength(1)
    expect(downloads[0]).toBeVisible()
    await user.click(screen.getByRole('button', { name: /Export history/ }))
    downloads = screen.getAllByRole('link').filter(link => link.getAttribute('href')?.includes('download=true'))
    expect(downloads).toHaveLength(2)
    expect(downloads[1]).toBeVisible()
  })
  it('puts existing clips before source tools and hides production controls until requested', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP(path => path.endsWith('/plan') ? response({ revision: 1, status: 'awaiting_confirmation', options: { ...defaultPlanOptions, goals: ['content'] }, reason: 'Local inspection', auto_export: false }) :
      path.endsWith('/source-preview') ? response({ status: 'idle' }) : response(detail()))
    render(<RouterProvider router={createMemoryRouter([{ path: '/project/:id', element: <ProjectPage /> }], { initialEntries: ['/project/project1'] })} />)
    await screen.findByRole('heading', { name: 'First draft' })
    await waitFor(() => expect(fetch.mock.calls.some(([path]) => path.endsWith('/plan'))).toBe(true))
    expect(screen.getByRole('button', { name: 'Confirm and start production' })).not.toBeVisible()
    expect(screen.queryByRole('button', { name: 'Delete project…' })).not.toBeInTheDocument()
    await user.click(screen.getByText('Production settings'))
    expect(screen.getByRole('button', { name: 'Confirm and start production' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Project actions' }))
    await user.click(screen.getByRole('button', { name: 'Delete project…' }))
    expect(screen.getByRole('dialog', { name: 'Delete project permanently?' })).toBeInTheDocument()
    expect(fetch.mock.calls.some(([, init]) => init?.method === 'DELETE' || init?.method === 'POST')).toBe(false)
  })
})
