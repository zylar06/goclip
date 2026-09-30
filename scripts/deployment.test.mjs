import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import { execFileSync } from 'node:child_process'
const command=process.env.COMPOSE_EXE||'docker'
const prefix=process.env.COMPOSE_EXE?[]:['compose']
const config=JSON.parse(execFileSync(command,[...prefix,'-f','compose.yaml','config','--format','json'],{
  encoding:'utf8',timeout:15000,windowsHide:true,env:{
    ...process.env,BIND_IP:'127.0.0.1',PORT:'8080',
    APT_SOURCE_MODE:'mirror',DEBIAN_MIRROR:'https://deb.debian.org/debian',
    DEBIAN_SECURITY_MIRROR:'https://deb.debian.org/debian-security',DEBIAN_SNAPSHOT:'20260901T000000Z',
    AUTOCLIP_TEXT_BASE_URL:'https://text.example/v1',AUTOCLIP_TEXT_MODEL:'test-text',
    AUTOCLIP_TEXT_API_KEY:'test-text-not-a-real-key',
    AUTOCLIP_VISION_BASE_URL:'https://vision.example/v1',AUTOCLIP_VISION_MODEL:'test-vision',
    AUTOCLIP_VISION_API_KEY:'test-vision-not-a-real-key',
  },
}))
const lock=JSON.parse(fs.readFileSync('tools.lock.json','utf8'))
const dockerfile=fs.readFileSync('Dockerfile','utf8')
test('one shared image, local durable volumes and separate web/worker commands',()=>{
  const {web,worker}=config.services
  assert.equal(web.image,worker.image)
  assert.deepEqual(web.command,['web']);assert.deepEqual(worker.command,['worker'])
  for(const service of [web,worker]){
    assert.equal(service.platform,undefined,'let Docker select the native amd64 or arm64 image')
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
    for(const [field, checksum] of Object.entries(lock[name]).filter(([field])=>field.endsWith('sha256'))){
      assert.match(checksum,/^[a-f0-9]{64}$/)
      assert(dockerfile.includes(checksum),`unused checksum ${name}.${field}`)
    }
  }
  assert(dockerfile.includes('COPY api/ ./api/'),'frontend contract must be available at build time')
  assert(dockerfile.includes('USER 10001:10001'),'runtime is not root')
})
test('only web builds the shared image; worker never pulls a different copy',()=>{
  assert(config.services.web.build)
  assert(!config.services.worker.build)
  assert.equal(config.services.worker.pull_policy,'never')
  assert.equal(config.services.web.build.args.APT_SOURCE_MODE,lock.debian_source_default)
  assert.equal(config.services.web.build.args.DEBIAN_MIRROR,lock.debian_mirrors.main)
  assert.equal(config.services.web.build.args.DEBIAN_SECURITY_MIRROR,lock.debian_mirrors.security)
  assert.equal(config.services.web.build.args.DEBIAN_SNAPSHOT,lock.debian_snapshot)
})
test('APT, npm and Go downloads are cached without disabling build-time tests',()=>{
  assert(!/^#\s*syntax=/m.test(dockerfile),'do not require an extra frontend image pull')
  assert(dockerfile.includes('FROM go-base AS backend'))
  assert(dockerfile.includes('COPY --from=go-base /etc/ssl/certs/ca-certificates.crt'))
  assert(dockerfile.includes('FROM apt-base AS native'))
  assert(dockerfile.includes('FROM apt-base AS runtime'))
  for(const id of ['autoclip-apt-native-${TARGETARCH}','autoclip-apt-engine-${TARGETARCH}','autoclip-apt-runtime-${TARGETARCH}']){
    assert(dockerfile.includes(`id=${id},target=/var/cache/apt,sharing=locked`))
  }
  for(const target of ['/root/.npm','/go/pkg/mod','/root/.cache/go-build']){
    assert(dockerfile.includes(`target=${target}`),`missing dependency cache ${target}`)
  }
  assert(dockerfile.includes('COPY engine/requirements.txt ./requirements.txt'))
  assert(dockerfile.includes('AUTOCLIP_ENGINE_PYTHON=/opt/goclip-engine/bin/python'))
  const engineRequirements=fs.readFileSync('engine/requirements.txt','utf8')
  assert.match(engineRequirements,/autoclip @ git\+https:\/\/github\.com\/artbyjazi\/autoclip\.git@5d0eac36fa615b79dd2104083bf273a96f8d68bb/)
  assert(dockerfile.includes('npm run typecheck && npm test && npm run build'))
  assert(dockerfile.includes('go test -timeout 180s ./...'))
})
test('model environment reaches runtime only; secrets never become build arguments',()=>{
  const expected={
    AUTOCLIP_TEXT_BASE_URL:'https://text.example/v1',AUTOCLIP_TEXT_MODEL:'test-text',
    AUTOCLIP_TEXT_API_KEY:'test-text-not-a-real-key',
    AUTOCLIP_VISION_BASE_URL:'https://vision.example/v1',AUTOCLIP_VISION_MODEL:'test-vision',
    AUTOCLIP_VISION_API_KEY:'test-vision-not-a-real-key',
  }
  for(const service of Object.values(config.services)){
    for(const [key,value] of Object.entries(expected)){
      assert.equal(service.environment[key],value)
      assert(!(key in (service.build?.args||{})))
      assert(!dockerfile.includes(key),'credentials must not be baked into the image')
    }
  }
  const ignored=fs.readFileSync('.dockerignore','utf8').split(/\r?\n/)
  assert(ignored.includes('.env'),'local secrets must not enter build context')
  const gitignore=fs.readFileSync('.gitignore','utf8').split(/\r?\n/)
  assert(gitignore.includes('.env'),'local secrets must not enter Git')
  const example=fs.readFileSync('.env.example','utf8')
  for(const key of Object.keys(expected)){
    assert(example.split(/\r?\n/).includes(`${key}=`),'example must contain empty placeholders only')
  }
})
