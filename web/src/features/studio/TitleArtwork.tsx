import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { studioApi, errorText } from './api'
import type { Draft } from './types'

export default function TitleArtwork({ projectId, draft }: { projectId: string; draft: Draft }) {
  const { t } = useTranslation()
  const [image, setImage] = useState('')
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const signature = JSON.stringify(draft)
  useEffect(() => {
    const controller = new AbortController()
    let url = ''
    setImage(''); setError('')
    const timer = setTimeout(() => {
      studioApi.titlePreview(projectId, draft, controller.signal).then(blob => {
        if (controller.signal.aborted) return
        url = URL.createObjectURL(blob)
        setImage(url)
      }).catch(cause => {
        if (!controller.signal.aborted) setError(errorText(cause))
      })
    }, 250)
    return () => { clearTimeout(timer); controller.abort(); if (url) URL.revokeObjectURL(url) }
  }, [projectId, signature, retry])
  return <>
    {image && <img className="studio-title-art" src={image} alt={t('标题排版预览')} onError={() => setError(t('文字预览暂不可用'))} />}
    {error && <div className="studio-title-status" role="alert">{error}<button type="button" onClick={() => setRetry(v => v + 1)}>{t('重试')}</button></div>}
    {!image && !error && <span className="studio-title-status">{t('生成文字预览…')}</span>}
  </>
}
