/* DSH Portable community wrapper, MIT. Upstream functionality retained with bounded Host/network/update adapters. */
import { app, dialog, session, net } from 'electron';
import {installPortableNodeProxy,networkLog} from './portable-node-proxy.mjs';
import {configureElectronProxy} from './portable-network.mjs';
import {installPortableUpdate} from './portable-update.mjs';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
const root = process.env.DSH_PORTABLE_ROOT;
function writeFailure(error) {
  try {
    if (!root || !path.isAbsolute(root)) return;
    const logs = path.join(root, 'Data', 'Logs');
    fs.mkdirSync(logs,{recursive:true});
    fs.appendFileSync(path.join(logs,'bootstrap-error.txt'),
      new Date().toISOString() + ' ' + String(error?.stack || error) + '\n', 'utf8');
  } catch {} // A removed or unwritable USB must not trigger recursive error handling.
}
process.on('uncaughtExceptionMonitor',writeFailure);
async function boot() {
  const __dirname = path.dirname(fileURLToPath(import.meta.url));
  const run = process.env.DSH_PORTABLE_RUN;
  if (!root || !run || !path.isAbsolute(root)) throw Error('Please start DSH with DSH-Portable.exe');
  const data = path.join(root, 'Data');
  for (const [name, rel] of Object.entries({
    appData:'AppData/Roaming', userData:'Desktop', sessionData:'Desktop/Session',
    temp:'Temp', logs:'Logs/Desktop', crashDumps:'CrashDumps', downloads:'Downloads', documents:'Documents'
  })) {
    const folder = path.join(data, rel);
    fs.mkdirSync(folder, {recursive:true});
    app.setPath(name, folder);
  }
  app.setAppLogsPath(path.join(data, 'Logs', 'Desktop'));
  app.commandLine.appendSwitch('disk-cache-dir', path.join(data,'Cache','Chromium'));
  app.commandLine.appendSwitch('disable-breakpad');
  app.commandLine.appendSwitch('disable-crash-reporter');
  app.setAsDefaultProtocolClient = () => false;
  app.removeAsDefaultProtocolClient = () => false;
  app.setLoginItemSettings = () => {};
  app.addRecentDocument = () => {};
  const bridge=process.env.DSH_PORTABLE_PROXY;
  const token=process.env.DSH_PORTABLE_PROXY_TOKEN;
  if(!bridge||!token)throw Error('Portable proxy configuration missing');
  configureElectronProxy({app,session,net},bridge,token,networkLog).catch(writeFailure);
  await installPortableNodeProxy('desktop');
  installPortableUpdate({app,dialog},run,process.env.DSH_PORTABLE_NONCE);
  const ready = path.join(run,'ready');
  const windows = new Set();
  let wroteReady = false;
  function markReady(w) {
    if (wroteReady || w.isDestroyed() || w.webContents.isDestroyed() || !w.isVisible() || w.webContents.isLoadingMainFrame()) return;
    const u = w.webContents.getURL();
    if (!u || u === 'about:blank' || u.startsWith('devtools:')) return;
    try { fs.writeFileSync(ready, process.env.DSH_PORTABLE_NONCE, {encoding:'utf8',mode:0o600}); wroteReady = true; } catch {}
  }
  app.on('browser-window-created', (_,w) => {
    windows.add(w);
    w.once('closed', () => windows.delete(w));
    w.webContents.on('did-finish-load', () => markReady(w));
    w.on('show', () => markReady(w));
    w.once('ready-to-show', () => markReady(w));
  });
  const timer = setInterval(() => {
    for (const w of windows) { try { markReady(w); } catch {} }
    if (!fs.existsSync(root)) { app.exit(2); return; }
    const q = path.join(run,'quit');
    try {
      if (fs.readFileSync(q,'utf8') === process.env.DSH_PORTABLE_NONCE) {
        fs.unlinkSync(q); app.quit();
      }
    } catch {}
  }, 600);
  timer.unref();
  app.once('will-quit', () => clearInterval(timer));
  // Keep upstream pre-ready initialization within the ESM startup promise.
  await import(pathToFileURL(path.join(__dirname, '__ORIGINAL_MAIN__')).href);
}
try {
  await boot();
} catch (error) {
  writeFailure(error);
  try { dialog.showErrorBox('DSH Portable 启动失败', String(error?.message || error)); } catch {}
  app.exit(1);
}
