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
      right={<div className="studio-actions"><Btn size="sm" disabled={parentBusy || !workspace.project?.duration} onClick={onManual}>{t('New manual draft')}</Btn></div>}>
      {(workspace.workflows ?? []).map(workflow => <div key={workflow.id} className="studio-details">
        <h3>{t('Production')} · {t(workflow.status)} · V{workflow.plan_revision}</h3>
        {(workflow.goals ?? []).map(goal => <div key={goal.goal} role="status"><b>{t(goal.goal)} · {t(goal.status)}</b>
          {goal.error && <p role="alert" className="studio-error">{goal.error}</p>}
        </div>)}
      </div>)}
      <div className="ac-grid-3 studio-result-grid">{workspace.drafts.map(draft => <DraftResultCard key={draft.id} projectId={projectId} draft={draft} jobs={workspace.jobs} onExport={() => { setError(''); setExporting(draft) }} />)}</div>
      {!workspace.drafts.length && <p className="ac-empty"><b>{t('还没有草稿')}</b>{t('No drafts yet. Confirm analysis or create a manual draft without AI.')}</p>}
    </Section>
    <Section title={t('导出记录')} count={workspace.exports.length}>
      {!workspace.exports.length && <p className="ac-empty"><b>{t('还没有成片')}</b>{t('No completed exports yet.')}</p>}
      {workspace.exports.map(output => <div key={output.task_id} className="studio-history-row">
        <div><b>{output.title} · V{output.revision}</b>
          <span className="studio-muted">{new Date(output.created_at).toLocaleString()}</span></div>
        <a className="ac-btn ac-btn--sm" href={urls.video(projectId, output.task_id)} target="_blank" rel="noreferrer">{t("Play completed MP4")}</a><StudioDownloadLink projectId={projectId} jobId={output.task_id} />
      </div>)}
    </Section>
    <Dialog open={!!exporting} title={t('导出成片')} onClose={() => !busy && setExporting(null)}
      description={t('Export an immutable snapshot of this revision.')}
      footer={<div className="studio-actions"><Btn disabled={busy} onClick={() => setExporting(null)}>{t('Cancel')}</Btn><Btn variant="cta" loading={busy} onClick={render}>{t('确认导出')}</Btn></div>}>
      <p>{exporting?.title} · V{exporting?.revision}</p>{error && <p className="studio-error" role="alert">{error}</p>}
    </Dialog>
  </>
}
