(()=>{
 if(page!=='logs')return;
 const message=text=>{const el=$('logs-message');if(el)el.textContent=text||'';};
 const formMessage=(id,text)=>{const el=$(id);if(el)el.textContent=text||'';};
 let data=null,busy=false,paused=false,liveTimer=0,editingAlert='',liveID='';
 function action(label,fn,kind='small-btn'){const b=document.createElement('button');b.type='button';b.className=kind;b.textContent=label;b.onclick=async()=>{b.disabled=true;try{await fn();}catch(e){message(e.message);}finally{b.disabled=false;}};return b;}
 function encodeID(id){return encodeURIComponent(id);}
 function fillSelect(select,items,blank){
  const old=select.value;
  select.length=0;
  if(blank)select.add(new Option(blank,''));
  for(const item of items)select.add(new Option(item.name||item.id,item.id));
  if([...select.options].some(o=>o.value===old))select.value=old;
 }
 function syncSourceHelp(){
  const kind=$('logs-source-kind').value;
  const resources=(data?.resources||{})[kind]||[];
  fillSelect($('logs-source-resource'),resources,'Select resource');
  const help=$('logs-source-help'),path=$('logs-source-path');
  if(kind==='worker'){help.textContent='Absolute path on this worker. Must stay under /var/log.';path.placeholder='/var/log/clofix-filevps/app.log';path.required=true;}
  else if(kind==='database'){help.textContent='File under the database project, or leave as docker to use docker logs.';path.placeholder='docker';path.required=false;}
  else {help.textContent='Absolute log path on the selected resource.';path.placeholder='/var/log/nginx/error.log';path.required=true;}
 }
 function fillAlertSources(){
  const items=[{id:'*',name:'All sources'},...(data?.sources||[]).map(s=>({id:s.id,name:s.name+' · '+s.id}))];
  fillSelect($('logs-alert-source'),items);
 }
 function renderSources(){
  const table=$('logs-sources-table');
  row(table,['Name','Kind','Resource','Path','Origin','State','Actions'],true);
  const sources=data?.sources||[];
  if(!sources.length)row(table,['No log sources yet.','','','','','','']);
  for(const src of sources){
   const actions=document.createElement('div');
   actions.className='row-actions';
   actions.append(action('Live',()=>{
    liveID=src.id;
    $('logs-live-source').value=src.id;
    location.hash='#logs-live';
    refreshLive();
   }));
   if(src.origin==='custom')actions.append(action('Delete',async()=>{
    if(!confirm('Delete custom source '+src.name+'?'))return;
    await api('DELETE','/api/logs/sources/'+encodeID(src.id));
    message('Source deleted.');
    await refreshCatalog();
   },'danger small-btn'));
   row(table,[src.name,src.kind,src.resource_id,src.path,src.origin,src.error||src.state,actions]);
  }
  fillSelect($('logs-live-source'),(data?.sources||[]).map(s=>({id:s.id,name:s.name})),'Select source');
  if(liveID)$('logs-live-source').value=liveID;
 }
 function renderAlerts(){
  const enabled=(data?.channels||[]).filter(c=>c.enabled);
  $('logs-channels-hint').textContent=enabled.length
   ?'Matching new lines notify: '+enabled.map(c=>c.name+' ('+c.type+')').join(', ')+'.'
   :'No notification channel is enabled. Alerts are recorded here. Configure channels in Settings.';
  const table=$('logs-alerts-table');
  row(table,['Name','Source','Match','Level','Threshold','Cooldown','Status','Actions'],true);
  const alerts=data?.alerts||[];
  if(!alerts.length)row(table,['No alerts configured.','','','','','','','']);
  for(const a of alerts){
   const actions=document.createElement('div');
   actions.className='row-actions';
   actions.append(action('Edit',()=>editAlert(a)),action(a.enabled?'Disable':'Enable',async()=>{
    await api('PUT','/api/logs/alerts/'+a.id,{...a,enabled:!a.enabled});
    message('Alert updated.');
    await refreshCatalog();
   }));
   row(table,[a.name,a.source_id,a.match||'—',a.level||'any',a.threshold,a.cooldown_seconds+(a.enabled?'':' · off'),a.enabled?'Enabled':'Disabled',actions]);
  }
 }
 function renderEvents(events){
  const table=$('logs-events-table');
  row(table,['Time','Alert','Source','Sample','Delivery'],true);
  if(!events?.length)row(table,['No alerts have fired yet.','','','','']);
  for(const event of [...(events||[])].reverse()){
   const deliveries=Object.entries(event.deliveries||{}).map(([id,d])=>{
    const name=(data?.channels||[]).find(c=>c.id===id)?.name||'Removed channel';
    return name+': '+d.status+' ('+d.attempts+' attempts)';
   }).join(' · ');
   row(table,[event.at?new Date(event.at).toLocaleString():'—',event.name,event.source_id,event.sample||'—',deliveries||'No enabled channel at event time']);
  }
 }
 async function refreshCatalog(){
  if(busy||!T&&!$('tok').value.trim())return;
  busy=true;
  try{
   data=await api('GET','/api/logs');
   $('auth-banner').hidden=true;
   renderSources();
   fillAlertSources();
   renderAlerts();
   const events=await api('GET','/api/logs/alert-events');
   renderEvents(events.events||[]);
   if(location.hash==='#logs-analysis')await refreshAnalysis();
  }catch(e){message(e.message);}
  finally{busy=false;}
 }
 async function refreshLive(){
  const id=$('logs-live-source').value;
  liveID=id;
  if(!id){$('logs-live-output').textContent='';$('logs-live-meta').textContent='Pick a source. The tail refreshes every 5 seconds while this panel is open.';return;}
  if(paused){$('logs-live-meta').textContent='Paused.';return;}
  try{
   const q=$('logs-live-q').value.trim(),level=$('logs-live-level').value;
   const query=new URLSearchParams();
   if(q)query.set('q',q);
   if(level)query.set('level',level);
   const out=await api('GET','/api/logs/'+encodeID(id)+'/tail'+(query.toString()?'?'+query:''));
   $('logs-live-output').textContent=out.logs||'No logs yet.';
   $('logs-live-meta').textContent=(out.error?out.error+' · ':'')+'Updated '+new Date().toLocaleTimeString()+'.';
  }catch(e){$('logs-live-meta').textContent=e.message;}
 }
 async function refreshAnalysis(){
  try{
   const q=$('logs-analysis-q').value.trim();
   const out=await api('GET','/api/logs/analysis'+(q?'?q='+encodeURIComponent(q):''));
   const totals=out.totals||{};
   $('logs-analysis-totals').textContent=`${totals.lines||0} lines · ${totals.errors||0} errors · ${totals.warns||0} warnings`+(q?` · ${totals.matches||0} keyword matches`:'')+'.';
   const table=$('logs-analysis-table');
   row(table,['Source','Lines','Errors','Warnings','Keyword matches'],true);
   if(!out.sources?.length)row(table,['No tails available.','','','','']);
   for(const src of out.sources||[])row(table,[src.name,src.lines,src.errors,src.warns,src.matches]);
  }catch(e){message(e.message);}
 }
 function liveOpen(){return location.hash==='#logs-live'||(!$('logs-live').hidden&&location.hash==='');}
 function tickLive(){
  if(document.hidden||paused||$('logs-live').hidden)return;
  refreshLive();
 }
 function startLive(){
  if(liveTimer)return;
  liveTimer=setInterval(tickLive,5000);
  refreshLive();
 }
 function stopLive(){if(liveTimer){clearInterval(liveTimer);liveTimer=0;}}
 function resetSourceForm(){$('logs-source-form').reset();syncSourceHelp();formMessage('logs-source-message','');}
 function resetAlertForm(){editingAlert='';$('logs-alert-form').reset();$('logs-alert-threshold').value='1';$('logs-alert-cooldown').value='300';$('logs-alert-enabled').checked=true;$('logs-alert-title').textContent='Add alert';$('logs-alert-delete').hidden=true;formMessage('logs-alert-message','');fillAlertSources();}
 function editAlert(a){
  editingAlert=a.id;
  $('logs-alert-title').textContent='Edit alert';
  $('logs-alert-name').value=a.name;
  fillAlertSources();
  $('logs-alert-source').value=a.source_id;
  $('logs-alert-match').value=a.match||'';
  $('logs-alert-level').value=a.level||'';
  $('logs-alert-threshold').value=a.threshold||1;
  $('logs-alert-cooldown').value=a.cooldown_seconds||300;
  $('logs-alert-enabled').checked=!!a.enabled;
  $('logs-alert-delete').hidden=false;
  formMessage('logs-alert-message','');
  if(!$('logs-alert-dialog').open)$('logs-alert-dialog').showModal();
 }
 $('logs-source-kind').onchange=syncSourceHelp;
 $('logs-add-source').onclick=()=>{resetSourceForm();$('logs-source-dialog').showModal();};
 $('logs-source-close').onclick=$('logs-source-reset').onclick=()=>$('logs-source-dialog').close();
 $('logs-source-form').onsubmit=async e=>{
  e.preventDefault();
  const form=e.currentTarget;
  if(form.getAttribute('aria-busy')==='true')return;
  const finish=beginFormProgress(form,$('logs-source-save'),'Saving…');
  formMessage('logs-source-message','Saving source…');
  try{
   await api('POST','/api/logs/sources',{name:$('logs-source-name').value.trim(),kind:$('logs-source-kind').value,resource_id:$('logs-source-resource').value,path:$('logs-source-path').value.trim()});
   $('logs-source-dialog').close();
   resetSourceForm();
   message('Source added.');
   await refreshCatalog();
  }catch(err){formMessage('logs-source-message',err.message);}
  finally{finish();}
 };
 $('logs-add-alert').onclick=()=>{resetAlertForm();$('logs-alert-dialog').showModal();};
 $('logs-alert-close').onclick=$('logs-alert-reset').onclick=()=>$('logs-alert-dialog').close();
 $('logs-alert-form').onsubmit=async e=>{
  e.preventDefault();
  const form=e.currentTarget;
  if(form.getAttribute('aria-busy')==='true')return;
  const finish=beginFormProgress(form,$('logs-alert-save'),'Saving…');
  formMessage('logs-alert-message','Saving alert…');
  try{
   const payload={name:$('logs-alert-name').value.trim(),source_id:$('logs-alert-source').value,match:$('logs-alert-match').value.trim(),level:$('logs-alert-level').value,threshold:Number($('logs-alert-threshold').value),cooldown_seconds:Number($('logs-alert-cooldown').value),enabled:$('logs-alert-enabled').checked};
   if(!payload.match&&!payload.level)throw new Error('Keyword or level is required');
   await api(editingAlert?'PUT':'POST','/api/logs/alerts'+(editingAlert?'/'+editingAlert:''),payload);
   $('logs-alert-dialog').close();
   resetAlertForm();
   message('Alert saved.');
   await refreshCatalog();
  }catch(err){formMessage('logs-alert-message',err.message);}
  finally{finish();}
 };
 $('logs-alert-delete').onclick=async()=>{
  if(!editingAlert||!confirm('Delete this alert?'))return;
  await api('DELETE','/api/logs/alerts/'+editingAlert);
  $('logs-alert-dialog').close();
  resetAlertForm();
  message('Alert deleted.');
  await refreshCatalog();
 };
 $('logs-live-source').onchange=()=>{paused=false;$('logs-pause').textContent='Pause';refreshLive();};
 $('logs-live-q').oninput=$('logs-live-level').onchange=()=>refreshLive();
 $('logs-pause').onclick=()=>{paused=!paused;$('logs-pause').textContent=paused?'Resume':'Pause';if(!paused)refreshLive();};
 $('logs-download').onclick=async()=>{
  const id=$('logs-live-source').value;
  if(!id){message('Pick a source to download.');return;}
  try{
   const r=await fetch('/api/logs/'+encodeID(id)+'/download',{credentials:'include',cache:'no-store'});
   if(!r.ok)throw new Error((await r.json().catch(()=>({}))).error||r.status);
   const blob=await r.blob();
   const a=document.createElement('a');
   a.href=URL.createObjectURL(blob);
   a.download=id.replace(/[:/]/g,'-')+'.log';
   a.click();
   URL.revokeObjectURL(a.href);
   message('Download started.');
  }catch(e){message(e.message);}
 };
 $('logs-analysis-q').onchange=refreshAnalysis;
 addEventListener('hashchange',()=>{
  if(location.hash==='#logs-live')startLive();
  else stopLive();
  if(location.hash==='#logs-analysis')refreshAnalysis();
 });
 $('save')?.addEventListener('click',refreshCatalog);
 $('tok')?.addEventListener('change',refreshCatalog);
 refreshCatalog().then(()=>{
  if(location.hash==='#logs-live'||(!location.hash&&!$('logs-live').hidden))startLive();
  if(location.hash==='#logs-analysis')refreshAnalysis();
 });
})();
