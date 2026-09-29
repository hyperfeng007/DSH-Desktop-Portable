// Real bundled Undici + native Node fetch; invoked by Go with ephemeral trusted TLS fixtures.
import {createRequire} from 'node:module';
import fs from 'node:fs';
import {pathToFileURL} from 'node:url';
import http from 'node:http';
import https from 'node:https';
import http2 from 'node:http2';
import net from 'node:net';
import {gzipSync,deflateSync,brotliCompressSync} from 'node:zlib';
import assert from 'node:assert/strict';
import {installPortableNodeProxy} from '../assets/portable-node-proxy.mjs';
const req=createRequire(process.env.DSH_TEST_RUNTIME+'/package.json'),undici=req('undici');
const expected={data:Array.from({length:12},(_,i)=>({id:`fixture-model-${i}`,name:'测试模型',object:'model',created:1727310000,owned_by:'fixture'}))};
const json=Buffer.from(JSON.stringify(expected));
const oldAdapter=process.env.DSH_TEST_OLD_ADAPTER;
const install=oldAdapter ? (await import(pathToFileURL(oldAdapter).href)).installPortableNodeProxy : installPortableNodeProxy;
let encoding='identity';let version;
function handler(r,w){version=r.httpVersion;if(r.url==='/unauthorized'){assert.equal(r.headers.authorization,'Bearer wrong-local-key');w.writeHead(401,{'content-type':'application/json'});w.end('{"error":"denied"}');return;}assert.equal(r.headers.authorization,'Bearer local-test-key');assert.equal(r.headers['proxy-authorization'],undefined);let body=json;if(encoding==='gzip')body=gzipSync(json);if(encoding==='deflate')body=deflateSync(json);if(encoding==='br')body=brotliCompressSync(json);w.setHeader('content-type','application/json; charset=utf-8');if(encoding!=='identity')w.setHeader('content-encoding',encoding);w.setHeader('content-length',body.length);w.end(body);}
const tls={key:fs.readFileSync(process.env.DSH_TEST_TLS_KEY),cert:fs.readFileSync(process.env.DSH_TEST_TLS_CERT)};
const h1=https.createServer(tls,handler),h2=http2.createSecureServer({...tls,allowHTTP1:true},handler);
const listen=s=>new Promise(resolve=>s.listen(0,'127.0.0.1',resolve));
await listen(h1);await listen(h2);
const proxy=http.createServer();let port=h1.address().port;
proxy.on('connect',(r,s,head)=>{assert.equal(r.headers['proxy-authorization'],'Basic '+Buffer.from('dsh:fixture-token').toString('base64'));const remote=net.connect(port,'127.0.0.1',()=>{s.write('HTTP/1.1 200 Connection Established\r\n\r\n');if(head.length)remote.write(head);s.pipe(remote);remote.pipe(s);});remote.on('error',()=>s.destroy());s.on('error',()=>remote.destroy());s.on('close',()=>remote.destroy());remote.on('close',()=>s.destroy());});
await listen(proxy);
process.env.DSH_PORTABLE_PROXY=`http://127.0.0.1:${proxy.address().port}`;process.env.DSH_PORTABLE_PROXY_TOKEN='fixture-token';
for(const [name,server] of [['h1',h1],['h2',h2]]){
 port=server.address().port;
 const dispose=await install('local-compression-fixture',undici);
 try{for(encoding of ['identity','gzip','deflate','br']){
 const r=await fetch(`https://models.portable.invalid/v1/models`,{headers:{accept:'application/json',authorization:'Bearer local-test-key'}});
 const broken=Boolean(oldAdapter)&&name==='h2';
 assert.equal(version,broken?'2.0':'1.1');assert.equal(r.headers.get('content-type'),broken?null:'application/json; charset=utf-8');assert.equal(r.headers.get('content-encoding'),broken||encoding==='identity'?null:encoding);
 const b=new Uint8Array(await r.arrayBuffer());let result,parseFailed=false;
 try{result=JSON.parse(new TextDecoder().decode(b));}catch{parseFailed=true;}
 const expectedFailure=broken&&encoding!=='identity';assert.equal(parseFailed,expectedFailure);
 if(!expectedFailure)assert.deepEqual(result,expected);
 console.log(JSON.stringify({fixture:name,protocol:version,encoding,status:r.status,contentTypeVisible:r.headers.has('content-type'),bodyBytes:b.length,models:result?.data.length??null,result:expectedFailure?'EXPECTED-JSON-FAILURE-REPRODUCED':'PASS'}));
 }
 const denied=await fetch('https://models.portable.invalid/unauthorized',{headers:{authorization:'Bearer wrong-local-key'}});
 assert.equal(denied.status,401);assert.equal(denied.ok,false);assert.deepEqual(await denied.json(),{error:'denied'});
 console.log(JSON.stringify({fixture:name,protocol:version,probe:'wrong-key',status:denied.status,result:'PASS'}));
 }finally{await dispose();}
}
await new Promise(r=>proxy.close(r));await new Promise(r=>h1.close(r));await new Promise(r=>h2.close(r));
