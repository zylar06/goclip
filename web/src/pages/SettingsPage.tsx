import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, errorText } from '../api/client'
import type { ModelKind, ModelStatus, Settings } from '../api/contracts'
import { Btn, Dialog, Section } from '../ui'

export function ModelForm({ kind, initial }: { kind: ModelKind; initial: ModelStatus }) {
  const { t } = useTranslation()
  const [saved, setSaved] = useState(initial)
  const [baseURL, setBaseURL] = useState(initial.base_url)
  const [model, setModel] = useState(initial.model)
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const dirty = baseURL !== saved.base_url || model !== saved.model || key !== ''
  const run = async (operation: 'save' | 'test') => {
    setBusy(operation); setError(''); setNotice('')
    try {
      if (operation === 'save') {
        const result = await api.saveModel(kind, { base_url: baseURL.trim(), model: model.trim(), api_key: key })
        setSaved(result); setBaseURL(result.base_url); setModel(result.model); setKey('')
        setNotice('Settings saved. The API key is not returned to this browser.')
      } else {
        const result = await api.testModel(kind)
        if (!result.ok) throw new Error(result.message || 'Model test failed.')
        setNotice(result.message)
      }
    } catch (cause) { setError(errorText(cause)) } finally { setBusy('') }
  }
  const label = kind === 'text' ? 'Text' : 'Vision'
  return <Section title={t(`${label} model`)} description={t(kind === 'text' ? 'Used for subtitle analysis, rewriting, and translation.' : 'Used only when you explicitly approve sampled image analysis.')}>
    <form onSubmit={e => { e.preventDefault(); void run('save') }}>
      <fieldset disabled={!!busy} className="studio-fieldset" aria-label={t(`${label} model settings`)}>
        <label className="studio-field">{t(`${label} base URL`)}<input type="url" required value={baseURL} onChange={e => setBaseURL(e.target.value)} placeholder="https://provider.example/v1" /></label>
        <label className="studio-field">{t(`${label} model name`)}<input required value={model} onChange={e => setModel(e.target.value)} /></label>
        <label className="studio-field">{t(`${label} API key`)}<input type="password" value={key} onChange={e => setKey(e.target.value)} autoComplete="new-password" placeholder={t(saved.configured ? 'Leave empty to preserve the saved key' : 'Enter API key')} /></label>
        <p className="studio-muted">{t(saved.configured ? 'A key is configured on the server.' : 'No saved key configured.')} {t('An empty key preserves the existing key; it never clears it.')}</p>
        <p className="studio-muted">{t('Tests call the saved model and may incur a small charge. Unsaved form values are never sent by Test.')}</p>
        {dirty && <p role="status" className="web-warning">{t('Unsaved changes. Save first to test these values.')}</p>}
        <div className="studio-actions studio-import-submit">
          <Btn variant="cta" type="submit" loading={busy === 'save'} disabled={!baseURL.trim() || !model.trim()}>{t(`Save ${kind} settings`)}</Btn>
          <Btn disabled={!saved.configured} loading={busy === 'test'} onClick={() => run('test')}>{t(`Test saved ${kind} settings`)}</Btn>
        </div>
      </fieldset>
    </form>
    {error && <p role="alert" className="studio-error">{error}</p>}
    {notice && <p role="status" className="web-note">{t(notice)}</p>}
  </Section>
}
export function CookiesForm({ initial }: { initial: boolean }) {
  const { t } = useTranslation()
  const [configured, setConfigured] = useState(initial)
  const [file, setFile] = useState<File | null>(null)
  const [inputVersion, setInputVersion] = useState(0)
  const [removing, setRemoving] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const upload = async () => {
    if (!file) return
    setBusy(true); setError(''); setNotice('')
    try {
      if (file.size > 1024 ** 2 || !file.size) throw new Error('Cookies file must be non-empty and at most 1 MiB.')
      const text = await file.text()
      if (!text.includes('Netscape HTTP Cookie File')) throw new Error('Upload a Netscape cookies.txt file.')
      await api.saveCookies(text)
      setFile(null); setInputVersion(v => v + 1); setConfigured(true); setNotice('Cookies uploaded.')
    } catch (cause) { setError(errorText(cause)) } finally { setBusy(false) }
  }
  const remove = async () => {
    setBusy(true); setError(''); setNotice('')
    try { await api.removeCookies(); setConfigured(false); setRemoving(false); setNotice('Cookies removed.') }
    catch (cause) { setError(errorText(cause)) } finally { setBusy(false) }
  }
  return <Section title={t('Import cookies')} description={t('Optional Netscape cookies.txt for restricted Bilibili / YouTube imports. Cookies are sensitive and shared by all server users; never upload someone else’s credentials.')}>
    <p><span className={`web-pill${configured ? ' web-pill--ok' : ''}`}>{t(configured ? 'Cookies configured' : 'No cookies configured')}</span></p>
    <label className="studio-field">{t('Netscape cookies file')}<input key={inputVersion} disabled={busy} type="file" accept=".txt" onChange={e => setFile(e.target.files?.[0] ?? null)} /></label>
    <div className="studio-actions"><Btn disabled={busy || !file} loading={busy && !removing} onClick={upload}>{t('Upload cookies')}</Btn><Btn variant="danger" disabled={busy || !configured} onClick={() => { setError(''); setRemoving(true) }}>{t('Remove cookies…')}</Btn></div>
    {notice && <p role="status" className="web-note">{t(notice)}</p>}
    {error && !removing && <p role="alert" className="studio-error">{error}</p>}
    <Dialog open={removing} onClose={() => !busy && setRemoving(false)} title={t('Remove shared cookies?')} description={t('Future imports may need authentication again.')}
      footer={<div className="studio-actions"><Btn disabled={busy} onClick={() => setRemoving(false)}>{t('Keep cookies')}</Btn><Btn variant="danger" loading={busy} onClick={remove}>{t('Confirm removal')}</Btn></div>}>
      {error && <p role="alert" className="studio-error">{error}</p>}
    </Dialog>
  </Section>
}
export default function SettingsPage() {
  const { t } = useTranslation()
  const [settings, setSettings] = useState<Settings | null>(null)
  const [error, setError] = useState('')
  const [version, setVersion] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    api.settings(controller.signal).then(data => { if (!controller.signal.aborted) { setSettings(data); setError('') } })
      .catch(cause => { if (!controller.signal.aborted) setError(errorText(cause)) })
    return () => controller.abort()
  }, [version])
  return <main className="ac-page ac-page--narrow"><h1 className="ac-title">{t('Settings')}</h1>
    <p className="ac-sub">{t('Text and vision settings are saved and tested independently. Keys stay on the server, not in local storage.')}</p>
    {error && <p role="alert" className="studio-error">{error} <Btn size="sm" onClick={() => setVersion(v => v + 1)}>{t('Retry')}</Btn></p>}
    {!settings && !error && <div className="ac-loading" role="status" aria-label={t('Loading settings…')}><span className="studio-sr">{t('Loading settings…')}</span><div className="ac-skeleton" /><div className="ac-skeleton" /></div>}
    {settings && <><ModelForm kind="text" initial={settings.text} /><ModelForm kind="vision" initial={settings.vision} /><CookiesForm initial={settings.cookies_configured} /></>}
  </main>
}
