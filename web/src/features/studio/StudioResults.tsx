import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api, errorText } from '../../api/client'
import { Btn, Dialog, Section, fmtDuration } from '../../ui'
import { draftDuration, type Draft, type Workspace } from './types'
import { draftExportState } from './draftExportState'
import StudioDownloadLink from './StudioDownloadLink'

export default function StudioResults({ projectId, workspace, onRefresh, onCollection, onManual, busy: parentBusy }: {
  projectId: string; workspace: Workspace; onRefresh: () => void; onCollection: () => void; onManual: () => void; busy: boolean
}) {
  const { t } = useTranslation()
  const [exporting, setExporting] = useState<Draft | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const render = async () => {
    if (!exporting) return
    setBusy(true); setError('')
    try { await api.export(projectId, exporting.id, exporting.revision); setExporting(null); onRefresh() }
    catch (cause) { setError(errorText(cause)) }
    finally { setBusy(false) }
  }
  return <>
    <Section title={t('剪辑与成片')} count={workspace.drafts.length}
      description={t('Source ready, drafts ready, and exported are separate states. Only completed exports can be downloaded.')}
      right={<div className="studio-actions"><Btn size="sm" disabled={parentBusy || !workspace.project?.duration} onClick={onManual}>{t('New manual draft')}</Btn><Btn size="sm" onClick={onCollection}>{t('Create collection')}</Btn></div>}>
      <div className="ac-grid-3 studio-result-grid">{workspace.drafts.map(draft => {
        const state = draftExportState(draft, workspace.jobs)
        return <article className="web-project-card" key={draft.id}>
          <p className={`studio-output-state studio-output-state--${state.status}`}>{t(state.status)} · V{draft.revision}</p>
          <h3>{draft.title}</h3><p>{draft.hook}</p><p className="studio-muted">{fmtDuration(draftDuration(draft))} · {draft.scenes.length} {t('scenes')} · {draft.aspect}</p>
          {state.status === 'updated' && <p>{t('Draft changed. Previous exports are still available in history.')}</p>}
          <div className="studio-actions"><Link className="ac-btn" to={`/project/${projectId}/studio/${draft.id}`}>{t('Edit draft')}</Link>
            {state.completed ? <StudioDownloadLink projectId={projectId} jobId={state.completed.job_id} /> :
              <Btn size="sm" disabled={!!state.active} onClick={() => { setError(''); setExporting(draft) }}>{t('Export…')}</Btn>}
          </div>
        </article>
      })}</div>
      {!workspace.drafts.length && <p className="ac-empty">{t('No drafts yet. Confirm analysis or create a manual draft without AI.')}</p>}
    </Section>
    <Section title={t('导出记录')} count={workspace.exports.length}>
      {!workspace.exports.length && <p className="studio-muted">{t('No completed exports yet.')}</p>}
      {workspace.exports.map(output => <div key={output.task_id} className="studio-history-row"><b>{output.title} · V{output.revision}</b>
        <span className="studio-muted">{new Date(output.created_at).toLocaleString()}</span>
        <StudioDownloadLink projectId={projectId} jobId={output.task_id} />
      </div>)}
    </Section>
    <Dialog open={!!exporting} title={t('导出成片')} onClose={() => !busy && setExporting(null)}
      description={t('Export an immutable snapshot of this revision. Translation may use the saved text model and incur charges.')}
      footer={<div className="studio-actions"><Btn disabled={busy} onClick={() => setExporting(null)}>{t('Cancel')}</Btn><Btn variant="cta" loading={busy} onClick={render}>{t('确认导出')}</Btn></div>}>
      <p>{exporting?.title} · V{exporting?.revision}</p>{error && <p className="studio-error" role="alert">{error}</p>}
    </Dialog>
  </>
}
