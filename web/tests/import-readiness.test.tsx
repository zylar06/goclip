import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import ImportReview from '../src/pages/ImportReview'
import ProjectPage from '../src/pages/ProjectPage'
import TaskPanel from '../src/components/TaskPanel'
import { defaultPlanOptions } from '../src/features/studio/PlanSummary'
import { detail, mockHTTP, response, task } from './fixtures'

const plan = { revision: 1, status: 'awaiting_confirmation', reason: 'Source fully imported',
  options: { ...defaultPlanOptions, goals: ['content'] }, auto_export: false, suggested_goals: ['content'] }
const pages = [
  { path: '/import/:id', entry: '/import/project1', element: <ImportReview />, title: 'Review imported source' },
  { path: '/project/:id', entry: '/project/project1', element: <ProjectPage />, title: 'My video' },
]

describe.each(pages)('incomplete import gate: $entry', page => {
  it.each(['queued', 'running', 'failed', 'interrupted', 'cancelled'] as const)('blocks %s imports despite positive legacy duration', async status => {
    const fetch = mockHTTP(() => response(detail({ drafts: [], tasks: [task({ kind: 'import', status, retryable: status !== 'running' && status !== 'queued' })] })))
    render(<RouterProvider router={createMemoryRouter([{ path: page.path, element: page.element }], { initialEntries: [page.entry] })} />)
    await screen.findByRole('heading', { name: page.title })
    expect(screen.queryByRole('heading', { name: 'Production plan' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Confirm and start production' })).not.toBeInTheDocument()
    expect(fetch.mock.calls.some(([path]) => path.endsWith('/plan'))).toBe(false)
    expect(fetch.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(false)
    if (['failed', 'interrupted', 'cancelled'].includes(status)) {
      expect(await screen.findByText(/Source import is incomplete/)).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Retry task…' })).toBeInTheDocument()
      if (page.entry.startsWith('/import')) expect(screen.getByText(/Import did not complete/)).toBeInTheDocument()
    }
  })

  it.each(['completed', 'legacy-no-task'])('permits %s projects to enter confirmation', async status => {
    mockHTTP(path => path.endsWith('/plan') ? response(plan) : path.endsWith('/source-preview') ? response({ status: 'idle' })
      : response(detail({ drafts: [], tasks: status === 'completed' ? [task({ kind: 'import', status })] : [] })))
    render(<RouterProvider router={createMemoryRouter([{ path: page.path, element: page.element }], { initialEntries: [page.entry] })} />)
    await screen.findByText(plan.reason, { exact: false })
    expect(screen.getByRole('button', { name: 'Confirm and start production' })).toBeDisabled()
  })
})

it('import recovery is explicitly confirmed in TaskPanel without granting ASR/cloud production', async () => {
  const user = userEvent.setup()
  const fetch = mockHTTP(() => response(task({ kind: 'import', status: 'queued' })))
  render(<TaskPanel tasks={[task({ kind: 'import', status: 'interrupted', retryable: true })]} onRefresh={vi.fn()} />)
  expect(screen.getByText(/Source import is incomplete/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Retry task…' }))
  const dialog = screen.getByRole('dialog', { name: 'Confirm import retry' })
  expect(within(dialog).getByText(/does not authorize transcription or cloud production/)).toBeInTheDocument()
  expect(within(dialog).queryByLabelText('I understand and approve possible additional charges.')).not.toBeInTheDocument()
  expect(within(dialog).getByRole('button', { name: 'Confirm retry' })).toBeDisabled()
  expect(fetch).not.toHaveBeenCalled()
  await user.click(within(dialog).getByLabelText('I confirm retrying source import.'))
  await user.click(within(dialog).getByRole('button', { name: 'Confirm retry' }))
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1))
  expect(fetch.mock.calls[0][0]).toBe('/api/v1/tasks/task1/retry')
  expect(fetch.mock.calls[0][1]?.method).toBe('POST')
})

it('non-retryable failed imports explain reimport instead of offering an invalid retry', () => {
  render(<TaskPanel tasks={[task({ kind: 'import', status: 'failed', retryable: false })]} onRefresh={vi.fn()} />)
  expect(screen.getByText(/otherwise import the source again/)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Retry task…' })).not.toBeInTheDocument()
})

it('successful import retry refreshes the review into confirmation without auto-starting production', async () => {
  let completed = false
  const fetch = mockHTTP((path) => {
    if (path.endsWith('/retry')) { completed = true; return response(task({ kind: 'import', status: 'completed' })) }
    if (path.endsWith('/plan')) return response(plan)
    if (path.endsWith('/source-preview')) return response({ status: 'idle' })
    return response(detail({ drafts: [], tasks: [task({ kind: 'import', status: completed ? 'completed' : 'interrupted', retryable: !completed })] }))
  })
  const user = userEvent.setup()
  render(<RouterProvider router={createMemoryRouter([{ path: '/import/:id', element: <ImportReview /> }], { initialEntries: ['/import/project1'] })} />)
  await screen.findByText(/Import did not complete/)
  await user.click(screen.getByRole('button', { name: 'Retry task…' }))
  await user.click(screen.getByLabelText('I confirm retrying source import.'))
  await user.click(screen.getByRole('button', { name: 'Confirm retry' }))
  await screen.findByText(plan.reason, { exact: false })
  expect(screen.getByRole('button', { name: 'Confirm and start production' })).toBeDisabled()
  expect(screen.queryByText(/Import did not complete/)).not.toBeInTheDocument()
  expect(fetch.mock.calls.filter(([, init]) => init?.method === 'POST').map(([path]) => path)).toEqual(['/api/v1/tasks/task1/retry'])
})
