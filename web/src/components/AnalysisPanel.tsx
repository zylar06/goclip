import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, errorText } from '../api/client'
import type { AnalysisOptions, Draft } from '../api/contracts'
import { Btn, Dialog, Section } from '../ui'

// Genre-specific prompt sets ported from upstream backend/prompt/<category>/.
// An empty value uses the shared prompts, which is the server-side default.
const categories = [
  { value: 'knowledge', label: 'Knowledge / explainer' },
  { value: 'speech', label: 'Talk / interview' },
  { value: 'opinion', label: 'Commentary / opinion' },
  { value: 'experience', label: 'Tutorial / how-to' },
  { value: 'business', label: 'Business / finance' },
  { value: 'entertainment', label: 'Variety / entertainment' },
  { value: 'content_review', label: 'Recap / analysis' },
] as const

export default function AnalysisPanel({ projectId, ready, active, onStarted }: {
  projectId: string; ready: boolean; active: boolean; onStarted: () => void
}) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<AnalysisOptions['mode']>('subtitle')
  const [images, setImages] = useState(false)
  const [goals, setGoals] = useState<AnalysisOptions['goals']>(['content'])
  const [duration, setDuration] = useState(30)
  const [aspect, setAspect] = useState<Draft['aspect']>('original')
  const [category, setCategory] = useState('')
  const [instruction, setInstruction] = useState('')
  const [open, setOpen] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const valid = ready && !active && goals.length > 0 && duration >= 10 && duration <= 120 &&
    Number.isInteger(duration) && new TextEncoder().encode(instruction).length <= 4000 && (mode !== 'visual' || images)
  const start = async () => {
    if (!valid || !confirmed) return
    setBusy(true); setError('')
    try {
	await api.analyze(projectId, { mode, allow_visual: (mode === 'visual' || mode === 'auto') && images, confirmed: true, goals, duration, aspect, category, instruction })
      setOpen(false); setConfirmed(false); setImages(false); onStarted()
    } catch (cause) { setError(errorText(cause)) }
    finally { setBusy(false) }
  }
  return <Section title={t('Analyze source')} description={t('Import never starts analysis. Choose a mode and confirm the cloud cost before each analysis.')}>
    {!ready && <p role="status" className="web-warning">{t('Wait for source import to complete.')}</p>}
    <fieldset className="studio-fieldset" disabled={!ready || active || busy}>
      <div className="studio-fields">
        <label className="studio-field">{t('Analysis mode')}<select value={mode} onChange={e => { setMode(e.target.value as AnalysisOptions['mode']); setImages(false); setConfirmed(false) }}>
		  <option value="subtitle">{t('Text / subtitles')}</option><option value="auto">{t('Smart (subtitles first)')}</option><option value="visual">{t('Vision / sampled frames')}</option>
        </select></label>
        <label className="studio-field">{t('Target seconds')}<input type="number" min={10} max={120} value={duration} onChange={e => setDuration(Number(e.target.value))} /></label>
        <label className="studio-field">{t('Aspect')}<select value={aspect} onChange={e => setAspect(e.target.value as Draft['aspect'])}><option value="original">{t('Original')}</option><option value="portrait">9:16</option><option value="landscape">16:9</option></select></label>
        <label className="studio-field">{t('Content type')}<select value={category} onChange={e => setCategory(e.target.value)}>
          <option value="">{t('General')}</option>
          {categories.map(c => <option key={c.value} value={c.value}>{t(c.label)}</option>)}
        </select></label>
      </div>
      <p className="studio-muted">{t('Content type tunes how topics are split and where clips are cut. General works for anything; pick a closer match for better clips.')}</p>
      <div className="studio-field studio-field--group"><span aria-hidden>{t('Goals')}</span>
        <div className="studio-actions" role="group" aria-label={t('Goals')}>{(['content', 'highlight', 'promo'] as const).map(goal => <label key={goal}>
          <input type="checkbox" checked={goals.includes(goal)} onChange={e => setGoals(e.target.checked ? [...goals, goal] : goals.filter(g => g !== goal))} /> {t(goal)}
        </label>)}</div>
      </div>
      <label className="studio-field">{t('Instructions (up to 4000 UTF-8 bytes)')}<textarea value={instruction} onChange={e => setInstruction(e.target.value)} /></label>
	  {(mode === 'visual' || mode === 'auto') && <label className="web-consent"><input type="checkbox" checked={images} onChange={e => setImages(e.target.checked)} />
        {t('I allow sampled video images to be uploaded to the saved vision model provider.')}
      </label>}
      {mode === 'visual' && <p className="web-warning">{t('Vision uses sampled images only, not audio or the full transcript. For lectures, interviews, or talking-head videos, use Text / subtitles to find highlights in the spoken content. No highlights may be found from images alone.')}</p>}
      {mode === 'subtitle' && <p className="studio-muted">{t('Subtitle text may be sent to the saved text model. No video images will be sent.')}</p>}
      <div className="studio-import-submit"><Btn variant="cta" disabled={!valid} onClick={() => { setConfirmed(false); setError(''); setOpen(true) }}>{t('Review analysis…')}</Btn></div>
    </fieldset>
    <Dialog open={open} onClose={() => !busy && setOpen(false)} title={t('Confirm analysis')}
      description={t('This starts paid cloud analysis using saved model settings. All users share projects and costs.')}
      footer={<div className="studio-actions"><Btn disabled={busy} onClick={() => setOpen(false)}>{t('Cancel')}</Btn><Btn variant="cta" loading={busy} disabled={!confirmed || !valid} onClick={start}>{t('Start confirmed analysis')}</Btn></div>}>
      <p className="ac-context">{t(mode === 'visual' ? 'Vision / sampled frames' : 'Text / subtitles')} · {goals.map(g => t(g)).join(', ')} · {duration}s · {aspect}</p>
      <p>{t(mode === 'visual' ? 'Sampled images will leave this server.' : 'No images will be uploaded.')}</p>
      <label className="web-consent"><input type="checkbox" checked={confirmed} onChange={e => setConfirmed(e.target.checked)} /> {t('I understand and approve possible additional charges.')}</label>
      {error && <p className="studio-error" role="alert">{error}</p>}
    </Dialog>
  </Section>
}
