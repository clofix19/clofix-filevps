(()=>{
 if(page!=='dns')return;
 let zone=null,editing=null,certTargets={alb:[],apps:[],docker:[]},certs=null;

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
 let certRaw={alb:[],apps:[],docker:[]};
 function fillCertResources(){
  const target=$('dns-cert-target')?.value||'alb',sel=$('dns-cert-resource');
  if(!sel)return;
  const keep=sel.value;
  sel.replaceChildren();
  const items=target==='docker'?certTargets.docker:target==='managed-app'?certTargets.apps:certTargets.alb;
  if(!items.length){
   const empty=target==='docker'?'No Docker apps yet — deploy one with a domain first':target==='managed-app'?'No managed apps yet — deploy one first':'No ALB yet — create an HTTP load balancer';
   sel.add(new Option(empty,''));
   fillCertDomains();
   return;
  }
  items.forEach(item=>{
   const n=(item.domains||[]).length;
   sel.add(new Option(`${item.name} · ${n?n+' domain'+(n===1?'':'s'):'no domains yet'}`,item.id));
  });
  if([...sel.options].some(o=>o.value===keep))sel.value=keep;
  fillCertDomains();
 }
 function fillCertDomains(){
  const target=$('dns-cert-target')?.value||'alb',id=$('dns-cert-resource')?.value,dom=$('dns-cert-domain'),list=$('dns-cert-domain-list');
  if(!dom||!list)return;
  const item=(target==='docker'?certTargets.docker:target==='managed-app'?certTargets.apps:certTargets.alb).find(x=>x.id===id);
  const domains=item?.domains||[];
  list.replaceChildren();
  domains.forEach(d=>{const o=document.createElement('option');o.value=d;list.append(o);});
  if(domains.length&&!dom.value)dom.value=domains[0];
  else if(domains.length&&!domains.includes(dom.value)&&!dom.value)dom.value=domains[0];
 }
 function renderCertificates(){
  const table=$('dns-cert-table');if(!table)return;
  const status=$('dns-cert-status');
  if(status){
   if(certs?.acme_email_configured)status.textContent='ACME email is configured on the controller.';
   else status.textContent='ACME email is not set. Add CLOFIX_ACME_EMAIL to the admin service environment, then recreate the admin container.';
   status.className='message '+(certs?.acme_email_configured?'':'load-error');
  }
  if($('dns-cert-note')&&certs?.note)$('dns-cert-note').textContent=certs.note+(certs.acme_email_configured?'':' Set CLOFIX_ACME_EMAIL on the controller before issuing.');
  row(table,['Domain','Target','Resource','State','Issued','Expires','Error','Actions'],true);
  const items=certs?.items||[];
  if(!items.length){row(table,['No certificates yet. Select a resource, enter a domain, then request one.','','','','','','','']);return;}
  items.forEach(c=>{
   const actions=document.createElement('div');actions.className='row-actions';
   if(c.state==='failed'||c.state==='pending'){
    const retry=document.createElement('button');retry.type='button';retry.className='small-btn';retry.textContent='Retry';
    retry.onclick=async()=>{retry.disabled=true;try{await api('POST','/api/certificates',{domain:c.domain,target:c.target,resource_id:c.resource_id});$('dns-cert-message').textContent='Certificate request re-queued.';await loadCertificates();}catch(e){$('dns-cert-message').textContent=e.message;}finally{retry.disabled=false;}};
    actions.append(retry);
   }
   const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';
   del.onclick=async()=>{if(!window.confirm('Delete certificate for '+c.domain+'?'))return;del.disabled=true;try{await api('DELETE',`/api/certificates/${encodeURIComponent(c.domain)}`);$('dns-cert-message').textContent='Certificate removed.';await loadCertificates();}catch(e){$('dns-cert-message').textContent=e.message;}finally{del.disabled=false;}};
   actions.append(del);
   const state=document.createElement('span');state.className='badge '+(c.state==='issued'?'RUNNING':c.state==='failed'?'FAILED':'STARTING');state.textContent=c.state||'—';
   row(table,[c.domain,c.target,c.resource_id||'—',state,c.issued?new Date(c.issued).toLocaleString():'—',c.expires?new Date(c.expires).toLocaleString():'—',c.error||'—',actions]);
  });
 }
 async function ensureCertDomain(target,resourceId,domain){
  if(target==='alb'){
   const b=certRaw.alb.find(x=>x.id===resourceId);
   if(!b)throw new Error('ALB not found.');
   if((b.domains||[]).includes(domain))return;
   await api('PUT','/api/load-balancers/'+resourceId,{...b,domains:[...(b.domains||[]),domain]});
   return;
  }
  if(target==='docker'){
   const a=(certRaw.docker||[]).find(x=>x.id===resourceId);
   if(!a)throw new Error('Docker app not found.');
   if((a.domain||'')===domain)return;
   throw new Error('Set the domain on the Docker application before requesting a certificate.');
  }
  const a=certRaw.apps.find(x=>x.id===resourceId);
  if(!a)throw new Error('Managed app not found.');
  if((a.domains||[]).includes(domain))return;
  await api('PUT',`/api/managed-apps/${resourceId}/domains`,{domains:[...(a.domains||[]),domain]});
 }
 async function loadCertificates(){
  if(!$('dns-cert-table'))return;
  const settled=await Promise.allSettled([
   api('GET','/api/load-balancers'),
   api('GET','/api/managed-apps'),
   api('GET','/api/certificates'),
   api('GET','/api/docker/applications'),
  ]);
  const lbs=settled[0].status==='fulfilled'?(Array.isArray(settled[0].value)?settled[0].value:[]):[];
  const apps=settled[1].status==='fulfilled'?(Array.isArray(settled[1].value)?settled[1].value:[]):[];
  const dockerApps=settled[3].status==='fulfilled'?(Array.isArray(settled[3].value)?settled[3].value:[]):[];
  if(settled[2].status!=='fulfilled')throw settled[2].reason||new Error('Could not load certificates');
  certRaw.alb=lbs.filter(b=>(b.mode||'http')!=='nlb'&&!b.deleting);
  certRaw.apps=apps;
  certRaw.docker=dockerApps;
  certTargets.alb=certRaw.alb.map(b=>({id:b.id,name:b.name||b.id,domains:b.domains||[]}));
  certTargets.apps=certRaw.apps.map(a=>({id:a.id,name:a.name||a.id,domains:a.domains||[]}));
  certTargets.docker=certRaw.docker.map(a=>({id:a.id,name:a.name||a.id,domains:a.domain?[a.domain]:[]}));
  certs=settled[2].value;fillCertResources();renderCertificates();
  const err=[];
  if(settled[0].status==='rejected')err.push('load balancers: '+settled[0].reason.message);
  if(settled[1].status==='rejected')err.push('managed apps: '+settled[1].reason.message);
  if(settled[3].status==='rejected')err.push('docker apps: '+settled[3].reason.message);
  if(err.length&&$('dns-cert-message'))$('dns-cert-message').textContent='Partial load · '+err.join(' · ');
 }
 async function loadDNS(){
  zone=await api('GET','/api/dns');
  renderOverview();renderZone();renderRecords();
  try{await renderZoneFile();}catch(e){$('dns-zonefile-message').textContent=e.message;}
  try{await loadCertificates();}catch(e){if($('dns-cert-message'))$('dns-cert-message').textContent=e.message;}
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
 $('dns-cert-target')?.addEventListener('change',fillCertResources);
 $('dns-cert-resource')?.addEventListener('change',fillCertDomains);
 $('dns-cert-form')?.addEventListener('submit',async e=>{
  e.preventDefault();const msg=$('dns-cert-message'),btn=$('dns-cert-save');
  const target=$('dns-cert-target').value,resourceId=$('dns-cert-resource').value,domain=($('dns-cert-domain').value||'').trim().toLowerCase();
  if(!resourceId){msg.textContent='Select a load balancer or managed app first.';return;}
  if(!domain||domain.includes(' ')){msg.textContent='Enter a valid DNS hostname.';return;}
  btn.disabled=true;msg.textContent='Attaching domain and requesting certificate…';
  try{
   await ensureCertDomain(target,resourceId,domain);
   await api('POST','/api/certificates',{domain,target,resource_id:resourceId});
   msg.textContent='Certificate request queued for '+domain+'.';
   await loadCertificates();
  }catch(err){msg.textContent=err.message;}
  finally{btn.disabled=false;}
 });

 async function boot(){if(!T&&!$('tok').value.trim())return;try{await loadDNS();const banner=$('auth-banner');if(banner)banner.hidden=true;}catch(e){showMessage(e.message);if(e.message.startsWith('Unauthorized')&&$('auth-banner'))$('auth-banner').hidden=false;}}
 $('save')?.addEventListener('click',boot);$('tok')?.addEventListener('change',boot);boot();
})();
