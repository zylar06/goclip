import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const fixture = {
  openapi: '3.0.3',
  components: { schemas: {
    Scene: { type: 'object', required: ['id'], properties: { id: { type: 'string' }, label: { type: 'string' } } },
    Task: { type: 'object', required: ['progress', 'scenes', 'status'], properties: {
      progress: { type: 'number', nullable: true },
      status: { type: 'string', enum: ['running', 'completed'] },
      scenes: { type: 'array', items: { $ref: '#/components/schemas/Scene' } },
      payload: { type: 'object', additionalProperties: true },
      cancel_requested: { type: 'boolean' },
    } },
  } },
}
const run = (input: string, output: string, check = false) => execFileSync(process.execPath,
  ['scripts/generate-api.mjs', '--input', input, '--output', output, ...(check ? ['--check'] : [])], { encoding: 'utf8', timeout: 10000, stdio: 'pipe' })
describe('dependency-free OpenAPI generation', () => {
  it('generates deterministic references, optional fields, enums and nullable progress; detects drift', () => {
    const temp = mkdtempSync(join(tmpdir(), 'autoclip-schema-'))
    const input = join(temp, 'openapi.json'), output = join(temp, 'api.ts')
    writeFileSync(input, JSON.stringify(fixture))
    run(input, output)
    const content = readFileSync(output, 'utf8')
    expect(content).toContain('"progress": (number | null)')
    expect(content).toContain('"scenes": Array<Scene>')
    expect(content).toContain('"status": "running" | "completed"')
    expect(content).toContain('"cancel_requested"?: boolean')
    expect(content).toContain('"payload"?: Record<string, unknown>')
    expect(run(input, output, true)).toContain('current')
    writeFileSync(output, 'stale')
    expect(() => run(input, output, true)).toThrow()
  })
  it('fails explicitly for unresolved refs and unsupported schema constructs', () => {
    const temp = mkdtempSync(join(tmpdir(), 'autoclip-invalid-schema-'))
    const input = join(temp, 'openapi.json'), output = join(temp, 'api.ts')
    writeFileSync(input, JSON.stringify({ openapi: '3.1.0', components: { schemas: { Task: { $ref: '#/components/schemas/Missing' } } } }))
    expect(() => run(input, output)).toThrow()
    writeFileSync(input, JSON.stringify({ openapi: '3.1.0', components: { schemas: { Task: { not: { type: 'string' } } } } }))
    expect(() => run(input, output)).toThrow()
  })
})
