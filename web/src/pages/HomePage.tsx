import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api, errorText } from '../api/client'
import type { Project } from '../api/contracts'
import { Btn, Section, fmtDuration } from '../ui'

export function validateSourceURL(value: string): string {
  const invalid = () => new Error('Use an HTTPS Bilibili or YouTube video-page link, or a b23.tv share link, without credentials or ports. Playlist pages are unsupported.')
  if (value.length > 4096 || /[\\\x00\r\n\t]/.test(value) || value.trim() !== value) throw invalid()
  const url = new URL(value)
  const authority = value.match(/^https:\/\/([^/?#]+)/)?.[1]
  if (!authority || authority.includes(':') || url.username || url.password || url.pathname.includes('%')) throw invalid()
  const youtubeID = /^[A-Za-z0-9_-]{11}$/
  if (['youtube.com', 'www.youtube.com', 'm.youtube.com', 'youtu.be'].includes(url.hostname)) {
    const id = url.hostname === 'youtu.be' ? url.pathname.slice(1) :
      url.pathname === '/watch' && url.searchParams.getAll('v').length === 1 ? url.searchParams.get('v') :
        url.pathname.startsWith('/shorts/') ? url.pathname.slice(8) : null
    if (!id || !youtubeID.test(id)) throw invalid()
    return `https://www.youtube.com/watch?v=${id}`
  }
  if (['bilibili.com', 'www.bilibili.com', 'm.bilibili.com'].includes(url.hostname) && /^\/video\/(BV[A-Za-z0-9]{10}|av[1-9][0-9]*)\/?$/i.test(url.pathname)) {
    const pages = url.searchParams.getAll('p')
    if (pages.length > 1 || (pages.length === 1 && !/^[1-9][0-9]{0,3}$/.test(pages[0]))) throw invalid()
    return `https://www.bilibili.com${url.pathname.replace(/\/$/, '')}${pages.length ? `?p=${pages[0]}` : ''}`
  }
  // Bilibili's share sheet emits b23.tv links. Only the shape is checkable here;
  // the server resolves the redirect and revalidates the destination, so an
  // unsupported target fails during import rather than at paste time.
  if (url.hostname === 'b23.tv' && /^\/[A-Za-z0-9]{4,24}\/?$/.test(url.pathname) && !url.search && !url.hash) {
    return `https://b23.tv${url.pathname.replace(/\/$/, '')}`
  }
  throw invalid()
}
export default function HomePage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [projects, setProjects] = useState<Project[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [version, setVersion] = useState(0)
  const [mode, setMode] = useState<'file' | 'url'>('file')
  const [name, setName] = useState('')
  const [url, setURL] = useState('')
  const [video, setVideo] = useState<File | null>(null)
  const [srt, setSRT] = useState<File | null>(null)
  const [instruction, setInstruction] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const changeMode = (next: 'file' | 'url') => {
    if (mode === next) return
    // File inputs remount between modes; do not upload a now-invisible old file.
    setMode(next); setVideo(null); setSRT(null); setError('')
  }
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    api.projects(controller.signal).then(data => { if (!controller.signal.aborted) { setProjects(data); setLoadError('') } })
      .catch(cause => { if (!controller.signal.aborted) setLoadError(errorText(cause)) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [version])
  const create = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true); setError('')
    try {
      let project: Project
      if (new TextEncoder().encode(instruction).length > 4000) throw new Error('Instructions must not exceed 4000 UTF-8 bytes.')
      if (srt && (!/\.srt$/i.test(srt.name) || srt.size === 0 || srt.size > 10 * 1024 ** 2)) throw new Error('Choose a non-empty SRT up to 10 MiB.')
      if (mode === 'url' && !srt) project = await api.importURL(name.trim(), validateSourceURL(url.trim()), instruction)
      else {
        const form = new FormData()
        form.set('name', name.trim() || (mode === 'file' ? video?.name ?? '' : ''))
        if (mode === 'file') {
          if (!video || video.size === 0 || video.size > 4 * 1024 ** 3) throw new Error('Choose a non-empty video up to 4 GiB.')
          if (!/\.(mp4|mkv|mov|webm|avi|m4v|flv|ts)$/i.test(video.name)) throw new Error('Unsupported video extension.')
          form.set('video', video)
        } else form.set('url', validateSourceURL(url.trim()))
        if (srt) form.set('subtitle', srt)
        if (instruction) form.set('instruction', instruction)
        project = await api.importFile(form)
      }
      navigate(`/import/${project.id}`, { state: { instruction } })
    } catch (cause) { setError(errorText(cause)) }
    finally { setBusy(false) }
  }
  return <main className="ac-page">
    <header className="web-hero"><span className="ac-eyebrow">AUTOCLIP · WEB</span><h1>{t('Your source. Your edit.')}</h1><p>{t('Import a video, choose what to make, and refine every scene.')}</p></header>
    <form className="studio-import-box" onSubmit={create} noValidate>
      <fieldset disabled={busy} className="studio-fieldset">
        <div className="studio-actions studio-mode-switch"><Btn aria-pressed={mode === 'file'} onClick={() => changeMode('file')}>{t('Upload video + SRT')}</Btn><Btn aria-pressed={mode === 'url'} onClick={() => changeMode('url')}>{t('Bilibili / YouTube URL')}</Btn></div>
        <label className="studio-field">{t('Project name (optional)')}<input maxLength={200} value={name} onChange={e => setName(e.target.value)} /></label>
        {mode === 'file' ? <div className="studio-fields studio-fields--2">
          <label className="studio-field">{t('Video file')}<input type="file" accept=".mp4,.mkv,.mov,.webm,.avi,.m4v,.flv,.ts" required onChange={e => setVideo(e.target.files?.[0] ?? null)} /></label>
        </div> : <label className="studio-field">{t('Video URL')}<input type="url" required placeholder="https://…" value={url} onChange={e => setURL(e.target.value)} /></label>}
        <label className="studio-field">{t('SRT subtitles (optional)')}<input key={mode} type="file" accept=".srt" onChange={e => setSRT(e.target.files?.[0] ?? null)} /></label>
        <details className="studio-details"><summary>{t('Special requirements (optional)')}</summary><label className="studio-field">{t('Production instructions')}<textarea value={instruction} onChange={e => setInstruction(e.target.value)} /></label></details>
        <p className="studio-muted">{t('Default limits: 4 GiB / 2 hours; SRT 10 MiB. Import only checks local media; transcription and production wait for your confirmation.')}</p>
        <div className="studio-import-submit"><Btn type="submit" variant="cta" loading={busy}>{t(busy ? 'Uploading / importing…' : 'Create project')}</Btn></div>
      </fieldset>
      {busy && <p role="status" className="web-note">{t('Keep this page open until upload completes. Do not submit again after a timeout before checking the project list.')}</p>}
      {error && <p className="studio-error" role="alert">{error}</p>}
    </form>
    <Section title={t('Projects')} count={projects.length} right={<Btn size="sm" onClick={() => setVersion(v => v + 1)}>{t('Refresh')}</Btn>}>
      {loading && <div className="ac-loading" role="status" aria-label={t('Loading projects…')}><span className="studio-sr">{t('Loading projects…')}</span>
        <div className="ac-skeleton" /><div className="ac-skeleton" /><div className="ac-skeleton" /></div>}
      {loadError && <p className="studio-error" role="alert">{loadError}</p>}
      {!loading && !loadError && !projects.length && <p className="ac-empty"><b>{t('No projects yet')}</b>{t('用上面的表单导入第一个素材。')}</p>}
      <div className="ac-grid-3">{projects.map(project => <Link className="web-project-card" key={project.id} to={`/project/${project.id}`}>
        <span className="ac-eyebrow">{t(project.status)}</span><h2>{project.name}</h2>
        <p className="web-card-metrics"><span>{fmtDuration(project.duration)}</span><span>{project.width} × {project.height}</span></p>
        <small>{new Date(project.updated_at).toLocaleString()}</small>{project.error && <p className="studio-error">{project.error}</p>}
      </Link>)}</div>
    </Section>
  </main>
}
