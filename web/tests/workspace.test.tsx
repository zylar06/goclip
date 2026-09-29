import { act, renderHook, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { useWorkspace } from '../src/features/studio/useWorkspace'
import { detail, draft, mockHTTP, response, task } from './fixtures'

it('merges task SSE updates and reloads completed drafts/exports, cleaning subscriptions on unmount', async () => {
  const streams: Stream[] = []
  class Stream extends EventTarget {
    close = vi.fn()
    onerror: (() => void) | null = null
    constructor() { super(); streams.push(this) }
    send(value: unknown) { this.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify(value) })) }
  }
  vi.stubGlobal('EventSource', Stream)
  let server = detail({ drafts: [], tasks: [task()] })
  const fetch = mockHTTP(() => response(server))
  const { result, unmount } = renderHook(() => useWorkspace('project1'))
  await waitFor(() => expect(streams).toHaveLength(1))
  act(() => streams[0].send(task({ progress: 40, stage: 'Flexible stage', updated_at: '2026-09-29T00:00:10Z' })))
  expect(result.current.workspace.tasks[0]).toMatchObject({ progress: 40, stage: 'Flexible stage' })
  server = detail({ drafts: [draft], tasks: [task({ status: 'completed', progress: 100, updated_at: '2026-09-29T00:00:20Z' })] })
  act(() => streams[0].send(server.tasks[0]))
  await waitFor(() => expect(result.current.workspace.drafts).toHaveLength(1))
  expect(fetch.mock.calls.length).toBeGreaterThan(1)
  expect(streams[0].close).toHaveBeenCalled()
  unmount()
})
