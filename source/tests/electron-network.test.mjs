import test from 'node:test';
import assert from 'node:assert/strict';
import {EventEmitter} from 'node:events';
import {configureElectronProxy} from '../assets/portable-network.mjs';

test('default and updater sessions gate main startup and use the same endpoint',async()=>{
 const app=new EventEmitter();let resolveReady;
 app.whenReady=()=>new Promise(resolve=>{resolveReady=resolve});app.commandLine={appendSwitch(){}};
 const seen=[];const configure=name=>({async setProxy(config){seen.push({name,config});await Promise.resolve()}});
 const session={defaultSession:configure('default'),fromPartition(name){return configure(name)}};
 const net={request(){return new EventEmitter()}};
 configureElectronProxy({app,session,net},'http://127.0.0.1:17890','secret');
 let main=false;const waiting=app.whenReady().then(()=>main=true);assert.equal(main,false);resolveReady();await waiting;
 assert.deepEqual(seen.map(v=>v.name),['default','electron-updater']);assert.ok(seen.every(v=>v.config.mode==='fixed_servers'&&v.config.proxyRules==='http://127.0.0.1:17890'));
 app.emit('session-created',configure('new-session'));await Promise.resolve();assert.equal(seen.length,3);
});
test('ClientRequest proxy login responds once without handing credentials to origin/upstream handlers',async()=>{
 const app=new EventEmitter();app.whenReady=()=>Promise.resolve();app.commandLine={appendSwitch(){}};
 const session={defaultSession:{async setProxy(){}},fromPartition(){return {async setProxy(){}}}};
 const net={request(){return new EventEmitter()}};
 await configureElectronProxy({app,session,net},'http://127.0.0.1:17890','secret');
 const request=net.request();let upstream=0,credentials;
 request.on('login',()=>upstream++);
 request.emit('login',{isProxy:true,host:'127.0.0.1',port:17890},(...v)=>credentials=v);
 assert.deepEqual(credentials,['dsh','secret']);assert.equal(upstream,0);
 credentials=undefined;request.emit('login',{isProxy:false,host:'127.0.0.1',port:17890},(...v)=>credentials=v);
 assert.equal(credentials,undefined);assert.equal(upstream,1);
 request.emit('login',{isProxy:true,host:'upstream.example',port:17890},()=>assert.fail('credential leak'));
 assert.equal(upstream,2);
});
