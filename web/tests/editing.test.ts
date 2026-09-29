import { describe, expect, it } from 'vitest'
import { draft } from './fixtures'
import { applyCandidate, applyRewrite, draftError, moveScene, newID, portraitDesign } from '../src/features/studio/types'
import { collectionDraft } from '../src/components/CollectionDialog'
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
  it('rewriting changes text only, preserving timing, identity and revision', () => {
    expect(applyRewrite(draft, { ...draft, title: 'Better', hook: 'Look!', id: 'wrong', revision: 99, scenes: [], original_audio: false }))
      .toEqual({ ...draft, title: 'Better', hook: 'Look!' })
  })
  it('collections preserve selection order and mint new unique scene IDs', () => {
    const collection = collectionDraft('  Collection  ', [draft.scenes[0], draft.scenes[0]])
    expect(collection.title).toBe('Collection')
    expect(collection.scenes.map(s => s.start)).toEqual([1, 1])
    expect(new Set(collection.scenes.map(s => s.id)).size).toBe(2)
    expect(collection.scenes[0].id).not.toBe('scene1')
    expect(draftError(collection, 120)).toBeNull()
  })
  it('offers portrait crop and secure-context-independent IDs', () => {
    expect(portraitDesign(draft)).toMatchObject({ aspect: 'portrait', layout: 'crop', title_style: 'comic' })
    expect(newID()).toMatch(/^[a-f0-9]{32}$/)
  })
})
describe('web import/language selection', () => {
  it('accepts supported videos and rejects unsafe hosts, credentials, redirects and non-video links', () => {
    expect(validateSourceURL('https://youtu.be/abcdefghijk')).toContain('abcdefghijk')
    for (const url of ['http://youtube.com/watch?v=abcdefghijk', 'https://youtube.com.evil.test/watch?v=abcdefghijk',
      'https://user:pass@youtube.com/watch?v=abcdefghijk', 'https://127.0.0.1/a', 'https://b23.tv/abc', 'https://youtube.com/playlist?list=x']) {
      expect(() => validateSourceURL(url)).toThrow()
    }
  })
  it('retains upstream supported-language resolution', () => {
    expect(resolveLanguage('system', ['pt-BR', 'en'])).toBe('pt')
    expect(resolveLanguage('ja', ['en-US'])).toBe('ja')
    expect(resolveLanguage('system', ['xx'])).toBe('en')
  })
})
