(()=>{
 if(page!=='security'&&document.body?.dataset?.page!=='security'&&!location.pathname.startsWith('/security')){
  // Still expose home activity helper when included globally.
 }
 let overview=null,busy=false;

 function fmtAt(v){
  if(!v)return '—';
  const d=new Date(v);
  return Number.isNaN(d.getTime())?String(v):d.toLocaleString();
 }

 async function loadOverview(){
  overview=await api('GET','/api/security/overview');
  return overview;
 }

 function renderScore(){
  if(!overview)return;
  if($('security-score-value'))$('security-score-value').textContent=String(overview.score??overview.global?.score??'—');
  if($('security-score-findings-count'))$('security-score-findings-count').textContent=String((overview.findings||[]).length);
  if($('security-score-alerts'))$('security-score-alerts').textContent=String(overview.alerts?.open_24h??'—');
  const userns=$('security-score-userns');
  if(userns){
   if(!overview.posture?.user_ns_known)userns.textContent='Unknown';
   else userns.textContent=overview.posture.user_ns_enabled?'Enabled':'Lab (false)';
  }
  const formula=$('security-score-formula');
  if(formula&&overview.formula){
   formula.textContent='Score formula: '+overview.formula.map(f=>`${f.reason} ${f.delta}`+(f.cap?` (cap ${f.cap})`:'')).join(' · ');
  }
  const links=$('security-deeplinks');
  if(links){
   links.replaceChildren();
   (overview.deep_links||[]).forEach(l=>{
    const a=document.createElement('a');a.className='small-btn';a.href=l.href;a.textContent=l.label;links.append(a);
   });
  }
  const table=$('security-findings-table');
  if(table){
   row(table,['Scope','Asset','Delta','Reason'],true);
   const findings=overview.findings||[];
   if(!findings.length)row(table,['No findings — score is 100','','','']);
   findings.forEach(f=>row(table,[f.scope,f.asset,String(f.delta),f.reason]));
  }
  if($('security-alerts-open'))$('security-alerts-open').textContent=String(overview.alerts?.open_24h??'—');
  if($('security-alerts-total'))$('security-alerts-total').textContent=String(overview.alerts?.total??'—');
 }

 async function renderAlerts(){
  const table=$('security-alerts-table');if(!table)return;
  row(table,['When','Name','Source','Sample'],true);
  try{
   const data=await api('GET','/api/logs/alert-events');
   const events=(data.events||[]).slice().sort((a,b)=>new Date(b.at)-new Date(a.at)).slice(0,25);
   if(!events.length)row(table,['No alert-events yet','','','']);
   events.forEach(e=>row(table,[fmtAt(e.at),e.name||e.alert_id,e.source_id,(e.sample||'').slice(0,120)]));
  }catch(e){row(table,[e.message,'','','']);}
 }

 async function renderAudit(){
  const table=$('security-audit-table');if(!table)return;
  row(table,['When','Actor','Action','Target','Result'],true);
  try{
   const data=await api('GET','/api/audit?limit=50');
   const events=data.events||[];
   if(!events.length)row(table,['No audit events','','','','']);
   events.forEach(e=>row(table,[fmtAt(e.at),e.actor,e.action,e.target,e.result||'']));
  }catch(e){row(table,[e.message,'','','','']);}
 }

 async function renderModes(){
  const table=$('security-modes-table');if(!table)return;
  row(table,['ID','Name','Template','Version','Controls'],true);
  try{
   const data=await api('GET','/api/security/modes');
   const policies=(data.policies||[]).filter(p=>p.id!=='tpl-quarantine'&&p.template!=='quarantine'&&p.id!=='tpl-database_protection'&&p.template!=='database_protection');
   policies.forEach(p=>{
    const controls=Object.entries(p.controls||{}).map(([k,v])=>k+'='+v).join(', ');
    row(table,[p.id,p.name,p.template||'—',String(p.version),controls]);
   });
   if(!policies.length)row(table,['No policies','','','','']);
  }catch(e){row(table,[e.message,'','','','']);}
 }

 async function runMode(dry){
  const policy=$('security-mode-policy')?.value.trim();
  const targets=($('security-mode-targets')?.value||'').split(',').map(s=>s.trim()).filter(Boolean);
  const msg=$('security-modes-message'),plan=$('security-mode-plan');
  if(!policy||!targets.length){if(msg)msg.textContent='Policy ID and at least one VPS ID are required.';return;}
  try{
   const path=dry?`/api/security/modes/${encodeURIComponent(policy)}/dry-run`:`/api/security/modes/${encodeURIComponent(policy)}/apply`;
   const result=await api('POST',path,{instance_ids:targets});
   if(msg)msg.textContent=dry?'Dry-run complete.':'Apply complete.';
   if(plan){plan.hidden=false;plan.textContent=JSON.stringify(result,null,2);}
  }catch(e){if(msg)msg.textContent=e.message;}
 }

 async function renderDetections(){
  const table=$('security-detections-table');if(!table)return;
  row(table,['When','Type','Severity','Asset','Evidence'],true);
  try{
   const data=await api('GET','/api/security/detections?limit=50');
   const list=data.detections||[];
   if(!list.length)row(table,['No detections — click Refresh detections','','','','']);
   list.forEach(d=>row(table,[fmtAt(d.at),d.type,d.severity,d.asset,(d.evidence||'').slice(0,140)]));
  }catch(e){row(table,[e.message,'','','','']);}
  const fim=$('security-fim-table');
  if(fim){
   row(fim,['Worker','Paths','Updated'],true);
   try{
    const data=await api('GET','/api/security/fim');
    const list=data.baselines||[];
    if(!list.length)row(fim,['No FIM baselines reported','','']);
    list.forEach(b=>row(fim,[b.worker_id,String(Object.keys(b.paths||{}).length),fmtAt(b.updated)]));
   }catch(e){row(fim,[e.message,'','']);}
  }
 }

 async function renderVulns(){
  try{
   const data=await api('GET','/api/security/vulnerabilities');
   if($('security-db-score'))$('security-db-score').textContent=String(data.databases?.score??'—');
   if($('security-cks-score'))$('security-cks-score').textContent=String(data.cks?.score??'—');
   if($('security-cks-note'))$('security-cks-note').textContent=data.cks?.cks_readiness_status?.note||'CKS Readiness Status';
   if($('security-pkg-status'))$('security-pkg-status').textContent=data.packages?.status||'Planned';
   const table=$('security-vuln-findings');
   if(table){
    row(table,['Scope','Asset','Delta','Reason'],true);
    const findings=[...(data.databases?.findings||[]),...(data.cks?.findings||[])];
    if(!findings.length)row(table,['No DB/CKS findings','','','']);
    findings.forEach(f=>row(table,[f.scope,f.asset,String(f.delta),f.reason]));
   }
  }catch(e){if($('security-db-score'))$('security-db-score').textContent=e.message;}
 }

 async function renderAutoPilot(){
  try{
   const data=await api('GET','/api/security/autopilot');
   const mode=data.autopilot?.mode||'monitor';
   if($('security-autopilot-mode'))$('security-autopilot-mode').textContent=mode;
   if($('security-autopilot-select'))$('security-autopilot-select').value=mode;
   if($('security-autopilot-note'))$('security-autopilot-note').textContent=data.autopilot?.updated_by?`Updated by ${data.autopilot.updated_by}`:'';
   if($('security-autopilot-expires')&&data.autopilot?.expires)$('security-autopilot-expires').value=data.autopilot.expires;
  }catch(e){if($('security-autopilot-message'))$('security-autopilot-message').textContent=e.message;}
  const inc=$('security-incidents-table');
  if(inc){
   row(inc,['ID','Title','Severity','Status','Updated'],true);
   try{
    const data=await api('GET','/api/security/incidents');
    const list=data.incidents||[];
    if(!list.length)row(inc,['No incidents','','','','']);
    list.forEach(i=>row(inc,[i.id,i.title,i.severity,i.status,fmtAt(i.updated)]));
   }catch(e){row(inc,[e.message,'','','','']);}
  }
  const comp=$('security-compliance-table');
  if(comp){
   row(comp,['Framework','Control','Status','Detail'],true);
   try{
    const data=await api('GET','/api/security/compliance');
    (data.controls||[]).forEach(c=>row(comp,[c.framework,c.title,c.status,c.detail||'']));
   }catch(e){row(comp,[e.message,'','','']);}
  }
 }

 async function saveAutoPilot(){
  const msg=$('security-autopilot-message');
  const body={mode:$('security-autopilot-select')?.value||'monitor'};
  const exp=$('security-autopilot-expires')?.value.trim();
  if(exp)body.expires=new Date(exp).toISOString();
  try{
   const result=await api('PUT','/api/security/autopilot',body);
   if(msg)msg.textContent='AutoPilot saved: '+result.mode;
   await renderAutoPilot();
  }catch(e){if(msg)msg.textContent=e.message;}
 }

 async function renderAdvancedProtection(){
  const sum=overview?.protection_catalog_summary||{};
  if($('security-prot-live'))$('security-prot-live').textContent=String(sum.live_or_ro??'—');
  if($('security-prot-partial'))$('security-prot-partial').textContent=String(sum.partial??'—');
  if($('security-prot-planned'))$('security-prot-planned').textContent=String(sum.planned??'—');
  const cat=$('security-protection-catalog');
  if(cat){
   row(cat,['#','Domain','Feature','Status','Note'],true);
   try{
    const data=await api('GET','/api/security/protection-catalog');
    (data.features||[]).forEach(f=>row(cat,[String(f.id),f.domain,f.name,f.status,(f.note||'').slice(0,80)]));
   }catch(e){row(cat,[e.message,'','','','']);}
  }
  const fp=$('security-filevps-posture');
  if(fp){
   row(fp,['VPS','Protection','Root RO','Pkg lock','SSH block','Key-only'],true);
   (overview?.filevps||[]).forEach(i=>row(fp,[i.instance_id,String(!!i.protection_mode),String(!!i.root_ro),String(!!i.package_install_locked),String(!!i.ssh_login_blocked),String(!!i.ssh_key_only)]));
   if(!(overview?.filevps||[]).length)row(fp,['No VPS','','','','','']);
  }
  const ne=$('security-network-exposure');
  if(ne){
   row(ne,['VPS','Custom forwards','FW restrict','Ports','Egress'],true);
   try{
    const data=await api('GET','/api/security/network-exposure');
    (data.exposures||[]).forEach(x=>row(ne,[x.instance_id,String(x.custom_forwards),String(!!x.firewall_restrict),String((x.ports||[]).length),x.egress_status||'planned']));
    if(!(data.exposures||[]).length)row(ne,['No exposure data','','','','']);
   }catch(e){row(ne,[e.message,'','','','']);}
  }
  const wp=$('security-worker-posture');
  if(wp){
   row(wp,['Worker','Status','User NS','Maintenance','Seccomp','LSM'],true);
   (overview?.worker_posture||[]).forEach(w=>row(wp,[w.worker_id,w.status,String(w.user_ns),String(!!w.maintenance_lock),w.seccomp,w.apparmor_selinux]));
   if(!(overview?.worker_posture||[]).length)row(wp,['No workers','','','','','']);
  }
  const st=$('security-storage-protection');
  if(st){
   row(st,['Volume','Delete protection','State'],true);
   try{
    const data=await api('GET','/api/security/storage-protection');
    (data.volumes||[]).forEach(v=>row(st,[v.id+' · '+(v.name||''),String(!!v.delete_protection),v.state]));
    if(!(data.volumes||[]).length)row(st,['No volumes','','']);
   }catch(e){row(st,[e.message,'','']);}
  }
  const apr=$('security-approvals-table');
  if(apr){
   row(apr,['ID','Type','Status','Requested','Updated'],true);
   try{
    const data=await api('GET','/api/security/approvals');
    (data.approvals||[]).forEach(a=>row(apr,[a.id,a.type,a.status,a.requested_by,fmtAt(a.updated)]));
    if(!(data.approvals||[]).length)row(apr,['No approvals','','','','']);
   }catch(e){row(apr,[e.message,'','','','']);}
  }
 }

 window.renderSecurityCenter=async function renderSecurityCenter(){
  if(busy)return;busy=true;
  try{
   await loadOverview();
   renderScore();
   await Promise.all([renderAlerts(),renderAudit(),renderModes(),renderAdvancedProtection(),renderDetections(),renderVulns(),renderAutoPilot()]);
  }catch(e){
   if($('security-loading')){$('security-loading').hidden=false;$('security-loading').textContent='Security Center failed: '+e.message;}
  }finally{busy=false;if(typeof window.enhanceStatWaves==='function')window.enhanceStatWaves();}
 };

 window.loadHomeActivity=async function loadHomeActivity(){
  const state=$('home-activity-state'),empty=$('home-activity-empty'),list=$('home-activity-list');
  if(!list)return;
  try{
   const data=await api('GET','/api/audit?limit=10');
   const events=data.events||[];
   list.replaceChildren();
   if(!events.length){
    if(empty){empty.hidden=false;empty.textContent='No audit events yet.';}
    if(state){state.textContent='Empty';state.className='badge STOPPED';}
    list.hidden=true;
    return;
   }
   if(empty)empty.hidden=true;
   list.hidden=false;
   events.forEach(e=>{
    const li=document.createElement('li');
    li.textContent=`${fmtAt(e.at)} · ${e.actor||'system'} · ${e.action} · ${e.target||''}${e.result?' · '+e.result:''}`;
    list.append(li);
   });
   if(state){state.textContent='Live';state.className='badge RUNNING';}
  }catch(e){
   if(empty){empty.hidden=false;empty.textContent='Could not load audit: '+e.message;}
   if(state){state.textContent='Error';state.className='badge FAILED';}
   list.hidden=true;
  }
 };

 if(typeof page!=='undefined'&&page==='security'){
  $('security-modes-refresh')?.addEventListener('click',()=>renderModes());
  $('security-mode-dryrun')?.addEventListener('click',()=>runMode(true));
  $('security-mode-apply')?.addEventListener('click',()=>runMode(false));
  $('security-detections-refresh')?.addEventListener('click',async()=>{
   try{await api('POST','/api/security/detections/refresh',{});await renderDetections();}
   catch(e){if($('security-modes-message'))$('security-modes-message').textContent=e.message;}
  });
  $('security-autopilot-save')?.addEventListener('click',saveAutoPilot);
  $('security-unlock-btn')?.addEventListener('click',async()=>{
   const msg=$('security-protection-message');
   try{
    const result=await api('POST','/api/security/unlock',{instance_id:$('security-unlock-vps')?.value.trim(),control:$('security-unlock-control')?.value,ttl_seconds:Number($('security-unlock-ttl')?.value)||3600});
    if(msg)msg.textContent='Unlock '+result.id+' until '+fmtAt(result.expires);
   }catch(e){if(msg)msg.textContent=e.message;}
  });
  $('security-scheduler-tick')?.addEventListener('click',async()=>{
   const msg=$('security-protection-message');
   try{const r=await api('POST','/api/security/scheduler/tick',{});if(msg)msg.textContent='Scheduler tick: '+(r.applied||[]).length+' binding(s), unlocks restored '+(r.unlocks_restored||[]).length;}
   catch(e){if(msg)msg.textContent=e.message;}
  });
  $('security-quarantine-btn')?.addEventListener('click',async()=>{
   const msg=$('security-protection-message'),id=$('security-unlock-vps')?.value.trim();
   if(!id){if(msg)msg.textContent='Enter a VPS ID first.';return;}
   try{await api('POST','/api/security/quarantine/'+encodeURIComponent(id),{});if(msg)msg.textContent='Quarantined '+id;await renderAdvancedProtection();}
   catch(e){if(msg)msg.textContent=e.message;}
  });
 }
})();
