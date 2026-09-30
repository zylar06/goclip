import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import PlanSummary, { defaultPlanOptions } from '../src/features/studio/PlanSummary'
import ImportReview from '../src/pages/ImportReview'
import ProjectPage from '../src/pages/ProjectPage'
import SourcePreview from '../src/features/studio/SourcePreview'
import StudioResults from '../src/features/studio/StudioResults'
import { toWorkspace } from '../src/features/studio/api'
import type { ProductionPlan, Workflow } from '../src/api/contracts'
import { detail, draft, mockHTTP, response, task } from './fixtures'

const plan: ProductionPlan = { revision: 3, status: 'awaiting_confirmation',
  options: { ...defaultPlanOptions, goals: ['content'] }, auto_export: false, suggested_goals: ['content'], reason: 'Local subtitle evidence; not a model recommendation.' }
const workflow: Workflow = { id: 'flow1', project_id: 'project1', plan_revision: 4, options: plan.options, auto_export: false,
  status: 'running', goals: [{ goal: 'content', status: 'running', draft_ids: [], export_task_ids: [] }], created_at: '', updated_at: '' }
function mountPlan() {
  return render(<PlanSummary project={{ ...detail().project, has_audio: true, subtitle_status: 'missing' }} active={false} onChanged={vi.fn()} />)
}
describe('durable production plan consent', () => {
  it('never invents a recommendation when local inspection has no subtitles', async () => {
    mockHTTP(() => response({ ...plan, suggested_goals: [], options: { ...plan.options, goals: [] } }))
    mountPlan()
    await screen.findByText(plan.reason, { exact: false })
    expect(within(screen.getByRole('group', { name: 'Goals' })).getAllByRole('checkbox').every(c => !(c as HTMLInputElement).checked)).toBe(true)
    expect(screen.getByRole('button', { name: 'Save plan only' })).toBeDisabled()
  })
  it('saves first and confirms only the returned revision, with automatic semantic duration and no subtitle burn', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP((path, init) => {
      if (init.method === 'PUT') return response({ ...plan, revision: 7, options: JSON.parse(init.body as string).options })
      if (path.endsWith('/confirm')) return response(workflow)
      return response(plan)
    })
    mountPlan()
    expect(await screen.findByText(plan.reason, { exact: false })).toBeInTheDocument()
    expect(screen.getByLabelText('Target seconds (0 = automatic)')).toHaveValue(0)
    expect(screen.getByLabelText('Add a new subtitle layer (off by default)')).not.toBeChecked()
    expect(screen.getByLabelText('One-click MP4 for highlight / promo')).not.toBeChecked()
    expect(screen.getByText(/Content clips always export MP4 automatically/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Confirm and start production' })).toBeDisabled()
    await user.type(screen.getByLabelText('Instructions (up to 4000 UTF-8 bytes)'), 'Preserve full explanations')
    await user.click(screen.getByLabelText('I understand and approve possible additional charges.'))
    await user.click(screen.getByRole('button', { name: 'Confirm and start production' }))
    await waitFor(() => expect(fetch.mock.calls.filter(([, i]) => i?.method === 'POST')).toHaveLength(1))
    const writes = fetch.mock.calls.filter(([, i]) => ['PUT', 'POST'].includes(i?.method ?? ''))
    expect(writes.map(([, i]) => i?.method)).toEqual(['PUT', 'POST'])
    expect(JSON.parse(writes[0][1]!.body as string)).toMatchObject({ revision: 3, auto_export: false, options: { duration: 0, burn_subtitles: false, allow_visual: false, confirmed: false } })
    expect(JSON.parse(writes[1][1]!.body as string)).toEqual({ plan_revision: 7, confirmed: true })
  })
  it('does not confirm after save conflicts and preserves the edited instruction', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP((_path, init) => init.method === 'PUT' ? response({ message: 'Plan revision changed' }, 409) : response(plan))
    mountPlan()
    await screen.findByText(plan.reason, { exact: false })
    await user.type(screen.getByLabelText('Instructions (up to 4000 UTF-8 bytes)'), 'Keep the explanation')
    await user.click(screen.getByLabelText('I understand and approve possible additional charges.'))
    await user.click(screen.getByRole('button', { name: 'Confirm and start production' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Plan revision changed')
    expect(screen.getByLabelText('Instructions (up to 4000 UTF-8 bytes)')).toHaveValue('Keep the explanation')
    expect(fetch.mock.calls.some(([p]) => p.endsWith('/confirm'))).toBe(false)
  })
  it('requires distinct image permission; switching mode resets it; duration 1–9 is invalid', async () => {
    const user = userEvent.setup()
    mockHTTP(() => response(plan))
    mountPlan()
    await screen.findByText(plan.reason, { exact: false })
    await user.selectOptions(screen.getByLabelText('Analysis mode'), 'visual')
    await user.click(within(screen.getByRole('group', { name: 'Goals' })).getByLabelText('highlight'))
    await user.click(screen.getByLabelText('I understand and approve possible additional charges.'))
    expect(screen.getByRole('button', { name: 'Confirm and start production' })).toBeDisabled()
    await user.click(screen.getByLabelText('I allow sampled video images to be uploaded to the saved vision model provider.'))
    expect(screen.getByRole('button', { name: 'Confirm and start production' })).toBeEnabled()
    await user.selectOptions(screen.getByLabelText('Analysis mode'), 'subtitle')
    await user.selectOptions(screen.getByLabelText('Analysis mode'), 'visual')
    expect(screen.getByLabelText('I allow sampled video images to be uploaded to the saved vision model provider.')).not.toBeChecked()
    fireEvent.change(screen.getByLabelText('Target seconds (0 = automatic)'), { target: { value: '9' } })
    expect(screen.getByRole('button', { name: 'Save plan only' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Target seconds (0 = automatic)'), { target: { value: '120' } })
    expect(screen.getByRole('button', { name: 'Save plan only' })).toBeEnabled()
  })
  it('restores saved options after remount but never restores payment or image authorization', async () => {
    const saved = { ...plan, auto_export: true, options: { ...plan.options, mode: 'visual', goals: ['highlight', 'promo'], duration: 80, instruction: 'Saved request', allow_visual: true, confirmed: true } }
    mockHTTP(() => response(saved))
    const view = mountPlan()
    await waitFor(() => expect(screen.getByLabelText('Target seconds (0 = automatic)')).toHaveValue(80))
    view.unmount(); mountPlan()
    await waitFor(() => expect(screen.getByLabelText('Instructions (up to 4000 UTF-8 bytes)')).toHaveValue('Saved request'))
    expect(screen.getByLabelText('One-click MP4 for highlight / promo')).toBeChecked()
    expect(screen.getByLabelText('I understand and approve possible additional charges.')).not.toBeChecked()
    expect(screen.getByLabelText('I allow sampled video images to be uploaded to the saved vision model provider.')).not.toBeChecked()
  })
  it('keeps a successful plan save on confirmation failure, and saves visual authorization without paid confirmation in options', async () => {
    const user = userEvent.setup()
    let saved = plan
    const fetch = mockHTTP((path, init) => {
      if (init.method === 'PUT') { saved = { ...plan, ...JSON.parse(init.body as string), revision: 4 }; return response(saved) }
      if (path.endsWith('/confirm')) return response({ message: 'Provider unavailable; source retained' }, 503)
      return response(saved)
    })
    const view = mountPlan()
    await screen.findByText(plan.reason, { exact: false })
    await user.selectOptions(screen.getByLabelText('Analysis mode'), 'visual')
    await user.click(within(screen.getByRole('group', { name: 'Goals' })).getByLabelText('highlight'))
    await user.click(screen.getByLabelText('I allow sampled video images to be uploaded to the saved vision model provider.'))
    await user.click(screen.getByLabelText('I understand and approve possible additional charges.'))
    await user.click(screen.getByRole('button', { name: 'Confirm and start production' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Provider unavailable; source retained')
    expect(saved.options).toMatchObject({ confirmed: false, allow_visual: true })
    expect(fetch.mock.calls.filter(([p]) => p.endsWith('/confirm'))).toHaveLength(1)
    view.unmount(); mountPlan()
    await waitFor(() => expect(screen.getByLabelText('Analysis mode')).toHaveValue('visual'))
    expect(screen.getByLabelText('I understand and approve possible additional charges.')).not.toBeChecked()
  })
})
describe('review and result routes', () => {
  it('restores in-progress production on the independent import page without resubmitting', async () => {
    const fetch = mockHTTP(path => path.endsWith('/plan') ? response(plan) : path.endsWith('/source-preview') ? response({ status: 'idle' }) : response(detail({ workflows: [workflow] })))
    render(<RouterProvider router={createMemoryRouter([{ path: '/import/:id', element: <ImportReview /> }], { initialEntries: ['/import/project1'] })} />)
    expect(await screen.findByText('Production is in progress. Saved choices are retained; follow the tasks below.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'View production progress' })).toHaveAttribute('href', '/project/project1')
    expect(screen.getByRole('button', { name: 'Confirm and start production' })).toBeDisabled()
    expect(fetch.mock.calls.some(([, i]) => i?.method === 'POST')).toBe(false)
  })
  it('requires an explicit batch subtitle confirmation and preserves old draft flags until then', async () => {
    const user = userEvent.setup()
    const fetch = mockHTTP((path, init) => {
      if (path.endsWith('/plan')) return response(plan)
      if (path.endsWith('/source-preview')) return response({ status: 'idle' })
      if (init.method === 'POST') return response({ updated: 1 })
      return response(detail({ drafts: [{ ...draft, subtitles: true }] }))
    })
    render(<RouterProvider router={createMemoryRouter([{ path: '/project/:id', element: <ProjectPage /> }], { initialEntries: ['/project/project1'] })} />)
    await user.click(await screen.findByRole('button', { name: 'Disable added subtitles in existing drafts…' }))
    expect(fetch.mock.calls.some(([, i]) => i?.method === 'POST')).toBe(false)
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm disabling added subtitles' }))
    expect(await screen.findByText('Updated drafts: 1')).toBeInTheDocument()
    const post = fetch.mock.calls.find(([, i]) => i?.method === 'POST')!
    expect(post[0]).toContain('/drafts/disable-subtitles')
    expect(JSON.parse(post[1]!.body as string)).toEqual({ confirm: true })
  })
  it('shows goal-level failures, real ranges and thumbnails; completed tasks alone never enable downloads', () => {
    mockHTTP(() => response({}))
    render(<RouterProvider router={createMemoryRouter([{ path: '/', element: <StudioResults projectId="project1"
      workspace={toWorkspace(detail({ workflows: [{ ...workflow, goals: [{ ...workflow.goals[0], status: 'failed', error: 'Provider unavailable' }] }],
        tasks: [task({ kind: 'export', status: 'completed', payload: { draft } })] }))}
      onRefresh={vi.fn()} onManual={vi.fn()} busy={false} /> }])} />)
    expect(screen.getByRole('alert')).toHaveTextContent('Provider unavailable')
    expect(screen.getByRole('heading', { name: `Production · ${workflow.status} · V${workflow.plan_revision}` })).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent(`${workflow.goals[0].goal} · failed`)
    expect(screen.getByText(/1.000–6.000s/)).toBeInTheDocument()
    expect(document.querySelector('img')).toHaveAttribute('src', '/api/v1/projects/project1/drafts/draft1/thumbnail?revision=1')
    expect(screen.getByRole('link', { name: 'Play full draft: First draft' })).toHaveAttribute('href', '/project/project1/studio/draft1')
    expect(screen.queryByRole('link', { name: /Download/ })).not.toBeInTheDocument()
  })
  it('discovers compatible preview read-only and starts conversion only with explicit consent', async () => {
    const user = userEvent.setup()
    let completed = false
    const fetch = mockHTTP((_path, init) => {
      if (init.method === 'POST') { completed = true; return response(task({ kind: 'preview', status: 'completed' })) }
      return response({ status: completed ? 'completed' : 'idle' })
    })
    render(<SourcePreview projectId="project1" />)
    await screen.findByText('Compatible preview: idle')
    expect(fetch.mock.calls.some(([, i]) => i?.method === 'POST')).toBe(false)
    await user.click(screen.getByRole('button', { name: 'Create compatible preview' }))
    await waitFor(() => expect(screen.getByLabelText('Source video')).toHaveAttribute('src', '/api/v1/projects/project1/source-preview/video'))
    expect(JSON.parse(fetch.mock.calls.find(([, i]) => i?.method === 'POST')![1]!.body as string)).toEqual({ confirmed: true })
  })
  it('restores a completed preview and displays conversion failures without automatically retrying', async () => {
    let status = 'completed'
    const fetch = mockHTTP(() => response({ status, ...(status === 'failed' ? { task: task({ kind: 'preview', status: 'failed', error: 'Local converter failed' }) } : {}) }))
    const view = render(<SourcePreview projectId="project1" />)
    await waitFor(() => expect(screen.getByLabelText('Source video')).toHaveAttribute('src', '/api/v1/projects/project1/source-preview/video'))
    view.unmount(); status = 'failed'
    render(<SourcePreview projectId="project1" />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Local converter failed')
    expect(screen.getByLabelText('Source video')).toHaveAttribute('src', '/api/v1/projects/project1/source')
    expect(fetch.mock.calls.some(([, i]) => i?.method === 'POST')).toBe(false)
  })
})
