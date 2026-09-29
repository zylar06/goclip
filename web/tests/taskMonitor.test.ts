import { describe, expect, it, vi } from 'vitest'
import { monitorTask } from '../src/api/taskMonitor'
import { task } from './fixtures'

class Stream extends EventTarget {
  close = vi.fn()
  onerror: (() => void) | null = null
  snapshot(value: unknown) { this.dispatchEvent(new MessageEvent('snapshot', { data: JSON.stringify(value) })) }
}
function setup(options: Partial<Parameters<typeof monitorTask>[1]> = {}) {
  const streams: Stream[] = []
  const onTask = vi.fn()
  const onConnection = vi.fn()
  const read = vi.fn().mockResolvedValue(task({ progress: 25 }))
  const open = vi.fn(() => { const stream = new Stream(); streams.push(stream); return stream as unknown as EventSource })
  const stop = monitorTask(task(), { onTask, onConnection, read, open, ...options })
  return { streams, onTask, onConnection, read, open, stop }
}
describe('SSE reconnect and polling fallback', () => {
  it('accepts flexible stage strings and null progress, closes on terminal snapshots', () => {
    const monitor = setup()
    monitor.streams[0].snapshot(task({ stage: 'vendor-specific/new-stage', progress: null }))
    expect(monitor.onTask).toHaveBeenLastCalledWith(expect.objectContaining({ stage: 'vendor-specific/new-stage', progress: null }))
    monitor.streams[0].snapshot(task({ status: 'completed', progress: 100, updated_at: '2026-09-29T00:00:02Z' }))
    expect(monitor.streams[0].close).toHaveBeenCalled()
    expect(monitor.read).not.toHaveBeenCalled()
  })
  it('polls during disconnect, reconnects, and ignores out-of-order snapshots', async () => {
    vi.useFakeTimers()
    const monitor = setup()
    monitor.streams[0].onerror?.()
    await vi.advanceTimersByTimeAsync(0)
    expect(monitor.read).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1000)
    expect(monitor.open).toHaveBeenCalledTimes(2)
    monitor.streams[1].snapshot(task({ progress: 70, updated_at: '2026-09-29T00:00:10Z' }))
    monitor.streams[1].snapshot(task({ progress: 20, updated_at: '2026-09-29T00:00:05Z' }))
    expect(monitor.onTask).toHaveBeenLastCalledWith(expect.objectContaining({ progress: 70 }))
    monitor.stop()
    const count = monitor.read.mock.calls.length
    await vi.advanceTimersByTimeAsync(30000)
    expect(monitor.read).toHaveBeenCalledTimes(count)
  })
  it('surfaces malformed snapshots and falls back instead of swallowing errors', async () => {
    vi.useFakeTimers()
    const monitor = setup()
    monitor.streams[0].dispatchEvent(new MessageEvent('snapshot', { data: '{broken' }))
    await vi.advanceTimersByTimeAsync(0)
    expect(monitor.onConnection).toHaveBeenCalledWith('polling', expect.stringContaining('snapshot failed'))
    expect(monitor.read).toHaveBeenCalled()
    monitor.stop()
  })
  it('stops after six consecutive polling errors and requires explicit reconnect', async () => {
    vi.useFakeTimers()
    const read = vi.fn().mockRejectedValue(new Error('offline'))
    const monitor = setup({ read, open: () => { throw new Error('no SSE') }, pollMs: 100 })
    await vi.advanceTimersByTimeAsync(15000)
    expect(read).toHaveBeenCalledTimes(6)
    expect(monitor.onConnection).toHaveBeenLastCalledWith('stopped', expect.stringContaining('Refresh to reconnect'))
  })
  it('watches silent streams, bounds monitor lifetime and aborts outstanding reads', async () => {
    vi.useFakeTimers()
    const monitor = setup({ staleMs: 50, budgetMs: 200 })
    await vi.advanceTimersByTimeAsync(60)
    expect(monitor.read).toHaveBeenCalled()
    expect(monitor.streams[0].close).toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(200)
    expect(monitor.onConnection).toHaveBeenLastCalledWith('stopped', expect.stringContaining('30-minute limit'))
    expect(monitor.read.mock.calls[0][1].aborted).toBe(true)
  })
})
