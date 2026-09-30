import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import PlanSummary, { defaultPlanOptions } from '../src/features/studio/PlanSummary'
import type { ProductionPlan, Project } from '../src/api/contracts'
import { detail, mockHTTP, response } from './fixtures'

const base: ProductionPlan = {
  revision: 3, status: 'awaiting_confirmation', options: { ...defaultPlanOptions, goals: ['content'] },
  auto_export: false, suggested_goals: ['content'], reason: 'Initial local evidence',
}
const project: Project = { ...detail().project, subtitle_status: 'available', plan: base }
const cost = () => screen.getByLabelText('I understand and approve possible additional charges.')
const start = () => screen.getByRole('button', { name: 'Confirm and start production' })
const instruction = () => screen.getByLabelText('Instructions (up to 4000 UTF-8 bytes)')
const props = { project, active: false, onChanged: vi.fn() }

describe('plan recovery and cost boundaries', () => {
  it('R2-02: a lost accepted confirmation replays the same revision, never another PUT, and consumes consent', async () => {
    let saved = structuredClone(base)
    const revisions: number[] = []
    const fetch = mockHTTP((path, init) => {
      if (init.method === 'PUT') {
        saved = { ...saved, ...JSON.parse(init.body as string), revision: saved.revision + 1 }
        return response(saved)
      }
      if (path.endsWith('/confirm')) {
        revisions.push(JSON.parse(init.body as string).plan_revision)
        if (revisions.length === 1) return Promise.reject(new TypeError('Response lost after acceptance'))
        return response({ id: 'same-workflow', plan_revision: saved.revision })
      }
      return response(saved)
    })
    const user = userEvent.setup()
    render(<PlanSummary {...props} />)
    await screen.findByText(base.reason, { exact: false })
    await user.type(instruction(), 'Keep full context')
    await user.click(cost()); await user.click(start())
    expect(await screen.findByRole('alert')).toHaveTextContent('Response lost after acceptance')
    expect(cost()).not.toBeChecked()
    expect(instruction()).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Save plan only' })).toBeDisabled()
    const retry = screen.getByRole('button', { name: 'Reconfirm saved revision' })
    expect(retry).toBeDisabled()
    await user.click(cost()); await user.click(retry)
    await waitFor(() => expect(revisions).toEqual([4, 4]))
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1)
    expect(await screen.findByText(/This plan was already confirmed/)).toBeInTheDocument()
    expect(cost()).not.toBeChecked()
  })

  it('refresh after acceptance never starts another round until explicitly requested with new consent', async () => {
    let saved = { ...base, revision: 4, status: 'confirmed' }
    const fetch = mockHTTP((path, init) => {
      if (init.method === 'PUT') {
        saved = { ...saved, ...JSON.parse(init.body as string), revision: 5, status: 'awaiting_confirmation' }
        return response(saved)
      }
      return response(path.endsWith('/confirm') ? { id: 'new-workflow' } : saved)
    })
    const user = userEvent.setup()
    render(<PlanSummary {...props} project={{ ...project, plan: saved }} />)
    await screen.findByText(base.reason, { exact: false })
    await user.click(cost())
    expect(start()).toBeDisabled()
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: 'Prepare another production round' }))
    expect(cost()).not.toBeChecked()
    expect(start()).toBeDisabled()
    await user.click(cost()); await user.click(start())
    await waitFor(() => expect(fetch.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1))
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1)
    expect(JSON.parse(fetch.mock.calls.find(([path]) => path.endsWith('/confirm'))![1]!.body as string)).toEqual({ plan_revision: 5, confirmed: true })
  })

  it('visual confirmation failure consumes both grants and replays without persisting them again', async () => {
    const visual: ProductionPlan = { ...base, options: { ...base.options, mode: 'visual', goals: ['highlight'] } }
    const fetch = mockHTTP((path, init) => {
      if (path.endsWith('/confirm')) return response({ message: 'Response unavailable' }, 502)
      if (init.method === 'PUT') return response({ ...visual, revision: 4, options: JSON.parse(init.body as string).options })
      return response(visual)
    })
    const user = userEvent.setup()
    render(<PlanSummary {...props} />)
    await screen.findByText(base.reason, { exact: false })
    const images = screen.getByLabelText('I allow sampled video images to be uploaded to the saved vision model provider.')
    await user.click(images); await user.click(cost()); await user.click(start())
    await screen.findByRole('alert')
    expect(images).not.toBeChecked()
    expect(cost()).not.toBeChecked()
    await user.click(cost())
    const retry = screen.getByRole('button', { name: 'Reconfirm saved revision' })
    expect(retry).toBeDisabled()
    await user.click(images); await user.click(retry)
    await waitFor(() => expect(fetch.mock.calls.filter(([path]) => path.endsWith('/confirm'))).toHaveLength(2))
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1)
    expect(fetch.mock.calls.filter(([path]) => path.endsWith('/confirm')).map(([, init]) => JSON.parse(init!.body as string).plan_revision)).toEqual([4, 4])
  })

  it('refresh with an awaiting saved plan reconfirms that revision without PUT; rapid clicks stay single-flight', async () => {
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    const fetch = mockHTTP((path) => path.endsWith('/confirm') ? gate.then(() => response({ id: 'flow' })) : response(base))
    const user = userEvent.setup()
    render(<PlanSummary {...props} />)
    await screen.findByText(base.reason, { exact: false })
    await user.click(cost())
    const button = start()
    fireEvent.click(button); fireEvent.click(button)
    expect(fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(0)
    expect(fetch.mock.calls.filter(([path]) => path.endsWith('/confirm'))).toHaveLength(1)
    expect(JSON.parse(fetch.mock.calls.find(([path]) => path.endsWith('/confirm'))![1]!.body as string).plan_revision).toBe(3)
    release()
    await screen.findByText(/This plan was already confirmed/)
  })

  it('R2-08: completed reinspection reloads clean evidence and saves against the new revision', async () => {
    let saved = structuredClone(base)
    const fetch = mockHTTP((_path, init) => response(init.method === 'PUT' ? { ...saved, revision: 5 } : saved))
    const user = userEvent.setup()
    const view = render(<PlanSummary {...props} />)
    await screen.findByText(base.reason, { exact: false })
    await user.click(cost())
    view.rerender(<PlanSummary {...props} active />)
    saved = { ...saved, revision: 4, reason: 'Fresh inspected evidence' }
    view.rerender(<PlanSummary {...props} project={{ ...project, plan: saved }} />)
    await screen.findByText(saved.reason, { exact: false })
    expect(cost()).not.toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Save plan only' }))
    await waitFor(() => expect(fetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1))
    const put = fetch.mock.calls.find(([, init]) => init?.method === 'PUT')!
    expect(JSON.parse(put[1]!.body as string).revision).toBe(4)
  })

  it('R2-08: changed remote revision preserves dirty edits, blocks stale saves and requires explicit reload', async () => {
    let saved = structuredClone(base)
    const fetch = mockHTTP(() => response(saved))
    const user = userEvent.setup()
    const view = render(<PlanSummary {...props} />)
    await screen.findByText(base.reason, { exact: false })
    await user.type(instruction(), 'Unsaved local request')
    await user.click(cost())
    saved = { ...saved, revision: 4, reason: 'New evidence', options: { ...saved.options, instruction: 'Server request' } }
    view.rerender(<PlanSummary {...props} project={{ ...project, plan: saved }} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Your edits are retained')
    expect(instruction()).toHaveValue('Unsaved local request')
    expect(cost()).not.toBeChecked()
    expect(start()).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Save plan only' })).toBeDisabled()
    expect(fetch).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole('button', { name: 'Reload saved plan' }))
    await waitFor(() => expect(instruction()).toHaveValue('Server request'))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save plan only' })).toBeEnabled()
  })

  it('same-revision workspace heartbeats do not overwrite edits or refetch the plan', async () => {
    const fetch = mockHTTP(() => response(base))
    const user = userEvent.setup()
    const view = render(<PlanSummary {...props} />)
    await screen.findByText(base.reason, { exact: false })
    await user.type(instruction(), 'Local text')
    await user.click(cost())
    view.rerender(<PlanSummary {...props} project={{ ...project, updated_at: 'later', plan: structuredClone(base) }} />)
    expect(instruction()).toHaveValue('Local text')
    expect(cost()).toBeChecked()
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('R3-05: visual-only content is blocked locally; adding a visual goal restores validity', async () => {
    const fetch = mockHTTP(() => response(base))
    const user = userEvent.setup()
    render(<PlanSummary {...props} />)
    await screen.findByText(base.reason, { exact: false })
    await user.selectOptions(screen.getByLabelText('Analysis mode'), 'visual')
    await user.click(screen.getByLabelText('I allow sampled video images to be uploaded to the saved vision model provider.'))
    await user.click(cost())
    expect(await screen.findByRole('alert')).toHaveTextContent('Content clips require subtitle analysis')
    expect(start()).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Save plan only' })).toBeDisabled()
    await user.click(within(screen.getByRole('group', { name: 'Goals' })).getByLabelText('highlight'))
    expect(cost()).not.toBeChecked()
    await user.click(cost())
    expect(start()).toBeEnabled()
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('R3-03: mixed content/visual consent discloses subtitle text transmission and possible ASR', async () => {
    const mixed: ProductionPlan = { ...base, options: { ...base.options, mode: 'visual', goals: ['content', 'highlight'] } }
    mockHTTP(() => response(mixed))
    render(<PlanSummary {...props} project={{ ...project, subtitle_status: 'missing', has_audio: true }} />)
    await screen.findByText(base.reason, { exact: false })
    expect(screen.getByText(/Missing subtitles will be transcribed only after confirmation/)).toBeInTheDocument()
    expect(screen.getByText(/This mixed plan sends subtitle text to the saved text model/)).toBeInTheDocument()
    expect(screen.queryByText(/Visual mode does not require transcription/)).not.toBeInTheDocument()
    expect(screen.queryByText(/not audio or the full transcript/)).not.toBeInTheDocument()
    expect(start()).toBeDisabled()
  })
})
