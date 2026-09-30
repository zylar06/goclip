// Real Chrome + isolated Go web/worker acceptance. Node 22 built-ins only.
// Run: node scripts/parity-browser.mjs (requires permission to launch headless Chrome).
// Hard limit: five minutes, including cleanup; no existing service/data is used.
import assert from 'node:assert/strict'
import { spawn, execFile } from 'node:child_process'
import { createHash } from 'node:crypto'
import fs from 'node:fs'
import fsp from 'node:fs/promises'
import http from 'node:http'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'
import { setTimeout as sleep } from 'node:timers/promises'

const exec = promisify(execFile)
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
export function isolatedEnvironment(input, directory, port) {
  const env = { ...input }
  for (const key of Object.keys(env)) {
    if (/^AUTOCLIP_(TEXT|VISION)_/i.test(key) || /^(https?|all|no)_proxy$/i.test(key)) delete env[key]
  }
  for (const kind of ['TEXT', 'VISION']) for (const name of ['BASE_URL', 'MODEL', 'API_KEY']) env[`AUTOCLIP_${kind}_${name}`] = ''
  return { ...env, AUTOCLIP_ADDR: `127.0.0.1:${port}`, AUTOCLIP_DATA_DIR: path.join(directory, 'data'),
    AUTOCLIP_WEB_DIR: path.join(root, 'web', 'dist'), FONT_DIR: path.join(root, 'assets', 'fonts'),
    WHISPER_PATH: path.join(directory, 'ASR-MUST-NOT-RUN.exe'), WHISPER_MODEL: path.join(directory, 'NO-ASR-MODEL'),
    TASK_TIMEOUT_SECONDS: '120', MAX_VIDEO_SECONDS: '100', MAX_UPLOAD_BYTES: '67108864' }
}
const replies = {
  outline: [{ title: 'First complete explanation', subtopics: ['Evidence'] }, { title: 'Second complete explanation', subtopics: ['Conclusion'] }],
  timeline: [{ topic_id: 'topic-1', start: 0, end: 40 }, { topic_id: 'topic-2', start: 40, end: 80 }],
  scoring: [{ id: 'text-1', score: .9, reason: 'Complete explanation' }, { id: 'text-2', score: .8, reason: 'Complete conclusion' }],
  titles: [{ id: 'text-1', title: 'First complete explanation', hook: 'First part' }, { id: 'text-2', title: 'Second complete explanation', hook: 'Second part' }],
}
export function mockReply(body) {
  assert(Array.isArray(body.messages), 'Missing model messages')
  assert(body.messages.every(m => typeof m.content === 'string'), 'Images are forbidden in this local text-only acceptance')
  const prompt = body.messages.map(m => m.content).join('\n')
  const stage = Object.keys(replies).find(key => prompt.includes(`AUTOCLIP_STAGE: ${key}\n`))
  assert(stage, 'Unexpected model stage; refusing to fabricate a response')
  return { stage, body: { choices: [{ finish_reason: 'stop', message: { content: JSON.stringify(replies[stage]) } }] } }
}
export function inside(base, target) {
  const relative = path.relative(path.resolve(base), path.resolve(target))
  assert(relative && !relative.startsWith('..') && !path.isAbsolute(relative), 'Path must remain inside the dedicated evidence directory')
  return path.resolve(target)
}
export function assertWorkflowSeparators(heading, goalLabels) {
  assert.equal(typeof heading, 'string', 'Workflow heading is missing')
  assert.match(heading, /^Production\s+\u00b7\s+completed\s+\u00b7\s+V\d+$/, 'Workflow heading must use middle-dot separators')
  assert(Array.isArray(goalLabels) && goalLabels.length > 0, 'Goal status labels are missing')
  for (const label of goalLabels) {
    assert.match(label, /^(content|highlight|promo)\s+\u00b7\s+completed$/, 'Goal status must use a middle-dot separator')
  }
}
export class CDP {
  constructor(socket, signal, onEvent = () => {}) {
    this.socket = socket; this.id = 0; this.pending = new Map()
    socket.addEventListener('message', event => {
      const value = JSON.parse(event.data)
      const pending = this.pending.get(value.id)
      if (pending) {
        clearTimeout(pending.timer); this.pending.delete(value.id)
        if (value.error) pending.reject(new Error(JSON.stringify(value.error)))
        else pending.resolve(value.result)
      } else if (value.method) onEvent(value)
    })
    const stop = () => {
      for (const request of this.pending.values()) { clearTimeout(request.timer); request.reject(new Error('CDP disconnected or acceptance deadline reached')) }
      this.pending.clear()
    }
    signal.addEventListener('abort', stop, { once: true })
    socket.addEventListener('close', stop)
  }
  send(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.id
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`CDP command timed out: ${method}`)) }, 10000)
      this.pending.set(id, { resolve, reject, timer })
      this.socket.send(JSON.stringify({ id, method, params }))
    })
  }
  async evaluate(expression) {
    const value = await this.send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true, userGesture: true })
    if (value.exceptionDetails) throw new Error(JSON.stringify(value.exceptionDetails))
    return value.result?.value
  }
}
async function freePort() {
  const server = net.createServer()
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = server.address().port
  await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
  assert.notEqual(port, 8080)
  return port
}
export async function run() {
  const evidenceRoot = path.join(root, 'artifacts', 'parity', '20260930', 'browser')
  await fsp.mkdir(evidenceRoot, { recursive: true })
  const directory = inside(evidenceRoot, await fsp.mkdtemp(path.join(evidenceRoot, 'run-')))
  const started = Date.now()
  const summary = { status: 'running', started_at: new Date().toISOString(), directory, steps: [], defects: [], limitations: [
    'Local deterministic model responses only; real model semantic quality is not evaluated.',
    'Headless Chrome on this Windows host, not a cross-browser or human visual quality certification.',
  ] }
  const controller = new AbortController()
  const signal = controller.signal
  // Leave 15 seconds for bounded process-tree cleanup and evidence writing.
  const deadline = setTimeout(() => controller.abort(new Error('Acceptance exceeded its 285-second work budget')), 285000)
  const children = []
  const handles = []
  let cdp, socket, mock, base, projectId
  let stage = 'preflight'
  const log = (message, extra = {}) => {
    const entry = { at: new Date().toISOString(), elapsed_ms: Date.now() - started, stage, message, ...extra }
    fs.appendFileSync(path.join(directory, 'progress.jsonl'), JSON.stringify(entry) + '\n')
    console.log(JSON.stringify(entry))
  }
  const save = (name, value) => fsp.writeFile(path.join(directory, name), JSON.stringify(value, null, 2) + '\n')
  const spawnLogged = (name, executable, args, env) => {
    const fd = fs.openSync(path.join(directory, name + '.log'), 'a'); handles.push(fd)
    const child = spawn(executable, args, { cwd: root, env, windowsHide: true, stdio: ['ignore', fd, fd] })
    child.on('error', error => { child.launchError = error; log('child launch failure', { name, error: error.message }) })
    children.push({ name, child })
    log('child started', { name, pid: child.pid })
    return child
  }
  const kill = async ({ name, child }) => {
    if (!child.pid || child.exitCode !== null || child.signalCode !== null) return
    try {
      if (process.platform === 'win32') await exec('taskkill.exe', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, timeout: 5000 })
      else child.kill('SIGKILL')
      if (child.exitCode === null && child.signalCode === null) await new Promise(resolve => {
        const timer = setTimeout(resolve, 1500)
        child.once('exit', () => { clearTimeout(timer); resolve() })
      })
      log('owned process tree stopped', { name, pid: child.pid })
    } catch (error) {
      if (child.exitCode === null && child.signalCode === null) { summary.defects.push(`Cleanup ${name}: ${error.message}`); log('cleanup failed', { name, error: error.message }) }
    }
  }
  const native = async (name, executable, args, env, timeout = 30000) => {
    const child = spawnLogged(name, executable, args, env)
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => { void kill({ name, child }); reject(new Error(`${name} exceeded ${timeout}ms`)) }, timeout)
      const abort = () => { void kill({ name, child }); reject(signal.reason) }
      signal.addEventListener('abort', abort, { once: true })
      child.once('error', error => { clearTimeout(timer); signal.removeEventListener('abort', abort); reject(error) })
      child.once('exit', code => { clearTimeout(timer); signal.removeEventListener('abort', abort); code === 0 ? resolve() : reject(new Error(`${name} exited ${code}; see ${name}.log`)) })
    })
  }
  const request = async (url, init = {}) => {
    const response = await fetch(url, { ...init, signal: AbortSignal.any([signal, AbortSignal.timeout(10000)]) })
    if (!response.ok) throw new Error(`HTTP ${response.status}: ${url}: ${(await response.text()).slice(0, 1000)}`)
    return response
  }
  const api = async (route, method = 'GET', body) => (await request(base + '/api/v1' + route,
    body === undefined ? { method } : { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })).json()
  const probe = async (label, fn, timeout = 15000, interval = 250) => {
    const end = Math.min(started + 280000, Date.now() + timeout)
    let last = 0, attempt = 0
    while (Date.now() < end && attempt++ < Math.ceil(timeout / interval)) {
      signal.throwIfAborted()
      const value = await fn()
      if (value) return value
      if (++last >= 3) { log('probe pending', { label, attempt }); last = 0 }
      await sleep(interval, undefined, { signal })
    }
    throw new Error(`${label}: bounded probe deadline exceeded`)
  }
  const button = label => `Array.from(document.querySelectorAll('button')).find(e=>e.textContent.trim()===${JSON.stringify(label)})`
  const field = label => `Array.from(document.querySelectorAll('label')).find(e=>e.textContent.trim().startsWith(${JSON.stringify(label)}))?.querySelector('input,textarea,select')`
  const click = async label => cdp.evaluate(`(()=>{const e=${button(label)};if(!e||e.disabled)throw Error('Unavailable button: '+${JSON.stringify(label)});e.scrollIntoView({block:'center'});e.click();return true})()`)
  const setInput = async (label, value) => cdp.evaluate(`(()=>{const e=${field(label)};if(!e)throw Error('Missing input: '+${JSON.stringify(label)});const p=e instanceof HTMLTextAreaElement?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;Object.getOwnPropertyDescriptor(p,'value').set.call(e,${JSON.stringify(String(value))});e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));return true})()`)
  const screenshot = async name => {
    await cdp.evaluate('window.scrollTo(0,0)')
    const { cssContentSize: size } = await cdp.send('Page.getLayoutMetrics')
    const image = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true,
      clip: { x: 0, y: 0, width: Math.min(size.width, 1600), height: Math.min(size.height, 6000), scale: 1 } })
    await fsp.writeFile(path.join(directory, name + '.png'), Buffer.from(image.data, 'base64'))
    await fsp.writeFile(path.join(directory, name + '.txt'), await cdp.evaluate('document.body.innerText'))
  }
  const pass = (name, evidence = {}) => { summary.steps.push({ name, status: 'passed', ...evidence }); log('PASS ' + name, evidence) }
  try {
    const binary = path.join(root, 'artifacts', 'parity', '20260930', 'autoclip.exe')
    const chrome = ['C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe', 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe'].find(f => fs.existsSync(f))
    assert(chrome, 'Installed Chrome/Edge unavailable')
    assert(fs.existsSync(binary), 'Main-agent Go binary is not built yet')
    assert(fs.existsSync(path.join(root, 'web/dist/index.html')), 'Built web/dist is missing')
    const ffdir = path.join(process.env.LOCALAPPDATA ?? '', 'AutoClip Desktop', 'resources', 'ffmpeg')
    const ffmpeg = path.join(ffdir, 'ffmpeg.exe'), ffprobe = path.join(ffdir, 'ffprobe.exe')
    assert(fs.existsSync(ffmpeg) && fs.existsSync(ffprobe), 'Installed FFmpeg tools unavailable')
    const port = await freePort()
    base = `http://127.0.0.1:${port}`
    const env = { ...isolatedEnvironment(process.env, directory, port), FFMPEG_PATH: ffmpeg, FFPROBE_PATH: ffprobe }
    const modelCalls = []
    mock = http.createServer(async (req, res) => {
      try {
        assert.equal(req.method, 'POST'); assert.equal(req.url, '/v1/chat/completions')
        const chunks = []; let bytes = 0
        for await (const chunk of req) { bytes += chunk.length; assert(bytes < 512 * 1024, 'Model request too large'); chunks.push(chunk) }
        const reply = mockReply(JSON.parse(Buffer.concat(chunks).toString()))
        modelCalls.push({ stage: reply.stage, at: new Date().toISOString() })
        log('local mock request', { model_stage: reply.stage })
        res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(reply.body))
      } catch (error) { log('mock rejected request', { error: error.message }); res.writeHead(400); res.end('Unsupported acceptance request') }
    })
    await new Promise((resolve, reject) => { mock.once('error', reject); mock.listen(0, '127.0.0.1', resolve) })
    summary.isolation = { origin: base, data: env.AUTOCLIP_DATA_DIR, chrome_profile: path.join(directory, 'chrome-profile'),
      text_vision_environment_cleared: true, mock_origin: `http://127.0.0.1:${mock.address().port}`, binary,
      binary_sha256: createHash('sha256').update(await fsp.readFile(binary)).digest('hex') }
    const dist = path.join(root, 'web', 'dist')
    const index = await fsp.readFile(path.join(dist, 'index.html'), 'utf8')
    const resources = ['index.html', ...Array.from(index.matchAll(/(?:src|href)="\/(assets\/[^"]+)"/g), match => match[1])]
    summary.build = { binary_mtime: (await fsp.stat(binary)).mtime.toISOString(), frontend: [] }
    for (const resource of resources) {
      const filename = inside(dist, path.join(dist, resource))
      summary.build.frontend.push({ resource, sha256: createHash('sha256').update(await fsp.readFile(filename)).digest('hex') })
    }
    const fixture = path.join(directory, 'source-80s.mp4'), subtitles = path.join(directory, 'source.srt')
    stage = 'fixture'
    await native('fixture', ffmpeg, ['-hide_banner', '-loglevel', 'error', '-y', '-f', 'lavfi', '-i', 'testsrc2=size=320x180:rate=30:duration=80',
      '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000:duration=80', '-c:v', 'libx264', '-preset', 'ultrafast', '-threads', '2',
      '-pix_fmt', 'yuv420p', '-c:a', 'aac', fixture], env)
    const stamp = s => `00:${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')},000`
    await fsp.writeFile(subtitles, Array.from({ length: 8 }, (_, i) =>
      `${i + 1}\n${stamp(i * 10)} --> ${stamp((i + 1) * 10)}\nComplete explanation sentence ${i + 1}; preserve its context.\n`).join('\n'))
    stage = 'services'
    spawnLogged('web', binary, ['web'], env)
    await probe('web health', async () => {
      try { return (await api('/health')).status === 'ok' }
      catch (error) { log('health not ready', { error: error.message }); return false }
    })
    const settings = await api('/settings')
    assert.equal(settings.text.configured, false); assert.equal(settings.vision.configured, false)
    await api('/settings/text', 'PUT', { base_url: `http://127.0.0.1:${mock.address().port}/v1`, model: 'local-parity-mock', api_key: '' })
    spawnLogged('worker', binary, ['worker'], env)
    spawnLogged('chrome', chrome, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
      '--disable-background-networking', '--disable-component-update', '--disable-sync', '--disable-extensions',
      '--disable-features=Translate,MediaRouter', '--remote-debugging-port=0', '--remote-debugging-address=127.0.0.1',
      '--autoplay-policy=no-user-gesture-required', '--window-size=1440,1000', '--lang=en-US',
      '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1, EXCLUDE localhost',
      `--user-data-dir=${summary.isolation.chrome_profile}`, 'about:blank'], env)
    const devtoolsFile = path.join(summary.isolation.chrome_profile, 'DevToolsActivePort')
    await probe('Chrome DevTools ready', () => fs.existsSync(devtoolsFile), 20000)
    const debugPort = Number((await fsp.readFile(devtoolsFile, 'utf8')).split('\n')[0])
    const tabs = await (await request(`http://127.0.0.1:${debugPort}/json/list`)).json()
    const tab = tabs.find(t => t.type === 'page')
    assert(tab, 'Chrome page target unavailable')
    socket = new WebSocket(tab.webSocketDebuggerUrl)
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('CDP websocket connection timed out')), 10000)
      socket.addEventListener('open', () => { clearTimeout(timer); resolve() }, { once: true })
      socket.addEventListener('error', () => { clearTimeout(timer); reject(new Error('CDP websocket failed')) }, { once: true })
    })
    const browserEvents = [], mutations = []
    cdp = new CDP(socket, signal, event => {
      if (event.method === 'Runtime.exceptionThrown' || event.method === 'Log.entryAdded') browserEvents.push(event)
      if (event.method === 'Network.requestWillBeSent') {
        const r = event.params.request
        if (r.url.startsWith(base) && ['POST', 'PUT', 'DELETE'].includes(r.method))
          mutations.push({ at: Date.now(), method: r.method, url: r.url, body: r.headers['Content-Type']?.includes('json') ? r.postData : undefined })
      }
    })
    await cdp.send('Page.enable'); await cdp.send('Runtime.enable'); await cdp.send('Network.enable'); await cdp.send('Log.enable')
    await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false })
    await cdp.send('Page.addScriptToEvaluateOnNewDocument', { source: `if(location.protocol==='http:')localStorage.setItem('autoclip.language','en')` })
    await cdp.send('Page.navigate', { url: base + '/' })
    stage = 'browser-upload'
    await probe('home controls', () => cdp.evaluate(`!!document.querySelector('input[type=file]')`))
    const doc = await cdp.send('DOM.getDocument')
    const files = await cdp.send('DOM.querySelectorAll', { nodeId: doc.root.nodeId, selector: 'input[type=file]' })
    assert.equal(files.nodeIds.length, 2)
    await cdp.send('DOM.setFileInputFiles', { nodeId: files.nodeIds[0], files: [fixture] })
    await cdp.send('DOM.setFileInputFiles', { nodeId: files.nodeIds[1], files: [subtitles] })
    await click('Create project')
    await probe('independent import route', () => cdp.evaluate(`location.hash.startsWith('#/import/')`), 25000)
    projectId = (await cdp.evaluate('location.hash')).split('/').at(-1)
    await probe('import and local plan', async () => {
      const state = await api(`/projects/${projectId}`)
      const failed = state.tasks.find(t => ['failed', 'interrupted'].includes(t.status))
      if (failed) throw new Error(JSON.stringify(failed))
      return state.project.status === 'source_ready' && state.project.plan
    }, 30000, 1000)
    await probe('plan displayed', () => cdp.evaluate(`!!${field('Target seconds (0 = automatic)')}`))
    await probe('review source has decoded media', () => cdp.evaluate(`(()=>{const v=document.querySelector('video');return !!v&&v.readyState>=2&&v.videoWidth>0})()`))
    assert.equal(modelCalls.length, 0, 'Import invoked a model before consent')
    assert.equal(await cdp.evaluate(`${field('Add a new subtitle layer (off by default)')}.checked`), false)
    assert.equal(await cdp.evaluate(`${field('One-click MP4 for highlight / promo')}.checked`), false)
    assert.equal(await cdp.evaluate(`${button('Confirm and start production')}.disabled`), true)
    await screenshot('01-review')
    pass('real browser file upload and independent review; no pre-confirmation model calls')
    stage = 'confirmed-production'
    // The current UI can confirm an unchanged durable plan directly. Make a real
    // user edit so this sequence specifically exercises save-before-confirm.
    await setInput('Instructions (up to 4000 UTF-8 bytes)', 'Preserve both complete explanations without forced truncation.')
    await cdp.evaluate(`${field('I understand and approve possible additional charges.')}.click()`)
    await click('Confirm and start production')
    await probe('project route after confirmation', () => cdp.evaluate(`location.hash===${JSON.stringify('#/project/' + projectId)}`))
    let previous = '', unchanged = 0, result
    result = await probe('two automatic MP4 exports', async () => {
      const state = await api(`/projects/${projectId}`)
      const signature = JSON.stringify(state.tasks.map(t => [t.id, t.status, t.stage, t.progress, t.heartbeat]))
      if (signature === previous) unchanged++; else { unchanged = 0; previous = signature; log('worker progress', { tasks: state.tasks.map(t => ({ kind: t.kind, status: t.status, stage: t.stage, progress: t.progress })) }) }
      if (unchanged >= 3) throw new Error('Three unchanged worker probes; inspect worker.log rather than repeat production')
      if (state.tasks.some(t => ['failed', 'interrupted', 'cancelled'].includes(t.status))) throw new Error(JSON.stringify(state.tasks))
      return state.exports.length === 2 && state.workflows?.every(w => w.status === 'completed') ? state : false
    }, 120000, 3000)
    assert.equal(result.drafts.length, 2)
    assert(result.drafts.every(d => d.subtitles === false && Math.abs(d.scenes.reduce((sum, s) => sum + s.end - s.start, 0) - 40) < .001))
    assert.deepEqual(modelCalls.map(c => c.stage), ['outline', 'timeline', 'scoring', 'titles'])
    await save('project-completed.json', result)
    await save('model-calls.json', modelCalls)
    await save('browser-mutations.json', mutations)
    const writes = mutations.filter(m => /\/(plan|confirm)$/.test(m.url))
    assert.deepEqual(writes.map(m => m.method), ['PUT', 'POST'])
    assert.equal(JSON.parse(writes[0].body).options.confirmed, false)
    assert.equal(JSON.parse(writes[1].body).plan_revision, result.workflows[0].plan_revision)
    // Read state after reload, not a cached success banner.
    await cdp.send('Page.reload')
    await probe('completed result cards restored', () => cdp.evaluate(`document.querySelectorAll('.studio-result-thumbnail').length===2`), 15000)
    await screenshot('02-project')
    summary.visual_observations = await cdp.evaluate(`(()=>{
      const heading=Array.from(document.querySelectorAll('h3')).find(e=>e.textContent.startsWith('Production'));
      return {workflow_heading:heading?.textContent,goal_statuses:heading?Array.from(heading.parentElement.querySelectorAll('[role="status"] > b'),e=>e.textContent):[]}
    })()`)
    assertWorkflowSeparators(summary.visual_observations.workflow_heading, summary.visual_observations.goal_statuses)
    pass('workflow and goal status use middle-dot separators without literal question marks', summary.visual_observations)
    pass('content automatically exported two 40-second files', { model_calls: modelCalls.length, workflow: result.workflows[0].status })
    stage = 'downloads'
    const downloads = []
    for (const output of result.exports) {
      const url = base + `/api/v1/projects/${projectId}/exports/${output.task_id}/video?download=true`
      const response = await request(url)
      const data = Buffer.from(await response.arrayBuffer())
      const filename = path.join(directory, `export-${output.task_id}.mp4`)
      await fsp.writeFile(filename, data)
      await native(`probe-${output.task_id}`, ffprobe, ['-v', 'error', '-show_format', '-show_streams', '-of', 'json', filename], env, 10000)
      const info = JSON.parse(await fsp.readFile(path.join(directory, `probe-${output.task_id}.log`), 'utf8'))
      assert(Math.abs(Number(info.format.duration) - 40) < .04)
      assert(info.streams.some(s => s.codec_type === 'audio'))
      await native(`decode-${output.task_id}`, ffmpeg, ['-v', 'error', '-i', filename, '-f', 'null', '-'], env, 20000)
      const ranged = await request(url, { headers: { Range: 'bytes=0-31' } })
      assert.equal(ranged.status, 206); assert.equal((await ranged.arrayBuffer()).byteLength, 32)
      downloads.push({ task_id: output.task_id, bytes: data.length, sha256: createHash('sha256').update(data).digest('hex'),
        duration: Number(info.format.duration), video_codec: info.streams.find(s => s.codec_type === 'video').codec_name, full_decode: 'passed', range: 206 })
    }
    await save('downloads.json', downloads); pass('HTTP downloads, ffprobe durations/audio, range streaming and full decode', { downloads })
    stage = 'editor-playback'
    const draft = [...result.drafts].sort((a, b) => a.scenes[0].start - b.scenes[0].start)[0]
    await cdp.evaluate(`document.querySelector('a[href$="/studio/${draft.id}"]').click()`)
    await probe('editor full draft media ready', () => cdp.evaluate(`(()=>{const v=document.querySelector('video[aria-label="Full draft playback"]');return !!v&&v.readyState>=2})()`))
    const mediaLayout = await cdp.evaluate(`(()=>{const v=document.querySelector('video');const ancestors=[];for(let e=v;e&&ancestors.length<5;e=e.parentElement){const r=e.getBoundingClientRect(),s=getComputedStyle(e);ancestors.push({tag:e.tagName,class:e.className,width:r.width,height:r.height,display:s.display,visibility:s.visibility,position:s.position,contain:s.contain,containerType:s.containerType,overflow:s.overflow})}return {videoWidth:v.videoWidth,videoHeight:v.videoHeight,readyState:v.readyState,ancestors}})()`)
    await save('editor-media-layout.json', mediaLayout)
    await screenshot('03-editor-before-playback')
    await cdp.evaluate(`(()=>{const v=document.querySelector('video');window.__playback=[];v.addEventListener('timeupdate',()=>window.__playback.push({t:v.currentTime,paused:v.paused,wall:performance.now()}));v.playbackRate=8;return v.play()})()`)
    await probe('full 40-second draft plays and stops at its end', () => cdp.evaluate(`(()=>{const v=document.querySelector('video');return v.paused&&v.currentTime>=39.9})()`), 20000, 300)
    await save('full-playback.json', await cdp.evaluate('window.__playback'))
    pass('real browser decoded and played the complete 40-second draft at 8x')
    await cdp.evaluate(`document.querySelector('.studio-source-details').open=true`)
    for (const [start, end] of [[50, 52], [60, 62]]) {
      await setInput('Manual start (seconds)', start); await setInput('Manual end (seconds)', end)
      await click('Add manual range')
    }
    await probe('three manual/editor ranges', () => cdp.evaluate(`document.querySelectorAll('.studio-scene-row').length===3`))
    await click('Source / choose cut points')
    await probe('unrestricted source media', () => cdp.evaluate(`!!document.querySelector('video[aria-label="Source playback"]')`))
    await cdp.evaluate(`document.querySelector('video').currentTime=55`)
    await click('Use current source time')
    assert.equal(Number(await cdp.evaluate(`${field('Manual start (seconds)')}.value`)), 55)
    await click('Play full draft')
    const bounds = await cdp.evaluate(`(()=>{const e=document.querySelector('.studio-draft-timeline input');e.scrollIntoView({block:'center'});const r=e.getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height,max:Number(e.max)}})()`)
    assert.equal(bounds.max, 44)
    const x0 = bounds.x + 8, y = bounds.y + bounds.h / 2, x1 = x0 + (bounds.w - 16) * 38.5 / 44
    await cdp.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: x0, y, button: 'left', clickCount: 1 })
    await cdp.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: x1, y, button: 'left', buttons: 1 })
    await cdp.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: x1, y, button: 'left', clickCount: 1 })
    const sought = await cdp.evaluate('document.querySelector("video").currentTime')
    assert(sought > 37 && sought < 40, `Real slider drag sought unexpected source time: ${sought}`)
    await cdp.evaluate(`window.__playback=[];document.querySelector('video').playbackRate=2;document.querySelector('video').play()`)
    await probe('multi-range playback final stop', () => cdp.evaluate(`(()=>{const v=document.querySelector('video');return v.paused&&v.currentTime>=61.9})()`), 15000, 200)
    const sequence = await cdp.evaluate('window.__playback')
    assert(sequence.some(s => s.t >= 50 && s.t <= 52.1), 'Second range was not played')
    assert(sequence.some(s => s.t >= 60 && s.t <= 62.1), 'Third range was not played')
    await save('multi-range-playback.json', sequence)
    await screenshot('03-editor')
    pass('actual slider mouse drag, original-source selection, three-range continuous preview', { sought, ranges: [[0, 40], [50, 52], [60, 62]] })
    await save('browser-events.json', browserEvents)
    assert(mediaLayout.ancestors[0].width >= 100 && mediaLayout.ancestors[0].height >= 90 &&
      mediaLayout.ancestors[1].height >= 90, 'Editor video is decoded but its visible layout is collapsed; see editor-media-layout.json and screenshots')
    assert(!browserEvents.some(e => e.method === 'Runtime.exceptionThrown'), 'Unhandled browser exception; see browser-events.json')
    assert.equal(createHash('sha256').update(await fsp.readFile(binary)).digest('hex'), summary.isolation.binary_sha256, 'Executable changed during acceptance')
    for (const resource of summary.build.frontend) {
      assert.equal(createHash('sha256').update(await fsp.readFile(path.join(dist, resource.resource))).digest('hex'),
        resource.sha256, 'Frontend resource changed during acceptance')
    }
    pass('binary and frontend resource hashes remained unchanged during acceptance')
    summary.status = 'passed'
  } catch (error) {
    summary.status = 'failed'; summary.failed_stage = stage; summary.defects.push(error.stack ?? String(error))
    log('FAIL', { error: error.message })
    if (cdp && !signal.aborted) {
      try { await screenshot('failure') } catch (captureError) { log('failure capture unavailable', { error: captureError.message }) }
    }
    if (base && projectId && !signal.aborted) {
      try { await save('project-at-failure.json', await api(`/projects/${projectId}`)) }
      catch (captureError) { log('project snapshot unavailable', { error: captureError.message }) }
    }
  } finally {
    stage = 'cleanup'
    clearTimeout(deadline); controller.abort(new Error('Acceptance finished'))
    if (socket) socket.close()
    await Promise.all([...children].reverse().map(kill))
    if (mock) { mock.closeAllConnections(); await new Promise(resolve => mock.close(resolve)) }
    for (const fd of handles) fs.closeSync(fd)
    summary.finished_at = new Date().toISOString(); summary.elapsed_ms = Date.now() - started
    summary.processes = children.map(({ name, child }) => ({ name, pid: child.pid, exit_code: child.exitCode, signal: child.signalCode }))
    if (summary.defects.length && summary.status === 'passed') summary.status = 'failed'
    await save('summary.json', summary)
    await fsp.writeFile(path.join(evidenceRoot, 'latest.json'), JSON.stringify({ directory, status: summary.status }, null, 2) + '\n')
    console.log(JSON.stringify({ status: summary.status, evidence: directory, elapsed_ms: summary.elapsed_ms }))
  }
  return summary
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  run().then(result => { process.exitCode = result.status === 'passed' ? 0 : 1 })
    .catch(error => { console.error(error); process.exitCode = 1 })
}
