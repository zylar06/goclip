// Read-only visual acceptance against a running GoClip API and the local web/dist.
// No package install, model requests, uploads, settings writes, or service restarts.
// Run after npm run build: node scripts/frontend-browser.mjs
import assert from 'node:assert/strict'
import fs from 'node:fs'
import fsp from 'node:fs/promises'
import http from 'node:http'
import path from 'node:path'
import { spawn, execFile } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { setTimeout as delay } from 'node:timers/promises'
import { CDP } from './parity-browser.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const directory = await fsp.mkdtemp(path.join(root, 'artifacts', 'frontend-redesign', 'browser-'))
const dist = path.join(root, 'web', 'dist')
const upstream = new URL(process.env.AUTOCLIP_PREVIEW_ORIGIN || 'http://127.0.0.1:8080')
assert(['127.0.0.1', 'localhost'].includes(upstream.hostname), 'Acceptance only allows a loopback API')
const controller = new AbortController()
const deadline = setTimeout(() => controller.abort(new Error('Browser acceptance exceeded 120 seconds')), 120000)
const summary = { status: 'running', directory, steps: [], mutations: [], errors: [], cancelled_reads: 0, started_at: new Date().toISOString() }
let chrome, socket, cdp
const log = fs.openSync(path.join(directory, 'chrome.log'), 'a')
const request = async url => {
  const response = await fetch(url, { signal: AbortSignal.any([controller.signal, AbortSignal.timeout(15000)]) })
  assert(response.ok, `HTTP ${response.status}: ${url}`)
  return response
}
async function probe(name, fn) {
  for (let attempt = 0; attempt < 80; attempt++) {
    controller.signal.throwIfAborted()
    if (await fn()) return
    await delay(150, undefined, { signal: controller.signal })
  }
  throw new Error(`Timed out: ${name}`)
}
const server = http.createServer(async (req, res) => {
  try {
    if (!['GET', 'HEAD'].includes(req.method)) {
      summary.mutations.push({ method: req.method, path: req.url })
      res.writeHead(405); res.end('Read-only acceptance'); return
    }
    if (req.url.startsWith('/api/v1/')) {
      let cancelled = false
      const proxy = http.request(new URL(req.url, upstream), { method: req.method, headers: req.headers.range ? { range: req.headers.range } : {} }, incoming => {
        res.writeHead(incoming.statusCode, incoming.headers); incoming.pipe(res)
      })
      proxy.setTimeout(15000, () => proxy.destroy(new Error('Read-only API proxy timeout')))
      proxy.on('error', error => {
        if (cancelled) { summary.cancelled_reads++; return }
        summary.errors.push(error.message)
        if (!res.headersSent) res.writeHead(502)
        res.end('API unavailable')
      })
      res.on('close', () => { if (!res.writableEnded) { cancelled = true; proxy.destroy() } })
      proxy.end(); return
    }
    const requested = decodeURIComponent(req.url.split('?')[0])
    const file = path.resolve(dist, '.' + (requested === '/' ? '/index.html' : requested))
    assert(file.startsWith(dist + path.sep), 'Asset path escapes web/dist')
    const body = await fsp.readFile(file)
    res.writeHead(200, { 'Content-Type': { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.webp': 'image/webp', '.svg': 'image/svg+xml' }[path.extname(file)] || 'application/octet-stream' })
    res.end(body)
  } catch (error) {
    console.error('Visual acceptance request failed:', error.message)
    res.writeHead(404); res.end('Asset unavailable')
  }
})
try {
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const base = `http://127.0.0.1:${server.address().port}`
  const executable = ['C:/Program Files/Google/Chrome/Application/chrome.exe', 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'].find(fs.existsSync)
  assert(executable, 'Chrome or Edge required')
  const profile = path.join(directory, 'profile')
  chrome = spawn(executable, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--disable-background-networking', '--disable-component-update', '--disable-sync', '--disable-extensions',
    '--disable-features=Translate,MediaRouter', '--remote-debugging-port=0', '--remote-debugging-address=127.0.0.1',
    '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1, EXCLUDE localhost',
    `--user-data-dir=${profile}`, 'about:blank'], { windowsHide: true, stdio: ['ignore', log, log] })
  chrome.on('error', error => controller.abort(error))
  const portFile = path.join(profile, 'DevToolsActivePort')
  await probe('Chrome debug port', () => fs.existsSync(portFile))
  const port = Number((await fsp.readFile(portFile, 'utf8')).split('\n')[0])
  const targets = await (await request(`http://127.0.0.1:${port}/json/list`)).json()
  socket = new WebSocket(targets.find(target => target.type === 'page').webSocketDebuggerUrl)
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true })
    socket.addEventListener('error', reject, { once: true })
    controller.signal.addEventListener('abort', () => reject(controller.signal.reason), { once: true })
  })
  cdp = new CDP(socket, controller.signal, event => {
    if (event.method === 'Runtime.exceptionThrown') summary.errors.push(event.params.exceptionDetails.text)
  })
  await cdp.send('Page.enable'); await cdp.send('Runtime.enable')
  await cdp.send('Page.addScriptToEvaluateOnNewDocument', { source: `if(location.protocol==='http:') localStorage.setItem('autoclip.language','zh')` })
  const viewport = async (width, dark = false) => {
    await cdp.send('Emulation.setDeviceMetricsOverride', { width, height: 1000, deviceScaleFactor: 1, mobile: false })
    await cdp.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: dark ? 'dark' : 'light' }, { name: 'prefers-reduced-motion', value: 'reduce' }] })
  }
  const visit = async (route, selector) => {
    await cdp.send('Page.navigate', { url: base + '/#' + route })
    await probe(route, () => cdp.evaluate(`!!document.querySelector(${JSON.stringify(selector)})`))
    await cdp.evaluate('document.fonts.ready')
  }
  const screenshot = async name => {
    await cdp.evaluate('window.scrollTo(0,0)')
    const size = await cdp.evaluate('({width:innerWidth, height:innerHeight, scroll:document.documentElement.scrollWidth})')
    assert(size.scroll <= size.width + 1, `${name}: horizontal overflow ${JSON.stringify(size)}`)
    const image = await cdp.send('Page.captureScreenshot', { format: 'png' })
    await fsp.writeFile(path.join(directory, `${name}.png`), Buffer.from(image.data, 'base64'))
    summary.steps.push({ name, ...size })
    console.log(`PASS ${name}`)
  }
  const openImport = async () => {
    const point = await cdp.evaluate(`(()=>{const r=document.querySelector('.web-page-heading button').getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()`)
    await cdp.send('Input.dispatchMouseEvent', { type: 'mousePressed', ...point, button: 'left', clickCount: 1 })
    await cdp.send('Input.dispatchMouseEvent', { type: 'mouseReleased', ...point, button: 'left', clickCount: 1 })
    await probe('import dialog', () => cdp.evaluate(`!!document.querySelector('[role=dialog]')`))
  }
  await viewport(1440)
  await visit('/', '.web-library')
  await probe('project list loaded', () => cdp.evaluate(`!document.querySelector('.ac-loading')`))
  assert(await cdp.evaluate(`!document.querySelector('input[type=file]') && !document.querySelector('.web-hero')`), 'Home must lead with projects, not import or marketing')
  await screenshot('home-desktop')
  await openImport()
  await screenshot('import-desktop')
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape' })
  await probe('Import Escape restores focus', () => cdp.evaluate(`!document.querySelector('[role=dialog]') && document.activeElement === document.querySelector('.web-page-heading button')`))
  await viewport(390)
  await screenshot('home-mobile')
  await openImport()
  await screenshot('import-mobile')
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape' })
  await probe('mobile import dismissed', () => cdp.evaluate(`!document.querySelector('[role=dialog]')`))
  await viewport(1440, true); await screenshot('home-dark')
  await viewport(1440)
  await visit('/settings', '.web-model-grid')
  await screenshot('settings-desktop')
  await viewport(390); await screenshot('settings-mobile')
  const projects = await (await request(new URL('/api/v1/projects', upstream))).json()
  let workspace
  for (const project of projects.slice(0, 30)) {
    const detail = await (await request(new URL(`/api/v1/projects/${project.id}`, upstream))).json()
    if (detail.drafts?.length) { workspace = detail; break }
  }
  if (workspace) {
    await viewport(1440)
    await visit(`/project/${workspace.project.id}`, '.studio-result-grid .web-project-card')
    await probe('visible result thumbnails', () => cdp.evaluate(`Array.from(document.querySelectorAll('.studio-result-thumbnail img')).filter(img=>img.getBoundingClientRect().top<innerHeight).every(img=>img.complete&&img.naturalWidth>0)`))
    assert(await cdp.evaluate(`Array.from(document.querySelectorAll('.web-plan-disclosure,.web-export-history,.web-task-history')).every(el => !el.open)`), 'Project disclosures must start collapsed')
    await screenshot('project-desktop')
    await viewport(390); await screenshot('project-mobile')
    await viewport(1440, true); await screenshot('project-dark')
    await viewport(1440)
    await visit(`/project/${workspace.project.id}/studio/${workspace.drafts[0].id}`, '.studio-editor-grid')
    await probe('editor media ready', () => cdp.evaluate(`document.querySelector('video')?.readyState >= 2`))
    await screenshot('editor-desktop')
    await viewport(390); await screenshot('editor-mobile')
    await viewport(1440)
    await visit(`/import/${workspace.project.id}`, '.web-review-grid')
    await probe('review media ready', () => cdp.evaluate(`document.querySelector('video')?.readyState >= 2`))
    await screenshot('review-desktop')
    await viewport(390); await screenshot('review-mobile')
  } else {
    summary.steps.push({ name: 'project/editor/review', status: 'not exercised', reason: 'No existing draft; read-only acceptance never creates data.' })
  }
  assert.deepEqual(summary.mutations, [], 'Acceptance must not mutate production')
  assert.deepEqual(summary.errors, [], 'Browser/runtime/API errors must not be ignored')
  summary.status = 'passed'
} catch (error) {
  summary.status = 'failed'; summary.failure = error.stack
  console.error(error); process.exitCode = 1
} finally {
  clearTimeout(deadline)
  if (cdp && !controller.signal.aborted) {
    try { await cdp.send('Browser.close') } catch (error) { console.error('Browser close:', error.message) }
  }
  socket?.close()
  if (chrome && chrome.exitCode === null) {
    await new Promise(resolve => execFile('taskkill.exe', ['/PID', String(chrome.pid), '/T', '/F'], { windowsHide: true, timeout: 10000 }, error => {
      if (error && chrome.exitCode === null) { console.error('Chrome cleanup failed:', error.message); summary.status = 'failed'; process.exitCode = 1 }
      resolve()
    }))
  }
  server.closeAllConnections()
  await new Promise(resolve => server.close(resolve))
  fs.closeSync(log)
  summary.finished_at = new Date().toISOString()
  if (summary.errors.length) { summary.status = 'failed'; process.exitCode = 1 }
  await fsp.writeFile(path.join(directory, 'summary.json'), JSON.stringify(summary, null, 2))
  console.log(JSON.stringify({ status: summary.status, directory }))
}
