import { describe, expect, it } from 'vitest'
import { draft } from './fixtures'
import { applyCandidate, draftError, moveScene, newID, portraitDesign } from '../src/features/studio/types'
import { validateSourceURL } from '../src/pages/HomePage'
import { resolveLanguage } from '../src/i18n/language'

describe('preserved editing behavior', () => {
  it('moves and replaces scenes without mutating originals', () => {
    const second = { ...draft.scenes[0], id: 'second', start: 10, end: 15 }
    const source = { ...draft, scenes: [...draft.scenes, second] }
    expect(moveScene(source, 0, 1).scenes[0]).toEqual(second)
    expect(source.scenes[0].id).toBe('scene1')
    const replaced = applyCandidate(source, { ...second, score: .8, kind: 'visual' }, 0, 'newscene')
    expect(replaced.scenes[0]).toEqual({ ...second, id: 'newscene' })
    expect(replaced.scenes[0]).not.toHaveProperty('score')
  })
  it('enforces source bounds, duplicates, maximum duration and title placement', () => {
    expect(draftError(draft, 120)).toBeNull()
    expect(draftError(draft, 2)).toContain('source duration')
    expect(draftError({ ...draft, scenes: [draft.scenes[0], draft.scenes[0]] })).toContain('unique')
    expect(draftError({ ...draft, scenes: [{ ...draft.scenes[0], end: 1900 }] })).toContain('30 minutes')
    expect(draftError({ ...draft, title_scale: Number.NaN })).toContain('placement')
    expect(draftError({ ...draft, scenes: [{ ...draft.scenes[0], end: 1.01 }] })).toContain('0.1')
    expect(() => applyCandidate(draft, { ...draft.scenes[0], kind: 'visual', score: 1 }, 10, 'x')).toThrow('no longer exists')
  })
  it('offers portrait crop and secure-context-independent IDs', () => {
    expect(portraitDesign(draft)).toMatchObject({ aspect: 'portrait', layout: 'crop', title_style: 'comic' })
    expect(newID()).toMatch(/^[a-f0-9]{32}$/)
  })
})
describe('web import/language selection', () => {
  it('accepts supported videos and rejects unsafe hosts, credentials, redirects and non-video links', () => {
    expect(validateSourceURL('https://youtu.be/abcdefghijk')).toContain('abcdefghijk')
    // Bilibili's share sheet emits b23.tv links; the shape is accepted here and
    // the server resolves the redirect, so pasting one no longer fails outright.
    expect(validateSourceURL('https://b23.tv/abcdefg')).toBe('https://b23.tv/abcdefg')
    expect(validateSourceURL('https://b23.tv/abcdefg/')).toBe('https://b23.tv/abcdefg')
    // Tracking query is dropped from a full Bilibili URL.
    expect(validateSourceURL('https://www.bilibili.com/video/BV1P5h16JE8n/?spm_id_from=333.1391.0.0&vd_source=x'))
      .toBe('https://www.bilibili.com/video/BV1P5h16JE8n')
    for (const url of ['http://youtube.com/watch?v=abcdefghijk', 'https://youtube.com.evil.test/watch?v=abcdefghijk',
      'https://user:pass@youtube.com/watch?v=abcdefghijk', 'https://127.0.0.1/a', 'https://b23.tv/abc', 'https://youtube.com/playlist?list=x',
      'https://b23.tv/', 'https://b23.tv/a/b', 'https://b23.tv/abcdefg?x=1']) {
      expect(() => validateSourceURL(url)).toThrow()
    }
  })
  it('retains upstream supported-language resolution', () => {
    expect(resolveLanguage('system', ['pt-BR', 'en'])).toBe('pt')
    expect(resolveLanguage('ja', ['en-US'])).toBe('ja')
    expect(resolveLanguage('system', ['xx'])).toBe('en')
  })
})
