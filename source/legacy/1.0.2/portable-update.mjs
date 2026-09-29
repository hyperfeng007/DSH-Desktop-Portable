import fs from 'node:fs';
import path from 'node:path';
import { randomBytes } from 'node:crypto';

export function installPortableUpdate({app,dialog},run,nonce){
  let pending;
  globalThis.__dshPortableCheckUpdate=()=> pending ??= check().finally(()=>{pending=undefined});
  async function check(){
    const id=randomBytes(12).toString('hex');
    const response=path.join(run,'update-check.response.json');
    const request=path.join(run,'update-check.request.json');
    fs.writeFileSync(request+'.new',JSON.stringify({nonce,id}),'utf8');fs.renameSync(request+'.new',request);
    let result;
    const until=Date.now()+110000;
    while(Date.now()<until){
      await new Promise(resolve=>setTimeout(resolve,250));
      try{const r=JSON.parse(fs.readFileSync(response,'utf8'));if(r.nonce===nonce&&r.id===id){result=r;break}}catch{}
    }
    if(!result||result.error){await dialog.showMessageBox({type:'error',title:'便携版更新检查',message:result?.error??'更新检查超时，请查看 Data/Logs/network.log。',buttons:['确定']});return}
    if(result.version===app.getVersion()){await dialog.showMessageBox({type:'info',title:'便携版更新检查',message:'已是官方当前版本：'+result.version+'\n本次检查使用便携启动器的网络代理。',buttons:['确定']});return}
    const answer=await dialog.showMessageBox({type:'question',title:'发现官方桌面版本 '+result.version,
      message:'当前版本：'+app.getVersion()+'\n官方当前版本：'+result.version+'\n\n确认后将请求正常退出，再由便携启动器校验并解包更新，不运行安装器。升级前请备份 Data。\n若取消官方退出确认，更新计划保留到下次正常退出。',buttons:['暂不更新','安排更新并退出'],defaultId:0,cancelId:0});
    if(answer.response!==1)return;
    fs.writeFileSync(path.join(run,'update-install.request'),nonce,'utf8');app.quit();
  }
}
