import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { Scene } from './types'

/** Plays the edit decision list in order, including repeated/overlapping ranges.
 * Source mode is deliberately unrestricted so users can choose new cut points.
 */
export default function DraftPlayer({ src, scenes, sourceMode = false, selected = 0, muted = false, style, children, onPoint, onError }: {
  src: string; scenes: Scene[]; sourceMode?: boolean; selected?: number; muted?: boolean
  style?: CSSProperties; children?: ReactNode; onPoint?: (time: number) => void; onError?: () => void
}) {
  const { t } = useTranslation()
  const video = useRef<HTMLVideoElement>(null)
  const index = useRef(0)
  const finished = useRef(false)
  const [error, setError] = useState('')
  const [position, setPosition] = useState(0)
  const [activeScene, setActiveScene] = useState(0)
  const total = scenes.reduce((sum, scene) => sum + scene.end - scene.start, 0)
  const ranges = JSON.stringify(scenes.map(s => [s.start, s.end]))
  const reset = () => {
    index.current = sourceMode ? Math.min(selected, scenes.length - 1) : 0
    setActiveScene(index.current); setPosition(0)
    finished.current = false
    if (video.current) video.current.currentTime = scenes[index.current]?.start ?? 0
  }
  useEffect(() => { reset() }, [src, sourceMode, selected, ranges])
  const advance = () => {
    const v = video.current
    const scene = scenes[index.current]
    if (!v || sourceMode || !scene || finished.current) return
    if (v.currentTime >= scene.end - .001 || v.ended) {
      const next = scenes[index.current + 1]
      if (next) {
        const needsResume = v.ended
        index.current += 1
        setActiveScene(index.current)
        v.currentTime = next.start
        if (needsResume) void v.play().catch(cause => setError(String(cause)))
      } else { finished.current = true; v.pause(); v.currentTime = scene.end }
    } else if (v.currentTime < scene.start) v.currentTime = scene.start
    const current = scenes[index.current]
    setPosition(scenes.slice(0, index.current).reduce((sum, s) => sum + s.end - s.start, 0) +
      Math.max(0, Math.min(current.end - current.start, v.currentTime - current.start)))
  }
  return <>
    <div className="studio-playback-viewport">
    <video ref={video} aria-label={t(sourceMode ? 'Source playback' : 'Full draft playback')} controls preload="metadata" src={src} muted={muted} style={style}
      onLoadedMetadata={reset} onLoadedData={() => setError('')} onTimeUpdate={advance} onEnded={advance}
      onPlay={() => { if (finished.current) reset() }}
      onError={() => { setError(t('Playback unavailable. Try a compatible preview.')); onError?.() }} />
    {activeScene === 0 && position < 4 && children}
    </div>
    {!sourceMode && <label className="studio-draft-timeline">{t('Draft position (seconds)')} · {position.toFixed(1)} / {total.toFixed(1)}
      <input type="range" min={0} max={Math.max(0, total)} step=".01" value={position} onChange={e => {
        let remaining = Number(e.target.value)
        for (let i = 0; i < scenes.length; i++) {
          const length = scenes[i].end - scenes[i].start
          if (remaining < length || i === scenes.length - 1) {
            index.current = i; setActiveScene(i); finished.current = false
            if (video.current) video.current.currentTime = scenes[i].start + Math.min(length, remaining)
            setPosition(Number(e.target.value)); break
          }
          remaining -= length
        }
      }} />
    </label>}
    {sourceMode && onPoint && <button type="button" className="ac-btn ac-btn--sm studio-source-point" onClick={() => onPoint(video.current?.currentTime ?? 0)}>{t('Use current source time')}</button>}
    {error && <p role="alert" className="studio-error">{error}</p>}
  </>
}
