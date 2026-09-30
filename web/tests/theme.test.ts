import { readFileSync } from 'node:fs'
import { expect, it } from 'vitest'

const css = readFileSync('src/index.css', 'utf8')
function tokens(block: string) {
  return Object.fromEntries([...block.matchAll(/--(ac-[\w-]+):\s*(#[0-9a-f]{6})\s*;/gi)].map(match => [match[1], match[2]]))
}
function luminance(hex: string) {
  const channels = [1, 3, 5].map(offset => parseInt(hex.slice(offset, offset + 2), 16) / 255)
    .map(value => value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4)
  return channels[0] * .2126 + channels[1] * .7152 + channels[2] * .0722
}
function contrast(a: string, b: string) {
  const values = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (values[0] + .05) / (values[1] + .05)
}
it('keeps text, actions and status labels readable on all light/dark workbench surfaces', () => {
  const light = tokens(css.slice(0, css.indexOf('@media')))
  const dark = { ...light, ...tokens(css.slice(css.indexOf(':root[data-theme="dark"]'))) }
  for (const [theme, palette] of Object.entries({ light, dark })) {
    for (const text of ['ac-ink', 'ac-sub', 'ac-muted', 'ac-accent', 'ac-accent-strong', 'ac-ok', 'ac-warn', 'ac-error']) {
      for (const surface of ['ac-bg', 'ac-sunken', 'ac-card']) {
        expect(contrast(palette[text], palette[surface]), `${theme} ${text} on ${surface}`).toBeGreaterThanOrEqual(4.5)
      }
    }
    expect(contrast(palette['ac-cta-fg'], palette['ac-cta-bg'])).toBeGreaterThanOrEqual(4.5)
  }
})
