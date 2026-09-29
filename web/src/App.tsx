import { useState } from 'react'
import { NavLink, Outlet, useRouteError } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { languages, readPreference, setLanguage } from './i18n'
import type { LanguagePreference } from './i18n/language'
import { errorText } from './api/client'
import { Btn } from './ui'

export default function App() {
  const { t } = useTranslation()
  const [language, setPreference] = useState(readPreference)
  const [error, setError] = useState('')
  return <>
    <header className="web-header"><NavLink to="/" className="web-brand">AutoClip<span>WEB</span></NavLink>
      <nav aria-label={t('Main navigation')}><NavLink to="/" end>{t('Projects')}</NavLink><NavLink to="/settings">{t('Settings')}</NavLink></nav>
      <label><span className="studio-sr">{t('Interface language')}</span><select aria-label={t('Interface language')} value={language} onChange={async e => {
        const value = e.target.value as LanguagePreference
        try { await setLanguage(value); setPreference(value); setError('') } catch (cause) { setError(errorText(cause)) }
      }}><option value="system">{t('System language')}</option>{languages.map(l => <option key={l.value} value={l.value}>{l.label}</option>)}</select></label>
    </header>
    {error && <p role="alert" className="studio-error">{error}</p>}
    <Outlet />
    <footer className="web-footer">AutoClip {__APP_VERSION__} · {t('Trusted LAN / VPN only. Shared projects and cloud costs.')}</footer>
  </>
}
export function RouteError() {
  const error = useRouteError()
  console.error('Page rendering failed', error)
  return <main className="ac-page"><h1>Page unavailable</h1><p role="alert">{errorText(error)}</p><Btn onClick={() => window.location.reload()}>Reload page</Btn></main>
}
