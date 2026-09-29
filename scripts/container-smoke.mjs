// No paid calls. Run after scripts/smoke-docker.sh supplies a synthetic video.
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
const base=process.env.AUTOCLIP_URL||'http://127.0.0.1:8080/api/v1'
const [fixture]=process.argv.slice(2)
if(!fixture)throw new Error('Usage: node scripts/container-smoke.mjs fixture.mp4')
async function api(path,options={}){
  const response=await fetch(base+path,{...options,signal:AbortSignal.timeout(30000)})
  if(!response.ok)throw new Error(`${response.status}: ${await response.text()}`)
  return response.json()
}
const post=(path,data)=>api(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)})
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms))
async function wait(pid,taskId){
  const deadline=Date.now()+120000
  let previous='',unchanged=0
  while(Date.now()<deadline){
    const state=await api(`/projects/${pid}`)
    const task=taskId?state.tasks.find(t=>t.id===taskId):state.tasks[0]
    if(task.status==='completed')return state
    if(['failed','interrupted','cancelled'].includes(task.status))throw new Error(JSON.stringify(task))
    const signature=JSON.stringify([task.status,task.stage,task.progress,task.heartbeat])
    if(signature===previous){if(++unchanged>=3)throw new Error('Three unchanged task probes; inspect worker logs')}
    else{previous=signature;unchanged=0;console.log(task.kind,task.status,task.stage,task.progress)}
    await sleep(2000)
  }
  throw new Error('Container task exceeded two-minute smoke deadline')
}
assert.equal((await api('/health')).status,'ok')
const body=new FormData()
body.append('name','容器验收')
body.append('video',new Blob([await fs.readFile(fixture)],{type:'video/mp4'}),'fixture.mp4')
body.append('subtitle',new Blob(['1\n00:00:00,000 --> 00:00:02,000\n容器验收\n']),'fixture.srt')
const project=await api('/projects',{method:'POST',body})
console.log('project',project.id)
await wait(project.id)
const draft=await post(`/projects/${project.id}/drafts`,{
  title:'Docker 中文测试',hook:'GO 自动剪辑',scenes:[{id:'scene1',label:'test',start:0,end:2,evidence:''}],
  language:'source',aspect:'original',layout:'fit',crop_x:.5,title_style:'comic',
  title_template_version:6,title_motion:true,title_scale:1,title_y:.12,
  title_accent:null,subtitles:true,original_audio:true,revision:1
})
const task=await post(`/projects/${project.id}/drafts/${draft.id}/export`,{revision:draft.revision})
const state=await wait(project.id,task.id)
assert.equal(state.exports.length,1)
const res=await fetch(base+`/projects/${project.id}/exports/${task.id}/video`,{headers:{Range:'bytes=0-31'},signal:AbortSignal.timeout(30000)})
assert.equal(res.status,206)
const bytes=new Uint8Array(await res.arrayBuffer())
assert.equal(bytes.length,32)
assert.equal(new TextDecoder().decode(bytes.slice(4,8)),'ftyp')
console.log('PASS API import → persisted draft → real FFmpeg MP4 → Range download')
// Test project deliberately retained for inspection/persistence checks.
