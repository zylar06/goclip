import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, errorText } from '../api/client'
import type { AnalysisOptions, Draft, Language } from '../api/contracts'
import { languages } from '../features/studio/types'
import { Btn, Dialog, Section } from '../ui'

export default function AnalysisPanel({ projectId, ready, active, onStarted }: {
  projectId: string; ready: boolean; active: boolean; onStarted: () => void
}) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<AnalysisOptions['mode']>('subtitle')
  const [images, setImages] = useState(false)
  const [goals, setGoals] = useState<AnalysisOptions['goals']>(['content'])
  const [duration, setDuration] = useState(30)
  const [aspect, setAspect] = useState<Draft['aspect']>('original')
  const [language, setLanguage] = useState<Language>('source')
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
      await api.analyze(projectId, { mode, allow_visual: mode === 'visual' && images, confirmed: true, goals, duration, aspect, language, instruction })
      setOpen(false); setConfirmed(false); setImages(false); onStarted()
    } catch (cause) { setError(errorText(cause)) }
    finally { setBusy(false) }
  }
  return <Section title={t('Analyze source')} description={t('Import never starts analysis. Choose a mode and confirm the cloud cost before each analysis.')}>
    {!ready && <p role="status">{t('Wait for source import to complete.')}</p>}
    <fieldset className="studio-fieldset" disabled={!ready || active || busy}>
      <div className="studio-fields">
        <label className="studio-field">{t('Analysis mode')}<select value={mode} onChange={e => { setMode(e.target.value as AnalysisOptions['mode']); setImages(false); setConfirmed(false) }}>
          <option value="subtitle">{t('Text / subtitles')}</option><option value="visual">{t('Vision / sampled frames')}</option>
        </select></label>
        <label className="studio-field">{t('Target seconds')}<input type="number" min={10} max={120} value={duration} onChange={e => setDuration(Number(e.target.value))} /></label>
        <label className="studio-field">{t('Output language')}<select value={language} onChange={e => setLanguage(e.target.value as Language)}>{languages.map(l => <option key={l.value} value={l.value}>{t(l.label)}</option>)}</select></label>
        <label className="studio-field">{t('Aspect')}<select value={aspect} onChange={e => setAspect(e.target.value as Draft['aspect'])}><option value="original">{t('Original')}</option><option value="portrait">9:16</option><option value="landscape">16:9</option></select></label>
      </div>
      <div className="studio-actions" role="group" aria-label={t('Goals')}>{(['content', 'highlight', 'promo'] as const).map(goal => <label key={goal}>
        <input type="checkbox" checked={goals.includes(goal)} onChange={e => setGoals(e.target.checked ? [...goals, goal] : goals.filter(g => g !== goal))} /> {t(goal)}
      </label>)}</div>
      <label className="studio-field">{t('Instructions (up to 4000 UTF-8 bytes)')}<textarea value={instruction} onChange={e => setInstruction(e.target.value)} /></label>
      {mode === 'visual' && <label className="web-consent"><input type="checkbox" checked={images} onChange={e => setImages(e.target.checked)} />
        {t('I allow sampled video images to be uploaded to the saved vision model provider.')}
      </label>}
      {mode === 'subtitle' && <p className="studio-muted">{t('Subtitle text may be sent to the saved text model. No video images will be sent.')}</p>}
      <Btn variant="cta" disabled={!valid} onClick={() => { setConfirmed(false); setError(''); setOpen(true) }}>{t('Review analysis…')}</Btn>
    </fieldset>
    <Dialog open={open} onClose={() => !busy && setOpen(false)} title={t('Confirm analysis')}
      description={t('This starts paid cloud analysis using saved model settings. All users share projects and costs.')}
      footer={<div className="studio-actions"><Btn disabled={busy} onClick={() => setOpen(false)}>{t('Cancel')}</Btn><Btn variant="cta" loading={busy} disabled={!confirmed || !valid} onClick={start}>{t('Start confirmed analysis')}</Btn></div>}>
      <p>{t(mode === 'visual' ? 'Vision / sampled frames' : 'Text / subtitles')} · {goals.map(g => t(g)).join(', ')} · {duration}s · {aspect} · {language}</p>
      <p>{t(mode === 'visual' ? 'Sampled images will leave this server.' : 'No images will be uploaded.')}</p>
      <label className="web-consent"><input type="checkbox" checked={confirmed} onChange={e => setConfirmed(e.target.checked)} /> {t('I understand and approve possible additional charges.')}</label>
      {error && <p className="studio-error" role="alert">{error}</p>}
    </Dialog>
  </Section>
}
