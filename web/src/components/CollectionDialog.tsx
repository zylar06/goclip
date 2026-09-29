import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, errorText } from '../api/client'
import type { Candidate, Draft, Scene } from '../api/contracts'
import { draftError, newDraft, newID } from '../features/studio/types'
import { Btn, Dialog, fmtDuration } from '../ui'

export function collectionDraft(title: string, scenes: Scene[]): Draft {
  return newDraft(title.trim(), scenes.map(scene => ({ ...scene, id: newID() })), 'manual')
}
export default function CollectionDialog({ projectId, duration, drafts, candidates, onClose, onCreated }: {
  projectId: string; duration: number; drafts: Draft[]; candidates: Candidate[]; onClose: () => void; onCreated: (draft: Draft) => void
}) {
  const { t } = useTranslation()
  const [title, setTitle] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const choices = [
    ...drafts.map(d => ({ key: `draft:${d.id}`, label: d.title, scenes: d.scenes })),
    ...candidates.map(c => ({ key: `candidate:${c.id}`, label: c.label, scenes: [c] })),
  ]
  const sceneList = selected.flatMap(key => choices.find(c => c.key === key)?.scenes ?? [])
  const move = (index: number, delta: number) => {
    const result = [...selected]
    ;[result[index], result[index + delta]] = [result[index + delta], result[index]]
    setSelected(result)
  }
  const create = async () => {
    const draft = collectionDraft(title, sceneList)
    const invalid = draftError(draft, duration)
    if (invalid) { setError(t(invalid)); return }
    setBusy(true); setError('')
    try { onCreated(await api.createDraft(projectId, draft)) }
    catch (cause) { setError(errorText(cause)) }
    finally { setBusy(false) }
  }
  return <Dialog open title={t('Create collection')} onClose={() => !busy && onClose()}
    description={t('Combine scenes from this project into a new editable draft. Sources and exports stay unchanged.')}
    footer={<div className="studio-actions"><Btn disabled={busy} onClick={onClose}>{t('Cancel')}</Btn><Btn variant="cta" loading={busy} disabled={!title.trim() || !sceneList.length || sceneList.length > 30} onClick={create}>{t('Create collection')}</Btn></div>}>
    <fieldset disabled={busy} className="studio-fieldset">
      <label className="studio-field">{t('Collection title')}<input maxLength={200} value={title} onChange={e => setTitle(e.target.value)} /></label>
      <div className="web-choice-list">{choices.map(choice => <label className="web-choice" key={choice.key}><input type="checkbox" checked={selected.includes(choice.key)} onChange={e => setSelected(e.target.checked ? [...selected, choice.key] : selected.filter(key => key !== choice.key))} /> {choice.label}</label>)}</div>
      {!choices.length && <p>{t('Create a draft or analyze the source first.')}</p>}
      <ol>{selected.map((key, index) => <li key={key} className="studio-row">{choices.find(c => c.key === key)?.label}<span className="studio-actions">
        <Btn size="sm" disabled={index === 0} onClick={() => move(index, -1)}>{t('Move up')}</Btn><Btn size="sm" disabled={index === selected.length - 1} onClick={() => move(index, 1)}>{t('Move down')}</Btn>
      </span></li>)}</ol>
      <p>{sceneList.length}/30 {t('scenes')} · {fmtDuration(sceneList.reduce((total, scene) => total + scene.end - scene.start, 0))}</p>
    </fieldset>
    {error && <p className="studio-error" role="alert">{error}</p>}
  </Dialog>
}
