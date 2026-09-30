import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import StudioEditor from '../src/features/studio/StudioEditor'
import TitleArtwork from '../src/features/studio/TitleArtwork'
import { t } from '../src/i18n'
import { detail, draft, mockHTTP, response, task } from './fixtures'

function mountEditor() {
  return render(<RouterProvider router={createMemoryRouter([
    { path: '/project/:id/studio/:draftId', element: <StudioEditor /> },
    { path: '/project/:id', element: <p>Project results</p> },
  ], { initialEntries: ['/project/project1/studio/draft1'] })} />)
}
describe('ported StudioEditor real behavior', () => {
  it('saves local edits before duplicate and sends only saved-draft duplicate fields', async () => {
    const user = userEvent.setup()
    let server = structuredClone(draft)
    const fetch = mockHTTP((path, init) => {
      if (init.method === 'PUT') { server = { ...JSON.parse(init.body as string), revision: server.revision + 1 }; return response(server) }
      if (path.endsWith('/duplicate')) return response({ ...server, id: 'copy1', title: 'Copy', revision: 1 })
      return response(detail({ drafts: [server, { ...server, id: 'copy1', title: 'Copy' }] }))
    })
    mountEditor()
    const title = await screen.findByLabelText(t('成片名称'))
    await user.clear(title); await user.type(title, 'Edited before duplicate')
    await user.click(screen.getByRole('button', { name: t('另存为新版本') }))
    await waitFor(() => expect(fetch.mock.calls.some(([path]) => path.endsWith('/duplicate'))).toBe(true))
    const writes = fetch.mock.calls.filter(([, init]) => init?.method === 'PUT' || init?.method === 'POST')
    expect(writes[0][1]!.method).toBe('PUT')
    expect(JSON.parse(writes[0][1]!.body as string).title).toBe('Edited before duplicate')
    expect(Object.keys(JSON.parse(writes[1][1]!.body as string)).sort()).toEqual(['title'])
    await waitFor(() => expect(screen.getByLabelText(t('成片名称'))).toHaveValue('Copy'))
    expect(screen.queryByRole('dialog', { name: 'Leave unsaved edits?' })).not.toBeInTheDocument()
  })
  it('saves first, exports the returned revision, and never exports after a conflict', async () => {
    const user = userEvent.setup()
    let conflict = false
    let server = structuredClone(draft)
    const fetch = mockHTTP((path, init) => {
      if (init.method === 'PUT') {
        if (conflict) return response({ code: 'conflict', message: 'Revision changed', request_id: 'rev-1' }, 409)
        server = { ...JSON.parse(init.body as string), revision: 2 }
        return response(server)
      }
      if (path.endsWith('/export')) return response(task({ kind: 'export', status: 'queued' }))
      return response(detail({ drafts: [server] }))
    })
    mountEditor()
    await user.type(await screen.findByLabelText(t('成片名称')), ' edited')
    await user.click(screen.getByRole('button', { name: t('导出成片') }))
    expect(within(screen.getByRole('dialog')).getByText('Original aspect · longest edge up to 1920 px')).toBeInTheDocument()
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: t('确认导出') }))
    await waitFor(() => expect(fetch.mock.calls.some(([path]) => path.endsWith('/export'))).toBe(true))
    expect(JSON.parse(fetch.mock.calls.find(([path]) => path.endsWith('/export'))![1]!.body as string)).toEqual({ revision: 2 })
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: t('关闭') }))
    conflict = true
    await user.type(screen.getByLabelText(t('成片名称')), ' conflicting')
    await user.click(screen.getByRole('button', { name: t('导出成片') }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: t('确认导出') }))
    await waitFor(() => expect(screen.getAllByText('Revision changed (rev-1)').length).toBeGreaterThan(0))
    expect(fetch.mock.calls.filter(([path]) => path.endsWith('/export'))).toHaveLength(1)
  })
  it('recovers browser edits and blocks navigation without silently discarding them', async () => {
    const user = userEvent.setup()
    localStorage.setItem('autoclip.studio.project1.draft1', JSON.stringify({ ...draft, title: 'Recovered title' }))
    mockHTTP(() => response(detail()))
    mountEditor()
    expect(await screen.findByLabelText(t('成片名称'))).toHaveValue('Recovered title')
    await user.click(screen.getByRole('button', { name: t('‹ 返回项目') }))
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveAccessibleName('Leave unsaved edits?')
    await user.click(within(dialog).getByRole('button', { name: 'Keep editing' }))
    expect(screen.getByLabelText(t('成片名称'))).toHaveValue('Recovered title')
  })
  it('keeps local edits on conflict and reloads the current server revision only after confirmation', async () => {
    const user = userEvent.setup()
    let server = structuredClone(draft)
    mockHTTP((_path, init) => init.method === 'PUT'
      ? response({ code: 'conflict', message: 'Revision changed', request_id: 'revision-2' }, 409)
      : response(detail({ drafts: [server] })))
    mountEditor()
    await user.type(await screen.findByLabelText(t('成片名称')), ' local')
    server = { ...draft, title: 'Changed by another user', revision: 2 }
    await user.click(screen.getByRole('button', { name: t('保存草稿') }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Revision changed (revision-2)')
    expect(screen.getByLabelText(t('成片名称'))).toHaveValue('First draft local')
    await user.click(screen.getByRole('button', { name: 'Reload latest saved draft…' }))
    expect(screen.getByLabelText(t('成片名称'))).toHaveValue('First draft local')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Discard and reload' }))
    await waitFor(() => expect(screen.getByLabelText(t('成片名称'))).toHaveValue('Changed by another user'))
    expect(localStorage.getItem('autoclip.studio.project1.draft1')).toBeNull()
  })
  it('adds manual source ranges without candidates and never silently disables old subtitles', async () => {
    const user = userEvent.setup()
    mockHTTP(() => response(detail({ drafts: [{ ...draft, subtitles: true }], candidates: [] })))
    mountEditor()
    expect(await screen.findByLabelText(t('烧录原字幕'))).toBeChecked()
    expect(screen.getByLabelText('Full draft playback')).toBeInTheDocument()
    await user.click(screen.getByText(new RegExp(t('调整镜头'))))
    const start = screen.getByLabelText('Manual start (seconds)')
    const end = screen.getByLabelText('Manual end (seconds)')
    await user.clear(start); await user.type(start, '40')
    await user.clear(end); await user.type(end, '45')
    await user.click(screen.getByRole('button', { name: 'Add manual range' }))
    expect(screen.getAllByLabelText(t('起点（秒）'))).toHaveLength(2)
    expect(screen.getByLabelText(t('烧录原字幕'))).toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Source selection' }))
    expect(screen.getByLabelText('Source playback')).toBeInTheDocument()
  })
})
it('title previews debounce, use only the documented PNG route, and revoke blob URLs', async () => {
  const create = vi.fn().mockReturnValue('blob:title')
  const revoke = vi.fn()
  vi.stubGlobal('URL', class extends URL { static createObjectURL = create; static revokeObjectURL = revoke })
  const fetch = mockHTTP(() => new Response(new Blob(['png'], { type: 'image/png' }), { headers: { 'Content-Type': 'image/png' } }))
  const { unmount } = render(<TitleArtwork projectId="project1" draft={{ ...draft, title_style: 'frosted', hook: 'Title' }} />)
  expect(await screen.findByRole('img', { name: t('标题排版预览') })).toHaveAttribute('src', 'blob:title')
  expect(fetch).toHaveBeenCalledTimes(1)
  expect(fetch.mock.calls[0][0]).toBe('/api/v1/projects/project1/title-preview')
  unmount()
  expect(revoke).toHaveBeenCalledWith('blob:title')
})
