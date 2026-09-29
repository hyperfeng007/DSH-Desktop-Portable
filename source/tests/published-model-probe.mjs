// Invoked by Go integration tests against the JS extracted from the signed-download payload.
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
import path from 'node:path';
import assert from 'node:assert/strict';
import {installPortableNodeProxy} from '../assets/portable-node-proxy.mjs';
const req=createRequire(path.join(process.env.DSH_TEST_RUNTIME,'package.json'));
const load=async name=>import(pathToFileURL(req.resolve(name)).href);
const undici=req('undici');
const {Context}=await load('@deepseek-ai/cordis');
const Llm=await load('@deepseek-ai/dsh-llm');
const Pi=await load('@deepseek-ai/dsh-llm-pi-ai');
let dispose;
let replaced;
if(process.env.DSH_TEST_BASELINE==='1'){
 const proxyModule=await load('@deepseek-ai/dsh-http-proxy');
 const value='http://dsh:'+process.env.DSH_PORTABLE_PROXY_TOKEN+'@'+new URL(process.env.DSH_PORTABLE_PROXY).host;
 dispose=await proxyModule.installProxyFromEnvironment({get:name=>['https_proxy','http_proxy','all_proxy','HTTPS_PROXY','HTTP_PROXY','ALL_PROXY'].includes(name)?{value}:undefined},()=>{});
}else{
 dispose=await installPortableNodeProxy('host-integration',undici);
 replaced=new undici.Agent();undici.setGlobalDispatcher(replaced);
}
const ctx=new Context();
try{
 await ctx.plugin(Llm.default);
 await ctx.plugin(Pi,{});
 const result=await ctx.llm.discoverModels('llm-pi-ai',{baseURL:process.env.DSH_TEST_ORIGIN+'/v1',api:'openai-completions',apiKey:'fixture-model-key'});
 assert.deepEqual(result.map(m=>m.id),['fixture-model']);
 const loopback=await fetch(process.env.DSH_TEST_LOOPBACK+'/health');assert.equal(await loopback.text(),'local-ok');
 console.log(process.env.DSH_TEST_BASELINE==='1'?'PASS published upstream model discovery via bridge':'PASS published model discovery via explicit bridge dispatcher, despite global dispatcher replacement');
}finally{
 await ctx.fiber.dispose();await dispose();await replaced?.close();
}
