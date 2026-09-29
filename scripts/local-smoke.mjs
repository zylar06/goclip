// Live two-process smoke test. No cloud calls; all data stays in a fresh
// artifacts directory. Needs an already-built binary and native FFmpeg tools.
import assert from 'node:assert/strict'
import { spawn, execFile } from 'node:child_process'
import { promisify } from 'node:util'
import fs from 'node:fs'
import fsp from 'node:fs/promises'
import path from 'node:path'
import net from 'node:net'
const exec=promisify(execFile)
const root=process.cwd()
const binary=path.resolve(process.argv[2]||'artifacts/autoclip.exe')
const ffmpeg=process.env.FFMPEG_PATH||'ffmpeg'
const working=await fsp.mkdtemp(path.join(root,'artifacts','live-smoke-'))
const port=await new Promise((resolve,reject)=>{
  const s=net.createServer();s.once('error',reject)
  s.listen(0,'127.0.0.1',()=>{const port=s.address().port;s.close(err=>err?reject(err):resolve(port))})
})
const base=`http://127.0.0.1:${port}/api/v1`
const env={...process.env,AUTOCLIP_ADDR:`127.0.0.1:${port}`,AUTOCLIP_DATA_DIR:path.join(working,'data'),
  AUTOCLIP_WEB_DIR:path.join(root,'web','dist'),FONT_DIR:path.join(root,'assets','fonts'),AUTOCLIP_URL:base}
let children=[],handles=[]
const launch=mode=>{
  const fd=fs.openSync(path.join(working,`${mode}.log`),'a');handles.push(fd)
  const child=spawn(binary,[mode],{env,cwd:root,windowsHide:true,stdio:['ignore',fd,fd]})
  child.on('error',error=>console.error(`${mode} process:`,error))
  children.push(child)
  return child
}
async function stop(){
  await Promise.all(children.map(child=>new Promise(resolve=>{
    if(child.exitCode!==null||child.signalCode!==null)return resolve()
    child.once('exit',resolve);child.kill('SIGTERM')
    const timeout=setTimeout(()=>{if(child.exitCode===null&&child.signalCode===null)child.kill('SIGKILL');resolve()},5000)
    timeout.unref()
  })))
  children=[];for(const fd of handles)fs.closeSync(fd);handles=[]
}
const timer=setTimeout(()=>{console.error('Live smoke exceeded 3-minute budget');for(const child of children)child.kill('SIGKILL');process.exitCode=1},180000)
async function healthy(){
  for(let attempt=0;attempt<30;attempt++){
    try{
      const r=await fetch(base+'/health',{signal:AbortSignal.timeout(1000)})
      if(r.ok)return
    }catch(error){
      if(attempt===29)throw new Error('API readiness deadline exceeded',{cause:error})
    }
    if(children.some(c=>c.exitCode!==null))throw new Error('Service exited before health')
    await new Promise(resolve=>setTimeout(resolve,250))
  }
  throw new Error('API did not become healthy')
}
try{
  launch('web');await healthy();launch('worker')
  const fixture=path.join(working,'fixture.mp4')
  await exec(ffmpeg,['-hide_banner','-loglevel','error','-y','-f','lavfi','-i','testsrc2=size=320x180:rate=30',
    '-f','lavfi','-i','anullsrc=r=48000:cl=stereo','-t','5','-c:v','libx264','-pix_fmt','yuv420p','-c:a','aac',fixture],
    {timeout:30000,windowsHide:true})
  const out=await exec(process.execPath,['scripts/container-smoke.mjs',fixture],{env,cwd:root,timeout:130000,windowsHide:true})
  console.log(out.stdout)
  if(out.stderr)console.error(out.stderr)
  const before=await (await fetch(base+'/projects',{signal:AbortSignal.timeout(3000)})).json()
  assert.equal(before.length,1)
  await stop()
  launch('web');await healthy();launch('worker')
  const after=await (await fetch(base+`/projects/${before[0].id}`,{signal:AbortSignal.timeout(3000)})).json()
  assert.equal(after.project.status,'exported')
  assert.equal(after.exports.length,1)
  assert(after.tasks.every(t=>t.status==='completed'))
  const page=await fetch(`http://127.0.0.1:${port}/`,{signal:AbortSignal.timeout(3000)})
  assert.equal(page.status,200);assert((await page.text()).includes('<div id="root">'))
  console.log('PASS live API + worker + built React + restart persistence')
  console.log('Logs and playable MP4 retained:',working)
}finally{
  clearTimeout(timer);await stop()
}
