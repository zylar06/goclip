import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { urls } from '../../api/client'
import { Btn, fmtDuration } from '../../ui'
import { draftDuration, type Draft, type RenderJob } from './types'
import { draftExportState } from './draftExportState'
import StudioDownloadLink from './StudioDownloadLink'

export default function DraftResultCard({ projectId, draft, jobs, onExport }: {
  projectId: string; draft: Draft; jobs: RenderJob[]; onExport: () => void
}) {
  const { t } = useTranslation()
  const state = draftExportState(draft, jobs)
  const [failed, setFailed] = useState('')
  const thumbnail = urls.thumbnail(projectId, draft.id, draft.revision)
  const editor = `/project/${projectId}/studio/${draft.id}`
  return <article className="web-project-card">
    <Link to={editor} className="studio-result-thumbnail" aria-label={t('Play full draft') + ': ' + draft.title}>
      {failed === thumbnail ? <span>{t('Thumbnail unavailable. Open full playback.')}</span> :
        <img src={thumbnail} alt="" width={480} height={270} loading="lazy" onError={() => setFailed(thumbnail)} />}
      <span>{fmtDuration(draftDuration(draft))} · ▷</span>
    </Link>
    <p className={`studio-output-state studio-output-state--${state.status}`}>{t(state.status)} · V{draft.revision}</p>
    <h3>{draft.title}</h3>{draft.hook && <p>{draft.hook}</p>}
    <details className="web-clip-details"><summary>{t('Source ranges')}</summary>
      <p>{draft.scenes.map(s => `${s.start.toFixed(3)}–${s.end.toFixed(3)}s`).join(' / ')}</p>
      <p className="studio-muted">{t('Source thumbnail; final packaging appears in the exported MP4.')}</p>
    </details>
    {state.failure && <p role="alert" className="studio-error">{state.failure.error || t('Export failed. Draft retained.')}</p>}
    {state.status === 'updated' && <p>{t('Draft changed. Previous exports are still available in history.')}</p>}
    <div className="studio-actions studio-card-actions">
      <Link className="ac-btn ac-btn--sm" to={editor}>{t('Edit draft')}</Link>
      {state.completed ? <><a className="ac-btn ac-btn--sm" href={urls.video(projectId, state.completed.job_id)} target="_blank" rel="noreferrer">{t('Play completed MP4')}</a><StudioDownloadLink projectId={projectId} jobId={state.completed.job_id} /></> :
        <Btn size="sm" variant="cta" disabled={!!state.active} onClick={onExport}>{t('Export…')}</Btn>}
    </div>
  </article>
}
