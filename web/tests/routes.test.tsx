import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { expect, it } from 'vitest'
import { appRoutes } from '../src/routes'
import { defaultPlanOptions } from '../src/features/studio/PlanSummary'
import type { ProductionPlan, Workflow } from '../src/api/contracts'
import { detail, mockHTTP, response } from './fixtures'

it('uses the deployed route table for upload → independent review → saved revision confirmation → project', async () => {
  const user = userEvent.setup()
  const project = { ...detail().project, subtitle_status: 'available' }
  const plan: ProductionPlan = { revision: 1, status: 'awaiting_confirmation', reason: 'Local evidence only',
    suggested_goals: ['content'], auto_export: false, options: { ...defaultPlanOptions, goals: ['content'] } }
  let producing = false
  const workflow: Workflow = { id: 'workflow1', project_id: project.id, plan_revision: 9, status: 'running',
    options: plan.options, auto_export: false, goals: [{ goal: 'content', status: 'running', draft_ids: [], export_task_ids: [] }], created_at: '', updated_at: '' }
  const fetch = mockHTTP((path, init) => {
    if (path === '/api/v1/projects') return response(init.method === 'POST' ? project : [])
    if (path.endsWith('/plan')) return response({ ...plan, revision: init.method === 'PUT' ? 9 : 1 })
    if (path.endsWith('/source-preview')) return response({ status: 'idle' })
    if (path.endsWith('/confirm')) { producing = true; return response(workflow) }
    return response(detail({ project, drafts: [], workflows: producing ? [workflow] : [] }))
  })
  const router = createMemoryRouter(appRoutes, { initialEntries: ['/'] })
  render(<RouterProvider router={router} />)
  await user.click(screen.getByRole('button', { name: 'Import video' }))
  await user.upload(screen.getByLabelText('Video file'), new File(['video'], 'source.mp4', { type: 'video/mp4' }))
  await user.click(screen.getByRole('button', { name: 'Create project' }))
  expect(await screen.findByRole('heading', { name: 'Review imported source' })).toBeInTheDocument()
  expect(router.state.location.pathname).toBe('/import/project1')
  expect(fetch.mock.calls.some(([path]) => /\/(analyze|confirm|inspect)$/.test(path))).toBe(false)
  await screen.findByText('Local evidence only', { exact: false })
  await user.type(screen.getByLabelText('Instructions (up to 4000 UTF-8 bytes)'), 'Keep complete explanations')
  await user.click(screen.getByLabelText('I understand and approve possible additional charges.'))
  await user.click(screen.getByRole('button', { name: 'Confirm and start production' }))
  await waitFor(() => expect(router.state.location.pathname).toBe('/project/project1'))
  expect(JSON.parse(fetch.mock.calls.find(([path]) => path.endsWith('/confirm'))![1]!.body as string)).toEqual({ plan_revision: 9, confirmed: true })
  expect(await screen.findByText('Production is in progress. Saved choices are retained; follow the tasks below.')).toBeInTheDocument()
})
