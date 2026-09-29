import type { Draft } from './types'
import comicThumbnail from '../../assets/title-presets/comic.webp'
import neonThumbnail from '../../assets/title-presets/neon.webp'
import arenaThumbnail from '../../assets/title-presets/arena.webp'
import pixelThumbnail from '../../assets/title-presets/pixel.webp'
import editorialThumbnail from '../../assets/title-presets/editorial.webp'
import frostedThumbnail from '../../assets/title-presets/frosted.webp'

export const titlePresets = [
  {value:'comic',label:'漫画冲击'}, {value:'neon',label:'荧光挑战'},
  {value:'arena',label:'竞技斜切'}, {value:'pixel',label:'像素街机'},
  {value:'editorial',label:'极简大字'}, {value:'frosted',label:'磨砂字幕卡'},
  {value:'impact',label:'高能大字'}, {value:'card',label:'简洁标题卡'},
  {value:'plain',label:'基础文字'},
] as const

export function isArtworkStyle(style: Draft['title_style'] | undefined) {
  return ['comic','neon','arena','editorial','pixel','frosted'].includes(style || '')
}

export function titleVersions(style: Draft['title_style']) {
  // The Go renderer ports the current design only, not six historical engines.
  return [{value:isArtworkStyle(style)?6:1,label:'Go 当前版'}]
}

// Design references help users identify the look; TitleArtwork renders their actual text.
export const titleDesignThumbnails: Partial<Record<NonNullable<Draft['title_style']>, string>> = {
  comic: comicThumbnail,
  neon: neonThumbnail,
  arena: arenaThumbnail,
  pixel: pixelThumbnail,
  editorial: editorialThumbnail,
  frosted: frostedThumbnail,
}
