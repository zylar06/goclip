import { useState } from 'react'
import { NavLink, Outlet, useRouteError } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { languages, readPreference, setLanguage, t } from './i18n'
import type { LanguagePreference } from './i18n/language'
import { errorText } from './api/client'
import { Btn } from './ui'

export default function App() {
  const { t } = useTranslation()
  const [language, setPreference] = useState(readPreference)
  const [error, setError] = useState('')
  return <>
    <header className="web-header"><div className="web-header-inner"><NavLink to="/" className="web-brand">AutoClip<span>WEB</span></NavLink>
      <nav aria-label={t('Main navigation')}><NavLink to="/" end>{t('Projects')}</NavLink><NavLink to="/settings">{t('Settings')}</NavLink></nav>
      <label><span className="studio-sr">{t('Interface language')}</span><select aria-label={t('Interface language')} value={language} onChange={async e => {
        const value = e.target.value as LanguagePreference
        try { await setLanguage(value); setPreference(value); setError('') } catch (cause) { setError(errorText(cause)) }
      }}><option value="system">{t('System language')}</option>{languages.map(l => <option key={l.value} value={l.value}>{l.label}</option>)}</select></label>
    </div></header>
    {error && <p role="alert" className="studio-error web-shell-alert">{error}</p>}
    <div className="web-shell-body"><Outlet /></div>
    <footer className="web-footer">AutoClip {__APP_VERSION__} · {t('Trusted LAN / VPN only. Shared projects and cloud costs.')}</footer>
  </>
}
export function RouteError() {
  const error = useRouteError()
  console.error('Page rendering failed', error)
  return <main className="ac-page ac-page--narrow"><h1 className="ac-title">{t('页面无法显示')}</h1>
    <p role="alert" className="studio-error">{errorText(error)}</p>
    <Btn variant="cta" onClick={() => window.location.reload()}>{t('重新加载页面')}</Btn>
  </main>
}
/** Hash router fallback route. A component so the label follows language changes. */
export function NotFound() {
  const { t } = useTranslation()
  return <main className="ac-page ac-page--narrow"><h1 className="ac-title">{t('页面不存在')}</h1>
    <p className="ac-sub">{t('该地址没有对应的页面，可能项目已被删除。')}</p>
    <a className="ac-btn ac-btn--cta" href="#/">{t('Back to projects')}</a>
  </main>
}
