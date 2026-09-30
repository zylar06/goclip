import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api, errorText } from '../api/client'
import { terminal, type Cue } from '../api/contracts'
import PlanSummary from '../features/studio/PlanSummary'
import SourcePreview from '../features/studio/SourcePreview'
import TaskPanel from '../components/TaskPanel'
import StudioResults from '../features/studio/StudioResults'
import { newDraft, newID } from '../features/studio/types'
import { useWorkspace } from '../features/studio/useWorkspace'
import { Btn, Dialog, fmtDuration } from '../ui'

function Subtitles({ projectId }: { projectId: string }) {
  const { t } = useTranslation()
  const [cues, setCues] = useState<Cue[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [version, setVersion] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    api.subtitles(projectId, controller.signal).then(data => { setCues(data); setError('') })
      .catch(cause => { if (!controller.signal.aborted) setError(errorText(cause)) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [projectId, version])
  return <div className="web-transcript">{loading && <p>{t('Loading subtitles…')}</p>}
    {error && <p role="alert" className="studio-error">{error} <Btn size="sm" onClick={() => setVersion(v => v + 1)}>{t('Retry')}</Btn></p>}
    {!loading && !error && !cues.length && <p>{t('No subtitle cues are available.')}</p>}
    {cues.map((cue, index) => <p key={index}><span className="ac-mono">{fmtDuration(cue.start)}–{fmtDuration(cue.end)}</span> {cue.text}</p>)}
  </div>
}
export default function ProjectPage() {
  const { id } = useParams()
  return <ProjectView key={id} projectId={id!} />
}
function ProjectView({ projectId }: { projectId: string }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { workspace, loading, error, refresh, connections } = useWorkspace(projectId)
  const [actionError, setActionError] = useState('')
  const [busy, setBusy] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [transcript, setTranscript] = useState(false)
  const [disableSubtitles, setDisableSubtitles] = useState(false)
  const [subtitleNotice, setSubtitleNotice] = useState('')
  const project = workspace.project
  const active = workspace.tasks.some(task => !terminal(task)) || (workspace.workflows ?? []).some(w => ['queued', 'running', 'producing'].includes(w.status))
  const ready = (project?.duration ?? 0) > 0 && !workspace.tasks.some(task => task.kind === 'import' && task.status !== 'completed')
  const action = async (fn: () => Promise<void>) => {
    setBusy(true); setActionError('')
    try { await fn() } catch (cause) { setActionError(errorText(cause)) } finally { setBusy(false) }
  }
  return <main className="ac-page">
    <Link className="ac-back" to="/">{t('Back to projects')}</Link>
    {error && <p className="studio-error" role="alert">{error} <Btn size="sm" onClick={refresh}>{t('Retry')}</Btn></p>}
    {!project ? (loading
      ? <div className="ac-loading" role="status" aria-label={t('Loading project…')}><span className="studio-sr">{t('Loading project…')}</span><div className="ac-skeleton" /><div className="ac-skeleton" /></div>
      : <p role="status" className="ac-empty"><b>{t('Project unavailable.')}</b></p>) : <>
      <header className="studio-row studio-project-head"><div><h1 className="ac-title">{project.name}</h1><p className="ac-meta">{t(project.status)} · {fmtDuration(project.duration)} · {project.width} × {project.height}</p></div>
        <Btn variant="danger" disabled={active || busy} onClick={() => { setActionError(''); setDeleting(true) }}>{t('Delete project…')}</Btn>
      </header>
      <p className="web-readiness">
        <span className={`web-pill${ready ? ' web-pill--ok' : ' web-pill--warn'}`}>{t(ready ? 'Source ready' : 'Source importing / unavailable')}</span>
        <span className="web-stat"><span className="web-stat-label">{t('Drafts')}</span><span className="web-stat-value">{workspace.drafts.length}</span></span>
        <span className="web-stat"><span className="web-stat-label">{t('Completed exports')}</span><span className="web-stat-value">{workspace.exports.length}</span></span>
      </p>
      {project.error && <p className="studio-error" role="alert">{project.error}</p>}
      <TaskPanel tasks={workspace.tasks} connections={connections} onRefresh={refresh} />
      {ready && <PlanSummary project={project} active={active} onChanged={refresh} onStarted={refresh} />}
      <Btn disabled={active || busy || !workspace.drafts.some(d => d.subtitles)} onClick={() => setDisableSubtitles(true)}>{t('Disable added subtitles in existing drafts…')}</Btn>
      <StudioResults projectId={projectId} workspace={workspace} onRefresh={refresh} busy={busy}
        onManual={() => action(async () => {
          const draft = await api.createDraft(projectId, newDraft(t('New draft'), [{ id: newID(), label: t('Source'), start: 0, end: Math.min(project.duration, 30), evidence: '' }]))
          navigate(`/project/${projectId}/studio/${draft.id}`)
        })} />
      <details className="studio-details"><summary>{t('Source video')}</summary>
        {ready && <SourcePreview projectId={projectId} />}
      </details>
      <details className="studio-details" onToggle={e => setTranscript(e.currentTarget.open)}><summary>{t('Subtitles')}</summary>{transcript && ready && <Subtitles projectId={projectId} />}</details>
      <details className="studio-details"><summary>{t('Candidate scenes')} ({workspace.candidates.length})</summary>
        {!workspace.candidates.length && <p className="studio-muted">{t('暂无候选镜头。先完成一次分析。')}</p>}
        {workspace.candidates.map(candidate =>
        <div className="studio-source-row" key={candidate.id}><div><b>{candidate.label}</b><p>{fmtDuration(candidate.start)}–{fmtDuration(candidate.end)} · {candidate.kind} · {candidate.score}</p><p className="studio-muted">{candidate.evidence}</p></div>
          <Btn size="sm" disabled={busy} onClick={() => action(async () => {
            const draft = await api.createDraft(projectId, newDraft(candidate.label || t('New draft'), [{ ...candidate, id: newID() }].map(({ id, label, start, end, evidence }) => ({ id, label, start, end, evidence }))))
            navigate(`/project/${projectId}/studio/${draft.id}`)
          })}>{t('Use in new draft')}</Btn>
        </div>)}</details>
      <Dialog open={deleting} title={t('Delete project permanently?')} onClose={() => !busy && setDeleting(false)} description={t('This removes the source, drafts, task history, and exports for all users. Active tasks must finish first.')}
        footer={<div className="studio-actions"><Btn disabled={busy} onClick={() => setDeleting(false)}>{t('Keep project')}</Btn><Btn variant="danger" loading={busy} disabled={active} onClick={() => action(async () => { await api.removeProject(projectId); navigate('/') })}>{t('Confirm permanent deletion')}</Btn></div>}>
        <p>{project.name}</p>{actionError && <p role="alert" className="studio-error">{actionError}</p>}
      </Dialog>
      <Dialog open={disableSubtitles} title={t('Disable added subtitles in existing drafts?')} onClose={() => !busy && setDisableSubtitles(false)}
        description={t('This explicitly updates saved draft revisions. Original on-screen captions and completed MP4 files are unchanged; export again to apply.')}
        footer={<div className="studio-actions"><Btn disabled={busy} onClick={() => setDisableSubtitles(false)}>{t('Cancel')}</Btn><Btn disabled={active} loading={busy} onClick={() => action(async () => {
          const result = await api.disableSubtitles(projectId); setDisableSubtitles(false); refresh()
          setActionError(''); setSubtitleNotice(t('Updated drafts: {{count}}', { count: result.updated }))
        })}>{t('Confirm disabling added subtitles')}</Btn></div>}>
        {actionError && <p role="alert" className="studio-error">{actionError}</p>}
      </Dialog>
      {subtitleNotice && <p role="status">{subtitleNotice}</p>}
      {actionError && !deleting && <p role="alert" className="studio-error">{actionError}</p>}
    </>}
  </main>
}
