import { useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api, errorText } from '../api/client'
import { terminal } from '../api/contracts'
import { useWorkspace } from '../features/studio/useWorkspace'
import PlanSummary from '../features/studio/PlanSummary'
import SourcePreview from '../features/studio/SourcePreview'
import TaskPanel from '../components/TaskPanel'
import { Btn, fmtDuration } from '../ui'

export default function ImportReview() {
  const { id } = useParams()
  const location = useLocation()
  const navigate = useNavigate()
  const { t } = useTranslation()
  const { workspace, loading, error, refresh, connections } = useWorkspace(id)
  const [actionError, setActionError] = useState('')
  const [busy, setBusy] = useState(false)
  const project = workspace.project
  const active = workspace.tasks.some(task => !terminal(task)) ||
    (workspace.workflows ?? []).some(w => ['queued', 'running', 'producing'].includes(w.status))
  const incompleteImport = workspace.tasks.find(task => task.kind === 'import' && task.status !== 'completed')
  const ready = (project?.duration ?? 0) > 0 && !incompleteImport
  return <main className="ac-page">
    <Link className="ac-back" to="/">{t('Back to projects')}</Link>
    <h1 className="ac-title">{t('Review imported source')}</h1>
    <p>{t('Upload → inspect and confirm → production')}</p>
    {error && <p role="alert" className="studio-error">{error}</p>}
    {loading && !project && <p role="status">{t('Loading project…')}</p>}
    {project && <>
      <h2>{project.name}</h2><p>{fmtDuration(project.duration)} · {project.width} × {project.height}</p>
      {project.error && <p role="alert" className="studio-error">{project.error}</p>}
      {ready ? <>
        <SourcePreview projectId={project.id} />
        <PlanSummary project={project} active={active} onChanged={refresh}
          initialInstruction={(location.state as { instruction?: string } | null)?.instruction}
          onStarted={() => navigate(`/project/${project.id}`)} />
        <Btn disabled={active || busy} onClick={async () => {
          setBusy(true); setActionError('')
          try { await api.inspect(project.id); refresh() } catch (cause) { setActionError(errorText(cause)) } finally { setBusy(false) }
        }}>{t('Recheck local evidence (no model call)')}</Btn>
      </> : <p role="status">{t(incompleteImport && terminal(incompleteImport)
        ? 'Import did not complete. Review the import task below and retry before production.'
        : 'Importing source. No transcription or production has started.')}</p>}
      <p><Link className="ac-btn" to={`/project/${project.id}`}>{t(active ? 'View production progress' : 'Open project / manual editing')}</Link></p>
    </>}
    {actionError && <p role="alert" className="studio-error">{actionError}</p>}
    <TaskPanel tasks={workspace.tasks} connections={connections} onRefresh={refresh} />
  </main>
}
