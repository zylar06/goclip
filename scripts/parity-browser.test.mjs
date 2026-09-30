import assert from 'node:assert/strict'
import { test } from 'node:test'
import path from 'node:path'
import { isolatedEnvironment, mockReply, inside, assertWorkflowSeparators, CDP } from './parity-browser.mjs'

test('workflow and goal labels require actual middle dots and reject the literal-question-mark regression', () => {
  assertWorkflowSeparators('Production \u00b7 completed \u00b7 V2', ['content \u00b7 completed'])
  assert.throws(() => assertWorkflowSeparators('Production ? completed ? V2', ['content \u00b7 completed']), /Workflow heading/)
  assert.throws(() => assertWorkflowSeparators('Production \u00b7 completed \u00b7 V2', ['content ? completed']), /Goal status/)
  assert.throws(() => assertWorkflowSeparators(undefined, []), /heading is missing/)
  assert.throws(() => assertWorkflowSeparators('Production \u00b7 completed \u00b7 V2', []), /labels are missing/)
})

test('isolated service environment blanks both model groups and never inherits proxy settings or production data', () => {
  const env = isolatedEnvironment({ AUTOCLIP_TEXT_API_KEY: 'secret', AUTOCLIP_VISION_MODEL: 'paid',
    AUTOCLIP_TEXT_OTHER: 'untrusted', AUTOCLIP_DATA_DIR: 'production', HTTP_PROXY: 'external', Path: 'tools' }, 'fresh', 49101)
  assert.equal(env.AUTOCLIP_TEXT_API_KEY, ''); assert.equal(env.AUTOCLIP_VISION_MODEL, '')
  assert.equal(env.AUTOCLIP_TEXT_OTHER, undefined); assert.equal(env.HTTP_PROXY, undefined)
  assert.equal(env.AUTOCLIP_ADDR, '127.0.0.1:49101'); assert.equal(env.AUTOCLIP_DATA_DIR, path.join('fresh', 'data'))
})
test('mock model supports only the four declared stages and rejects image or unknown requests', () => {
  for (const stage of ['outline', 'timeline', 'scoring', 'titles']) {
    const response = mockReply({ messages: [{ content: `AUTOCLIP_STAGE: ${stage}\n` }] })
    assert.equal(response.stage, stage)
    assert.equal(JSON.parse(response.body.choices[0].message.content).length, 2)
  }
  assert.throws(() => mockReply({ messages: [{ content: 'unknown' }] }), /Unexpected model stage/)
  assert.throws(() => mockReply({ messages: [{ content: [{ type: 'image_url' }] }] }), /Images are forbidden/)
})
test('evidence paths cannot escape the named browser directory', () => {
  const base = path.resolve('artifacts/parity/20260930/browser')
  assert.equal(inside(base, path.join(base, 'run-a')), path.join(base, 'run-a'))
  assert.throws(() => inside(base, path.join(base, '..', 'production')))
  assert.throws(() => inside(base, base))
})
test('CDP surfaces browser protocol and Runtime errors instead of silently reporting success', async () => {
  class Socket extends EventTarget {
    send(text) {
      const request = JSON.parse(text)
      queueMicrotask(() => this.dispatchEvent(new MessageEvent('message', { data: JSON.stringify(
        request.method === 'Runtime.evaluate' ? { id: request.id, result: { exceptionDetails: { text: 'browser failed' } } }
          : { id: request.id, error: { message: 'bad method' } }) })))
    }
  }
  const cdp = new CDP(new Socket(), new AbortController().signal)
  await assert.rejects(cdp.send('Invalid.method'), /bad method/)
  await assert.rejects(cdp.evaluate('bad()'), /browser failed/)
})
