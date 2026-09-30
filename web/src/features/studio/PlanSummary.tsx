import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, errorText } from '../../api/client'
import type { PlanOptions, ProductionPlan, Project } from '../../api/contracts'
import { Btn, Section } from '../../ui'
import { highlightScenarios } from './highlightScenarios'

export const defaultPlanOptions: PlanOptions = {
	mode: 'auto', goals: [], duration: 0, aspect: 'original', category: '', instruction: '',
  allow_visual: false, confirmed: false, burn_subtitles: false,
}
type PlanSummaryProps = {
  project: Project; active: boolean; onChanged: () => void; onStarted?: () => void; initialInstruction?: string
}
export default function PlanSummary(props: PlanSummaryProps) {
  return <PlanForm key={props.project.id} {...props} />
}
function PlanForm({ project, active, onChanged, onStarted, initialInstruction = '' }: PlanSummaryProps) {
  const { t } = useTranslation()
  const [plan, setPlan] = useState<ProductionPlan | null>(null)
  const [options, setOptions] = useState<PlanOptions>(defaultPlanOptions)
  const [autoExport, setAutoExport] = useState(false)
  const [paid, setPaid] = useState(false)
  const [images, setImages] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [version, setVersion] = useState(0)
  const [loading, setLoading] = useState(true)
  const [stale, setStale] = useState(false)
  const [pendingRevision, setPendingRevision] = useState<number | null>(null)
  const [newRound, setNewRound] = useState(false)
  const [scenarioId, setScenarioId] = useState('general')
  const dirty = useRef(false)
  const saving = useRef(false)
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    api.plan(project.id, controller.signal).then(value => {
      if (controller.signal.aborted) return
      setPlan(value); setOptions({ ...defaultPlanOptions, ...value.options, goals: value.options.goals ?? [],
        instruction: value.options.instruction || initialInstruction })
      dirty.current = !value.options.instruction && !!initialInstruction
      setAutoExport(value.auto_export); setPaid(false); setImages(false); setError(''); setStale(false)
      setNewRound(false)
      setScenarioId('general')
    }).catch(cause => { if (!controller.signal.aborted) setError(errorText(cause)) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [project.id, version])
  useEffect(() => {
    const remote = project.plan
    if (!plan || !remote || loading || busy || pendingRevision !== null) return
    // Ignore a workspace response older than our own successful save.
    if (remote.revision < plan.revision || (remote.revision === plan.revision &&
      (remote.status === plan.status || plan.status === 'confirmed'))) return
    setPaid(false); setImages(false)
    if (dirty.current) {
      setStale(true)
      setError(t('The saved plan changed. Your edits are retained; reload the saved plan before continuing.'))
    } else {
      setVersion(v => v + 1)
    }
  }, [project.plan?.revision, project.plan?.status, plan, loading, busy, pendingRevision, t])
  const patch = (value: Partial<PlanOptions>) => { dirty.current = true; setOptions(o => ({ ...o, ...value })); setPaid(false); setNotice('') }
  const incompatible = options.mode === 'visual' && options.goals.length === 1 && options.goals[0] === 'content'
  const mixed = options.mode === 'visual' && options.goals.includes('content')
  const locked = pendingRevision !== null || (plan?.status === 'confirmed' && !newRound)
  const valid = !incompatible && options.goals.length > 0 && Number.isInteger(options.duration) &&
    (options.duration === 0 || (options.duration >= 10 && options.duration <= 120)) &&
    new TextEncoder().encode(options.instruction).length <= 4000
    const consent = paid && (!['visual', 'fused'].includes(options.mode) || images)
  const save = async (start: boolean) => {
    if (!plan || !valid || saving.current || loading || active || stale ||
      (locked && !(start && pendingRevision !== null)) || (start && !consent)) return
    saving.current = true
    setBusy(true); setError(''); setNotice('')
    // Consume this authorization even if the response is lost. It is never a reusable grant.
    if (start) { setPaid(false); setImages(false) }
    try {
      let saved = plan
		const allowVisual = (options.mode === 'visual' || options.mode === 'auto' || options.mode === 'fused') && images
      // An unchanged persisted plan (including after refresh) can be confirmed directly.
      // In particular, an unknown confirmation MUST NOT be preceded by another PUT.
      if (pendingRevision === null && (!start || dirty.current || newRound || !!plan.options.allow_visual !== allowVisual)) {
        saved = await api.savePlan(project.id, plan.revision, {
          ...options, allow_visual: allowVisual, confirmed: false,
        }, autoExport)
        dirty.current = false
        setPlan(saved); setOptions({ ...defaultPlanOptions, ...saved.options }); setNotice(t('Plan saved.'))
      }
      if (start) {
        const revision = pendingRevision ?? saved.revision
        setPendingRevision(revision)
        await api.confirm(project.id, revision)
        setPendingRevision(null); setNewRound(false); setPlan({ ...saved, status: 'confirmed' })
        onStarted?.()
      }
      onChanged()
    } catch (cause) { setError(errorText(cause)); onChanged() } finally { saving.current = false; setBusy(false) }
  }
  if (plan?.status === 'confirmed' && !newRound && pendingRevision === null) return <Section title={t('Production plan')}>
    <p role="status">{t(active ? 'Production is in progress. Saved choices are retained; follow the tasks below.'
      : 'This plan was already confirmed. Check its production results or explicitly prepare another round.')}</p>
    <p className="studio-muted">{options.goals.map(goal => t(goal)).join(' / ')}</p>
    <Btn disabled={active || busy || loading} onClick={() => { setNewRound(true); setPaid(false); setImages(false); setNotice('') }}>{t('Prepare another production round')}</Btn>
    {error && <p role="alert" className="studio-error">{error}</p>}
  </Section>
  return <Section title={t('Production plan')}>
    <details className="studio-details web-plan-evidence"><summary>{t('Local evidence')}</summary>
    <p>{t('Local evidence')}: {plan?.reason || t('Loading plan…')}</p>
    <p className="studio-muted">{t('Subtitle source')}: {t(project.subtitle_source || 'Unknown')} · {t(project.subtitle_status || 'Unknown')}</p>
    </details>
    <p className="web-note">{t(mixed ? 'Content uses subtitles even in a mixed visual plan. Missing subtitles will be transcribed only after confirmation.'
      : options.mode === 'visual' ? 'Visual mode does not require transcription. Images do not reveal speech.'
      : project.subtitle_status === 'available' ? 'Existing subtitles will be used; transcription is not needed.'
        : project.has_audio === false ? 'No audio or usable subtitles. Choose visual analysis or manual editing.'
          : 'Transcription is needed if usable subtitles are absent; it starts only after confirmation.')}</p>
    {active && <p role="status">{t('Production is in progress. Saved choices are retained; follow the tasks below.')}</p>}
    {pendingRevision !== null && <p role="status">{t('Confirmation outcome is unknown. Reconfirm only this saved revision; no new production round will be created.')}</p>}
    <fieldset className="studio-fieldset" disabled={busy || active || loading || !plan}>
      <fieldset className="studio-fieldset" disabled={locked}>
      <label className="studio-field">高光场景
        <select value={scenarioId} onChange={e => { const scenario = highlightScenarios.find(item => item.id === e.target.value) || highlightScenarios[0]; setScenarioId(scenario.id); patch({ instruction: scenario.rules }) }}>
          {highlightScenarios.map(scenario => <option key={scenario.id} value={scenario.id}>{scenario.label}</option>)}
        </select>
        <small className="studio-muted">{highlightScenarios.find(item => item.id === scenarioId)?.description}</small>
      </label>
      <div className="studio-actions" role="group" aria-label={t('Goals')}>{(options.mode === 'fused' ? ['highlight'] as const : ['content', 'highlight', 'promo'] as const).map(goal =>
        <label key={goal}><input type="checkbox" checked={options.goals.includes(goal)} onChange={e => patch({ goals: e.target.checked ? [...options.goals, goal] : options.goals.filter(g => g !== goal) })} />{t(goal)} {plan?.suggested_goals?.includes(goal) && <small>{t('Suggested')}</small>}</label>)}</div>
      <div className="studio-fields web-plan-fields">
		<label className="studio-field">{t('Analysis mode')}<select value={options.mode} onChange={e => { patch({ mode: e.target.value as PlanOptions['mode'], ...(e.target.value === 'fused' ? { goals: ['highlight'] } : {}) }); setImages(false) }}><option value="fused">高光融合（字幕 + 画面）</option><option value="subtitle">{t('Text / subtitles')}</option><option value="auto">{t('Smart (subtitles first)')}</option><option value="visual">{t('Vision / sampled frames')}</option></select></label>
        <label className="studio-field">{t('Target seconds (0 = automatic)')}<input type="number" min={0} max={120} step={1} value={options.duration} onChange={e => patch({ duration: Number(e.target.value) })} /></label>
        <label className="studio-field">{t('Aspect')}<select value={options.aspect} onChange={e => patch({ aspect: e.target.value as PlanOptions['aspect'] })}><option value="original">{t('Original')}</option><option value="portrait">9:16</option><option value="landscape">16:9</option></select></label>
        <label className="studio-field">{t('Content type')}<select value={options.category || ''} onChange={e => patch({ category: e.target.value })}><option value="">{t('General')}</option>{['knowledge', 'speech', 'opinion', 'experience', 'business', 'entertainment', 'content_review'].map(c => <option key={c} value={c}>{t(c)}</option>)}</select></label>
      </div>
      {incompatible && <p role="alert">{t('Content clips require subtitle analysis. Choose Text / subtitles, or select a visual highlight or promo goal.')}</p>}
      <p className="studio-muted">{t('0 preserves complete meaning automatically. 10–120 seconds is a target, not a forced cut.')}</p>
      <label className="studio-field">{t('Instructions (up to 4000 UTF-8 bytes)')}<textarea value={options.instruction} onChange={e => patch({ instruction: e.target.value })} /></label>
      <label className="web-consent"><input type="checkbox" checked={options.burn_subtitles ?? false} onChange={e => patch({ burn_subtitles: e.target.checked })} />{t('Add a new subtitle layer (off by default)')}</label>
      <p className="studio-muted">{t('Existing on-screen captions cannot be removed. This choice does not change old drafts.')}</p>
      <label className="web-consent"><input type="checkbox" checked={autoExport} onChange={e => { dirty.current = true; setAutoExport(e.target.checked); setPaid(false) }} />{t('One-click MP4 for highlight / promo')}</label>
      <p className="web-note">{t('Content clips always export MP4 automatically. Other goals create editable drafts unless one-click MP4 is enabled.')}</p>
      </fieldset>
	  {(options.mode === 'visual' || options.mode === 'auto' || options.mode === 'fused') && <label className="web-consent"><input type="checkbox" checked={images} onChange={e => setImages(e.target.checked)} />{t('I allow sampled video images to be uploaded to the saved vision model provider.')}</label>}
	      <p className="studio-muted">{t(options.mode === 'fused' ? '高光融合会同时分析字幕和抽样画面，再合并重叠证据。没有字幕时仍可使用视觉证据。'
        : mixed ? 'This mixed plan sends subtitle text to the saved text model for content, and sampled images to the saved vision model for visual goals. Both paths may incur charges; images do not reveal speech.'
        : options.mode === 'visual' ? 'Vision uses sampled images only, not audio or the full transcript. For lectures, interviews, or talking-head videos, use Text / subtitles to find highlights in the spoken content. No highlights may be found from images alone.' : 'Subtitle text may be sent to the saved text model. No video images will be sent.')}</p>
      <label className="web-consent"><input type="checkbox" checked={paid} onChange={e => setPaid(e.target.checked)} />{t('I understand and approve possible additional charges.')}</label>
      <div className="studio-actions"><Btn disabled={!valid || stale || locked} onClick={() => save(false)}>{t('Save plan only')}</Btn><Btn variant="cta" disabled={!valid || stale || !consent || (locked && pendingRevision === null)} onClick={() => save(true)}>{t(pendingRevision !== null ? 'Reconfirm saved revision' : 'Confirm and start production')}</Btn>
      </div>
    </fieldset>
    <p className="studio-muted">{t('模型失败不删除原素材或已有成片。瞬时网络或服务错误最多尝试三次，可能产生额外费用；鉴权、配置和响应结构错误不自动重试。')}</p>
    {notice && <p role="status">{notice}</p>}
    {error && <p className="studio-error" role="alert">{error} {pendingRevision === null && <Btn size="sm" disabled={busy || loading} onClick={() => setVersion(v => v + 1)}>{t('Reload saved plan')}</Btn>}</p>}
  </Section>
}
