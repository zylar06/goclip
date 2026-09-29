import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { createServer } from 'node:http'
import { promisify } from 'node:util'
import { execFile, spawnSync } from 'node:child_process'

const windows = process.platform === 'win32'
const shell = process.env.BASH_EXE || (windows ? 'C:/Program Files/Git/bin/bash.exe' : '/bin/sh')
const shellPath = value => windows
  ? value.replaceAll('\\', '/').replace(/^([A-Za-z]):\//, (_, drive) => `/${drive.toLowerCase()}/`)
  : value

function runSmoke(t, failTransfer = false) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'autoclip-smoke-test-'))
  t.after(() => {
    assert.equal(path.dirname(path.resolve(root)), path.resolve(os.tmpdir()))
    assert(path.basename(root).startsWith('autoclip-smoke-test-'))
    fs.rmSync(root, { recursive: true, force: true })
  })
  const bin = path.join(root, 'bin')
  fs.mkdirSync(bin)
  // Includes NUL, CR/LF and invalid UTF-8: no text decoding/TTY conversion allowed.
  const bytes = Buffer.concat([Buffer.from('\0\0\0\x18ftypisom\r\n'), Buffer.from([0, 255, 128, 10, 13])])
  fs.writeFileSync(path.join(root, 'source.mp4'), bytes)
  fs.writeFileSync(path.join(bin, 'docker'), `#!/bin/sh
set -eu
printf '%s\\n' "$*" >> "$SMOKE_COMMAND_LOG"
case "$*" in
  "compose cp "*) echo "Cannot copy a tmpfs path through the archive API" >&2; exit 19 ;;
  "compose exec -T worker cat /tmp/smoke.mp4")
    cat "$SMOKE_SOURCE"
    if [ "$SMOKE_FAIL_TRANSFER" = 1 ]; then echo "transfer failed" >&2; exit 17; fi
    ;;
  "compose exec -T worker ffmpeg "*) ;;
  "compose up "*|"compose restart") ;;
  *) echo "Unexpected Docker command" >&2; exit 23 ;;
esac
`, { mode: 0o755 })
  fs.writeFileSync(path.join(bin, 'node'), `#!/bin/sh
set -eu
printf 'node %s\\n' "$*" >> "$SMOKE_COMMAND_LOG"
`, { mode: 0o755 })
  const driver = path.join(root, 'driver.sh')
  fs.writeFileSync(driver, `#!/bin/sh
set -eu
PATH="$SMOKE_BIN:$PATH"
export PATH
exec sh "$SMOKE_SCRIPT"
`)
  const log = path.join(root, 'commands.log')
  const result = spawnSync(shell, [
    ...(windows ? ['--noprofile', '--norc'] : []), shellPath(driver),
  ], {
    cwd: root, encoding: 'utf8', timeout: 15000, windowsHide: true,
    env: {
      ...process.env,
      SMOKE_BIN: shellPath(bin),
      SMOKE_SCRIPT: shellPath(path.resolve('scripts/smoke-docker.sh')),
      SMOKE_SOURCE: shellPath(path.join(root, 'source.mp4')),
      SMOKE_COMMAND_LOG: shellPath(log),
      SMOKE_FAIL_TRANSFER: failTransfer ? '1' : '0',
      // Prevent Git Bash rewriting Docker's absolute container paths.
      MSYS_NO_PATHCONV: '1',
    },
  })
  assert.ifError(result.error)
  return { result, root, bytes, commands: fs.readFileSync(log, 'utf8') }
}

test('container smoke streams tmpfs bytes without archive copy or TTY conversion', t => {
  const { result, root, bytes, commands } = runSmoke(t)
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(fs.readFileSync(path.join(root, 'artifacts/container-fixture.mp4')), bytes)
  assert(commands.includes('compose exec -T worker cat /tmp/smoke.mp4'))
  assert(!commands.includes('compose cp '))
  assert(commands.includes('node scripts/container-smoke.mjs artifacts/container-fixture.mp4'))
  assert(commands.includes('compose restart'))
})

test('failed fixture transfer aborts before API acceptance or service restart', t => {
  const { result, commands } = runSmoke(t, true)
  assert.equal(result.status, 17, result.stderr)
  assert.match(result.stderr, /transfer failed/)
  assert(!commands.includes('node scripts/container-smoke.mjs'))
  assert(!commands.includes('compose restart'))
})

test('restart persistence check uses the isolated API and rejects HTTP errors', async t => {
  // Execute the actual inline program, rather than a second copy of its logic.
  const script = fs.readFileSync('scripts/smoke-docker.sh', 'utf8')
  const line = script.split(/\r?\n/).find(line => line.startsWith("node -e '"))
  assert(line?.endsWith("'"), 'missing persistence check')
  const program = line.slice("node -e '".length, -1)
  let status = 200
  let requests = 0
  const server = createServer((req, res) => {
    requests++
    assert.equal(req.url, '/isolated-api/projects')
    res.writeHead(status, { 'Content-Type': 'application/json' })
    // Even a plausible success-shaped body must not mask HTTP failure.
    res.end(JSON.stringify([{ name: '容器验收' }]))
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => new Promise(resolve => server.close(resolve)))
  const options = {
    env: { ...process.env, AUTOCLIP_URL: `http://127.0.0.1:${server.address().port}/isolated-api` },
    windowsHide: true, timeout: 10000, encoding: 'utf8',
  }
  const run = promisify(execFile)
  const passed = await run(process.execPath, ['-e', program], options)
  assert.match(passed.stdout, /PASS persistence/)
  status = 503
  await assert.rejects(run(process.execPath, ['-e', program], options), error => {
    assert.equal(error.code, 1)
    assert.match(error.stderr, /Persistence check HTTP 503/)
    return true
  })
  assert.equal(requests, 2)
})
