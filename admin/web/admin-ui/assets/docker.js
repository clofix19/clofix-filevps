(() => {
 if(document.body.dataset.page!=='docker')return;
 let busy=false, pending=false, templates=[], workers=[], noteUntil=0, logPoll=null, logJobId='', logStarted=0, logTries=0;
 let dockerWAFTargets=[], dockerWAFTarget='', dockerWAFErrorStatus='403', dockerWAFErrorDirty=false, dockerWAFErrorSource='';
 const dockerWAFStatuses=['403','404','406','429','500'];
 const dockerHistoryKey='cf_docker_history_v1';
 const dockerHistory=[];
 let lastNetSample=null;
 try{
  const saved=JSON.parse(localStorage.getItem(dockerHistoryKey)||'[]');
  if(Array.isArray(saved))saved.forEach(p=>{
   if(Array.isArray(p)&&Number.isFinite(+p[0]))dockerHistory.push({
    at:+p[0],cpuUsed:+p[1]||0,cpuLimit:+p[2]||0,ramUsed:+p[3]||0,ramLimit:+p[4]||0,
    diskUsed:+p[5]||0,diskLimit:+p[6]||0,containersRunning:+p[7]||0,containersTotal:+p[8]||0,
    netIn:+p[9]||0,netOut:+p[10]||0,netBw:+p[11]||0
   });
  });
 }catch(e){}
 while(dockerHistory.length&&dockerHistory[0].at<Date.now()-2592000000)dockerHistory.shift();
 const msg=$('docker-message');
 const note=text=>{if(msg){msg.textContent=text||'No Docker activity yet.';noteUntil=text?Date.now()+8000:0;}};
 const action=(text,fn,cls='small-btn')=>{const b=document.createElement('button');b.type='button';b.className=cls;b.textContent=text;b.onclick=async ev=>{ev.preventDefault();ev.stopPropagation();if(b.disabled)return;b.disabled=true;try{await fn();}finally{b.disabled=false;}};return b;};
 const on=(id,fn)=>{const el=$(id);if(el)el.onclick=fn;};
 function containerResources(c){
  const rt=c?.runtime||{};
  const cpuPct=Number(rt.cpu_percent)||0;
  const cpuAlloc=Number(c?.spec?.cpu)||0;
  const ramUsed=Number(rt.memory_bytes)||0;
  const ramLim=Number(rt.memory_limit_bytes)||(Number(c?.spec?.ram_mb)||0)*1048576;
  const diskUsed=Number(rt.disk_bytes)||0;
  const diskLim=(Number(c?.spec?.disk_mb)||0)*1048576;
  const cpuTxt=cpuAlloc?`${cpuPct.toFixed(1)}% of ${cpuAlloc} CPU`:`${cpuPct.toFixed(1)}%`;
  const ramTxt=ramLim?`${fmtBytes(ramUsed)} / ${fmtBytes(ramLim)}`:(ramUsed?fmtBytes(ramUsed):'—');
  const diskTxt=diskLim?`${fmtBytes(diskUsed)} / ${fmtBytes(diskLim)}`:(diskUsed?fmtBytes(diskUsed):'—');
  return {cpuPct,cpuAlloc,ramUsed,ramLim,diskUsed,diskLim,cpuTxt,ramTxt,diskTxt};
 }
 function appResources(a,containers){
  const ids=new Set(a?.container_ids||[]);
  const mine=(containers||[]).filter(c=>ids.has(c.id)||c.app_id===a.id);
  if(!mine.length&&a?.spec){
   const cpuAlloc=Number(a.spec.cpu)||0,ramLim=(Number(a.spec.ram_mb)||0)*1048576,diskLim=(Number(a.spec.disk_mb)||0)*1048576;
   return {
    cpuTxt:cpuAlloc?`— / ${cpuAlloc} CPU`:'—',
    ramTxt:ramLim?`— / ${fmtBytes(ramLim)}`:'—',
    diskTxt:diskLim?`— / ${fmtBytes(diskLim)}`:'—',
   };
  }
  let cpuPct=0,cpuAlloc=0,ramUsed=0,ramLim=0,diskUsed=0,diskLim=0;
  mine.forEach(c=>{
   const r=containerResources(c);
   cpuPct+=r.cpuPct;cpuAlloc+=r.cpuAlloc||Number(c.spec?.cpu)||0;
   ramUsed+=r.ramUsed;ramLim+=r.ramLim;
   diskUsed+=r.diskUsed;diskLim+=r.diskLim||(Number(c.spec?.disk_mb)||0)*1048576;
  });
  if(!cpuAlloc)cpuAlloc=Number(a?.spec?.cpu)||0;
  if(!ramLim)ramLim=(Number(a?.spec?.ram_mb)||0)*1048576;
  if(!diskLim)diskLim=(Number(a?.spec?.disk_mb)||0)*1048576;
  return {
   cpuTxt:cpuAlloc?`${cpuPct.toFixed(1)}% of ${cpuAlloc} CPU`:`${cpuPct.toFixed(1)}%`,
   ramTxt:ramLim?`${fmtBytes(ramUsed)} / ${fmtBytes(ramLim)}`:(ramUsed?fmtBytes(ramUsed):'—'),
   diskTxt:diskLim?`${fmtBytes(diskUsed)} / ${fmtBytes(diskLim)}`:(diskUsed?fmtBytes(diskUsed):'—'),
  };
 }
 function dockerWAFSelected(){return dockerWAFTargets.find(t=>`${t.kind}:${t.id}`===dockerWAFTarget)||null;}
 function dockerWAFDefaultHTML(status){
  if(typeof window.cfDefaultWAFErrorHTML==='function')return window.cfDefaultWAFErrorHTML(status);
  return `<!DOCTYPE html><html><body><h1>${status}</h1><p>Request blocked by CloFix WAF.</p></body></html>`;
 }
 function dockerWAFCurrentHTML(t){
  const custom=(t?.waf_error_pages||{})[dockerWAFErrorStatus];
  return (custom&&String(custom).trim())||dockerWAFDefaultHTML(dockerWAFErrorStatus);
 }
 function renderDockerWAFErrorTabs(){
  const tabs=$('docker-waf-error-status-tabs');if(!tabs)return;
  if(tabs.children.length!==dockerWAFStatuses.length){
   tabs.textContent='';
   dockerWAFStatuses.forEach(code=>{
    const b=document.createElement('button');b.type='button';b.className='waf-status-tab';b.dataset.status=code;b.textContent=code;b.setAttribute('role','tab');
    b.onclick=()=>{if(dockerWAFErrorDirty&&!confirm('Discard unsaved changes for this error page?'))return;dockerWAFErrorStatus=code;dockerWAFErrorDirty=false;dockerWAFErrorSource='';renderDockerWAF();};
    tabs.append(b);
   });
  }
  [...tabs.children].forEach(b=>b.setAttribute('aria-selected',String(b.dataset.status===dockerWAFErrorStatus)));
 }
 function renderDockerWAF(){
  if(!$('docker-waf-status')||!$('docker-waf-target'))return;
  const items=window.cfWAFItems||[];
  const sel=$('docker-waf-target');
  if(sel){
   const keep=dockerWAFTarget||sel.value;
   const cur=sel.value;
   if(sel.options.length!==dockerWAFTargets.length+1||[...sel.options].slice(1).map(o=>o.value).join('|')!==dockerWAFTargets.map(t=>`${t.kind}:${t.id}`).join('|')){
    sel.innerHTML='<option value="">Select a target</option>';
    dockerWAFTargets.forEach(t=>{
      const o=document.createElement('option');
      o.value=`${t.kind}:${t.id}`;
      o.textContent=`${t.name} · ${t.kind}${t.domain?' · '+t.domain:''}`;
      sel.append(o);
    });
   }
   const next=dockerWAFTargets.some(t=>`${t.kind}:${t.id}`===keep)?keep:(dockerWAFTargets[0]?`${dockerWAFTargets[0].kind}:${dockerWAFTargets[0].id}`:'');
   if(sel.value!==next)sel.value=next;
   dockerWAFTarget=sel.value||'';
   if(cur!==sel.value){dockerWAFErrorDirty=false;dockerWAFErrorSource='';}
  }
  const t=dockerWAFSelected();
  const state=t?.waf_state||'', installed=state==='INSTALLED', installing=state==='INSTALLING', uninstalling=state==='UNINSTALLING', failed=state==='FAILED';
  const status=$('docker-waf-status'), install=$('docker-waf-install'), uninstall=$('docker-waf-uninstall'), controls=$('docker-waf-controls'), msg=$('docker-waf-message');
  if(status){
   status.textContent=!t?'Select a target':installed?'Installed':installing?'Installing':uninstalling?'Uninstalling':failed?'Install failed':state==='UNINSTALLED'?'Not installed':'Not installed';
   status.className='badge '+(installed?'RUNNING':failed?'FAILED':'STOPPED');
  }
  if(install){
   install.hidden=!!(installed||installing||uninstalling);
   install.disabled=!t||installing||uninstalling;
   install.textContent=installing?'Installing…':failed?'Retry WAF install':'Install OWASP CRS WAF';
  }
  if(uninstall){
   uninstall.hidden=!(installed||uninstalling||failed);
   uninstall.disabled=!t||installing||uninstalling;
   uninstall.textContent=uninstalling?'Uninstalling…':'Uninstall OWASP CRS WAF';
  }
  if(controls){
   if(controls.children.length!==items.length){
    controls.textContent='';
    items.forEach(([key,label,tag])=>{
     const item=document.createElement('div');item.className='waf-rule';
     const info=document.createElement('div');info.className='waf-rule-info';
     const title=document.createElement('div');title.className='waf-rule-title';title.textContent=label;
     const code=document.createElement('div');code.className='waf-rule-tag';code.textContent=tag;
     info.append(title,code);
     const toggle=document.createElement('button');toggle.type='button';toggle.className='waf-toggle';toggle.setAttribute('role','switch');toggle.dataset.key=key;
     item.append(info,toggle);controls.append(item);
    });
   }
   items.forEach(([key,label],idx)=>{
    const toggle=controls.children[idx]?.querySelector('.waf-toggle');if(!toggle)return;
    const enabled=!t||t.owasp_protection?.[key]!==false;
    toggle.setAttribute('aria-checked',String(enabled));
    toggle.setAttribute('aria-label',(enabled?'Disable ':'Enable ')+label);
    toggle.disabled=!installed||!t;
    toggle.onclick=async()=>{
     if(!t)return;
     toggle.disabled=true;
     if(msg)msg.textContent='Queueing rule-group update…';
     try{
      const r=await api('POST',`/api/docker/${t.kind==='application'?'applications':'containers'}/${t.id}/waf`,{category:key,enabled:!enabled});
      if(msg)msg.textContent=r.unchanged?'Already set.':'Rule-group update queued.';
      await refresh(true);
     }catch(e){if(msg)msg.textContent=e.message;}
    };
   });
  }
  const enabledCount=items.filter(([key])=>!t||t.owasp_protection?.[key]!==false).length;
  if($('docker-waf-rule-summary'))$('docker-waf-rule-summary').textContent=installed?`${enabledCount} of ${items.length} rule groups enabled`:'Install the WAF to manage rule groups.';
  if(msg&&!msg.dataset.sticky){
   if(!t)msg.textContent='Select a Docker application or container with a published host port.';
   else if(installing)msg.textContent='Installing edge OWASP CRS proxy — the app is briefly recreated without host ports.';
   else if(uninstalling)msg.textContent='Removing WAF proxy and restoring host ports on the app.';
   else if(!installed)msg.textContent=failed?(t.error||'Install failed. Retry.'):'Install WAF to put ModSecurity CRS in front of this published port.';
   else msg.textContent='WAF edge is protecting published HTTP(S) traffic for this target.';
  }
  renderDockerWAFErrorTabs();
  const html=$('docker-waf-error-html'), label=$('docker-waf-error-status-label'), save=$('docker-waf-error-save'), preview=$('docker-waf-error-preview'), def=$('docker-waf-error-default');
  if(label)label.textContent=dockerWAFErrorStatus;
  const sourceKey=(t?.id||'')+':'+dockerWAFErrorStatus;
  if(html&&dockerWAFErrorSource!==sourceKey&&!dockerWAFErrorDirty){html.value=dockerWAFCurrentHTML(t);dockerWAFErrorSource=sourceKey;}
  if(html)html.disabled=!installed;
  if(save)save.disabled=!installed;
  if(preview)preview.disabled=false;
  if(def)def.disabled=!installed;
 }

 async function loadDockerServerHistory(){
  if(typeof fetchMetricsSeries!=='function'||typeof mergeMetricsPoints!=='function')return;
  const w=typeof usageWindowBounds==='function'?usageWindowBounds():{from:Date.now()-3600000,to:Date.now()};
  const pts=await fetchMetricsSeries('docker',w.from,w.to);
  if(!pts.length)return;
  const merged=mergeMetricsPoints(dockerHistory,pts);
  dockerHistory.splice(0,dockerHistory.length,...merged);
  try{localStorage.setItem(dockerHistoryKey,JSON.stringify(dockerHistory.map(p=>[p.at,p.cpuUsed,p.cpuLimit,p.ramUsed,p.ramLimit,p.diskUsed,p.diskLimit,p.containersRunning,p.containersTotal,p.netIn,p.netOut,p.netBw])));}catch(e){}
  renderDockerCharts();
 }
 function renderDockerCharts(){
  if(typeof drawUsageChart!=='function')return;
  drawUsageChart('chart-docker-cpu','cpuUsed','cpuLimit','cores','chart-docker-cpu-current',dockerHistory);
  drawUsageChart('chart-docker-memory','ramUsed','ramLimit','GB','chart-docker-memory-current',dockerHistory);
  drawUsageChart('chart-docker-disk','diskUsed','diskLimit','GB','chart-docker-disk-current',dockerHistory);
  if(typeof drawCloudChart==='function'){
   drawCloudChart('chart-docker-containers',{history:dockerHistory,series:[{key:'containersRunning',label:'Running',color:'#43d6c5',area:true},{key:'containersTotal',label:'Total',color:'#77baff',cls:'secondary-line'}],currentID:'chart-docker-containers-current',formatCurrent:last=>(Number(last.containersRunning)||0)+' / '+(Number(last.containersTotal)||0)});
  }
  if(typeof drawRatePair==='function')drawRatePair('chart-docker-net','netIn','netOut','chart-docker-net-current',dockerHistory);
  if(typeof drawRateSingle==='function')drawRateSingle('chart-docker-bw','netBw','chart-docker-bw-current',dockerHistory);
 }
 window.renderDockerCharts=renderDockerCharts;
 function captureDockerSample(metrics,containers,apps){
  const now=Date.now();
  const workersMap=metrics&&typeof metrics.workers==='object'?metrics.workers:{};
  let cpuUsed=0,cpuLimit=0,ramUsed=0,ramLimit=0,diskUsed=0,diskLimit=0,running=0,liveTotal=0,netRx=0,netTx=0,liveRamLimit=0;
  Object.values(workersMap).forEach(st=>{
   if(!st||typeof st!=='object')return;
   const eng=st.engine||{};
   diskUsed+=(Number(eng.disk_used)||0)/1073741824;
   diskLimit+=(Number(eng.disk_total)||0)/1073741824;
   (st.containers||[]).forEach(c=>{
    liveTotal++;
    const state=String(c.state||c.status||'').toLowerCase();
    if(state.includes('run'))running++;
    cpuUsed+=Math.max(0,Number(c.cpu_percent)||0)/100;
    ramUsed+=(Number(c.memory_bytes)||0)/1073741824;
    liveRamLimit+=(Number(c.memory_limit_bytes)||0)/1073741824;
    diskUsed+=(Number(c.disk_bytes)||0)/1073741824;
    netRx+=Number(c.net_rx_bytes)||0;
    netTx+=Number(c.net_tx_bytes)||0;
   });
  });
  (containers||[]).forEach(c=>{
   cpuLimit+=Number(c.spec?.cpu)||0;
   ramLimit+=(Number(c.spec?.ram_mb)||0)/1024;
   const st=String(c.state||'').toLowerCase();
   if(!liveTotal&&(st==='running'||st.includes('run')))running++;
  });
  if(!ramLimit)ramLimit=liveRamLimit;
  if(!cpuLimit)cpuLimit=Math.max(liveTotal||(containers||[]).length,cpuUsed,1);
  if(!ramLimit)ramLimit=Math.max(ramUsed,0.25);
  if(!liveTotal)liveTotal=(containers||[]).length;
  let netIn=0,netOut=0;
  if(lastNetSample&&now>lastNetSample.at){
   const dt=Math.max(1,(now-lastNetSample.at)/1000);
   netIn=Math.max(0,(netRx-lastNetSample.rx)/dt);
   netOut=Math.max(0,(netTx-lastNetSample.tx)/dt);
  }
  lastNetSample={at:now,rx:netRx,tx:netTx};
  const sample={at:now,cpuUsed,cpuLimit,ramUsed,ramLimit,diskUsed,diskLimit,containersRunning:running,containersTotal:Math.max(liveTotal,(containers||[]).length),netIn,netOut,netBw:netIn+netOut};
  const last=dockerHistory[dockerHistory.length-1];
  if(!last||now-last.at>=4000){
   dockerHistory.push(sample);
   while(dockerHistory.length&&dockerHistory[0].at<now-2592000000)dockerHistory.shift();
   try{
    localStorage.setItem(dockerHistoryKey,JSON.stringify(dockerHistory.map(p=>[p.at,p.cpuUsed,p.cpuLimit,p.ramUsed,p.ramLimit,p.diskUsed,p.diskLimit,p.containersRunning,p.containersTotal,p.netIn,p.netOut,p.netBw])));
   }catch(e){}
  }else{
   dockerHistory[dockerHistory.length-1]=sample;
  }
  renderDockerCharts();
  loadDockerServerHistory().catch(()=>{});
 }
 function showMethodFields(){
  const method=$('docker-app-method')?.value||'image';
  document.querySelectorAll('[data-docker-method]').forEach(el=>{
   const ok=el.getAttribute('data-docker-method').split(/\s+/).includes(method);
   el.hidden=!ok;
  });
 }
 function setLogStatus(text){if($('docker-log-status'))$('docker-log-status').textContent=text||'';}
 function stopLogPoll(){if(logPoll){clearInterval(logPoll);logPoll=null;}logJobId='';logTries=0;}
 async function pollLogJob(jobId){
  if(!jobId)return;
  logTries++;
  try{
   const j=await api('GET',`/api/docker/deployments/${jobId}`);
   const done=j.state==='SUCCEEDED'||j.state==='FAILED'||j.state==='CANCELLED';
   const age=Math.round((Date.now()-logStarted)/1000);
   setLogStatus(done?(j.state==='SUCCEEDED'?'Ready':'Job '+j.state.toLowerCase()):`Job ${String(j.state||'queued').toLowerCase()} · ${age}s`);
   if(j.logs){$('docker-log-content').textContent=j.logs;}
   else if(j.error){$('docker-log-content').textContent=j.error;}
   else if(!done){
    const hint=age>20
     ?'Still waiting for the worker heartbeat.\nIf this stays queued, the worker cannot reach admin (check Docker DNS / restart worker).\nJob: '+jobId
     :'Waiting for worker heartbeat…';
    $('docker-log-content').textContent=hint;
   }else if(!$('docker-log-content').textContent||$('docker-log-content').textContent.startsWith('Waiting')||$('docker-log-content').textContent.startsWith('Still waiting')||$('docker-log-content').textContent==='Loading…'){
    $('docker-log-content').textContent=j.error||'(empty log output)';
   }
   if(done)stopLogPoll();
   else if(age>90){setLogStatus('Timed out waiting for worker');$('docker-log-content').textContent='Log job did not finish in 90s.\nWorker may be offline or stuck resolving admin.\nRestart the worker container, then try Logs again.\nJob: '+jobId;stopLogPoll();}
  }catch(e){setLogStatus(e.message);stopLogPoll();}
 }
 async function openLogs(title,start){
  stopLogPoll();
  logStarted=Date.now();
  if($('docker-log-title'))$('docker-log-title').textContent=title||'Logs';
  setLogStatus('Queuing log request…');
  if($('docker-log-content'))$('docker-log-content').textContent='Loading…';
  $('docker-log-dialog')?.showModal();
  try{
   const j=await start();
   logJobId=j.id||j.job_id||'';
   if(!logJobId){setLogStatus('No job id returned');$('docker-log-content').textContent=JSON.stringify(j,null,2);return;}
   setLogStatus('Queued · waiting for worker…');
   await pollLogJob(logJobId);
   logPoll=setInterval(()=>pollLogJob(logJobId),1500);
  }catch(e){setLogStatus(e.message);$('docker-log-content').textContent=e.message;}
 }
 async function refresh(force){
  if(busy){if(force)pending=true;return;}
  if(!$('tok')?.value?.trim())return;
  const run=async()=>{
  busy=true;
  try{
   const [overview,wks,containers,apps,images,networks,volumes,jobs,regs,backups,security,metrics,tpl]=await Promise.all([
    api('GET','/api/docker/overview'),
    api('GET','/api/docker/workers'),
    api('GET','/api/docker/containers'),
    api('GET','/api/docker/applications'),
    api('GET','/api/docker/images'),
    api('GET','/api/docker/networks'),
    api('GET','/api/docker/volumes'),
    api('GET','/api/docker/deployments'),
    api('GET','/api/docker/registries'),
    api('GET','/api/docker/backups'),
    api('GET','/api/docker/security'),
    api('GET','/api/docker/metrics'),
    api('GET','/api/docker/templates'),
   ]);
   workers=wks;templates=tpl;
   const ready=overview.docker_ready_workers??0,containersN=overview.containers??0,appsN=overview.applications??0,failedN=overview.failed??0;
   if($('docker-stat-workers'))$('docker-stat-workers').textContent=ready;
   if($('docker-stat-containers'))$('docker-stat-containers').textContent=containersN;
   if($('docker-stat-apps'))$('docker-stat-apps').textContent=appsN;
   if($('docker-stat-failed'))$('docker-stat-failed').textContent=failedN;
   if($('docker-stat-workers-note'))$('docker-stat-workers-note').textContent=ready?`${ready} worker(s) with host Docker`:'Waiting for Docker-ready workers';
   if($('docker-stat-containers-note'))$('docker-stat-containers-note').textContent=containersN?`${containersN} managed container(s)`:'No containers yet';
   if($('docker-stat-apps-note'))$('docker-stat-apps-note').textContent=appsN?`${appsN} application(s)`:'Deploy an image, Compose, Git or Quick App';
   if($('docker-stat-failed-note'))$('docker-stat-failed-note').textContent=failedN?`${failedN} need attention`:'All clear';
   if(typeof enhanceStatWaves==='function')enhanceStatWaves();
   const wt=$('docker-workers-table');row(wt,['Worker','Hostname','Ready','Version','Containers','Images'],true);
   wks.forEach(w=>row(wt,[w.id,w.hostname||'—',w.docker_ready?'Yes':'No',w.docker_version||'—',w.container_count??'—',w.image_count??'—']));
   if(!wks.length)row(wt,['No active workers','','','','','']);
   const et=$('docker-engine-table');row(et,['Worker','Ready','Version','Host','Actions'],true);
   wks.forEach(w=>{
    const acts=document.createElement('div');acts.className='row-actions';
    ['status','install','start','stop','restart','upgrade'].forEach(a=>acts.append(action(a,async()=>{try{await api('POST',`/api/docker/workers/${encodeURIComponent(w.id)}/engine/${a}`,{});note(`Engine ${a} queued on ${w.id}`);await refresh(true);}catch(e){note(e.message);}})));
    row(et,[w.id,String(!!w.docker_ready),w.docker_version||'—',w.docker_host||'—',acts]);
   });
   const ct=$('docker-containers-table');row(ct,['Name','Image','State','CPU','RAM','Disk','Worker','Actions'],true);
   containers.forEach(c=>{
    const res=containerResources(c);
    const acts=document.createElement('div');acts.className='row-actions';
    acts.append(action('Edit',()=>openEditContainer(c)));
    ['start','stop','restart','pause','unpause'].forEach(a=>acts.append(action(a,async()=>{try{await api('POST',`/api/docker/containers/${c.id}/${a}`,{});note(`${a} queued for ${c.name}`);await refresh(true);}catch(e){note(e.message);}})));
    acts.append(action('Logs',()=>openLogs(c.name+' · Logs',()=>api('GET',`/api/docker/containers/${c.id}/logs`))));
    acts.append(action('Delete',async()=>{if(!confirm(`Delete ${c.name}?`))return;try{await api('DELETE',`/api/docker/containers/${c.id}`);note(`Deleted ${c.name}`);await refresh(true);}catch(e){note(e.message);}},'danger small-btn'));
    row(ct,[c.name,`${c.spec?.image||''}:${c.spec?.tag||''}`,c.state,res.cpuTxt,res.ramTxt,res.diskTxt,c.worker_id,acts]);
   });
   if(!containers.length)row(ct,['No containers yet.','','','','','','','']);
   const at=$('docker-apps-table');row(at,['Name','Method','State','CPU','RAM','Disk','Worker','Actions'],true);
   apps.forEach(a=>{
    const res=appResources(a,containers);
    const acts=document.createElement('div');acts.className='row-actions';
    acts.append(action('Edit',()=>openEditApp(a)));
    if(a.method==='compose')acts.append(action('Logs',()=>openLogs(a.name+' · Compose logs',()=>api('POST',`/api/docker/compose/${a.id}/logs`,{}))));
    else if(a.container_ids?.[0])acts.append(action('Logs',()=>openLogs(a.name+' · Logs',()=>api('GET',`/api/docker/containers/${a.container_ids[0]}/logs`))));
    acts.append(action('Redeploy',async()=>{try{await api('POST',`/api/docker/applications/${a.id}/redeploy`,{});note(`Redeploy queued for ${a.name}`);await refresh(true);}catch(e){note(e.message);}}));
    acts.append(action('Delete',async()=>{if(!confirm(`Delete ${a.name}?`))return;try{await api('DELETE',`/api/docker/applications/${a.id}`);note(`Deleted ${a.name}`);await refresh(true);}catch(e){note(e.message);}},'danger small-btn'));
    row(at,[a.name,a.method+(a.template?' · '+a.template:''),a.state,res.cpuTxt,res.ramTxt,res.diskTxt,a.worker_id,acts]);
   });
   if(!apps.length)row(at,['No applications.','','','','','','','']);
   const tt=$('docker-templates-table');row(tt,['Template','Image','RAM','CPU','Actions'],true);
   const sel=$('docker-app-template');
   if(sel){sel.innerHTML='';}
   tpl.forEach(t=>{
    if(sel){const opt=document.createElement('option');opt.value=t.id;opt.textContent=t.name;sel.append(opt);}
    const acts=document.createElement('div');acts.className='row-actions';
    acts.append(action('Deploy',()=>{openDeploy('quickapp',t);}));
    row(tt,[t.name,`${t.image||'custom'}:${t.tag||''}`,t.ram_mb,t.cpu,acts]);
   });
   const composeApps=apps.filter(a=>a.method==='compose');
   const cot=$('docker-compose-table');row(cot,['Name','State','CPU','RAM','Disk','Worker','Actions'],true);
   composeApps.forEach(a=>{
    const res=appResources(a,containers);
    const acts=document.createElement('div');acts.className='row-actions';
    acts.append(action('Edit',()=>openEditApp(a)));
    ['start','stop','restart'].forEach(x=>acts.append(action(x,async()=>{try{await api('POST',`/api/docker/compose/${a.id}/${x}`,{});note(`Compose ${x} queued for ${a.name}`);await refresh(true);}catch(e){note(e.message);}})));
    acts.append(action('Logs',()=>openLogs(a.name+' · Compose logs',()=>api('POST',`/api/docker/compose/${a.id}/logs`,{}))));
    acts.append(action('remove',async()=>{if(!confirm(`Remove Compose project ${a.name}?`))return;try{await api('POST',`/api/docker/compose/${a.id}/remove`,{});note(`Compose remove queued for ${a.name}`);await refresh(true);}catch(e){note(e.message);}},'danger small-btn'));
    row(cot,[a.name,a.state,res.cpuTxt,res.ramTxt,res.diskTxt,a.worker_id,acts]);
   });
   if(!composeApps.length)row(cot,['No Compose projects.','','','','','','']);
   const it=$('docker-images-table');row(it,['Repository','Tag','ID','Digest'],true);
   images.forEach(i=>row(it,[i.repository||'—',i.tag||'—',(i.id||'').slice(0,12),i.digest||'—']));
   if(!images.length)row(it,['No images reported yet (worker heartbeat).','','','']);
   const rt=$('docker-registries-table');row(rt,['Name','Server','Username','Actions'],true);
   regs.forEach(reg=>{
    const acts=document.createElement('div');acts.className='row-actions';
    acts.append(action('Delete',async()=>{try{await api('DELETE',`/api/docker/registries/${reg.id}`);await refresh(true);}catch(e){note(e.message);}},'danger small-btn'));
    row(rt,[reg.name,reg.server,reg.username,acts]);
   });
   if(!regs.length)row(rt,['No registries.','','','']);
   const nt=$('docker-networks-table');row(nt,['Name','Internal','Worker','Actions'],true);
   networks.forEach(n=>{
    const acts=document.createElement('div');acts.className='row-actions';
    acts.append(action('Delete',async()=>{try{await api('DELETE',`/api/docker/networks/${n.id}`);await refresh(true);}catch(e){note(e.message);}},'danger small-btn'));
    row(nt,[n.name,String(!!n.internal),n.worker_id,acts]);
   });
   if(!networks.length)row(nt,['No networks.','','','']);
   const vt=$('docker-volumes-table');row(vt,['Name','Protection','Worker','Actions'],true);
   volumes.forEach(v=>{
    const acts=document.createElement('div');acts.className='row-actions';
    acts.append(action('Backup',async()=>{try{await api('POST','/api/docker/backups',{kind:'volume',target_id:v.id});note('Backup queued');await refresh(true);}catch(e){note(e.message);}}));
    acts.append(action('Delete',async()=>{if(!confirm(`Delete volume ${v.name}?`))return;try{await api('DELETE',`/api/docker/volumes/${v.id}`);await refresh(true);}catch(e){note(e.message);}},'danger small-btn'));
    row(vt,[v.name,String(!!v.delete_protection),v.worker_id,acts]);
   });
   if(!volumes.length)row(vt,['No volumes.','','','']);
   const jt=$('docker-jobs-table');row(jt,['Job','Action','State','Worker','Error','Actions'],true);
   jobs.slice(0,100).forEach(j=>{
    const acts=document.createElement('div');acts.className='row-actions';
    if(j.action==='container-logs'||j.action==='compose-logs'||j.logs){
     acts.append(action('View',()=>{
      stopLogPoll();
      if($('docker-log-title'))$('docker-log-title').textContent=j.action+' · '+j.id;
      setLogStatus(j.state);
      $('docker-log-content').textContent=j.logs||j.error||'(no output yet)';
      $('docker-log-dialog')?.showModal();
      if(j.state==='QUEUED'||j.state==='RUNNING'){logJobId=j.id;logPoll=setInterval(()=>pollLogJob(j.id),1500);}
     }));
    }
    row(jt,[j.id,j.action,j.state,j.worker_id,j.error||'',acts]);
   });
   if(!jobs.length)row(jt,['No jobs yet.','','','','','']);
   const volById=Object.fromEntries((volumes||[]).map(v=>[v.id,v]));
   const bsel=$('docker-backup-volume');
   if(bsel){
    const keep=bsel.value;
    bsel.innerHTML='<option value="">Select volume…</option>';
    volumes.forEach(v=>{
     const o=document.createElement('option');o.value=v.id;o.textContent=`${v.name} · ${v.worker_id||'—'}`;bsel.append(o);
    });
    if(keep&&[...bsel.options].some(o=>o.value===keep))bsel.value=keep;
   }
   const bt=$('docker-backups-table');row(bt,['ID','Volume','Created','State','Error','Actions'],true);
   const sorted=[...backups].sort((a,b)=>String(b.created||'').localeCompare(String(a.created||'')));
   sorted.forEach(b=>{
    const vol=volById[b.target_id];
    const volLabel=vol?vol.name:(b.target_id||'—');
    const created=b.created?new Date(b.created).toLocaleString():'—';
    const state=String(b.state||'').toUpperCase();
    const ready=state==='READY'||state==='FAILED';
    const deleting=state==='DELETING';
    const restoring=state==='RESTORING';
    const acts=document.createElement('div');acts.className='row-actions';
    if(ready){
     acts.append(action('Restore',async()=>{
      if(!confirm(`Restore backup into volume "${volLabel}"? Running containers using this volume may see inconsistent data until redeployed.`))return;
      try{await api('POST',`/api/docker/backups/${b.id}/restore`,{});note('Restore queued');await refresh(true);}catch(e){note(e.message);}
     }));
     acts.append(action('Redeploy',async()=>{
      if(!confirm(`Restore backup into "${volLabel}" and redeploy apps/containers that mount it?`))return;
      try{await api('POST',`/api/docker/backups/${b.id}/restore`,{redeploy:true});note('Restore queued; redeploy runs after restore succeeds');await refresh(true);}catch(e){note(e.message);}
     }));
    }else if(restoring||deleting||state==='RUNNING'){
     const tip=document.createElement('span');tip.className='muted';tip.textContent=state||b.state||'…';acts.append(tip);
    }
    // Delete stays visible for READY/FAILED and stuck RUNNING; only hide while DELETING.
    if(!deleting){
     acts.append(action('Delete',async()=>{
      if(!confirm(`Delete backup ${b.id}? This removes the archive from the worker.`))return;
      try{await api('DELETE',`/api/docker/backups/${b.id}`);note('Backup delete queued');await refresh(true);}catch(e){note(e.message);}
     },'danger small-btn'));
    }
    row(bt,[b.id,volLabel,created,b.state||'—',b.error||'',acts]);
   });
   if(!sorted.length)row(bt,['No backups yet. Create one from a volume above.','','','','','']);
   dockerWAFTargets=Array.isArray(security?.waf?.targets)?security.waf.targets:[];
   if($('docker-security-view'))$('docker-security-view').textContent=JSON.stringify({defaults:security?.defaults,scanner:security?.scanner,events:security?.events},null,2);
   renderDockerWAF();
   if($('docker-metrics-view'))$('docker-metrics-view').textContent=JSON.stringify(metrics,null,2);
   captureDockerSample(metrics,containers,apps);
   const wsel=$('docker-app-worker');
   if(wsel){
    const cur=wsel.value;
    wsel.innerHTML='<option value="">Automatic</option>';
    wks.filter(w=>w.docker_ready).forEach(w=>{const o=document.createElement('option');o.value=w.id;o.textContent=`${w.id} · ${w.hostname||''}`;wsel.append(o);});
    wsel.value=cur;
   }
   if(Date.now()>noteUntil) note('');
  }catch(e){note(e.message);}
  finally{
   busy=false;
   if(pending){pending=false;refresh(true);}
  }
  };
  if(typeof preservePageScroll==='function')return preservePageScroll(run);
  return run();
 }
 const sampleCompose=`services:
  web:
    image: nginx:1.27-alpine
    ports:
      - "18120:80"
    restart: unless-stopped
`;
 function setEditMode(kind,id,title,submitLabel){
  if($('docker-edit-kind'))$('docker-edit-kind').value=kind||'';
  if($('docker-edit-id'))$('docker-edit-id').value=id||'';
  if($('docker-deploy-title'))$('docker-deploy-title').textContent=title||'Deploy application';
  if($('docker-deploy-submit'))$('docker-deploy-submit').textContent=submitLabel||'Deploy';
  const methodSel=$('docker-app-method');
  if(methodSel)methodSel.disabled=!!kind;
  const workerSel=$('docker-app-worker');
  if(workerSel)workerSel.disabled=!!kind;
 }
 function openDeploy(method,template){
  const form=$('docker-deploy-form');
  if(!form)return;
  form.reset();
  setEditMode('','','Deploy application','Deploy');
  if($('docker-deploy-error'))$('docker-deploy-error').textContent='';
  if($('docker-app-method'))$('docker-app-method').value=method||'image';
  if(method==='compose'&&$('docker-app-compose')&&!$('docker-app-compose').value.trim()){
   $('docker-app-compose').value=sampleCompose;
  }
  if(template){
   if($('docker-app-template'))$('docker-app-template').value=template.id;
   if($('docker-app-image'))$('docker-app-image').value=template.image||'';
   if($('docker-app-tag'))$('docker-app-tag').value=template.tag||'latest';
   if($('docker-app-name'))$('docker-app-name').value=(template.name||'').toLowerCase().replace(/\s+/g,'-');
   if($('docker-app-ram'))$('docker-app-ram').value=template.ram_mb||256;
   if($('docker-app-cpu'))$('docker-app-cpu').value=template.cpu||0.5;
   const ports=template.ports||[];
   if(ports[0]&&$('docker-app-port'))$('docker-app-port').value=ports[0].container_port||80;
  }
  showMethodFields();
  $('docker-deploy-dialog')?.showModal();
 }
 function openEditContainer(c){
  const form=$('docker-deploy-form');
  if(!form||!c)return;
  form.reset();
  setEditMode('container',c.id,'Edit container · '+c.name,'Save & recreate');
  if($('docker-deploy-error'))$('docker-deploy-error').textContent='';
  const spec=c.spec||{};
  if($('docker-app-method'))$('docker-app-method').value='image';
  if($('docker-app-name'))$('docker-app-name').value=c.name||spec.name||'';
  if($('docker-app-image'))$('docker-app-image').value=spec.image||'';
  if($('docker-app-tag'))$('docker-app-tag').value=spec.tag||'latest';
  if($('docker-app-ram'))$('docker-app-ram').value=spec.ram_mb||256;
  if($('docker-app-cpu'))$('docker-app-cpu').value=spec.cpu||0.5;
  if($('docker-app-domain'))$('docker-app-domain').value=spec.domain||'';
  if($('docker-app-env'))$('docker-app-env').value=spec.env?JSON.stringify(spec.env,null,2):'';
  const ports=spec.ports||[];
  if($('docker-app-port'))$('docker-app-port').value=ports[0]?.container_port||80;
  if($('docker-app-host-port'))$('docker-app-host-port').value=ports[0]?.host_port||'';
  if($('docker-app-worker'))$('docker-app-worker').value=c.worker_id||'';
  showMethodFields();
  $('docker-deploy-dialog')?.showModal();
 }
 function openEditApp(a){
  const form=$('docker-deploy-form');
  if(!form||!a)return;
  form.reset();
  const title=a.method==='compose'?'Edit Compose · '+a.name:'Edit application · '+a.name;
  setEditMode('application',a.id,title,'Save & redeploy');
  if($('docker-deploy-error'))$('docker-deploy-error').textContent='';
  if($('docker-app-method'))$('docker-app-method').value=a.method==='quickapp'?'image':(a.method||'image');
  if(a.method==='compose')if($('docker-app-method'))$('docker-app-method').value='compose';
  if($('docker-app-name'))$('docker-app-name').value=a.name||'';
  if($('docker-app-compose'))$('docker-app-compose').value=a.compose_yaml||'';
  if($('docker-app-git'))$('docker-app-git').value=a.git_url||'';
  if($('docker-app-branch'))$('docker-app-branch').value=a.git_branch||'main';
  const spec=a.spec||{};
  if($('docker-app-image'))$('docker-app-image').value=a.image||spec.image||'';
  if($('docker-app-tag'))$('docker-app-tag').value=spec.tag||'latest';
  if($('docker-app-ram'))$('docker-app-ram').value=spec.ram_mb||256;
  if($('docker-app-cpu'))$('docker-app-cpu').value=spec.cpu||0.5;
  if($('docker-app-domain'))$('docker-app-domain').value=a.domain||spec.domain||'';
  if($('docker-app-env'))$('docker-app-env').value=spec.env?JSON.stringify(spec.env,null,2):'';
  const ports=spec.ports||[];
  if($('docker-app-port'))$('docker-app-port').value=ports[0]?.container_port||80;
  if($('docker-app-host-port'))$('docker-app-host-port').value=ports[0]?.host_port||'';
  if($('docker-app-worker'))$('docker-app-worker').value=a.worker_id||'';
  if(a.template&&$('docker-app-template'))$('docker-app-template').value=a.template;
  showMethodFields();
  $('docker-deploy-dialog')?.showModal();
 }
 if($('docker-app-method'))$('docker-app-method').onchange=showMethodFields;
 on('docker-refresh',()=>refresh(true));
 on('docker-deploy',()=>openDeploy('image'));
 on('docker-container-new',()=>openDeploy('image'));
 on('docker-compose-new',()=>openDeploy('compose'));
 document.querySelectorAll('[data-docker-close]').forEach(b=>b.onclick=()=>{setEditMode('','','Deploy application','Deploy');b.closest('dialog')?.close();});
 $('docker-deploy-dialog')?.addEventListener('close',()=>setEditMode('','','Deploy application','Deploy'));
 on('docker-log-close',()=>{$('docker-log-dialog')?.close();});
 $('docker-log-dialog')?.addEventListener('close',()=>stopLogPoll());
 if($('docker-deploy-form'))$('docker-deploy-form').onsubmit=async ev=>{
  ev.preventDefault();
  const err=$('docker-deploy-error');if(err)err.textContent='';
  const submit=ev.submitter||$('docker-deploy-submit');if(submit)submit.disabled=true;
  try{
   const editKind=$('docker-edit-kind')?.value||'';
   const editId=$('docker-edit-id')?.value||'';
   const method=$('docker-app-method').value;
   const body={name:$('docker-app-name').value.trim(),method,worker_id:$('docker-app-worker').value,ram_mb:+$('docker-app-ram').value||128,cpu:+$('docker-app-cpu').value||0.25};
   body.domain=$('docker-app-domain')?.value?.trim()||'';
   const envRaw=$('docker-app-env').value.trim();
   if(envRaw)body.env=JSON.parse(envRaw);
   if(method==='compose'){
    body.compose_yaml=$('docker-app-compose').value;
    if(!body.compose_yaml.trim())throw new Error('Compose YAML is required');
   }else if(method==='git'){
    body.git_url=$('docker-app-git').value.trim();body.git_branch=$('docker-app-branch').value.trim();body.git_token=$('docker-app-token').value;
    if(!body.git_url)throw new Error('Git URL is required');
   }else{
    body.image=$('docker-app-image').value.trim();body.tag=$('docker-app-tag').value.trim()||'latest';
    if(method==='quickapp')body.template=$('docker-app-template').value;
    if(!body.image&&method!=='quickapp'&&editKind!=='application')throw new Error('Image is required');
    const cp=+$('docker-app-port').value,hp=+$('docker-app-host-port').value;
    body.ports=[{container_port:cp||80,host_port:hp||undefined,protocol:'tcp'}];
   }
   if(editKind==='container'&&editId){
    await api('PUT',`/api/docker/containers/${editId}`,body);
    note('Container update queued.');
   }else if(editKind==='application'&&editId){
    await api('PUT',`/api/docker/applications/${editId}`,body);
    note('Application update queued.');
   }else{
    await api('POST','/api/docker/applications',body);
    note('Deployment queued.');
   }
   setEditMode('','','Deploy application','Deploy');
   $('docker-deploy-dialog').close();
   await refresh(true);
  }catch(e){if(err)err.textContent=e.message;else note(e.message);}
  finally{if(submit)submit.disabled=false;}
 };
 on('docker-image-pull',async()=>{
  const image=prompt('Image reference (repo:tag)');if(!image)return;
  try{await api('POST','/api/docker/images/pull',{image});note('Pull queued');await refresh(true);}catch(e){note(e.message);}
 });
 on('docker-image-prune',async()=>{try{await api('POST','/api/docker/images/prune',{});note('Prune queued');await refresh(true);}catch(e){note(e.message);}});
 on('docker-registry-add',async()=>{
  const name=prompt('Registry name');const server=prompt('Server (e.g. https://index.docker.io/v1/)')||'';const username=prompt('Username')||'';const password=prompt('Password / token')||'';
  if(!name)return;
  try{await api('POST','/api/docker/registries',{name,server,username,password});note('Registry saved');await refresh(true);}catch(e){note(e.message);}
 });
 on('docker-network-new',async()=>{
  const name=prompt('Network name');if(!name)return;
  const internal=confirm('Internal-only network?');
  try{await api('POST','/api/docker/networks',{name,internal});note('Network create queued');await refresh(true);}catch(e){note(e.message);}
 });
 on('docker-volume-new',async()=>{
  const name=prompt('Volume name');if(!name)return;
  try{await api('POST','/api/docker/volumes',{name,delete_protection:confirm('Enable delete protection?')});note('Volume create queued');await refresh(true);}catch(e){note(e.message);}
 });
 on('docker-backup-create',async()=>{
  const volId=$('docker-backup-volume')?.value;
  if(!volId){note('Select a volume to back up');return;}
  try{await api('POST','/api/docker/backups',{kind:'volume',target_id:volId});note('Backup queued');await refresh(true);}catch(e){note(e.message);}
 });
 on('docker-backup-refresh',async()=>{try{await refresh(true);note('Backups refreshed');}catch(e){note(e.message);}});
 on('docker-scan-run',async()=>{
  const image=$('docker-scan-image')?.value.trim();if(!image){note('Enter an image to scan');return;}
  try{await api('POST','/api/docker/security/scan',{image});note('Scan queued (requires trivy on worker)');await refresh(true);}catch(e){note(e.message);}
 });
 if($('docker-waf-target'))$('docker-waf-target').onchange=()=>{dockerWAFTarget=$('docker-waf-target').value;dockerWAFErrorDirty=false;dockerWAFErrorSource='';renderDockerWAF();};
 on('docker-waf-install',async()=>{
  const t=dockerWAFSelected(),msg=$('docker-waf-message');if(!t)return;
  if(msg){msg.dataset.sticky='1';msg.textContent='Queueing WAF install…';}
  try{
   await api('POST',`/api/docker/${t.kind==='application'?'applications':'containers'}/${t.id}/waf/install`,{});
   if(msg)msg.textContent='WAF install queued.';
   await refresh(true);
  }catch(e){if(msg)msg.textContent=e.message;}
  finally{if(msg)delete msg.dataset.sticky;}
 });
 on('docker-waf-uninstall',async()=>{
  const t=dockerWAFSelected(),msg=$('docker-waf-message');if(!t)return;
  if(!confirm(`Uninstall OWASP CRS WAF from ${t.name}? Host ports return to the app container.`))return;
  if(msg){msg.dataset.sticky='1';msg.textContent='Queueing WAF uninstall…';}
  try{
   await api('POST',`/api/docker/${t.kind==='application'?'applications':'containers'}/${t.id}/waf/uninstall`,{});
   if(msg)msg.textContent='WAF uninstall queued.';
   await refresh(true);
  }catch(e){if(msg)msg.textContent=e.message;}
  finally{if(msg)delete msg.dataset.sticky;}
 });
 if($('docker-waf-error-html'))$('docker-waf-error-html').oninput=()=>{dockerWAFErrorDirty=true;};
 on('docker-waf-error-preview',()=>{
  const html=$('docker-waf-error-html')?.value||dockerWAFDefaultHTML(dockerWAFErrorStatus);
  const blob=new Blob([html],{type:'text/html'});const url=URL.createObjectURL(blob);window.open(url,'_blank','noopener');setTimeout(()=>URL.revokeObjectURL(url),60000);
 });
 on('docker-waf-error-default',()=>{
  if($('docker-waf-error-html')){$('docker-waf-error-html').value=dockerWAFDefaultHTML(dockerWAFErrorStatus);dockerWAFErrorDirty=true;if($('docker-waf-error-message'))$('docker-waf-error-message').textContent='Default HTML loaded — click Save to apply.';}
 });
 on('docker-waf-error-save',async()=>{
  const t=dockerWAFSelected(),msg=$('docker-waf-error-message'),btn=$('docker-waf-error-save');if(!t)return;
  const html=($('docker-waf-error-html')?.value||'').trim();
  if(msg)msg.textContent='Saving error page…';if(btn)btn.disabled=true;
  try{
   await api('PUT',`/api/docker/${t.kind==='application'?'applications':'containers'}/${t.id}/waf/error-pages`,{status:dockerWAFErrorStatus,html});
   dockerWAFErrorDirty=false;dockerWAFErrorSource=t.id+':'+dockerWAFErrorStatus;
   if(msg)msg.textContent=`${dockerWAFErrorStatus} error page queued.`;
   await refresh(true);
  }catch(e){if(msg)msg.textContent=e.message;}
  finally{if(btn)btn.disabled=false;}
 });
 $('save')?.addEventListener('click',()=>refresh(true));
 setInterval(()=>{
  if(document.hidden)return;
  if($('docker-deploy-dialog')?.open||$('docker-log-dialog')?.open)return;
  refresh(false);
 },8000);
 window.cfPageRefreshers=window.cfPageRefreshers||{};
 window.cfPageRefreshers.docker=()=>refresh(true);
 showMethodFields();
 refresh(true);
})();
