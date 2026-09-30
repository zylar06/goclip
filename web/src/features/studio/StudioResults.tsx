import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, errorText, urls } from '../../api/client'
import { Btn, Dialog, Section } from '../../ui'
import { type Draft, type Workspace } from './types'
import DraftResultCard from './DraftResultCard'
import StudioDownloadLink from './StudioDownloadLink'

export default function StudioResults({ projectId, workspace, onRefresh, onManual, busy: parentBusy }: {
  projectId: string; workspace: Workspace; onRefresh: () => void; onManual: () => void; busy: boolean
}) {
  const { t } = useTranslation()
  const [exporting, setExporting] = useState<Draft | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [historyPanel, setHistoryPanel] = useState<'production' | 'exports' | null>(null)
  const render = async () => {
    if (!exporting) return
    setBusy(true); setError('')
    try { await api.export(projectId, exporting.id, exporting.revision); setExporting(null); onRefresh() }
    catch (cause) { setError(errorText(cause)) }
    finally { setBusy(false) }
  }
  return <>
    <Section title={t('剪辑与成片')} count={workspace.drafts.length}
      right={<div className="studio-actions"><Btn size="sm" disabled={parentBusy || !workspace.project?.duration} onClick={onManual}>{t('New manual draft')}</Btn></div>}>
      {(workspace.workflows ?? []).filter(workflow => workflow.status !== 'completed').map(workflow => <div key={workflow.id} className="web-workflow">
        <h3>{t('Production')} · {t(workflow.status)} · V{workflow.plan_revision}</h3>
        {(workflow.goals ?? []).map(goal => <div key={goal.goal} role="status"><b>{t(goal.goal)} · {t(goal.status)}</b>
          {goal.error && <p role="alert" className="studio-error">{goal.error}</p>}
        </div>)}
      </div>)}
      <div className="ac-grid-3 studio-result-grid">{workspace.drafts.map(draft => <DraftResultCard key={draft.id} projectId={projectId} draft={draft} jobs={workspace.jobs} onExport={() => { setError(''); setExporting(draft) }} />)}</div>
      {!workspace.drafts.length && <p className="ac-empty">{t('No drafts yet. Confirm analysis or create a manual draft without AI.')}</p>}
    </Section>
    {((workspace.workflows ?? []).some(w => w.status === 'completed') || !!workspace.exports.length) && <div className="web-history-tools">
      <nav className="web-tool-nav" aria-label={t('History')}>
        {(workspace.workflows ?? []).some(w => w.status === 'completed') && <button type="button" className={`web-tool-tab${historyPanel === 'production' ? ' is-active' : ''}`} aria-pressed={historyPanel === 'production'} onClick={() => setHistoryPanel(historyPanel === 'production' ? null : 'production')}>{t('Production history')}</button>}
        {!!workspace.exports.length && <button type="button" className={`web-tool-tab${historyPanel === 'exports' ? ' is-active' : ''}`} aria-pressed={historyPanel === 'exports'} onClick={() => setHistoryPanel(historyPanel === 'exports' ? null : 'exports')}>{t('导出记录')} ({workspace.exports.length})</button>}
      </nav>
      {historyPanel === 'production' && <div className="web-tool-panel">
      {(workspace.workflows ?? []).filter(w => w.status === 'completed').map(workflow => <div className="web-workflow" key={workflow.id}>
        <h3>{t('Production')} · {t(workflow.status)} · V{workflow.plan_revision}</h3>
        {(workflow.goals ?? []).map(goal => <p key={goal.goal} role="status"><b>{t(goal.goal)} · {t(goal.status)}</b>{goal.error && <span role="alert" className="studio-error">{goal.error}</span>}</p>)}
      </div>)}
      </div>}
      {historyPanel === 'exports' && <div className="web-tool-panel web-export-history">
      {workspace.exports.map(output => <div key={output.task_id} className="studio-history-row">
        <div><b>{output.title} · V{output.revision}</b>
          <span className="studio-muted">{new Date(output.created_at).toLocaleString()}</span></div>
        <a className="ac-btn ac-btn--sm" href={urls.video(projectId, output.task_id)} target="_blank" rel="noreferrer">{t("Play completed MP4")}</a><StudioDownloadLink projectId={projectId} jobId={output.task_id} />
      </div>)}
      </div>}
    </div>}
    <Dialog open={!!exporting} title={t('导出成片')} onClose={() => !busy && setExporting(null)}
      description={t('Export an immutable snapshot of this revision.')}
      footer={<div className="studio-actions"><Btn disabled={busy} onClick={() => setExporting(null)}>{t('Cancel')}</Btn><Btn variant="cta" loading={busy} onClick={render}>{t('确认导出')}</Btn></div>}>
      <p>{exporting?.title} · V{exporting?.revision}</p>{error && <p className="studio-error" role="alert">{error}</p>}
    </Dialog>
  </>
}
