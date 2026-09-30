import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { expect, it, vi } from 'vitest'
import StudioEditor from '../src/features/studio/StudioEditor'
import { t } from '../src/i18n'
import { detail, draft, mockHTTP, response, task } from './fixtures'

// Isolate the editor's layer visibility from PNG generation/debouncing.
vi.mock('../src/features/studio/TitleArtwork', () => ({ default: () => <span data-testid="title-layer">Title artwork</span> }))

function mount() {
  return render(<RouterProvider router={createMemoryRouter([
    { path: '/project/:id/studio/:draftId', element: <StudioEditor /> },
  ], { initialEntries: ['/project/project1/studio/draft1'] })} />)
}

it('title_enabled=false stays disabled on load and suppresses artwork and export packaging without erasing text', async () => {
  const user = userEvent.setup()
  const saved = { ...draft, title_enabled: false, hook: 'Retained opening text' }
  const fetch = mockHTTP(() => response(detail({ drafts: [saved] })))
  mount()
  expect(await screen.findByLabelText(t('添加开头标题'))).not.toBeChecked()
  expect(screen.getByLabelText(t('开头文字'))).toHaveValue(saved.hook)
  expect(screen.queryByTestId('title-layer')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: t('保存草稿') })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: t('导出成片') }))
  expect(within(screen.getByRole('dialog')).getByText(t('无开头文字'))).toBeInTheDocument()
  expect(fetch.mock.calls.some(([, init]) => ['PUT', 'POST'].includes(init?.method ?? ''))).toBe(false)
})

it('explicitly toggles the title layer in both directions and exports only the saved revision', async () => {
  const user = userEvent.setup()
  let saved = { ...draft, title_enabled: true, hook: 'Opening text', subtitles: true }
  const fetch = mockHTTP((path, init) => {
    if (init.method === 'PUT') {
      saved = { ...JSON.parse(init.body as string), revision: saved.revision + 1 }
      return response(saved)
    }
    if (path.endsWith('/export')) return response(task({ kind: 'export', status: 'completed' }))
    return response(detail({ drafts: [saved] }))
  })
  const view = mount()
  const toggle = await screen.findByLabelText(t('添加开头标题'))
  expect(toggle).toBeChecked()
  expect(screen.getByTestId('title-layer')).toBeInTheDocument()
  await user.click(toggle)
  expect(screen.queryByTestId('title-layer')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: t('导出成片') }))
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: t('确认导出') }))
  await waitFor(() => expect(fetch.mock.calls.some(([path]) => path.endsWith('/export'))).toBe(true))
  const put = fetch.mock.calls.find(([, init]) => init?.method === 'PUT')!
  expect(JSON.parse(put[1]!.body as string)).toMatchObject({
    title_enabled: false, title: draft.title, hook: 'Opening text',
    subtitles: true, original_audio: draft.original_audio, scenes: draft.scenes,
  })
  expect(JSON.parse(fetch.mock.calls.find(([path]) => path.endsWith('/export'))![1]!.body as string)).toEqual({ revision: 2 })
  view.unmount()
  mount()
  const restored = await screen.findByLabelText(t('添加开头标题'))
  expect(restored).not.toBeChecked()
  await user.click(restored)
  expect(screen.getByTestId('title-layer')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: t('保存草稿') }))
  await waitFor(() => expect(saved.revision).toBe(3))
  expect(saved.title_enabled).toBe(true)
  expect(saved.hook).toBe('Opening text')
  expect(saved.subtitles).toBe(true)
})

it('legacy drafts with no title_enabled retain enabled behavior without silently modifying the draft', async () => {
  const legacy = { ...draft, hook: 'Legacy opening', title_enabled: undefined }
  const fetch = mockHTTP(() => response(detail({ drafts: [legacy] })))
  mount()
  expect(await screen.findByLabelText(t('添加开头标题'))).toBeChecked()
  expect(screen.getByTestId('title-layer')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: t('保存草稿') })).toBeDisabled()
  expect(fetch.mock.calls.some(([, init]) => init?.method === 'PUT')).toBe(false)
})
