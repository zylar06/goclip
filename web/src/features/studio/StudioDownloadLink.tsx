import { useTranslation } from 'react-i18next'
import { Icon } from '../../ui'
import { studioApi } from './api'

/** Native navigation streams the file without buffering a multi-GB export in JS. */
export default function StudioDownloadLink({ projectId, jobId, className = 'studio-download' }: {
  projectId: string; jobId: string; className?: string
}) {
  const { t } = useTranslation()
  // The icon is aria-hidden, so the accessible name stays exactly the label text.
  return <a className={className} href={studioApi.video(projectId, jobId, true)} download><Icon.Down size={15} />{t('下载成片')}</a>
}
