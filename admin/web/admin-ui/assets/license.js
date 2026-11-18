(()=>{
 if(page!=='settings')return;
 let savedServer='',configured=false,installationID='';
 const node=name=>$('license-'+name);
 const message=text=>{const el=node('message');if(el)el.textContent=text;};
 const setText=(id,text)=>{const el=$(id);if(el)el.textContent=text??'—';};
 const limitLabel=(plan,value)=>{
  if(!plan.id)return '—';
  if(value==null)return 'Not specified';
  if(value===-1)return 'Unlimited';
  if(value===0)return 'Not included';
  return String(value);
 };
 const phaseLabel=phase=>({active:'Active',expired:'Expired · creates blocked',stopped:'Expired · services auto-stopped',invalid:'Invalid'}[phase]||phase||'—');
 const fmtWhen=v=>{
  if(!v||String(v).startsWith('0001'))return '—';
  try{return new Date(v).toLocaleString();}catch(e){return '—';}
 };

 const entitlementGroups=[
  {title:'Compute & networking',rows:[
   ['FileVPS instances','max_vps'],
   ['Workers','max_workers'],
   ['Load balancers','max_load_balancers'],
   ['Jobs & functions','max_applications'],
   ['Managed apps','max_managed_apps'],
  ]},
  {title:'Database',rows:[
   ['Database instances','max_database_projects'],
   ['Logical databases','max_databases'],
   ['Database users','max_database_users'],
   ['Database backup schedules','max_database_schedules'],
  ]},
  {title:'Storage & backups',rows:[
   ['Volumes','max_volumes'],
   ['Retained backups','max_backups'],
   ['Total storage entitlement (GiB)','max_storage_gb'],
   ['S3 connections','max_s3_connections'],
  ]},
  {title:'DNS & observability',rows:[
   ['DNS zones','max_dns_zones'],
   ['DNS records','max_dns_records'],
   ['Health check endpoints','max_health_checks'],
   ['Notification channels','max_notification_channels'],
  ]},
  {title:'IAM',rows:[
   ['IAM users','max_iam_users'],
   ['IAM groups','max_iam_groups'],
   ['IAM policies','max_iam_policies'],
   ['IAM access keys','max_access_keys'],
  ]},
  {title:'CKS · Redis · Docker',rows:[
   ['CKS clusters (EKS-style)','max_kubernetes_clusters'],
   ['CKS nodes','max_kubernetes_nodes'],
   ['CKS node pools','max_kubernetes_node_pools'],
   ['Redis clusters','max_redis_clusters'],
   ['Docker containers','max_docker_containers'],
   ['Docker applications','max_docker_applications'],
   ['Docker Compose projects','max_docker_compose'],
   ['Docker workers','max_docker_workers'],
   ['Docker volumes','max_docker_volumes'],
   ['Docker volume backups','max_docker_backups'],
  ]},
 ];

 async function refresh(){
  if(!T&&!$('tok')?.value?.trim())return;
  if(cfMe&&cfMe.kind!=='admin')return;
  const d=await api('GET','/api/license');
  savedServer=d.server_url||'';
  configured=!!d.configured;
  installationID=d.installation_id||'';
  if(node('server'))node('server').value=savedServer;
  if(node('key-note'))node('key-note').textContent=configured?'A key is saved. Leave blank to keep it.':'No license key saved.';
  if(node('validate'))node('validate').disabled=!configured;

  const plan=d.grant?.plan||{};
  const phase=phaseLabel(d.phase)+(d.offline?' · offline':'');
  const expires=fmtWhen(d.expires||d.grant?.expires);
  const stopAfter=fmtWhen(d.stop_services_after);

  setText('license-tile-status',d.status||'—');
  setText('license-tile-status-note',configured?(d.offline?'Using cached grant':'Synced with license server'):'No key configured');
  setText('license-tile-plan',plan.name||'—');
  setText('license-tile-plan-note',d.grant?.customer?`Customer · ${d.grant.customer}`:(plan.id?'Granted plan':'No plan'));
  setText('license-tile-expires',expires==='—'?'—':expires);
  setText('license-tile-expires-note',d.grant?.duration_days?`${d.grant.duration_days} day license`:'License end date');
  setText('license-tile-phase',phase);
  setText('license-tile-phase-note',stopAfter!=='—'?'Auto-stop after '+stopAfter:'Create / stop policy');

  const statusEl=$('license-tile-status');
  if(statusEl){
   const ok=String(d.status||'').toLowerCase().includes('valid')||d.phase==='active';
   const warn=d.phase==='expired'||d.phase==='stopped'||d.phase==='invalid'||!configured;
   statusEl.dataset.state=ok&&!warn?'ok':(warn?'warn':'');
  }

  const identity=node('identity');
  if(identity){
   identity.replaceChildren();
   for(const [name,value] of [
    ['License ID',d.grant?.license_id||'—'],
    ['Installation ID',d.installation_id||'—'],
    ['Customer',d.grant?.customer||'—'],
    ['Last sync',fmtWhen(d.last_sync||d.last_check)==='—'?'Not yet':fmtWhen(d.last_sync||d.last_check)],
    ['License duration',d.grant?.duration_days?d.grant.duration_days+' days':'—'],
    ['Auto-stop after',stopAfter],
   ])identity.append(meta(name,value));
  }

  const et=$('license-entitlements-table');
  if(et){
   row(et,['Group','Resource','Limit'],true);
   if(!plan.id){
    row(et,['—','Validate a license to load entitlements','—']);
   }else{
    entitlementGroups.forEach(g=>{
     g.rows.forEach((pair,i)=>{
      row(et,[i===0?g.title:'',pair[0],limitLabel(plan,plan[pair[1]])]);
     });
    });
   }
  }

  await renderLicenseUsage();
 }

 async function renderLicenseUsage(){
  const data=await api('GET','/api/license/usage');
  const table=node('usage-table');
  if(!table)return;
  row(table,['Resource','Used','License limit','Status'],true);
  const quotaLabel={
   'load-balancers':'Load balancers',
   'kubernetes-clusters':'CKS clusters (EKS-style)',
   'kubernetes-nodes':'CKS nodes',
   'kubernetes-node-pools':'CKS node pools',
   'redis-clusters':'Redis clusters',
   'docker-containers':'Docker containers',
   'docker-applications':'Docker applications',
   'docker-compose':'Docker Compose projects',
   'docker-workers':'Docker workers',
   'docker-volumes':'Docker volumes',
   'docker-backups':'Docker volume backups',
   'dns-zones':'DNS zones',
   'dns-records':'DNS records',
  };
  const quotas=Object.entries(data.quotas||{});
  if(!quotas.length)row(table,['No usage data yet.','','','']);
  quotas.forEach(([resource,q])=>{
   const used=q.unit==='bytes'?fmtBytes(q.used):String(q.used);
   const limit=q.limit<0?'Unlimited':q.limit===0?'Not included':q.unit==='bytes'?fmtBytes(q.limit):String(q.limit);
   const status=!data.valid?'License required':q.limit>=0&&q.used>q.limit?'Over quota':q.limit===0?'Not included':'Within quota';
   row(table,[quotaLabel[resource]||resource.replaceAll('-',' '),used,data.valid?limit:'Validate license first',status]);
  });
  if(node('storage-scope'))node('storage-scope').textContent=data.storage_scope||'';
  document.dispatchEvent(new Event('license-updated'));
 }

 node('copy-installation')?.addEventListener('click',async()=>{
  try{
   await navigator.clipboard.writeText(installationID);
   message('Installation ID copied. This license is valid only for this controller identity.');
  }catch(e){message('Installation ID: '+installationID);}
 });

 node('form')?.addEventListener('submit',async e=>{
  e.preventDefault();
  if(node('form').getAttribute('aria-busy')==='true')return;
  const finish=beginFormProgress(node('form'),node('save'),'Saving & validating…');
  message('Saving license configuration…');
  try{
   await api('PUT','/api/license',{server_url:node('server').value.trim(),key:node('key').value.trim(),clear:node('clear').checked});
   node('key').value='';
   node('clear').checked=false;
   const saved=await api('GET','/api/license');
   if(saved.configured){
    message('Configuration saved. Validating license…');
    await api('POST','/api/license/validate');
    message('License saved and validated successfully.');
   }else message('Configuration saved. No license key is configured.');
  }catch(err){message(err.message);}
  finally{finish();await refresh().catch(err=>message(err.message));}
 });

 node('validate')?.addEventListener('click',async()=>{
  if(node('form')?.getAttribute('aria-busy')==='true')return;
  if(node('key')?.value.trim()||node('clear')?.checked||node('server')?.value.trim()!==savedServer){
   node('form')?.requestSubmit();
   return;
  }
  const finish=beginFormProgress(node('form'),node('validate'),'Validating…');
  message('Contacting license server…');
  try{
   await api('POST','/api/license/validate');
   message('License is valid.');
  }catch(err){message(err.message);}
  finally{finish();await refresh().catch(err=>message(err.message));}
 });

 node('key')?.addEventListener('input',()=>{
  if(node('key').value.trim())node('clear').checked=false;
  if(node('validate'))node('validate').disabled=!configured&&!node('key').value.trim();
 });

 $('save')?.addEventListener('click',()=>refresh().catch(e=>message(e.message)));
 $('tok')?.addEventListener('change',()=>refresh().catch(e=>message(e.message)));
 refresh().catch(e=>message(e.message));
})();
