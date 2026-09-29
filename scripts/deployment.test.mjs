import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import { execFileSync } from 'node:child_process'
const command=process.env.COMPOSE_EXE||'docker'
const prefix=process.env.COMPOSE_EXE?[]:['compose']
const config=JSON.parse(execFileSync(command,[...prefix,'-f','compose.yaml','config','--format','json'],{
  encoding:'utf8',timeout:15000,windowsHide:true,env:{...process.env,BIND_IP:'127.0.0.1',PORT:'8080'},
}))
const lock=JSON.parse(fs.readFileSync('tools.lock.json','utf8'))
const dockerfile=fs.readFileSync('Dockerfile','utf8')
test('one shared image, local durable volumes and separate web/worker commands',()=>{
  const {web,worker}=config.services
  assert.equal(web.image,worker.image)
  assert.deepEqual(web.command,['web']);assert.deepEqual(worker.command,['worker'])
  for(const service of [web,worker]){
    assert.equal(service.platform,'linux/amd64')
    assert(service.volumes.some(v=>v.type==='volume'&&v.source==='data'&&v.target==='/data'))
    assert(service.volumes.some(v=>v.type==='volume'&&v.source==='models'&&v.target==='/models'))
    assert.equal(service.read_only,true);assert(service.cap_drop.includes('ALL'))
    assert(service.security_opt.includes('no-new-privileges:true'))
  }
})
test('loopback-only default binding and non-circular readiness',()=>{
  const {web,worker}=config.services
  assert.equal(web.ports.length,1);assert.equal(web.ports[0].host_ip,'127.0.0.1')
  assert.equal(web.ports[0].target,8080);assert(!worker.ports?.length)
  assert.equal(worker.depends_on.web.condition,'service_healthy')
  assert(!web.depends_on)
  assert.deepEqual(web.healthcheck.test,['CMD','autoclip','healthcheck'])
  assert.deepEqual(worker.healthcheck.test,['CMD','autoclip','healthcheck','worker'])
})
test('Dockerfile consumes the verified image/tool/model lock',()=>{
  for(const [image,digest] of Object.entries(lock.base_images)){
    assert.match(digest,/^sha256:[a-f0-9]{64}$/)
    assert(dockerfile.includes(`${image}@${digest}`),`unused pinned image ${image}`)
  }
  for(const name of ['whisper_cpp','whisper_model','yt_dlp','deno']){
    assert.match(lock[name].sha256,/^[a-f0-9]{64}$/)
    assert(dockerfile.includes(lock[name].sha256),`unused checksum ${name}`)
  }
  assert(dockerfile.includes('COPY api/ ./api/'),'frontend contract must be available at build time')
  assert(dockerfile.includes('USER 10001:10001'),'runtime is not root')
})
