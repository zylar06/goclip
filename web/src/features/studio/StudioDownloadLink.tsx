import { useTranslation } from 'react-i18next'
import { studioApi } from './api'

/** Native navigation streams the file without buffering a multi-GB export in JS. */
export default function StudioDownloadLink({ projectId, jobId, className = 'studio-link' }: {
  projectId: string; jobId: string; className?: string
}) {
  const { t } = useTranslation()
  return <a className={className} href={studioApi.video(projectId, jobId, true)} download>{t('下载成片')}</a>
}
