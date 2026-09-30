import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import os from 'node:os'
import { spawnSync } from 'node:child_process'

const workflow = fs.readFileSync('.github/workflows/ci.yml', 'utf8')
// Exercise the literal command/env from our simple workflow, not a second copy
// of its values. No general YAML parser or dependency is required.
const step = workflow.match(/      - run: (go test [^\r\n]+)\r?\n        env:\r?\n((?:          [^\r\n]+\r?\n)+)/)

test('CI keeps race/native coverage and removes only race exit sleeps within the original deadline', t => {
  assert(step, 'Go race test step with explicit environment is required')
  assert.equal(step[1], 'go test -race -timeout 180s ./...')
  const env = Object.fromEntries([...step[2].matchAll(/^          (\w+): "?([^"\r\n]+)"?$/gm)].map(match => [match[1], match[2]]))
  assert.equal(env.REQUIRE_MEDIA_TESTS, '1')
  assert.equal(env.GORACE, 'atexit_sleep_ms=0', 'Fake native subprocesses must not each sleep one second at exit')
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'goclip-ci-env-'))
  t.after(() => {
    assert.equal(path.dirname(path.resolve(root)), path.resolve(os.tmpdir()))
    assert(path.basename(root).startsWith('goclip-ci-env-'))
    fs.rmSync(root, { recursive: true, force: true })
  })
  fs.writeFileSync(path.join(root, 'go'), '#!/bin/sh\nset -eu\nprintf "%s\\n" "$*" "$GORACE" "$REQUIRE_MEDIA_TESTS"\n', { mode: 0o755 })
  const windows = process.platform === 'win32'
  const shellRoot = windows ? root.replaceAll('\\', '/').replace(/^([A-Za-z]):\//, (_, drive) => `/${drive.toLowerCase()}/`) : root
  const result = spawnSync(windows ? 'C:/Program Files/Git/bin/bash.exe' : '/bin/sh', ['-c', 'PATH="$CI_STUB:$PATH"; export PATH; ' + step[1]], {
    encoding: 'utf8', timeout: 10000, windowsHide: true, env: { ...process.env, ...env, CI_STUB: shellRoot },
  })
  assert.ifError(result.error)
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.stdout.replaceAll('\r', ''), 'test -race -timeout 180s ./...\natexit_sleep_ms=0\n1\n')
})

test('workflow executes this regression gate and does not weaken the container tests', () => {
  assert(workflow.includes('run: node --test scripts/ci.test.mjs'))
  assert(workflow.includes('run: sh scripts/smoke-docker.sh'))
  assert(!workflow.includes('continue-on-error'))
  const dockerfile = fs.readFileSync('Dockerfile', 'utf8')
  assert(dockerfile.includes('CGO_ENABLED=0 go test -timeout 180s ./...'))
})
