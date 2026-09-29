import { useCallback, useEffect, useState } from 'react'
import { api, errorText } from '../../api/client'
import { terminal } from '../../api/contracts'
import { monitorTask, type Connection } from '../../api/taskMonitor'
import { toWorkspace } from './api'
import type { Workspace } from './types'

const empty: Workspace = { project: null, drafts: [], tasks: [], exports: [], candidates: [], jobs: [] }
export function useWorkspace(projectId: string | undefined) {
  const [workspace, setWorkspace] = useState<Workspace>(empty)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [version, setVersion] = useState(0)
  const [connections, setConnections] = useState<Record<string, { state: Connection; message: string }>>({})
  const refresh = useCallback(() => setVersion(v => v + 1), [])
  useEffect(() => { setWorkspace(empty); setConnections({}) }, [projectId])
  useEffect(() => {
    if (!projectId) return
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    let attempts = 0
    let failures = 0
    setLoading(true)
    const load = async () => {
      try {
        const detail = await api.project(projectId, controller.signal)
        if (controller.signal.aborted) return
        setWorkspace(previous => toWorkspace({
          ...detail,
          tasks: detail.tasks.map(task => {
            const newer = previous.tasks.find(t => t.id === task.id && Date.parse(t.updated_at) > Date.parse(task.updated_at))
            return newer ?? task
          }),
        }))
        setError(''); failures = 0
      } catch (cause) {
        if (controller.signal.aborted) return
        failures += 1
        setError(errorText(cause) + (failures >= 6 ? ' Automatic refresh paused; retry to reconnect.' : ''))
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false)
          attempts += 1
          if (attempts < 180 && failures < 6) timer = setTimeout(() => void load(), 10000)
          else if (attempts >= 180) setError('Project monitoring paused after 30 minutes. Refresh to reconnect.')
        }
      }
    }
    void load()
    return () => { controller.abort(); clearTimeout(timer) }
  }, [projectId, version])
  const activeIds = workspace.tasks.filter(t => !terminal(t)).map(t => t.id).sort().join(',')
  useEffect(() => {
    const stops = workspace.tasks.filter(t => !terminal(t)).map(task => monitorTask(task, {
      onTask: updated => {
        setWorkspace(previous => {
          if (!previous.project || previous.project.id !== updated.project_id) return previous
          const current = previous.tasks.find(t => t.id === updated.id)
          if (current && Date.parse(current.updated_at) > Date.parse(updated.updated_at)) return previous
          return toWorkspace({ ...previous, project: previous.project, tasks: previous.tasks.map(t => t.id === updated.id ? updated : t) })
        })
        if (terminal(updated)) refresh()
      },
      onConnection: (state, message) => setConnections(c => ({ ...c, [task.id]: { state, message } })),
    }))
    return () => stops.forEach(stop => stop())
    // Only task membership or explicit reconnect starts a new subscription.
  }, [projectId, activeIds, version, refresh])
  useEffect(() => {
    const resume = () => { if (document.visibilityState === 'visible') refresh() }
    window.addEventListener('focus', resume)
    document.addEventListener('visibilitychange', resume)
    return () => {
      window.removeEventListener('focus', resume)
      document.removeEventListener('visibilitychange', resume)
    }
  }, [refresh])
  return { workspace, error, loading, refresh, connections }
}
