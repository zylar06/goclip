import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'

const windows = process.platform === 'win32'
const shell = process.env.BASH_EXE || (windows ? 'C:/Program Files/Git/bin/bash.exe' : '/bin/sh')
const shellPath = value => windows
  ? value.replaceAll('\\', '/').replace(/^([A-Za-z]):\//, (_, drive) => `/${drive.toLowerCase()}/`)
  : value

function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'autoclip-apt-'))
  t.after(() => {
    const resolved = path.resolve(root)
    assert.equal(path.dirname(resolved), path.resolve(os.tmpdir()))
    assert(path.basename(resolved).startsWith('autoclip-apt-'))
    fs.rmSync(resolved, { recursive: true, force: true })
  })
  fs.mkdirSync(path.join(root, 'sources.list.d'))
  fs.mkdirSync(path.join(root, 'apt.conf.d'))
  fs.writeFileSync(path.join(root, 'sources.list.d/debian.sources'), 'original source\n')
  fs.writeFileSync(path.join(root, 'apt.conf.d/docker-clean'), 'original cleanup hook\n')
  fs.writeFileSync(path.join(root, 'apt.conf.d/99local'), 'keep local config\n')
  return root
}

function configure(root, overrides = {}) {
  return spawnSync(shell, [
    ...(windows ? ['--noprofile', '--norc'] : []),
    shellPath(path.resolve('scripts/apt-setup.sh')),
    shellPath(root),
  ], {
    encoding: 'utf8', timeout: 10000, windowsHide: true,
    env: {
      ...process.env,
      APT_SOURCE_MODE: 'mirror',
      DEBIAN_MIRROR: 'https://deb.debian.org/debian',
      DEBIAN_SECURITY_MIRROR: 'https://deb.debian.org/debian-security',
      DEBIAN_SNAPSHOT: '20260901T000000Z',
      ...overrides,
    },
  })
}

function succeeded(result) {
  assert.ifError(result.error)
  assert.equal(result.status, 0, result.stderr)
}

test('default mirror keeps signature/freshness checks and all Bookworm suites', t => {
  const root = fixture(t)
  const result = configure(root)
  succeeded(result)
  const sources = fs.readFileSync(path.join(root, 'sources.list'), 'utf8')
  assert.equal(sources, [
    'deb https://deb.debian.org/debian bookworm main',
    'deb https://deb.debian.org/debian bookworm-updates main',
    'deb https://deb.debian.org/debian-security bookworm-security main',
    '',
  ].join('\n'))
  assert(!sources.includes('check-valid-until=no'))
  assert(!sources.includes('trusted=yes'))
  assert(!fs.existsSync(path.join(root, 'sources.list.d/debian.sources')))
  assert.match(result.stdout, /mirror/)
})

test('snapshot mode is explicit and retains the exact signed archive timestamp', t => {
  const root = fixture(t)
  const result = configure(root, { APT_SOURCE_MODE: 'snapshot' })
  succeeded(result)
  const sources = fs.readFileSync(path.join(root, 'sources.list'), 'utf8')
  assert.match(sources, /http:\/\/snapshot\.debian\.org\/archive\/debian\/20260901T000000Z bookworm main/)
  assert.match(sources, /archive\/debian-security\/20260901T000000Z bookworm-security main/)
  assert.equal((sources.match(/check-valid-until=no/g) || []).length, 3)
  assert(!sources.includes('trusted=yes'))
  assert.match(result.stdout, /snapshot/)
})

test('custom mirror URLs support HTTPS, a port and normalized trailing slash', t => {
  const root = fixture(t)
  succeeded(configure(root, {
    DEBIAN_MIRROR: 'https://mirror.example:8443/debian/',
    DEBIAN_SECURITY_MIRROR: 'http://mirror.example/debian-security/',
  }))
  const sources = fs.readFileSync(path.join(root, 'sources.list'), 'utf8')
  assert.match(sources, /https:\/\/mirror\.example:8443\/debian bookworm main/)
  assert.match(sources, /http:\/\/mirror\.example\/debian-security bookworm-security main/)
})

test('archive caching survives Docker cleanup hooks; network/update failures stay explicit', t => {
  const root = fixture(t)
  succeeded(configure(root))
  assert(!fs.existsSync(path.join(root, 'apt.conf.d/docker-clean')))
  assert.equal(fs.readFileSync(path.join(root, 'apt.conf.d/99local'), 'utf8'), 'keep local config\n')
  const config = fs.readFileSync(path.join(root, 'apt.conf.d/80autoclip'), 'utf8')
  assert.match(config, /APT::Keep-Downloaded-Packages "true";/)
  assert.match(config, /Acquire::Retries "2";/)
  assert.match(config, /Acquire::http::Timeout "30";/)
  assert.match(config, /Acquire::https::Timeout "30";/)
  assert.match(config, /APT::Update::Error-Mode "any";/)
  assert(!/AllowUnauthenticated|AllowInsecure|Verify-Peer.*false/.test(config))
  succeeded(configure(root))
})

test('invalid source mode or snapshot timestamp fails before altering any files', t => {
  for (const overrides of [
    { APT_SOURCE_MODE: 'automatic-fallback' },
    { APT_SOURCE_MODE: 'snapshot', DEBIAN_SNAPSHOT: 'latest' },
    { APT_SOURCE_MODE: 'snapshot', DEBIAN_SNAPSHOT: '20260901T000000Z injected' },
  ]) {
    const root = fixture(t)
    const result = configure(root, overrides)
    assert.ifError(result.error)
    assert.notEqual(result.status, 0)
    assert.match(result.stderr, /invalid|unsupported/i)
    assert.equal(fs.readFileSync(path.join(root, 'sources.list.d/debian.sources'), 'utf8'), 'original source\n')
    assert(fs.existsSync(path.join(root, 'apt.conf.d/docker-clean')))
    assert(!fs.existsSync(path.join(root, 'sources.list')))
  }
})

test('mirror URLs reject credentials, injection, missing hosts and unsupported schemes', t => {
  for (const url of [
    'file:///etc/passwd', 'https://', 'http:///debian', 'https://user:secret@mirror.example/debian',
    'https://mirror.example/debian?token=secret', 'https://mirror.example/debian#fragment',
    'https://mirror.example/debian\ntrusted=yes', 'https://mirror.example/debian extra',
    'https://mirror.example/$(whoami)',
  ]) {
    const root = fixture(t)
    const result = configure(root, { DEBIAN_MIRROR: url })
    assert.ifError(result.error)
    assert.notEqual(result.status, 0, `accepted ${JSON.stringify(url)}`)
    assert.match(result.stderr, /mirror URL/i)
    assert(!result.stderr.includes('secret'), 'errors must not echo rejected credentials')
    assert(!fs.existsSync(path.join(root, 'sources.list')))
  }
})

test('invalid configuration directory fails explicitly without touching system APT', t => {
  const root = fixture(t)
  const result = configure('relative-test-directory')
  assert.ifError(result.error)
  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /absolute/i)
  const file = path.join(root, 'not-a-directory')
  fs.writeFileSync(file, 'keep\n')
  const failed = configure(file)
  assert.ifError(failed.error)
  assert.notEqual(failed.status, 0)
  assert.equal(fs.readFileSync(file, 'utf8'), 'keep\n')
})
