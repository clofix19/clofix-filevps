(()=>{
 if(page!=='dns')return;
 let zone=null,editing=null;

 function targets(){return zone&&zone.targets||[];}
 function selectedTarget(){
  const resource=$('dns-record-resource').value,id=$('dns-record-target').value;
  return targets().find(item=>item.resource===resource&&item.id===id)||null;
 }
 function recordPort(){
  const map=$('dns-record-port-map'),custom=$('dns-record-port');
  return Number((map.hidden?custom:map).value)||0;
 }
 function fillPorts(selected){
  const resource=$('dns-record-resource').value,map=$('dns-record-port-map'),custom=$('dns-record-port'),label=$('dns-record-port-label');
  const keep=selected!=null?Number(selected):recordPort();
  if(resource==='manual'){
   map.hidden=true;custom.hidden=false;if(label)label.htmlFor='dns-record-port';
   custom.value=String(keep||0);
   return;
  }
  custom.hidden=true;map.hidden=false;if(label)label.htmlFor='dns-record-port-map';
  const item=selectedTarget(),ports=item?.ports||[];
  map.replaceChildren();
  map.add(new Option('No SRV port','0'));
  ports.forEach(p=>map.add(new Option(p.label||String(p.port),String(p.port))));
  if(keep&&![...map.options].some(o=>o.value===String(keep)))map.add(new Option('Current · '+keep,String(keep)));
  if(keep&&[...map.options].some(o=>o.value===String(keep)))map.value=String(keep);
  else if(item?.port)map.value=String(item.port);
  else map.value='0';
 }
 function fillTargets(){
  const resource=$('dns-record-resource').value,sel=$('dns-record-target'),keep=sel.value;
  sel.replaceChildren();
  const items=targets().filter(item=>item.resource===resource);
  if(resource==='manual'){
   sel.add(new Option('Enter address below',''));
   sel.disabled=true;
   $('dns-record-value').required=true;
   fillPorts();
   return;
  }
  sel.disabled=false;
  $('dns-record-value').required=false;
  if(!items.length){sel.add(new Option('No '+resource+' targets found',''));fillPorts(0);return;}
  items.forEach(item=>sel.add(new Option(`${item.label} · ${item.port?item.name+':'+item.port:item.name}`,item.id)));
  if([...sel.options].some(o=>o.value===keep))sel.value=keep;
  applyTarget();
 }
 function applyTarget(){
  const item=selectedTarget();
  if(!item){fillPorts(0);return;}
  $('dns-record-type').value=item.type||'CNAME';
  $('dns-record-value').value=item.value||'';
  if(!$('dns-record-name').value.trim()&&item.label){
   $('dns-record-name').placeholder=item.label.replace(/[^A-Za-z0-9-]+/g,'-').replace(/^-|-$/g,'').toLowerCase();
  }
  fillPorts(editing&&editing.resource===item.resource&&editing.resource_id===item.id?editing.port:item.port);
 }
 function clearRecordForm(){
  editing=null;
  $('dns-record-id').value='';
  $('dns-record-form').reset();
  $('dns-record-ttl').value='60';
  $('dns-record-port').value='0';
  $('dns-record-save').textContent='Add record';
  $('dns-record-cancel').hidden=true;
  fillTargets();
 }
 function editRecord(rec){
  editing=rec;
  $('dns-record-id').value=rec.id;
  $('dns-record-resource').value=rec.resource||'manual';
  fillTargets();
  if(rec.resource_id)$('dns-record-target').value=rec.resource_id;
  applyTarget();
  $('dns-record-name').value=rec.name||'';
  $('dns-record-type').value=rec.type||'A';
  $('dns-record-value').value=rec.value||'';
  $('dns-record-ttl').value=rec.ttl||60;
  $('dns-record-description').value=rec.description||'';
  fillPorts(rec.port||0);
  $('dns-record-save').textContent='Save record';
  $('dns-record-cancel').hidden=false;
  $('dns-record-form').scrollIntoView({behavior:'smooth',block:'center'});
 }
 function displayValue(rec){return rec.port?`${rec.value} :${rec.port}`:rec.value;}
 function renderOverview(){
  const box=$('dns-overview-stats'),o=zone.overview||{};
  $('dns-overview-note').textContent=zone.note||'';
  box.replaceChildren();
  [['Zone',o.domain],['Records',o.records],['Automatic',o.automatic],['Manual',o.manual],['Selectable targets',targets().length]].forEach(([label,value])=>{
   const card=document.createElement('div');
   card.className='stat-card';
   card.innerHTML=`<span>${label}</span><strong>${value??'—'}</strong>`;
   box.append(card);
  });
 }
 function renderZone(){
  const z=zone.zone||{};
  $('dns-zone-domain').value=z.domain||'clofix.internal';
  $('dns-zone-ttl').value=z.ttl||60;
  $('dns-zone-publish').value=z.publish_address||'';
  $('dns-auto-vps').checked=!!z.auto_vps;
  $('dns-auto-lb').checked=!!z.auto_lb;
  if($('dns-auto-cks'))$('dns-auto-cks').checked=!!z.auto_cks;
  $('dns-auto-db').checked=!!z.auto_db;
  $('dns-auto-app').checked=!!z.auto_app;
  $('dns-auto-worker').checked=!!z.auto_worker;
 }
 function renderRecords(){
  const table=$('dns-record-table');
  table.replaceChildren();
  row(table,['Name','Type','Value','Port','TTL','Source','Resource','Description','Actions'],true);
  const records=zone.records||[];
  if(!records.length){row(table,['No DNS records yet. Create a VPS, load balancer, CKS cluster, database or app, or add a record.','','','','','','','','']);return;}
  for(const rec of records){
   const actions=document.createElement('div');
   actions.className='row-actions';
   if(rec.source==='manual'){
    const edit=document.createElement('button');
    edit.type='button';edit.className='small-btn';edit.textContent='Edit';
    edit.onclick=()=>{editRecord(rec);$('dns-record-message').textContent='Editing '+rec.name+'.';};
    const del=document.createElement('button');
    del.type='button';del.className='danger small-btn';del.textContent='Delete';
    del.onclick=async()=>{del.disabled=true;try{await api('DELETE',`/api/dns/records/${encodeURIComponent(rec.id)}`);$('dns-record-message').textContent='Record deleted.';clearRecordForm();await loadDNS();}catch(e){$('dns-record-message').textContent=e.message;}finally{del.disabled=false;}};
    actions.append(edit,del);
   }
   row(table,[rec.name,rec.type,displayValue(rec),rec.port||'—',rec.ttl,rec.source,rec.resource_id?`${rec.resource} · ${rec.resource_id}`:rec.resource,rec.description||'—',actions]);
  }
  fillTargets();
 }
 async function renderZoneFile(){
  const pre=$('dns-zonefile-view');
  const r=await fetch('/api/dns/zonefile',{credentials:'include',cache:'no-store'});
  if(!r.ok){let err=r.status;try{err=(await r.json()).error||err;}catch(e){}throw new Error(err);}
  pre.textContent=await r.text();
 }
 async function downloadZoneFile(){
  const r=await fetch('/api/dns/zonefile',{credentials:'include',cache:'no-store'});
  if(!r.ok){let err=r.status;try{err=(await r.json()).error||err;}catch(e){}throw new Error(err);}
  const blob=await r.blob(),url=URL.createObjectURL(blob),a=document.createElement('a');
  a.href=url;a.download=(zone.zone?.domain||'clofix.internal')+'.zone';a.click();
  setTimeout(()=>URL.revokeObjectURL(url),1000);
 }
 async function loadDNS(){
  zone=await api('GET','/api/dns');
  renderOverview();renderZone();renderRecords();
  try{await renderZoneFile();}catch(e){$('dns-zonefile-message').textContent=e.message;}
 }

 $('dns-record-resource')?.addEventListener('change',fillTargets);
 $('dns-record-target')?.addEventListener('change',applyTarget);
 $('dns-zone-form')?.addEventListener('submit',async e=>{
  e.preventDefault();const msg=$('dns-zone-message');
  try{
   zone=await api('PUT','/api/dns/zone',{domain:$('dns-zone-domain').value.trim(),ttl:Number($('dns-zone-ttl').value)||60,publish_address:$('dns-zone-publish').value.trim(),auto_vps:$('dns-auto-vps').checked,auto_lb:$('dns-auto-lb').checked,auto_cks:$('dns-auto-cks')?$('dns-auto-cks').checked:true,auto_db:$('dns-auto-db').checked,auto_app:$('dns-auto-app').checked,auto_worker:$('dns-auto-worker').checked});
   msg.textContent='Zone saved. Automatic records refreshed from live inventory.';
   renderOverview();renderZone();renderRecords();await renderZoneFile();
  }catch(err){msg.textContent=err.message;}
 });
 $('dns-record-form')?.addEventListener('submit',async e=>{
  e.preventDefault();const msg=$('dns-record-message');
  const resource=$('dns-record-resource').value,target=$('dns-record-target').value,id=$('dns-record-id').value.trim();
  const body={name:$('dns-record-name').value.trim(),type:$('dns-record-type').value,value:$('dns-record-value').value.trim(),port:recordPort(),ttl:Number($('dns-record-ttl').value)||0,description:$('dns-record-description').value.trim(),resource,resource_id:resource==='manual'?'':target};
  try{
   if(id){
    await api('PUT',`/api/dns/records/${encodeURIComponent(id)}`,body);
    msg.textContent='Record updated.';
   }else{
    await api('POST','/api/dns/records',body);
    msg.textContent=resource==='manual'?'Manual record added.':'Record added for the selected '+resource+'.';
   }
   clearRecordForm();
   await loadDNS();
  }catch(err){msg.textContent=err.message;}
 });
 $('dns-record-cancel')?.addEventListener('click',()=>{clearRecordForm();$('dns-record-message').textContent='';});
 $('dns-zonefile-download')?.addEventListener('click',async()=>{
  try{await downloadZoneFile();$('dns-zonefile-message').textContent='Zone file downloaded.';}catch(e){$('dns-zonefile-message').textContent=e.message;}
 });

 async function boot(){if(!T&&!$('tok').value.trim())return;try{await loadDNS();const banner=$('auth-banner');if(banner)banner.hidden=true;}catch(e){showMessage(e.message);if(e.message.startsWith('Unauthorized')&&$('auth-banner'))$('auth-banner').hidden=false;}}
 $('save')?.addEventListener('click',boot);$('tok')?.addEventListener('change',boot);boot();
})();
