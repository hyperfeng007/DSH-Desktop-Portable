// Node-only portability-entry tests. These mock Electron and do not certify DSH/Electron.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
const source=fs.readFileSync(new URL('../assets/portable.mjs',import.meta.url),'utf8');
function fixture() {
 const root=fs.mkdtempSync(path.join(os.tmpdir(),'dsh-编码测试-'));
 const dir=path.join(root,"中文 空格 # % & ' 😀𠀀");fs.mkdirSync(dir);
 fs.writeFileSync(path.join(dir,'portable.mjs'),source.replaceAll('__ORIGINAL_MAIN__','upstream.mjs'));
 fs.writeFileSync(path.join(dir,'portable-node-proxy.mjs'),"export async function installPortableNodeProxy(){};export function networkLog(){};");
 fs.writeFileSync(path.join(dir,'portable-network.mjs'),"export function configureElectronProxy(){return Promise.resolve()}");
 fs.writeFileSync(path.join(dir,'portable-update.mjs'),"export function installPortableUpdate(){}");
 fs.writeFileSync(path.join(dir,'electron.mjs'),`
  import {EventEmitter} from 'node:events';
  export const session={};export const net={};
  export const app=new EventEmitter();
  app.paths={};app.switches={};
  app.setPath=(k,v)=>{if(process.env.TEST_SET_PATH_FAIL)throw Error('fixture path error 中文');app.paths[k]=v};
  app.setAppLogsPath=()=>{};
  app.commandLine={appendSwitch:(k,v)=>app.switches[k]=v};
  app.quit=()=>{};app.exit=c=>process.exit(c);
  export const dialog={showErrorBox:()=>{}};
 `);
 fs.writeFileSync(path.join(dir,'loader.mjs'),`
  export async function resolve(specifier,context,nextResolve){
   if(specifier==='electron')return {url:new URL('./electron.mjs',import.meta.url).href,shortCircuit:true};
   return nextResolve(specifier,context);
  }
 `);
 fs.writeFileSync(path.join(dir,'upstream.mjs'),`
  import {app} from 'electron';
  import {EventEmitter} from 'node:events';
  import fs from 'node:fs';
  import path from 'node:path';
  const expected=path.join(process.env.DSH_PORTABLE_ROOT,'Data');
  for(const v of Object.values(app.paths)){if(!v.startsWith(expected+path.sep))throw Error('directory escaped USB')}
  if(!app.paths.userData||app.setAsDefaultProtocolClient('dsh')!==false)throw Error('init order violated');
  const w=new EventEmitter();w.webContents=new EventEmitter();
  w.isDestroyed=()=>false;w.isVisible=()=>true;
  w.webContents.isDestroyed=()=>false;w.webContents.isLoadingMainFrame=()=>false;
  w.webContents.getURL=()=> 'dsh-app://app/';
  app.emit('browser-window-created',{},w);w.emit('show');
  fs.writeFileSync(path.join(process.env.DSH_PORTABLE_ROOT,'original-imported'),'ok');
 `);
 const run=path.join(dir,'Run');fs.mkdirSync(run);
 return {root,dir,run,execute(extra={}){
  return spawnSync(process.execPath,['--no-warnings','--experimental-loader',pathToFileURL(path.join(dir,'loader.mjs')).href,path.join(dir,'portable.mjs')],{encoding:'utf8',timeout:10000,env:{...process.env,DSH_PORTABLE_ROOT:dir,DSH_PORTABLE_RUN:run,DSH_PORTABLE_NONCE:'中文nonce😀',DSH_PORTABLE_PROXY:'http://127.0.0.1:9999',DSH_PORTABLE_PROXY_TOKEN:'test-token',...extra}});
 },close(){fs.rmSync(root,{recursive:true,force:true})}};
}
test('Unicode/space/#/% path imports original entry and publishes readiness',()=>{
 const f=fixture();try{const r=f.execute();assert.equal(r.status,0,r.stderr);assert.equal(fs.readFileSync(path.join(f.run,'ready'),'utf8'),'中文nonce😀');assert.ok(fs.existsSync(path.join(f.dir,'original-imported')))}finally{f.close()}
});
test('pre-import initialization failure is logged and exits nonzero',()=>{
 const f=fixture();try{const r=f.execute({TEST_SET_PATH_FAIL:'1'});assert.equal(r.status,1,r.stderr);assert.match(fs.readFileSync(path.join(f.dir,'Data','Logs','bootstrap-error.txt'),'utf8'),/fixture path error 中文/);assert.ok(!fs.existsSync(path.join(f.dir,'original-imported')))}finally{f.close()}
});
test('missing launch environment produces a diagnosed failure',()=>{
 const f=fixture();try{const r=f.execute({DSH_PORTABLE_RUN:''});assert.equal(r.status,1,r.stderr);assert.match(fs.readFileSync(path.join(f.dir,'Data','Logs','bootstrap-error.txt'),'utf8'),/Please start DSH/)}finally{f.close()}
});
