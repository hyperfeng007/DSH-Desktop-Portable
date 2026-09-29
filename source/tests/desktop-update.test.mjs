import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {installPortableUpdate} from '../assets/portable-update.mjs';
async function scenario(latest,answer){
 const dir=fs.mkdtempSync(path.join(os.tmpdir(),'portable-update-'));let quit=0,reads=0;const dialogs=[];
 const app={getVersion:()=> '0.1.7-rc.2',quit(){quit++}};
 const dialog={async showMessageBox(options){dialogs.push(options);return {response:answer}}};
 const timer=setInterval(()=>{
  try{
   const request=path.join(dir,'update-check.request.json');const r=JSON.parse(fs.readFileSync(request,'utf8'));fs.unlinkSync(request);reads++;
   assert.equal(r.nonce,'session-secret');fs.writeFileSync(path.join(dir,'update-check.response.json'),JSON.stringify({...r,version:latest}));
  }catch{}
 },10);
 try{installPortableUpdate({app,dialog},dir,'session-secret');await Promise.all([globalThis.__dshPortableCheckUpdate(),globalThis.__dshPortableCheckUpdate()]);return {quit,reads,dialogs,install:fs.existsSync(path.join(dir,'update-install.request'))?fs.readFileSync(path.join(dir,'update-install.request'),'utf8'):null}}
 finally{clearInterval(timer);fs.rmSync(dir,{recursive:true,force:true});delete globalThis.__dshPortableCheckUpdate;}
}
test('up-to-date desktop check delegates once to launcher and does not quit',async()=>{
 const r=await scenario('0.1.7-rc.2',0);assert.equal(r.reads,1);assert.equal(r.quit,0);assert.equal(r.install,null);assert.match(r.dialogs[0].message,/启动器的网络代理/);
});
test('update cancel does not schedule installation or quit',async()=>{
 const r=await scenario('0.1.8',0);assert.equal(r.quit,0);assert.equal(r.install,null);
});
test('approved portable update schedules nonce-bound handoff and requests ordinary quit',async()=>{
 const r=await scenario('0.1.8',1);assert.equal(r.quit,1);assert.equal(r.install,'session-secret');
});
