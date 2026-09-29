/* Explicit Electron sessions and ClientRequest proxy authentication. */
export function configureElectronProxy({app,session,net}, endpoint, token, log=()=>{}) {
  const proxy=new URL(endpoint);
  if(proxy.protocol!=='http:'||proxy.hostname!=='127.0.0.1'||!proxy.port||!token)throw Error('Invalid local proxy');
  const authenticates=auth=>auth?.isProxy && auth.host==='127.0.0.1' && String(auth.port)===proxy.port;
  const creds=(auth,callback)=>{if(authenticates(auth)){callback('dsh',token);return true}return false;};
  app.commandLine.appendSwitch('proxy-server',endpoint);
  app.commandLine.appendSwitch('proxy-bypass-list','localhost;127.0.0.1;[::1]');
  app.on('login',(event,_contents,_details,auth,callback)=>{if(authenticates(auth)){event.preventDefault();creds(auth,callback)}});
  const originalRequest=net.request.bind(net);
  net.request=(...args)=>{
    const request=originalRequest(...args);
    // A ClientRequest has its own login event. app.on('login') is not a
    // substitute for the handler used by electron-updater's separate session.
    const emit=request.emit;
    request.emit=function(event,...values){
      if(event==='login'&&authenticates(values[0])){
        log('electron','proxy-auth',endpoint);
        creds(values[0],values[1]);return true;
      }
      return emit.call(this,event,...values);
    };
    return request;
  };
  const nativeReady=app.whenReady.bind(app);
  const ready=nativeReady().then(async()=>{
    const configure=s=>s.setProxy({mode:'fixed_servers',proxyRules:endpoint,proxyBypassRules:'localhost,127.0.0.1,[::1]'});
    await Promise.all([configure(session.defaultSession),configure(session.fromPartition('electron-updater',{cache:false}))]);
    app.on('session-created',s=>{configure(s).catch(()=>log('electron','session-proxy-error'))});
    log('electron','sessions-proxy-ready');
  });
  // Upstream registers protocols before readiness, then enters main via whenReady.
  // Return a gated promise, without awaiting it here (which would deadlock startup).
  app.whenReady=()=>ready;
  return ready;
}
