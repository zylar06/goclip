import { api, errorText, urls } from './client'
import { terminal, type Task } from './contracts'

export type Connection = 'connecting' | 'live' | 'polling' | 'stopped'
interface Options {
  onTask: (task: Task) => void
  onConnection: (state: Connection, message: string) => void
  read?: typeof api.task
  open?: (url: string) => EventSource
  pollMs?: number
  staleMs?: number
  budgetMs?: number
}
/** Reconnect SSE at most three times; poll while disconnected.
 * Bound lifetime and consecutive read failures; cleanup aborts in-flight reads.
 * A heartbeat/snapshot is not evidence of progress: stages are displayed as sent.
 */
export function monitorTask(initial: Task, options: Options): () => void {
  if (terminal(initial)) return () => undefined
  const { onTask, onConnection, read = api.task, open = url => new EventSource(url),
    pollMs = 3000, staleMs = 20000, budgetMs = 30 * 60 * 1000 } = options
  const controller = new AbortController()
  let source: EventSource | undefined
  let pollTimer: ReturnType<typeof setTimeout> | undefined
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let staleTimer: ReturnType<typeof setTimeout> | undefined
  let stopped = false
  let reading = false
  let live = false
  let failures = 0
  let reconnects = 0
  let latest = initial
  const cleanup = () => {
    stopped = true
    source?.close()
    controller.abort()
    clearTimeout(pollTimer); clearTimeout(retryTimer); clearTimeout(staleTimer); clearTimeout(deadline)
  }
  const deadline = setTimeout(() => {
    onConnection('stopped', 'Live monitoring reached its 30-minute limit. Refresh to reconnect; the task continues on the server.')
    cleanup()
  }, budgetMs)
  const accept = (task: Task) => {
    if (!task || task.id !== initial.id || task.project_id !== initial.project_id ||
      !['queued', 'running', 'completed', 'failed', 'interrupted', 'cancelled'].includes(task.status) ||
      (task.progress !== null && (!Number.isFinite(task.progress) || task.progress < 0 || task.progress > 100))) {
      throw new Error('Invalid task snapshot received.')
    }
    // Poll and SSE may arrive out of order.
    if (Date.parse(task.updated_at) < Date.parse(latest.updated_at)) return
    latest = task
    onTask(task)
    if (terminal(task)) { onConnection('stopped', ''); cleanup() }
  }
  const poll = async () => {
    if (stopped || reading) return
    reading = true
    try {
      const task = await read(initial.id, controller.signal)
      if (stopped) return
      accept(task)
      failures = 0
    } catch (error) {
      if (controller.signal.aborted) return // Deliberate lifecycle cancellation, not a failed request.
      failures += 1
      onConnection(failures >= 6 ? 'stopped' : 'polling', `Task polling failed: ${errorText(error)}${failures >= 6 ? ' Refresh to reconnect.' : ''}`)
      if (failures >= 6) cleanup()
    } finally {
      reading = false
      if (!stopped && !live) pollTimer = setTimeout(() => { pollTimer = undefined; void poll() }, pollMs)
    }
  }
  const disconnected = (message: string) => {
    if (stopped) return
    source?.close(); source = undefined
    live = false
    clearTimeout(staleTimer)
    onConnection('polling', message)
    if (!reading && !pollTimer) void poll()
    if (reconnects < 3 && !retryTimer) {
      const delay = Math.min(30000, 1000 * 2 ** reconnects++)
      retryTimer = setTimeout(() => { retryTimer = undefined; connect() }, delay)
    }
  }
  const armWatchdog = () => {
    clearTimeout(staleTimer)
    staleTimer = setTimeout(() => disconnected('No live snapshot received. Polling task status while reconnecting.'), staleMs)
  }
  const connect = () => {
    if (stopped) return
    onConnection('connecting', '')
    try {
      source = open(urls.events(initial.id))
      const currentSource = source
      armWatchdog()
      source.addEventListener('snapshot', event => {
        if (stopped || source !== currentSource) return
        try {
          accept(JSON.parse((event as MessageEvent<string>).data))
          if (!stopped) {
            live = true
            onConnection('live', '')
            clearTimeout(pollTimer); pollTimer = undefined
            armWatchdog()
          }
        } catch (error) { disconnected(`Live task snapshot failed: ${errorText(error)}`) }
      })
      source.onerror = () => disconnected('Live connection lost. Polling task status while reconnecting.')
    } catch (error) { disconnected(`Live connection unavailable: ${errorText(error)}`) }
  }
  connect()
  return cleanup
}
