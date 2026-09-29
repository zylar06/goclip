import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api, errorText, urls } from '../api/client'
import { terminal, type Cue } from '../api/contracts'
import AnalysisPanel from '../components/AnalysisPanel'
import TaskPanel from '../components/TaskPanel'
import CollectionDialog from '../components/CollectionDialog'
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
    {error && <p role="alert" className="studio-error">{error} <Btn onClick={() => setVersion(v => v + 1)}>{t('Retry')}</Btn></p>}
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
  const [collection, setCollection] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [transcript, setTranscript] = useState(false)
  const [playbackError, setPlaybackError] = useState(false)
  const project = workspace.project
  const active = workspace.tasks.some(task => !terminal(task))
  const ready = !!project?.duration && !workspace.tasks.some(task => task.kind === 'import' && !terminal(task))
  const action = async (fn: () => Promise<void>) => {
    setBusy(true); setActionError('')
    try { await fn() } catch (cause) { setActionError(errorText(cause)) } finally { setBusy(false) }
  }
  return <main className="ac-page">
    <Link className="ac-back" to="/">{t('Back to projects')}</Link>
    {error && <p className="studio-error" role="alert">{error} <Btn onClick={refresh}>{t('Retry')}</Btn></p>}
    {!project ? <p role="status">{t(loading ? 'Loading project…' : 'Project unavailable.')}</p> : <>
      <header className="studio-row"><div><h1 className="ac-title">{project.name}</h1><p className="ac-meta">{t(project.status)} · {fmtDuration(project.duration)} · {project.width} × {project.height}</p></div>
        <Btn variant="danger" disabled={active || busy} onClick={() => { setActionError(''); setDeleting(true) }}>{t('Delete project…')}</Btn>
      </header>
      <p className="web-readiness">{t(ready ? 'Source ready' : 'Source importing / unavailable')} · {t('Drafts')}: {workspace.drafts.length} · {t('Completed exports')}: {workspace.exports.length}</p>
      {project.error && <p className="studio-error" role="alert">{project.error}</p>}
      <TaskPanel tasks={workspace.tasks} connections={connections} onRefresh={refresh} />
      <AnalysisPanel projectId={projectId} ready={ready} active={active} onStarted={refresh} />
      <StudioResults projectId={projectId} workspace={workspace} onRefresh={refresh} onCollection={() => setCollection(true)} busy={busy}
        onManual={() => action(async () => {
          const draft = await api.createDraft(projectId, newDraft(t('New draft'), [{ id: newID(), label: t('Source'), start: 0, end: Math.min(project.duration, 30), evidence: '' }]))
          navigate(`/project/${projectId}/studio/${draft.id}`)
        })} />
      <details className="studio-details"><summary>{t('Source video')}</summary>
        {ready && <video className="studio-source-video" controls preload="none" src={urls.source(projectId)} onError={() => setPlaybackError(true)} />}
        {playbackError && <p role="alert" className="studio-error">{t('This browser cannot play the source. Timing edits and export remain available; try a browser-compatible MP4 source.')}</p>}
      </details>
      <details className="studio-details" onToggle={e => setTranscript(e.currentTarget.open)}><summary>{t('Subtitles')}</summary>{transcript && ready && <Subtitles projectId={projectId} />}</details>
      <details className="studio-details"><summary>{t('Candidate scenes')} ({workspace.candidates.length})</summary>{workspace.candidates.map(candidate =>
        <div className="studio-source-row" key={candidate.id}><div><b>{candidate.label}</b><p>{fmtDuration(candidate.start)}–{fmtDuration(candidate.end)} · {candidate.kind} · {candidate.score}</p><p className="studio-muted">{candidate.evidence}</p></div>
          <Btn disabled={busy} onClick={() => action(async () => {
            const draft = await api.createDraft(projectId, newDraft(candidate.label || t('New draft'), [{ ...candidate, id: newID() }].map(({ id, label, start, end, evidence }) => ({ id, label, start, end, evidence }))))
            navigate(`/project/${projectId}/studio/${draft.id}`)
          })}>{t('Use in new draft')}</Btn>
        </div>)}</details>
      {collection && <CollectionDialog projectId={projectId} duration={project.duration} drafts={workspace.drafts} candidates={workspace.candidates} onClose={() => setCollection(false)} onCreated={draft => navigate(`/project/${projectId}/studio/${draft.id}`)} />}
      <Dialog open={deleting} title={t('Delete project permanently?')} onClose={() => !busy && setDeleting(false)} description={t('This removes the source, drafts, task history, and exports for all users. Active tasks must finish first.')}
        footer={<div className="studio-actions"><Btn disabled={busy} onClick={() => setDeleting(false)}>{t('Keep project')}</Btn><Btn variant="danger" loading={busy} disabled={active} onClick={() => action(async () => { await api.removeProject(projectId); navigate('/') })}>{t('Confirm permanent deletion')}</Btn></div>}>
        <p>{project.name}</p>{actionError && <p role="alert" className="studio-error">{actionError}</p>}
      </Dialog>
      {actionError && !deleting && <p role="alert" className="studio-error">{actionError}</p>}
    </>}
  </main>
}
