import { expectTypeOf, it } from 'vitest'
import { expect } from 'vitest'
import { readFileSync } from 'node:fs'
import type * as Generated from '../src/generated/api'
import type * as Web from '../src/api/contracts'

it('keeps editor/domain refinements assignable to the generated wire contract', () => {
  expectTypeOf<Web.Project>().toEqualTypeOf<Generated.Project>()
  expectTypeOf<Web.Scene>().toEqualTypeOf<Generated.Scene>()
  expectTypeOf<Web.Candidate>().toEqualTypeOf<Generated.Candidate>()
  expectTypeOf<Web.Cue>().toEqualTypeOf<Generated.Cue>()
  expectTypeOf<Web.Export>().toEqualTypeOf<Generated.Export>()
  expectTypeOf<Web.ModelStatus>().toEqualTypeOf<Generated.ModelStatus>()
  expectTypeOf<Web.ModelSettings>().toEqualTypeOf<Generated.ModelSettings>()
  expectTypeOf<Web.Draft>().toMatchTypeOf<Generated.Draft>()
  expectTypeOf<Web.AnalysisOptions>().toMatchTypeOf<Generated.AnalysisOptions>()
  // The only wire relaxations: Go may emit nil slices or omit false cancellation.
  expectTypeOf<Web.Task & { completed_steps: string[]; cancel_requested: boolean }>().toMatchTypeOf<Generated.Task>()
})
it('keeps development and preview servers bound to loopback by default', () => {
  const manifest = JSON.parse(readFileSync('package.json', 'utf8'))
  expect(manifest.scripts.dev).toBe('vite --host 127.0.0.1')
  expect(manifest.scripts.preview).toBe('vite preview --host 127.0.0.1')
})
