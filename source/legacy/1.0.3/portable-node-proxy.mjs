/* Community portability adapter. Scoped to the trusted Desktop main / Host process. */
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';

export function networkLog(role, event, destination='', code='') {
  const root=process.env.DSH_PORTABLE_ROOT;
  if (!root) return;
  try {
    const dir=path.join(root,'Data','Logs');fs.mkdirSync(dir,{recursive:true});
    let origin='';try {const u=new URL(destination);origin=u.protocol+'//'+u.host;} catch {}
    const safeCode=String(code).replace(/[^A-Za-z0-9_.-]/g,'').slice(0,64);
    fs.appendFileSync(path.join(dir,'network.log'),JSON.stringify({time:new Date().toISOString(),role,event,origin,code:safeCode})+'\n','utf8');
  } catch {}
}

export async function installPortableNodeProxy(role='host', injectedUndici) {
  const endpoint=process.env.DSH_PORTABLE_PROXY;
  const token=process.env.DSH_PORTABLE_PROXY_TOKEN;
  if (!endpoint || !token) throw Error('Portable proxy endpoint is missing; use DSH-Portable.exe');
  const proxy=new URL(endpoint);
  if (proxy.protocol!=='http:' || proxy.hostname!=='127.0.0.1' || !proxy.port || proxy.username || proxy.password) throw Error('Invalid portable proxy endpoint');
  const req=createRequire(new URL('./dsh/package.json',import.meta.url));
  const undici=injectedUndici ?? req('undici');
  const nativeFetch=globalThis.fetch.bind(globalThis);
  function local(origin){
    const h=new URL(origin).hostname.toLowerCase().replace(/^\[|\]$/g,'');
    return h==='localhost'||h==='::1'||h==='0.0.0.0'||/^127\./.test(h)||h.startsWith('::ffff:127.');
  }
  // Undici 8.11's legacy dispatcher adapter forwards an HTTP/2 header object
  // where Node's built-in fetch expects a raw header array. Compression headers
  // disappear, so gzip/br bytes can reach JSON.parse undecoded. Keep the owned
  // transport on HTTP/1.1 until that cross-version adapter is verified fixed.
  // This changes ALPN only: HTTPS verification and proxy routing stay intact.
  const route=new undici.Agent({allowH2:false,factory(origin,options){
    return local(origin) ? new undici.Pool(origin,{...options,allowH2:false}) : new undici.ProxyAgent({uri:endpoint,allowH2:false,requestTls:{allowH2:false},token:'Basic '+Buffer.from('dsh:'+token).toString('base64')});
  }});
  // Undici 8 and Node's built-in fetch use different dispatcher interfaces.
  // Capture Undici's official compatibility adapter instead of assuming that
  // a userland global dispatcher is always the one Electron/Node fetch reads.
  const previous=undici.getGlobalDispatcher();
  let dispatcher;
  try {undici.setGlobalDispatcher(route);dispatcher=Number(process.versions.undici?.split('.')[0] ?? 7)>=8 ? route : globalThis[Symbol.for('undici.globalDispatcher.1')];}
  finally {undici.setGlobalDispatcher(previous);}
  if (!dispatcher || typeof dispatcher.dispatch!=='function') {await route.close();throw Error('Bundled Undici has no compatible fetch dispatcher');}
  globalThis.fetch=async function portableFetch(input,init){
    const destination=typeof input==='string'||input instanceof URL ? String(input) : input.url;
    // Keep deliberate per-request security transports (e.g. DNS-pinned SSRF guards).
    if (init?.dispatcher) {
      networkLog(role,'explicit-dispatcher',destination);
      return nativeFetch(input,init);
    }
    networkLog(role,'fetch-start',destination,local(destination)?'loopback-direct':'portable-bridge');
    try {
      const response=await nativeFetch(input,{...init,dispatcher});
      networkLog(role,'fetch-response',destination,response.status);
      return response;
    } catch(error) {
      networkLog(role,'fetch-error',destination,error?.cause?.code ?? error?.code ?? 'UNKNOWN');
      throw error; // Never retry direct after proxy failure.
    }
  };
  networkLog(role,'proxy-installed');
  return async()=>{globalThis.fetch=nativeFetch;await route.close();};
}
