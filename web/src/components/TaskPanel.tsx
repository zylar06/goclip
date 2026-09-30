import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, errorText } from '../api/client'
import { terminal, type Task } from '../api/contracts'
import type { Connection } from '../api/taskMonitor'
import { Btn, Dialog, ProgressLine, Section } from '../ui'

export default function TaskPanel({ tasks, connections = {}, onRefresh }: {
  tasks: Task[]; connections?: Record<string, { state: Connection; message: string }>; onRefresh: () => void
}) {
  const { t } = useTranslation()
  const [action, setAction] = useState<{ task: Task; kind: 'retry' | 'cancel' } | null>(null)
  const [consent, setConsent] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const importRetry = action?.kind === 'retry' && action.task.kind === 'import'
  const act = async () => {
    if (!action || (action.kind === 'retry' && !consent)) return
    setBusy(true); setError('')
    try {
      const result = await api[action.kind](action.task.id)
      setNotice(result.cancel_requested ? 'Cancellation request accepted.' : action.kind === 'retry' ? 'Retry queued.' : 'Task cancelled.')
      setAction(null); setConsent(false); onRefresh()
    } catch (cause) { setError(errorText(cause)) }
    finally { setBusy(false) }
  }
  return <Section title={t('Tasks')} count={tasks.length} right={<Btn size="sm" onClick={onRefresh}>{t('Refresh / reconnect')}</Btn>}>
    {!tasks.length && <p className="ac-empty"><b>{t('暂无任务')}</b>{t('No tasks yet.')}</p>}
    {tasks.map(task => {
      const running = !terminal(task)
      const failed = ['failed', 'interrupted'].includes(task.status)
      const tone = task.status === 'completed' ? 'ok' : failed ? 'error'
        : task.status === 'cancelled' ? 'warn' : running ? 'running' : 'neutral'
      return <article className={`web-task${running ? ' web-task--active' : ''}${failed ? ' web-task--failed' : ''}`} key={task.id} aria-label={`${task.kind} ${task.id}`}>
        <div className="web-task-head">
          {/* One element, one accessible string: tests query "analyze · running" by text. */}
          <b className={`web-task-kind web-pill web-pill--${tone}`}>{t(task.kind)} · {t(task.status)}</b>
          <span className={`ac-mono web-task-percent${task.progress === null ? ' web-task-percent--idle' : ''}`}>{task.progress === null ? t('Progress unavailable') : `${task.progress}%`}</span>
        </div>
        {running && <ProgressLine percent={task.progress} large />}
        <p className="web-task-stage">{t('Stage')}: {task.stage || '—'}</p>
        {!!task.completed_steps?.length && <p className="web-task-steps">{t('Completed steps')}: {task.completed_steps.join(' → ')}</p>}
        <p className="web-task-facts"><span>{t('Heartbeat')}: {task.heartbeat || '—'}{connections[task.id] && !terminal(task) && ` · ${t(connections[task.id].state)}`}</span></p>
        {!terminal(task) && task.heartbeat && Date.now() - Date.parse(task.heartbeat) > 120000 &&
          <p role="status" className="web-warning">{t('Worker heartbeat is stale. Check server health; no automatic retry will be sent.')}</p>}
        {connections[task.id]?.message && !terminal(task) && <p className="web-warning" role="status">{t(connections[task.id].message)}</p>}
        {task.error && <p className="studio-error" role="alert">{task.error}</p>}
        {task.kind === 'import' && terminal(task) && task.status !== 'completed' &&
          <p className="web-warning" role="status">{t('Source import is incomplete. Use Retry task below if available; otherwise import the source again. Production remains unavailable until import completes.')}</p>}
        {task.cancel_requested && !terminal(task) && <p role="status" className="web-warning">{t('Cancellation requested. Waiting for the worker to stop.')}</p>}
        <div className="studio-actions web-task-actions">
          {!terminal(task) && <Btn size="sm" disabled={busy || task.cancel_requested} onClick={() => { setError(''); setAction({ task, kind: 'cancel' }) }}>{t('Cancel task')}</Btn>}
          {terminal(task) && task.retryable && task.status !== 'completed' &&
            <Btn size="sm" disabled={busy} onClick={() => { setError(''); setConsent(false); setAction({ task, kind: 'retry' }) }}>{t('Retry task…')}</Btn>}
        </div>
      </article>
    })}
    {notice && <p role="status" className="web-note">{t(notice)}</p>}
    <Dialog open={!!action} onClose={() => !busy && setAction(null)} title={t(importRetry ? 'Confirm import retry' : action?.kind === 'retry' ? 'Confirm paid retry' : 'Cancel this task?')}
      description={t(importRetry ? 'Retry resumes source import and subtitle inspection only. It does not authorize transcription or cloud production. No retry happens automatically.'
        : action?.kind === 'retry' ? 'Retry may repeat paid model calls. Previous charges are not refunded. No retry happens automatically.' : 'A running task stays running until its child process stops. Work already billed may still incur charges.')}
      footer={<div className="studio-actions"><Btn disabled={busy} onClick={() => setAction(null)}>{t('Keep task unchanged')}</Btn><Btn variant="cta" loading={busy} disabled={action?.kind === 'retry' && !consent} onClick={act}>{t(action?.kind === 'retry' ? 'Confirm retry' : 'Request cancellation')}</Btn></div>}>
      {action?.kind === 'retry' && <label className="web-consent"><input type="checkbox" checked={consent} onChange={e => setConsent(e.target.checked)} /> {t(importRetry ? 'I confirm retrying source import.' : 'I understand and approve possible additional charges.')}</label>}
      {error && <p className="studio-error" role="alert">{error}</p>}
    </Dialog>
  </Section>
}
