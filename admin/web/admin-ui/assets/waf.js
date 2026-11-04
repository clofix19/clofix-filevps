(()=>{
 const isList=page==='waf', isDetail=page==='waf-binding';
 if(!isList&&!isDetail)return;
 let data=null, editID=document.body.dataset.instanceId||'', errorStatus='403', errorDirty=false, errorSource='';
 const statuses=['403','404','406','429','500'];
 const msg=t=>{if($('waf-page-message'))$('waf-page-message').textContent=t||'';};
 const formMsg=t=>{if($('waf-binding-form-message'))$('waf-binding-form-message').textContent=t||'';};
 const detailMsg=t=>{if($('wafb-message'))$('wafb-message').textContent=t||'';};
 const items=()=>window.cfWAFItems||[];
 const defaultHTML=s=>typeof window.cfDefaultWAFErrorHTML==='function'?window.cfDefaultWAFErrorHTML(s):`<!DOCTYPE html><html><body><h1>${s}</h1><p>Request blocked by CloFix WAF.</p></body></html>`;
 const stateBadge=st=>{
  const b=document.createElement('span');
  const u=String(st||'INACTIVE').toUpperCase();
  b.className='badge '+(u==='ACTIVE'||u==='READY'?'RUNNING':u==='FAILED'?'FAILED':u==='ACTIVATING'||u==='DEACTIVATING'?'PENDING':'STOPPED');
  b.textContent=u;
  return b;
 };
 function binding(id){return (data?.bindings||[]).find(b=>b.id===id)||null;}
 function fillTargets(){
  const kind=$('waf-binding-kind')?.value||'vps', sel=$('waf-binding-target');
  if(!sel)return;
  const keep=sel.value;
  sel.textContent='';
  sel.add(new Option('Select target…',''));
  (data?.targets||[]).filter(t=>t.kind===kind&&!t.bound).forEach(t=>{
   const label=`${t.name||t.id} · ${t.worker_id||'—'}${t.waf_state?` · ${t.waf_state}`:''}`;
   sel.add(new Option(label,t.id));
  });
  if([...sel.options].some(o=>o.value===keep))sel.value=keep;
 }
 function renderEngines(){
  const table=$('waf-engines-table');if(!table)return;
  row(table,['Worker','Hostname','Status','Engine','Version / error'],true);
  const list=data?.engines||[];
  if(!list.length){row(table,['No workers enrolled','','','','']);return;}
  list.forEach(e=>{
   const ready=!!e.ready;
   const badge=document.createElement('span');
   badge.className='badge '+(ready?'RUNNING':(e.error?'FAILED':'STOPPED'));
   badge.textContent=ready?'READY':(e.error?'ERROR':'NOT READY');
   const detail=ready?(e.version||'owasp/modsecurity-crs:nginx'):(e.error||(e.docker_ready===false?'Docker engine required':'Waiting for worker prepare…'));
   const live=document.createElement('span');
   live.className='badge '+(e.live?'RUNNING':'STOPPED');
   live.textContent=e.live?'Live':(e.status||'offline');
   row(table,[e.worker_id,e.hostname||'—',live,badge,detail]);
  });
 }
 function renderBindings(){
  const table=$('waf-bindings-table');if(!table)return;
  row(table,['Name','Target','Worker','Hits','Blocks','State','Actions'],true);
  const list=data?.bindings||[];
  if(!list.length){row(table,['No bindings yet. Create one to activate WAF for a VPS or Docker target.','','','','','','']);return;}
  list.forEach(b=>{
   const actions=document.createElement('div');actions.className='row-actions';
   const open=document.createElement('a');open.className='small-btn';open.href='/waf/bindings/'+encodeURIComponent(b.id);open.textContent='Open';
   const act=document.createElement('button');act.type='button';act.className='small-btn';
   act.textContent=b.state==='ACTIVE'||b.state==='ACTIVATING'?'Deactivate':'Activate';
   act.onclick=async()=>{
    try{
     if(b.state==='ACTIVE'||b.state==='ACTIVATING'){
      if(!confirm(`Deactivate WAF for ${b.name||b.target_id}?`))return;
      await api('POST',`/api/waf/bindings/${b.id}/deactivate`,{});
      msg('Deactivate queued.');
     }else{
      await api('POST',`/api/waf/bindings/${b.id}/activate`,{});
      msg('Activate queued.');
     }
     await refreshWAF();
    }catch(e){msg(e.message);}
   };
   const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';
   del.onclick=async()=>{
    if(!confirm(`Delete binding ${b.name||b.id}? Active WAF will be deactivated.`))return;
    try{await api('DELETE',`/api/waf/bindings/${b.id}`);msg('Binding deleted.');await refreshWAF();}catch(e){msg(e.message);}
   };
   actions.append(open,act,del);
   const nameLink=document.createElement('a');
   nameLink.className='waf-name-btn';nameLink.href='/waf/bindings/'+encodeURIComponent(b.id);
   nameLink.textContent=b.name||b.id;nameLink.title='Open binding details';
   const target=`${b.target_kind} · ${b.target_id}`;
   const st=stateBadge(b.state);
   if(b.error){st.title=b.error;st.textContent=(b.state||'FAILED')+' · error';}
   const hits=b.stats!=null?String(b.stats.hits??0):'—';
   const blocks=b.stats!=null?String(b.stats.blocks??0):'—';
   row(table,[nameLink,target,b.worker_id||'—',hits,blocks,st,actions]);
  });
 }
 const wafHistKey=id=>'cf_waf_stats_hist_v1_'+id;
 function loadWAFHistory(id){
  try{const raw=JSON.parse(localStorage.getItem(wafHistKey(id))||'[]');return Array.isArray(raw)?raw:[];}catch(e){return [];}
 }
 function pushWAFHistory(id,s){
  if(!id||!s)return loadWAFHistory(id);
  const at=Date.parse(s.at)||Date.now();
  const point={at,hits:Number(s.hits)||0,blocks:Number(s.blocks)||0,attacks:Number(s.attacks)||0,unique_ips:Number(s.unique_ips)||0};
  let hist=loadWAFHistory(id);
  const last=hist[hist.length-1];
  // Avoid duplicate samples from the same stats snapshot.
  if(last&&Math.abs(last.at-point.at)<2000&&last.hits===point.hits&&last.blocks===point.blocks)return hist;
  hist.push(point);
  if(hist.length>120)hist=hist.slice(-120);
  try{localStorage.setItem(wafHistKey(id),JSON.stringify(hist));}catch(e){}
  return hist;
 }
 function sparkline(values,tone){
  const svg=document.createElementNS('http://www.w3.org/2000/svg','svg');
  svg.setAttribute('class','waf-stat-spark'+(tone?' spark-'+tone:''));
  svg.setAttribute('viewBox','0 0 120 36');
  svg.setAttribute('preserveAspectRatio','none');
  svg.setAttribute('aria-hidden','true');
  const pts=values.length?values:[0];
  const max=Math.max(1,...pts);
  const step=pts.length<2?120:120/(pts.length-1);
  const coords=pts.map((v,i)=>{
   const x=i*step,y=34-(v/max)*30;
   return [x,Math.max(2,Math.min(34,y))];
  });
  const line=coords.map((p,i)=>(i?'L':'M')+p[0].toFixed(1)+' '+p[1].toFixed(1)).join(' ');
  const area=line+' L'+coords[coords.length-1][0].toFixed(1)+' 36 L0 36 Z';
  const fill=document.createElementNS('http://www.w3.org/2000/svg','path');
  fill.setAttribute('d',area);fill.setAttribute('class','spark-fill');
  const path=document.createElementNS('http://www.w3.org/2000/svg','path');
  path.setAttribute('d',line);path.setAttribute('class','spark-line');
  svg.append(fill,path);
  return svg;
 }
 function renderWAFCharts(id,hist){
  if(typeof drawCloudChart!=='function')return;
  const seriesTraffic=[
   {key:'hits',label:'Hits',color:'#43d6c5',area:true},
   {key:'blocks',label:'Blocks',color:'#f4c56a',cls:'secondary-line'},
   {key:'attacks',label:'Attacks',color:'#77baff',cls:'secondary-line'},
  ];
  // drawCloudChart expects usageHistoryWindow; pass hist and override via opts.history
  // by temporarily swapping is not needed — drawCloudChart uses opts.history directly.
  drawCloudChart('chart-waf-traffic',{
   history:hist,
   series:seriesTraffic,
   currentID:'chart-waf-traffic-current',
   formatCurrent:last=>last?`${last.hits||0} hits · ${last.blocks||0} blocks`:'—',
  });
  drawCloudChart('chart-waf-ips',{
   history:hist,
   series:[{key:'unique_ips',label:'Unique IPs',color:'#77baff',area:true}],
   currentID:'chart-waf-ips-current',
   formatCurrent:last=>last?String(last.unique_ips||0)+' IPs':'—',
  });
 }
 function withScroll(run){
  if(typeof preservePageScroll==='function')return preservePageScroll(run);
  const x=window.scrollX,y=window.scrollY;
  const out=run();
  const restore=()=>{window.scrollTo(x,y);requestAnimationFrame(()=>window.scrollTo(x,y));};
  if(out&&typeof out.then==='function')return out.finally(restore);
  restore();
  return out;
 }
 function renderStats(b){
  const kpis=$('wafb-stats-kpis'), note=$('wafb-stats-note');
  const s=b?.stats;
  const hist=s?pushWAFHistory(b.id,s):loadWAFHistory(b?.id||editID);
  const series=key=>hist.map(p=>Number(p[key])||0);
  const cards=[
   ['Hits',s?String(s.hits??0):'—','hits',''],
   ['Blocks',s?String(s.blocks??0):'—','blocks','amber'],
   ['Attacks',s?String(s.attacks??0):'—','attacks','rose'],
   ['Unique IPs',s?String(s.unique_ips??0):'—','unique_ips','blue'],
  ];
  if(kpis){
   // Update cards in place so refresh does not jump scroll.
   if(kpis.children.length===cards.length){
    [...kpis.children].forEach((d,i)=>{
     const [,val,key,tone]=cards[i];
     const v=d.querySelector('.waf-stat-value');
     if(v)v.textContent=val;
     const old=d.querySelector('.waf-stat-spark');
     const next=sparkline(series(key),tone);
     if(old)old.replaceWith(next);else d.append(next);
    });
   }else{
    kpis.textContent='';
    cards.forEach(([label,val,key,tone])=>{
     const d=document.createElement('div');d.className='waf-stat';
     const l=document.createElement('span');l.className='waf-stat-label';l.textContent=label;
     const v=document.createElement('div');v.className='waf-stat-value';v.textContent=val;
     d.append(l,v,sparkline(series(key),tone));kpis.append(d);
    });
   }
  }
  renderWAFCharts(b?.id||editID,hist);
  if(note){
   if(!s)note.textContent='No samples yet — click Refresh stats to pull edge proxy logs.';
   else{
    const when=s.at?new Date(s.at).toLocaleString():'—';
    note.textContent=`Sampled ${s.sampled||0} events from ${s.proxy||'proxy'} · updated ${when}${s.error?' · '+s.error:''}`;
   }
  }
  const fill=(id,rows,empty)=>{
   const t=$(id);if(!t)return;
   row(t,['Key','Count'],true);
   if(!rows||!rows.length){row(t,[empty,'']);return;}
   rows.forEach(r=>row(t,[r.key,String(r.count)]));
  };
  fill('wafb-stats-ips',s?.top_ips,'No IPs yet');
  fill('wafb-stats-uris',s?.top_uris,'No URIs yet');
  fill('wafb-stats-attacks',s?.top_attacks,'No attacks yet');
  const recent=$('wafb-stats-recent');
  if(recent){
   row(recent,['Time','IP','Method','URI','Status','Attack','Detail'],true);
   const list=s?.recent||[];
   if(!list.length){row(recent,['No recent events','','','','','','']);}
   else list.slice(0,40).forEach(e=>{
    const badge=document.createElement('span');
    badge.className=e.blocked?'badge FAILED':'badge RUNNING';
    badge.textContent=e.status||(e.blocked?'block':'ok');
    row(recent,[e.time||'—',e.ip||'—',e.method||'—',e.uri||'—',badge,e.attack||'—',e.message||e.rule_id||'—']);
   });
  }
 }
 async function refreshStats(id){
  if(!id)return;
  try{
   await api('POST',`/api/waf/bindings/${id}/stats/refresh`,{});
   if($('wafb-stats-note'))$('wafb-stats-note').textContent='Refreshing from edge proxy…';
  }catch(e){detailMsg(e.message);}
 }
 function renderEditRules(b){
  const controls=$('wafb-controls');if(!controls)return;
  const active=b.state==='ACTIVE';
  controls.textContent='';
  items().forEach(([key,label,tag])=>{
   const item=document.createElement('div');item.className='waf-rule';
   const info=document.createElement('div');info.className='waf-rule-info';
   const title=document.createElement('div');title.className='waf-rule-title';title.textContent=label;
   const code=document.createElement('div');code.className='waf-rule-tag';code.textContent=tag;
   info.append(title,code);
   const toggle=document.createElement('button');toggle.type='button';toggle.className='waf-toggle';toggle.setAttribute('role','switch');
   const enabled=!b||b.owasp_protection?.[key]!==false;
   toggle.setAttribute('aria-checked',String(enabled));
   toggle.disabled=!active;
   toggle.onclick=async()=>{
    toggle.disabled=true;detailMsg('Queueing rule update…');
    try{
     await api('POST',`/api/waf/bindings/${b.id}/category`,{category:key,enabled:!enabled});
     detailMsg('Rule update queued.');
     await refreshWAF();
    }catch(e){detailMsg(e.message);toggle.disabled=false;}
   };
   item.append(info,toggle);controls.append(item);
  });
  const enabledCount=items().filter(([key])=>!b||b.owasp_protection?.[key]!==false).length;
  if($('wafb-rule-summary'))$('wafb-rule-summary').textContent=active?`${enabledCount} of ${items().length} rule groups enabled`:'Activate the binding to manage rules.';
 }
 function renderErrorTabs(b){
  const tabs=$('wafb-error-tabs');if(!tabs)return;
  if(tabs.children.length!==statuses.length){
   tabs.textContent='';
   statuses.forEach(code=>{
    const btn=document.createElement('button');btn.type='button';btn.className='waf-status-tab';btn.dataset.status=code;btn.textContent=code;btn.setAttribute('role','tab');
    btn.onclick=()=>{if(errorDirty&&!confirm('Discard unsaved changes?'))return;errorStatus=code;errorDirty=false;errorSource='';renderErrorEditor(b);};
    tabs.append(btn);
   });
  }
  [...tabs.children].forEach(btn=>btn.setAttribute('aria-selected',String(btn.dataset.status===errorStatus)));
 }
 function renderErrorEditor(b){
  renderErrorTabs(b);
  if($('wafb-error-label'))$('wafb-error-label').textContent=errorStatus;
  const html=$('wafb-error-html');
  const key=(b?.id||'')+':'+errorStatus;
  if(html&&errorSource!==key&&!errorDirty){
   const custom=(b?.waf_error_pages||{})[errorStatus];
   html.value=(custom&&String(custom).trim())||defaultHTML(errorStatus);
   errorSource=key;
  }
 }
 let detailHydrated=false, lastDetailState='';
 function renderDetail(b, opts){
  opts=opts||{};
  withScroll(()=>{
   if(!b){
    if($('wafb-title'))$('wafb-title').textContent='Binding not found';
    detailMsg('This WAF binding no longer exists.');
    return;
   }
   if($('wafb-title'))$('wafb-title').textContent=b.name||b.id;
   if($('wafb-meta'))$('wafb-meta').textContent=`${b.target_kind} · ${b.target_id} · worker ${b.worker_id||'—'}${b.error?' · '+b.error:''}`;
   const st=$('wafb-state');
   if(st){st.className=stateBadge(b.state).className;st.textContent=b.state||'INACTIVE';}
   if($('wafb-activate'))$('wafb-activate').disabled=b.state==='ACTIVE'||b.state==='ACTIVATING';
   if($('wafb-deactivate'))$('wafb-deactivate').disabled=b.state==='INACTIVE'||b.state==='DEACTIVATING';
   renderStats(b);
   // Poll refresh: skip rebuilding rules/error editor (causes scroll jump).
   if(!opts.statsOnly){
    renderEditRules(b);
    renderErrorEditor(b);
   }
   const title=$('page-title');
   if(title)title.textContent=b.name||'WAF Binding';
  });
 }
 async function refreshWAF(){
  data=await api('GET','/api/waf');
  if(isList){
   withScroll(()=>{renderEngines();renderBindings();fillTargets();});
  }
  if(isDetail&&editID){
   const b=binding(editID);
   const stateChanged=!!b&&b.state!==lastDetailState;
   renderDetail(b,{statsOnly:detailHydrated&&!stateChanged});
   lastDetailState=b?.state||'';
   detailHydrated=true;
  }
 }
 function openCreate(){
  if($('waf-binding-id'))$('waf-binding-id').value='';
  if($('waf-binding-name'))$('waf-binding-name').value='';
  if($('waf-binding-activate'))$('waf-binding-activate').checked=true;
  if($('waf-binding-title'))$('waf-binding-title').textContent='New WAF binding';
  if($('waf-binding-save'))$('waf-binding-save').textContent='Create binding';
  formMsg('');
  fillTargets();
  $('waf-binding-dialog')?.showModal();
 }

 if(isList){
  if($('waf-binding-add'))$('waf-binding-add').onclick=openCreate;
  if($('waf-binding-close'))$('waf-binding-close').onclick=()=>$('waf-binding-dialog')?.close();
  if($('waf-binding-cancel'))$('waf-binding-cancel').onclick=()=>$('waf-binding-dialog')?.close();
  if($('waf-binding-kind'))$('waf-binding-kind').onchange=()=>fillTargets();
  if($('waf-binding-form'))$('waf-binding-form').onsubmit=async e=>{
   e.preventDefault();
   const kind=$('waf-binding-kind').value, target_id=$('waf-binding-target').value;
   if(!target_id){formMsg('Select a target.');return;}
   const body={name:($('waf-binding-name').value||'').trim(),target_kind:kind,target_id,activate:!!$('waf-binding-activate')?.checked};
   formMsg('Creating…');
   try{
    const created=await api('POST','/api/waf/bindings',body);
    formMsg('');
    $('waf-binding-dialog')?.close();
    msg('Binding created.');
    if(created?.id){location.assign('/waf/bindings/'+encodeURIComponent(created.id));return;}
    await refreshWAF();
   }catch(err){formMsg(err.message);}
  };
 }

 if(isDetail){
  if($('wafb-stats-refresh'))$('wafb-stats-refresh').onclick=()=>{if(editID)refreshStats(editID);};
  if($('wafb-activate'))$('wafb-activate').onclick=async()=>{
   if(!editID)return;
   try{await api('POST',`/api/waf/bindings/${editID}/activate`,{});detailMsg('Activate queued.');await refreshWAF();}catch(e){detailMsg(e.message);}
  };
  if($('wafb-deactivate'))$('wafb-deactivate').onclick=async()=>{
   if(!editID)return;
   if(!confirm('Deactivate WAF for this binding?'))return;
   try{await api('POST',`/api/waf/bindings/${editID}/deactivate`,{});detailMsg('Deactivate queued.');await refreshWAF();}catch(e){detailMsg(e.message);}
  };
  if($('wafb-delete'))$('wafb-delete').onclick=async()=>{
   if(!editID)return;
   if(!confirm('Delete this binding? Active WAF will be deactivated.'))return;
   try{await api('DELETE',`/api/waf/bindings/${editID}`);location.assign('/waf#waf-bindings');}catch(e){detailMsg(e.message);}
  };
  if($('wafb-error-html'))$('wafb-error-html').oninput=()=>{errorDirty=true;};
  if($('wafb-error-preview'))$('wafb-error-preview').onclick=()=>{
   const html=$('wafb-error-html')?.value||defaultHTML(errorStatus);
   const blob=new Blob([html],{type:'text/html'});const url=URL.createObjectURL(blob);window.open(url,'_blank','noopener');setTimeout(()=>URL.revokeObjectURL(url),60000);
  };
  if($('wafb-error-default'))$('wafb-error-default').onclick=()=>{
   if($('wafb-error-html')){$('wafb-error-html').value=defaultHTML(errorStatus);errorDirty=true;if($('wafb-error-message'))$('wafb-error-message').textContent='Default HTML loaded — click Save.';}
  };
  if($('wafb-error-save'))$('wafb-error-save').onclick=async()=>{
   if(!editID)return;
   const html=($('wafb-error-html')?.value||'').trim(),m=$('wafb-error-message');
   if(m)m.textContent='Saving…';
   try{
    await api('PUT',`/api/waf/bindings/${editID}/error-pages`,{status:errorStatus,html});
    errorDirty=false;errorSource=editID+':'+errorStatus;
    if(m)m.textContent=`${errorStatus} error page saved.`;
    await refreshWAF();
   }catch(e){if(m)m.textContent=e.message;}
  };
 }

 async function loop(){
  try{
   await refreshWAF();
   if(isList)msg('');
   if(isDetail&&editID&&!binding(editID)?.stats)refreshStats(editID);
   else if(isDetail&&editID){/* keep polling via refreshWAF */}
   if(isDetail&&editID){
    // first load: always kick a refresh once
    if(!loop._statsOnce){loop._statsOnce=true;refreshStats(editID);}
   }
  }catch(e){if(isList)msg(e.message);else detailMsg(e.message);}
 }
 loop();
 setInterval(()=>{if(document.hidden)return;loop();},15000);
})();
