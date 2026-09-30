import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, errorText, urls } from '../../api/client'
import { terminal, type PreviewStatus } from '../../api/contracts'
import { monitorTask } from '../../api/taskMonitor'
import { Btn } from '../../ui'

/** Read-only discovery on mount; conversion requires the explicit button. */
export function useSourcePreview(projectId: string) {
  const [state, setState] = useState<PreviewStatus>({ status: 'idle' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [version, setVersion] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    api.previewStatus(projectId, controller.signal).then(value => { if (!controller.signal.aborted) { setState(value); setError('') } })
      .catch(cause => { if (!controller.signal.aborted) setError(errorText(cause)) })
    return () => controller.abort()
  }, [projectId, version])
  useEffect(() => {
    if (!state.task || terminal(state.task)) return
    return monitorTask(state.task, {
      onTask: task => { setState({ status: task.status, task }); if (terminal(task)) setVersion(v => v + 1) },
      onConnection: (_status, message) => { if (message) setError(message) },
    })
  }, [state.task?.id, version])
  const start = async () => {
    setBusy(true); setError('')
    try { const task = await api.preparePreview(projectId); setState({ status: task.status, task }) }
    catch (cause) { setError(errorText(cause)) } finally { setBusy(false) }
  }
  return { state, error, busy, start, refresh: () => setVersion(v => v + 1),
    src: state.status === 'completed' ? urls.preview(projectId) : urls.source(projectId) }
}
export function PreviewControls({ preview }: { preview: ReturnType<typeof useSourcePreview> }) {
  const { t } = useTranslation()
  const active = ['queued', 'running'].includes(preview.state.status)
  return <div className="studio-details">
    <p className="studio-muted">{t('Compatible preview is local conversion, not cloud analysis. Original media is retained.')}</p>
    <p role="status">{t('Compatible preview')}: {t(preview.state.status)} {preview.state.task?.stage} {preview.state.task?.progress != null ? `${preview.state.task.progress}%` : ''}</p>
    <Btn size="sm" disabled={active || preview.busy || preview.state.status === 'completed'} onClick={preview.start}>{t('Create compatible preview')}</Btn>
    <Btn size="sm" onClick={preview.refresh}>{t('Refresh preview status')}</Btn>
    {(preview.error || preview.state.task?.error) && <p role="alert" className="studio-error">{preview.error || preview.state.task?.error}</p>}
  </div>
}
export default function SourcePreview({ projectId }: { projectId: string }) {
  const preview = useSourcePreview(projectId)
  const { t } = useTranslation()
  const [failed, setFailed] = useState(false)
  return <><video className="studio-source-video" aria-label={t('Source video')} controls preload="metadata" src={preview.src} onError={() => setFailed(true)} onLoadedData={() => setFailed(false)} />
    {failed && <p role="alert" className="studio-error">{t('Playback unavailable. Try a compatible preview.')}</p>}<PreviewControls preview={preview} /></>
}
