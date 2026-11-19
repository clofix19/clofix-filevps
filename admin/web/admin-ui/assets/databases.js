(()=>{
 if(page!=='databases')return;
 const get=id=>document.getElementById(id),message=text=>get('database-message').textContent=text;
 let state={projects:[],backups:[],jobs:[],schedules:[],workers:[],connections:[]},license=null,selected='',operation=null,busy=false,watchedJob='';
 let explorer={database:'',table:'',tab:'structure',offset:0,limit:50,tables:[],columns:[],busy:false};
 let rowEditor={mode:'insert',columns:[],keys:{},values:{}};
 const project=()=>state.projects.find(item=>item.spec.id===selected);
 const explorerProject=()=>state.projects.find(item=>item.spec.id===(get('database-explorer-instance')?.value||selected));
 const terminalStates=['SUCCEEDED','FAILED','CANCELLED','INTERRUPTED','RETRIED'];
 const waitJob=async(jobId,timeoutMs=90000)=>{
  const started=Date.now();
  let delay=150;
  while(Date.now()-started<timeoutMs){
   const snapshot=await api('GET','/api/databases');
   state={...state,...snapshot};
   const job=(snapshot.jobs||[]).find(item=>item.id===jobId);
   if(job&&terminalStates.includes(job.state)){
    if(job.state!=='SUCCEEDED')throw Error(job.error||`${job.action} ${job.state.toLowerCase()}`);
    return job.result||{};
   }
   await new Promise(resolve=>setTimeout(resolve,delay));
   delay=Math.min(400,delay+50);
  }
  throw Error('Timed out waiting for database operation');
 };
 const runExplorer=async(body,timeoutMs=90000)=>{
  const p=explorerProject();
  if(!p)throw Error('Select a READY database instance first.');
  if(p.state!=='READY')throw Error('Instance is not ready.');
  if(p.spec.engine==='redis')throw Error('Use Redis Cluster for Redis workloads.');
  const accepted=await api('POST',`/api/databases/projects/${encodeURIComponent(p.spec.id)}/operations`,body);
  if(!accepted?.job_id)throw Error('Explorer operation was not accepted.');
  return waitJob(accepted.job_id,timeoutMs);
 };
 const isAutoColumn=col=>/auto_increment|nextval\(|identity/i.test(`${col.extra||''} ${col.default||''}`);
 const isPrimaryColumn=col=>String(col.key||'').toUpperCase().includes('PRI')||!!col.primary;
 const inputKindForType=type=>{
  const t=String(type||'').toLowerCase();
  if(/bool|bit\(1\)/.test(t))return 'boolean';
  if(/int|decimal|numeric|double|float|real|serial|money/.test(t))return 'number';
  if(/date/.test(t)&&!/time/.test(t))return 'date';
  if(/time|timestamp/.test(t))return 'datetime-local';
  if(/json|text|blob|bytea|xml|clob/.test(t))return 'textarea';
  return 'text';
 };
 const explorerControls=()=>[
  get('database-explorer-instance'),get('database-explorer-database'),get('database-explorer-refresh'),
  get('database-explorer-new-table'),get('database-explorer-add-column'),get('database-explorer-drop-table'),get('database-explorer-prev'),
  get('database-explorer-next'),get('database-explorer-run'),get('database-explorer-sql'),
  get('database-explorer-insert'),get('database-explorer-sql-upload'),
  ...document.querySelectorAll('#database-explorer [data-dbx-tab], #database-explorer .dbx-table-item')
 ].filter(Boolean);
 const paintBusyBtn=(el,on,idle,busyLabel)=>{
  if(!el)return;
  if(on){
   el.replaceChildren();
   const icon=document.createElement('span');icon.className='progress-icon';icon.setAttribute('aria-hidden','true');
   el.append(icon,document.createTextNode(busyLabel));
  }else el.textContent=idle;
 };
 const setExplorerBusy=(on,text='',opts={})=>{
  explorer.busy=!!on;
  const status=get('database-explorer-status'),spin=status?.querySelector('.dbx-status-spin'),body=get('database-explorer-body');
  if(status)status.dataset.busy=on?'true':'false';
  if(spin)spin.hidden=!on;
  if(body)body.classList.toggle('is-busy',!!on);
  if(get('database-explorer'))get('database-explorer').setAttribute('aria-busy',on?'true':'false');
  explorerControls().forEach(el=>{
   if(el.tagName==='BUTTON'||el.tagName==='SELECT'||el.tagName==='TEXTAREA')el.disabled=!!on;
  });
  paintBusyBtn(get('database-explorer-refresh'),on,'Refresh schema','Working…');
  paintBusyBtn(get('database-explorer-run'),on&&opts.run,'Run','Working…');
  if(!on)paintBusyBtn(get('database-explorer-run'),false,'Run','Working…');
  if(text||on)explorerMsg(text||'Working…');
 };
 const explorerMsg=text=>{const el=get('database-explorer-message');if(el)el.textContent=text||'';};
 const action=(label,handler,kind='small-btn')=>{const button=document.createElement('button');button.type='button';button.className=kind;button.textContent=label;button.onclick=async()=>{button.disabled=true;try{await handler();}catch(error){message(error.message);}finally{button.disabled=false;}};return button;};
 const textField=(id,label,value='',type='text')=>`<div class="form-field"><label for="${id}">${label}</label><input id="${id}" type="${type}" value="${String(value).replaceAll('&','&amp;').replaceAll('"','&quot;')}" required></div>`;
 const selectField=(id,label,options)=>`<div class="form-field"><label for="${id}">${label}</label><select id="${id}">${options.map(([value,name])=>`<option value="${value}">${name}</option>`).join('')}</select></div>`;
 function showOperation(title,fields,submit){operation=submit;get('database-operation-title').textContent=title;get('database-operation-fields').innerHTML=fields;get('database-operation-message').textContent='';get('database-operation-dialog').showModal();}
 function projectOperation(body){return api('POST',`/api/databases/projects/${encodeURIComponent(selected)}/operations`,body);}
 function render(){
  const terminal=['SUCCEEDED','FAILED','CANCELLED','INTERRUPTED','RETRIED'],readyCount=state.projects.filter(p=>p.state==='READY').length,activeJobs=state.jobs.filter(j=>!terminal.includes(j.state)),latestJob=state.jobs[0];
  get('database-project-count').textContent=state.projects.length;get('database-ready-count').textContent=readyCount;get('database-backup-count').textContent=state.backups.length;get('database-job-count').textContent=activeJobs.length;
  get('database-project-note').textContent=state.workers.length?`${state.workers.length} database-ready worker${state.workers.length===1?'':'s'}`:'No ready database worker';
  get('database-ready-note').textContent=!state.projects.length?'No instances provisioned':readyCount===state.projects.length?'All instances healthy':`${state.projects.length-readyCount} instance${state.projects.length-readyCount===1?'':'s'} need attention`;
  get('database-backup-note').textContent=state.backups.length?`${state.backups.filter(b=>b.state==='READY').length} recovery point${state.backups.filter(b=>b.state==='READY').length===1?'':'s'} available`:'No recovery points yet';
  get('database-job-note').textContent=activeJobs.length?`${activeJobs[0].action} · ${activeJobs[0].progress||0}%`:latestJob?`Latest: ${latestJob.action} · ${latestJob.state.toLowerCase()}`:'No recent activity';
  const readiness=get('database-readiness'),create=get('database-add-project'),quota=license?.quotas?.['database-projects'],problems=[];
  if(quota&&!quota.included)problems.push('Your active license does not include managed database instances. Add a Database projects quota to the company license plan, then revalidate the license.');
  else if(quota&&quota.limit>=0&&quota.used>=quota.limit)problems.push(`Database project quota reached (${quota.used}/${quota.limit}).`);
  if(!state.workers.length)problems.push('No worker is advertising a trusted database engine. Prepare the database image manifest on a Linux worker and restart that worker.');
  readiness.hidden=problems.length===0;readiness.classList.remove('is-ready');readiness.textContent=problems.join(' ');create.disabled=problems.length>0;create.title=problems.join(' ');
  if(watchedJob){const job=state.jobs.find(item=>item.id===watchedJob);if(job){if(job.state==='SUCCEEDED'){message(`Completed · ${job.action} · ${new Date(job.updated).toLocaleTimeString()}`);watchedJob='';}else if(terminal.includes(job.state)){message(`${job.action} ${job.state.toLowerCase()} · ${job.error||'Review job details.'}`);watchedJob='';}else message(`${job.action} · ${job.state.toLowerCase()} · ${job.progress||0}%`);}}
  const projects=get('database-projects');row(projects,['Name','Engine','Version','Worker','Resources','Status','Actions'],true);if(!state.projects.length)row(projects,['No managed database instances. A worker must advertise a trusted database engine before creation.','','','','','','']);
  state.projects.forEach(p=>{const actions=document.createElement('div');actions.className='row-actions';actions.append(action('Manage',()=>{selected=p.spec.id;render();}));if(p.state==='FAILED')actions.append(action('Retry',async()=>{watch(await api('POST',`/api/databases/projects/${p.spec.id}/retry`));message('Provisioning retry queued.');await refresh();}));row(projects,[p.name,p.spec.engine,p.spec.version,p.worker_id,`${p.spec.cpu} CPU · ${p.spec.ram_mb} MB · ${p.spec.disk_mb} MB`,badge(p.state),actions]);});
  const p=project(),recoveryJob=p&&state.jobs.find(j=>j.project_id===p.spec.id&&j.action==='backup'&&(['INTERRUPTED','CANCELLED'].includes(j.state)||j.state==='FAILED'&&j.error?.includes('database project requires inspection after interrupted work')));get('database-project-panel').hidden=!p;get('database-ca-actions').hidden=!p||!p.ca;get('database-inspect-recovery').hidden=!p||!recoveryJob;if(p){const vpcIP=databaseVPCIP(p);get('database-project-title').textContent=p.name;get('database-project-connection').textContent=p.spec.engine==='redis'?`${p.spec.engine} ${p.spec.version} · ${p.spec.host}:${p.spec.port} · VPC ${vpcIP}:${p.spec.port} · ${vpcAddress('',p.spec.vpc_id)} · TLS required · key prefix is the logical database name`: `${p.spec.engine} ${p.spec.version} · ${p.spec.host}:${p.spec.port} · VPC ${vpcIP}:${p.spec.port} · ${vpcAddress('',p.spec.vpc_id)} · TLS required`;get('database-metric-cpu').textContent=`${Number(p.metrics?.cpu_percent||0).toFixed(1)}%`;get('database-metric-ram').textContent=fmtBytes(p.metrics?.ram_bytes||0);get('database-metric-disk').textContent=fmtBytes(p.metrics?.disk_bytes||0);get('database-metric-connections').textContent=String(p.metrics?.connections||0);get('database-create-backup').disabled=p.spec.engine==='redis';get('database-add-schedule').disabled=p.spec.engine==='redis';get('database-upload-backup').disabled=p.spec.engine==='redis';renderLogical(p);}else{get('database-create-backup').disabled=false;get('database-add-schedule').disabled=false;get('database-upload-backup').disabled=false;}
  renderBackups();renderSchedules();renderJobs();syncExplorerSelectors();syncPropertiesSelectors();
 }
 function watch(result){if(result?.job_id)watchedJob=result.job_id;return result;}
 function schemaSummary(p){
  if(p.spec.engine==='redis')return 'Shared keyspace. Client keys use this name as a prefix.';
  const inv=p.inventory;
  if(!inv)return 'Open Explore to load tables';
  const tables=Array.isArray(inv.tables)?inv.tables.map(item=>typeof item==='string'?item:(item?.name||'')).filter(Boolean):[];
  if(!tables.length)return 'No tables yet · Open Explore';
  return tables.sort().join(', ');
 }
 function renderLogical(p){
  const logical=get('database-logical');row(logical,['Database','Schema / collections','Actions'],true);const names=Object.keys(p.databases||{}).sort();if(!names.length)row(logical,['No logical databases.','','']);names.forEach(name=>{const actions=document.createElement('div');actions.className='row-actions';if(p.spec.engine!=='redis'){actions.append(action('Explore',()=>openExplorer(p.spec.id,name)),action(p.spec.engine==='mongodb'?'Create collection':'Create table',()=>openTable(name)),action('Create index',()=>openIndex(name)),action('Backup',()=>openBackup(name)));}actions.append(action('Delete',async()=>{if(await window.cfPrompt(`Type DELETE to delete ${name}`,'',{title:'Delete database',label:'Confirmation',confirm:'Delete',hint:'Type DELETE exactly.'})!=='DELETE')return;await projectOperation({action:'delete-database',database:name,confirm:'DELETE'});message('Database deletion queued.');await refresh();},'danger small-btn'));row(logical,[name,schemaSummary(p),actions]);});
  const users=get('database-users');row(users,['User','Permissions','Actions'],true);const entries=Object.entries(p.users||{});if(!entries.length)row(users,['No client database users.','','']);entries.forEach(([name,grants])=>{const actions=document.createElement('div');actions.className='row-actions';actions.append(action('Modify',()=>openUser(name,grants)),action('Delete',async()=>{if(!confirm(`Delete database user ${name}?`))return;await projectOperation({action:'delete-user',username:name,confirm:'DELETE'});message('User deletion queued.');await refresh();},'danger small-btn'));row(users,[name,Object.entries(grants).map(([database,role])=>`${database}: ${role}`).join(' · ')||'No access',actions]);});
 }
 function recommendedProperties(p){
  const ram=Math.max(256,Number(p?.spec?.ram_mb)||1024),engine=p?.spec?.engine||'mysql';
  let maxConn=Math.min(500,Math.max(50,Math.floor(ram/8))),buffer=Math.min(Math.floor(ram*0.8),Math.max(128,Math.floor(ram*0.6))),work=0;
  if(engine==='postgresql'){buffer=Math.min(Math.floor(ram/2),Math.max(128,Math.floor(ram/4)));work=Math.min(64,Math.max(4,Math.floor(ram/64)));}
  else if(engine==='mongodb'){buffer=Math.min(Math.floor(ram*0.7),Math.max(128,Math.floor(ram/2)));}
  else if(engine==='redis'){maxConn=Math.min(10000,Math.max(100,Math.floor(ram/4)));buffer=Math.min(Math.floor(ram*0.9),Math.max(64,Math.floor(ram*0.7)));}
  return {max_connections:maxConn,buffer_mb:buffer,work_mem_mb:work};
 }
 function propertiesProject(){return state.projects.find(item=>item.spec.id===(get('database-properties-instance')?.value||selected));}
 function syncPropertiesSelectors(){
  const sel=get('database-properties-instance');if(!sel)return;
  const previous=sel.value||selected;
  sel.replaceChildren();
  state.projects.filter(p=>p.state==='READY').forEach(p=>{const option=document.createElement('option');option.value=p.spec.id;option.textContent=`${p.name} · ${p.spec.engine} ${p.spec.version}`;sel.append(option);});
  if([...sel.options].some(o=>o.value===previous))sel.value=previous;
  else if(sel.options.length)sel.selectedIndex=0;
  if(sel.value)selected=sel.value;
  renderProperties();
 }
 function databaseVPCIP(p){const v=typeof vpcByID==='function'?vpcByID(p?.spec?.vpc_id):null;return (v&&v.gateway)||'10.231.0.1';}
 function connectURI(p){
  const host=p.spec.host||'HOST',port=p.spec.port||0,dbName=Object.keys(p.databases||{})[0]||'DATABASE';
  switch(p.spec.engine){
   case 'mysql':return `mysql://USER:PASSWORD@${host}:${port}/${dbName}?ssl-mode=VERIFY_CA&ssl-ca=./${p.spec.id}-ca.pem`;
   case 'postgresql':return `postgresql://USER:PASSWORD@${host}:${port}/${dbName}?sslmode=verify-ca&sslrootcert=./${p.spec.id}-ca.pem`;
   case 'mongodb':return `mongodb://USER:PASSWORD@${host}:${port}/${dbName}?authSource=admin&tls=true&tlsCAFile=./${p.spec.id}-ca.pem`;
   case 'redis':return `rediss://:PASSWORD@${host}:${port}/0`;
   default:return `${host}:${port}`;
  }
 }
 function adminUser(engine){return engine==='postgresql'?'postgres':engine==='mongodb'?'clofixadmin':engine==='redis'?'(requirepass)':'root (worker-only)';}
 function bufferLabel(engine){return engine==='postgresql'?'Shared buffers (MB)':engine==='mongodb'?'WiredTiger cache (MB)':engine==='redis'?'Max memory (MB)':'InnoDB buffer pool (MB)';}
 function renderProperties(){
  const p=propertiesProject(),msg=get('database-properties-message'),fields=get('database-connect-fields'),uri=get('database-connect-uri');
  const apply=get('database-properties-apply'),defaults=get('database-properties-defaults'),ca=get('database-properties-ca');
  if(!p){
   if(fields)fields.replaceChildren();
   if(uri)uri.value='';
   if(msg)msg.textContent='Select a READY instance to view connection info and performance settings.';
   [apply,defaults,ca].forEach(el=>{if(el)el.disabled=true;});
   return;
  }
  [apply,defaults,ca].forEach(el=>{if(el)el.disabled=false;});
  if(ca)ca.disabled=!p.ca;
  const props=p.spec.properties&&(p.spec.properties.max_connections||p.spec.properties.buffer_mb)?p.spec.properties:recommendedProperties(p);
  const vpcIP=databaseVPCIP(p),vpcPort=Number(p.spec.port)||0;
  const entries=[
   ['Instance',p.name],
   ['Engine',`${p.spec.engine} ${p.spec.version}`],
   ['Public host',p.spec.host||'-'],
   ['Public port',vpcPort?String(vpcPort):'-'],
   ['VPC',vpcAddress('',p.spec.vpc_id)],
   ['VPC IP',vpcIP],
   ['VPC port',vpcPort?String(vpcPort):'-'],
   ['TLS','Required · download CA below'],
   ['Admin user',adminUser(p.spec.engine)],
   ['Logical databases',Object.keys(p.databases||{}).sort().join(', ')||'None yet'],
   ['Client users',Object.keys(p.users||{}).sort().join(', ')||'Create users under Instances'],
   ['Resources',`${p.spec.cpu} CPU · ${p.spec.ram_mb} MB RAM · ${p.spec.disk_mb} MB disk`]
  ];
  if(fields){
   fields.replaceChildren();
   entries.forEach(([k,v])=>{
    const dt=document.createElement('dt');dt.textContent=k;
    const dd=document.createElement('dd');dd.textContent=v;
    fields.append(dt,dd);
   });
  }
  if(uri)uri.value=connectURI(p);
  get('database-prop-buffer-label').textContent=bufferLabel(p.spec.engine);
  get('database-prop-workmem-field').hidden=p.spec.engine!=='postgresql';
  get('database-prop-max-connections').value=props.max_connections||recommendedProperties(p).max_connections;
  get('database-prop-buffer').value=props.buffer_mb||recommendedProperties(p).buffer_mb;
  get('database-prop-workmem').value=props.work_mem_mb||recommendedProperties(p).work_mem_mb||4;
  get('database-perf-engine-note').textContent=p.spec.engine==='postgresql'
   ?'PostgreSQL: max_connections, shared_buffers, work_mem, effective_cache_size. Apply recreates the container and keeps data.'
   :p.spec.engine==='mongodb'
   ?'MongoDB: maxConns and wiredTigerCacheSizeGB. Apply recreates the container and keeps data.'
   :p.spec.engine==='redis'
   ?'Redis: maxclients and maxmemory. Prefer Redis Cluster under Compute for production Redis.'
   :'MySQL: max_connections and innodb_buffer_pool_size. Apply recreates the container and keeps data.';
  get('database-prop-summary').textContent=`Recommended for ${p.spec.ram_mb} MB RAM · max ${recommendedProperties(p).max_connections} connections · ${recommendedProperties(p).buffer_mb} MB buffer`;
  if(msg&&!msg.dataset.sticky)msg.textContent='';
 }
 function fillRecommendedProperties(){
  const p=propertiesProject();if(!p)return;
  const rec=recommendedProperties(p);
  get('database-prop-max-connections').value=rec.max_connections;
  get('database-prop-buffer').value=rec.buffer_mb;
  get('database-prop-workmem').value=rec.work_mem_mb||4;
  const msg=get('database-properties-message');if(msg){msg.dataset.sticky='1';msg.textContent='Filled recommended values for this RAM size. Click Apply performance to restart the engine with them.';}
 }
 async function applyProperties(){
  const p=propertiesProject();if(!p)return;
  selected=p.spec.id;
  const properties={
   max_connections:Number(get('database-prop-max-connections').value),
   buffer_mb:Number(get('database-prop-buffer').value),
   work_mem_mb:p.spec.engine==='postgresql'?Number(get('database-prop-workmem').value):0
  };
  const msg=get('database-properties-message'),btn=get('database-properties-apply');
  if(msg){msg.dataset.sticky='1';msg.textContent='Applying performance settings…';}
  if(btn){btn.disabled=true;btn.replaceChildren();const spin=document.createElement('span');spin.className='progress-icon';spin.setAttribute('aria-hidden','true');btn.append(spin,document.createTextNode('Applying…'));}
  try{
   watch(await projectOperation({action:'update-properties',properties}));
   if(msg)msg.textContent='Performance update queued. The engine container will restart briefly.';
   await refresh();
  }catch(error){if(msg)msg.textContent=error.message;}
  finally{if(btn){btn.disabled=false;btn.textContent='Apply performance';}}
 }
 function openExplorer(projectId,database){
  selected=projectId||selected;
  const instance=get('database-explorer-instance');
  if(instance&&projectId)instance.value=projectId;
  explorer.database=database||'';
  explorer.table='';
  explorer.offset=0;
  location.hash='database-explorer';
  syncExplorerSelectors();
  loadExplorerTables();
 }
 function syncExplorerSelectors(){
  const instance=get('database-explorer-instance'),database=get('database-explorer-database');
  if(!instance||!database)return;
  const previous=instance.value||selected;
  instance.replaceChildren();
  state.projects.filter(p=>p.state==='READY'&&p.spec.engine!=='redis').forEach(p=>{
   const option=document.createElement('option');option.value=p.spec.id;option.textContent=`${p.name} · ${p.spec.engine} ${p.spec.version}`;instance.append(option);
  });
  if([...instance.options].some(o=>o.value===previous))instance.value=previous;
  else if(instance.options.length)instance.selectedIndex=0;
  const p=explorerProject();
  const names=Object.keys(p?.databases||{}).sort();
  database.replaceChildren();
  names.forEach(name=>{const option=document.createElement('option');option.value=name;option.textContent=name;database.append(option);});
  if(explorer.database&&names.includes(explorer.database))database.value=explorer.database;
  else if(names.length){database.selectedIndex=0;explorer.database=database.value;}
  else explorer.database='';
 }
 function setExplorerTab(tab){
  explorer.tab=tab;
  document.querySelectorAll('[data-dbx-tab]').forEach(btn=>btn.setAttribute('aria-selected',btn.dataset.dbxTab===tab?'true':'false'));
  document.querySelectorAll('[data-dbx-panel]').forEach(panel=>{panel.hidden=panel.dataset.dbxPanel!==tab;});
 }
 function renderExplorerTables(){
  const host=get('database-explorer-tables');
  if(!host)return;
  host.replaceChildren();
  if(!explorer.tables.length){const empty=document.createElement('p');empty.className='muted';empty.textContent='No tables yet.';host.append(empty);return;}
  explorer.tables.forEach(name=>{
   const button=document.createElement('button');
   button.type='button';
   button.className='dbx-table-item'+(explorer.table===name?' is-active':'');
   button.textContent=name;
   button.onclick=()=>{explorer.table=name;explorer.offset=0;explorer.columns=[];renderExplorerTables();loadExplorerPanel();};
   host.append(button);
  });
 }
 function fillResultTable(table,columns,rows){
  row(table,columns.length?columns:['Result'],true);
  if(!rows?.length){row(table,[columns.length?'No rows.':'No result.']);return;}
  rows.forEach(item=>{
   if(Array.isArray(item))row(table,item.map(value=>value??''));
   else row(table,columns.map(col=>item?.[col]??''));
  });
 }
 function normalizeExplorerTables(tables){
  return Array.isArray(tables)?tables.map(item=>typeof item==='string'?item:item?.name).filter(Boolean).sort():[];
 }
 function paintStructure(result){
  const p=explorerProject();
  const addCol=get('database-explorer-add-column');
  if(addCol)addCol.hidden=p?.spec.engine==='mongodb'||p?.spec.engine==='redis';
  if(p?.spec.engine==='mongodb'){
   row(get('database-explorer-columns'),['Field','Value'],true);
   row(get('database-explorer-columns'),['Collection',explorer.table]);
   row(get('database-explorer-columns'),['Stats',JSON.stringify(result.mongodb?.stats||{},null,0).slice(0,240)]);
   const indexes=result.mongodb?.indexes||[];
   row(get('database-explorer-indexes'),['Name','Keys'],true);
   if(!indexes.length)row(get('database-explorer-indexes'),['No indexes.','']);
   indexes.forEach(idx=>row(get('database-explorer-indexes'),[idx.name||'-',JSON.stringify(idx.key||idx)]));
   return;
  }
  const columns=result.columns||[];
  explorer.columns=columns;
  row(get('database-explorer-columns'),['Column','Type','Nullable','Key','Default','Extra','Actions'],true);
  if(!columns.length)row(get('database-explorer-columns'),['No columns.','','','','','','']);
  columns.forEach(col=>{
   const actions=document.createElement('div');actions.className='row-actions';
   const edit=document.createElement('button');edit.type='button';edit.className='small-btn';edit.textContent='Edit';
   const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';
   edit.onclick=()=>openColumnEditor('update',col);
   del.onclick=()=>deleteExplorerColumn(col);
   actions.append(edit,del);
   row(get('database-explorer-columns'),[col.name||'-',col.type||'-',col.nullable||'-',col.key||'-',col.default??'-',col.extra||'-',actions]);
  });
  const indexes=result.indexes||[];
  row(get('database-explorer-indexes'),['Index','Details'],true);
  if(!indexes.length)row(get('database-explorer-indexes'),['No indexes.','']);
  indexes.forEach(idx=>row(get('database-explorer-indexes'),[idx.name||'-',idx.definition||`${idx.column||''} · non_unique=${idx.non_unique??'-'}`]));
 }
 async function loadExplorerTables(){
  const p=explorerProject();
  syncExplorerSelectors();
  if(!p||!explorer.database){explorer.tables=[];renderExplorerTables();setExplorerBusy(false);explorerMsg(p?'Create a logical database first.':'Select a READY instance.');return;}
  if(explorer.busy)return;
  setExplorerBusy(true,'Loading schema…');
  try{
   const result=await runExplorer({action:'inspect-schema',database:explorer.database,table:explorer.table||undefined});
   explorer.tables=normalizeExplorerTables(result.tables);
   if(explorer.table&&!explorer.tables.includes(explorer.table))explorer.table='';
   if(result.table&&explorer.tables.includes(result.table))explorer.table=result.table;
   if(!explorer.table&&explorer.tables.length)explorer.table=explorer.tables[0];
   renderExplorerTables();
   get('database-explorer-structure-title').textContent=explorer.table?`Structure · ${explorer.table}`:'Structure';
   get('database-explorer-tables-title').textContent=explorer.database;
   if(explorer.tab==='structure'&&explorer.table&&(result.columns||result.mongodb)){
    paintStructure(result);
    explorerMsg(`Structure loaded for ${explorer.table}`);
   }else if(explorer.tab==='browse'&&explorer.table){
    await loadExplorerBrowse(true);
   }else{
    explorerMsg(`${explorer.tables.length} table${explorer.tables.length===1?'':'s'} in ${explorer.database}`);
   }
  }catch(error){explorerMsg(error.message);}
  finally{setExplorerBusy(false,get('database-explorer-message')?.textContent||'');}
 }
 async function loadExplorerPanel(nested=false){
  if(!explorer.table){row(get('database-explorer-columns'),['Column','Type','Nullable','Key','Default','Extra','Actions'],true);row(get('database-explorer-columns'),['Select a table.','','','','','','']);row(get('database-explorer-indexes'),['Index','Details'],true);row(get('database-explorer-indexes'),['No indexes.','']);row(get('database-explorer-rows'),['Rows'],true);row(get('database-explorer-rows'),['Select a table.']);return;}
  get('database-explorer-structure-title').textContent=`Structure · ${explorer.table}`;
  get('database-explorer-tables-title').textContent=explorer.database;
  if(explorer.tab==='structure')await loadExplorerStructure(nested);
  if(explorer.tab==='browse')await loadExplorerBrowse(nested);
 }
 function structureTypeOptions(engine){
  if(engine==='postgresql')return [['INTEGER','INTEGER'],['BIGINT','BIGINT'],['SMALLINT','SMALLINT'],['BOOLEAN','BOOLEAN'],['TEXT','TEXT'],['VARCHAR(255)','VARCHAR(255)'],['DATE','DATE'],['TIMESTAMP','TIMESTAMP'],['JSON','JSON'],['JSONB','JSONB'],['UUID','UUID'],['BYTEA','BYTEA'],['DOUBLE PRECISION','DOUBLE PRECISION'],['REAL','REAL'],['DECIMAL(18,2)','DECIMAL(18,2)']];
  return [['INTEGER','INTEGER'],['BIGINT','BIGINT'],['SMALLINT','SMALLINT'],['BOOLEAN','BOOLEAN'],['TEXT','TEXT'],['VARCHAR(255)','VARCHAR(255)'],['DATE','DATE'],['TIMESTAMP','TIMESTAMP'],['JSON','JSON'],['BLOB','BLOB'],['DECIMAL(18,2)','DECIMAL(18,2)']];
 }
 function normalizeStructureType(raw){
  const t=String(raw||'').trim().toUpperCase();
  if(!t)return 'TEXT';
  if(/^VARCHAR\(/i.test(t)||/^DECIMAL\(/i.test(t))return t.replace(/\s+/g,'');
  if(/INT\(/i.test(t))return 'INTEGER';
  if(/TINYINT\(1\)|BOOL/i.test(t))return 'BOOLEAN';
  if(/DOUBLE|FLOAT8/i.test(t))return 'DOUBLE PRECISION';
  if(/FLOAT|REAL/i.test(t))return 'REAL';
  if(/TIMESTAMP|DATETIME/i.test(t))return 'TIMESTAMP';
  if(/BYTEA|BLOB|BINARY/i.test(t))return /BYTEA/i.test(t)?'BYTEA':'BLOB';
  if(/JSONB/i.test(t))return 'JSONB';
  if(/JSON/i.test(t))return 'JSON';
  if(/UUID/i.test(t))return 'UUID';
  if(/TEXT|CHAR|CLOB/i.test(t))return /CHAR/i.test(t)&&!/TEXT/i.test(t)?'VARCHAR(255)':'TEXT';
  if(/DATE/i.test(t))return 'DATE';
  if(/BIGINT|INT8/i.test(t))return 'BIGINT';
  if(/SMALLINT|INT2/i.test(t))return 'SMALLINT';
  if(/INT/i.test(t))return 'INTEGER';
  return t;
 }
 function openColumnEditor(mode,col={}){
  const p=explorerProject();
  if(!p||!explorer.table){explorerMsg('Select a table first.');return;}
  if(p.spec.engine==='mongodb'||p.spec.engine==='redis'){explorerMsg('Column structure editing requires MySQL or PostgreSQL.');return;}
  selected=p.spec.id;
  const types=structureTypeOptions(p.spec.engine);
  const currentType=normalizeStructureType(col.type);
  if(currentType&&!types.some(([v])=>v===currentType))types.unshift([currentType,currentType]);
  const nullable=String(col.nullable||'').toUpperCase()!=='NO'&&col.nullable!==false;
  const def=col.default==null||col.default===undefined?'':String(col.default);
  const title=mode==='add'?`Add column · ${explorer.table}`:`Edit column · ${explorer.table}.${col.name||''}`;
  showOperation(title,
   `${textField('db-op-column','Column name',col.name||'')}${mode==='update'?textField('db-op-new-column','Rename to (optional)',''):''}${selectField('db-op-type','Type',types)}<label><input id="db-op-nullable" type="checkbox" ${nullable?'checked':''}> Nullable</label><label><input id="db-op-unique" type="checkbox"> Unique</label>${textField('db-op-default','Default (optional)',def)}`,
   async()=>{
    const name=get('db-op-column').value.trim();
    const column={name,type:get('db-op-type').value,nullable:!!get('db-op-nullable')?.checked,unique:mode==='add'&&!!get('db-op-unique')?.checked};
    const defVal=get('db-op-default').value;
    if(defVal!=='')column.default=defVal;
    else if(mode==='update')column.default=null;
    const body={action:mode==='add'?'add-column':'update-column',database:explorer.database,table:explorer.table,columns:[column]};
    if(mode==='update'){
     const renamed=get('db-op-new-column')?.value.trim();
     if(renamed&&renamed!==name)body.new_name=renamed;
    }
    setExplorerBusy(true,mode==='add'?`Adding ${name}…`:`Updating ${name}…`);
    try{
     await runExplorer(body);
     setExplorerBusy(false);
     await loadExplorerStructure();
     explorerMsg(mode==='add'?`Column ${name} added.`:`Column ${name} updated.`);
     return {ok:true};
    }catch(error){setExplorerBusy(false,error.message);throw error;}
   }
  );
  if(mode==='update'){const nameInput=get('db-op-column');if(nameInput)nameInput.readOnly=true;}
  const unique=get('db-op-unique');if(unique)unique.closest('label').hidden=mode!=='add';
 }
 async function deleteExplorerColumn(col){
  if(!col?.name||explorer.busy)return;
  if(await window.cfPrompt(`Type DELETE to drop column ${col.name}`,'',{title:'Drop column',label:'Confirmation',confirm:'Delete',hint:'Type DELETE exactly.'})!=='DELETE')return;
  setExplorerBusy(true,`Dropping column ${col.name}…`);
  try{
   await runExplorer({action:'delete-column',database:explorer.database,table:explorer.table,columns:[{name:col.name,type:'TEXT'}],confirm:'DELETE'});
   setExplorerBusy(false);
   await loadExplorerStructure();
   explorerMsg(`Column ${col.name} dropped.`);
  }catch(error){explorerMsg(error.message);setExplorerBusy(false,error.message);}
 }
 async function loadExplorerStructure(nested=false){
  if(!nested&&explorer.busy)return;
  if(!nested)setExplorerBusy(true,`Describing ${explorer.table}…`);
  else explorerMsg(`Describing ${explorer.table}…`);
  try{
   const result=await runExplorer({action:'describe-table',database:explorer.database,table:explorer.table});
   paintStructure(result);
   explorerMsg(`Structure loaded for ${explorer.table}`);
  }catch(error){explorerMsg(error.message);}
  finally{if(!nested)setExplorerBusy(false,get('database-explorer-message')?.textContent||'');}
 }
 function renderBrowseRows(columns,rows){
  const table=get('database-explorer-rows');
  row(table,[...columns,'Actions'],true);
  if(!rows?.length){row(table,['No rows.',...columns.map(()=>''),'']);return;}
  const pkCols=(explorer.columns||[]).filter(isPrimaryColumn).map(c=>c.name);
  rows.forEach(item=>{
   const values=Array.isArray(item)?item:columns.map(col=>item?.[col]??'');
   const record={};columns.forEach((col,i)=>record[col]=values[i]??'');
   const actions=document.createElement('div');actions.className='row-actions';
   const edit=document.createElement('button');edit.type='button';edit.className='small-btn';edit.textContent='Edit';
   const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';
   edit.onclick=()=>openRowEditor('update',record,pkCols);
   del.onclick=()=>deleteExplorerRow(record,pkCols);
   actions.append(edit,del);
   row(table,[...values.map(value=>value??''),actions]);
  });
 }
 function renderMongoDocs(docs){
  const table=get('database-explorer-rows');
  row(table,['Document','Actions'],true);
  if(!docs?.length){row(table,['No documents.','']);return;}
  docs.forEach(doc=>{
   const actions=document.createElement('div');actions.className='row-actions';
   const edit=document.createElement('button');edit.type='button';edit.className='small-btn';edit.textContent='Edit';
   const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';
   edit.onclick=()=>openMongoEditor('update',doc);
   del.onclick=()=>deleteMongoDoc(doc);
   actions.append(edit,del);
   row(table,[JSON.stringify(doc),actions]);
  });
 }
 async function ensureExplorerColumns(){
  if(explorer.columns?.length&&explorer.table)return explorer.columns;
  const result=await runExplorer({action:'describe-table',database:explorer.database,table:explorer.table});
  explorer.columns=result.columns||[];
  return explorer.columns;
 }
 async function loadExplorerBrowse(nested=false){
  if(!nested&&explorer.busy)return;
  if(!nested)setExplorerBusy(true,`Loading rows for ${explorer.table}…`);
  else explorerMsg(`Loading rows for ${explorer.table}…`);
  try{
   const p=explorerProject();
   if(p?.spec.engine!=='mongodb')await ensureExplorerColumns();
   const result=await runExplorer({action:'select-rows',database:explorer.database,table:explorer.table,limit:explorer.limit,offset:explorer.offset});
   if(p?.spec.engine==='mongodb')renderMongoDocs(result.documents||[]);
   else renderBrowseRows(result.columns||[],result.rows||[]);
   const from=explorer.offset+1,to=explorer.offset+(result.rows?.length||result.documents?.length||0);
   get('database-explorer-page').textContent=to?`${from}–${to}`:`${explorer.offset}`;
   explorerMsg(`Showing rows for ${explorer.table}`);
  }catch(error){explorerMsg(error.message);}
  finally{if(!nested)setExplorerBusy(false,get('database-explorer-message')?.textContent||'');}
 }
 function openRowEditor(mode,record={},pkCols=[]){
  const p=explorerProject();
  if(!p||!explorer.table){explorerMsg('Select a table first.');return;}
  if(p.spec.engine==='mongodb'){openMongoEditor(mode,record);return;}
  rowEditor={mode,columns:explorer.columns||[],keys:{},values:{}};
  const host=get('database-row-fields');host.replaceChildren();
  get('database-row-title').textContent=mode==='insert'?`Insert into ${explorer.table}`:`Edit ${explorer.table}`;
  get('database-row-message').textContent='';
  get('database-row-hint').textContent=mode==='insert'?'Auto-increment / identity columns can be left blank. Mark NULL for SQL NULL.':'Primary key fields identify the row. Changed values are written with UPDATE.';
  (explorer.columns||[]).forEach(col=>{
   const name=col.name;if(!name)return;
   const wrap=document.createElement('div');wrap.className='form-field form-span';
   const label=document.createElement('label');label.textContent=`${name} · ${col.type||'text'}${isPrimaryColumn(col)?' · PK':''}${isAutoColumn(col)?' · auto':''}`;
   const kind=inputKindForType(col.type);
   let input;
   if(kind==='textarea'){input=document.createElement('textarea');input.rows=3;}
   else if(kind==='boolean'){input=document.createElement('select');[['','(blank)'],['true','true'],['false','false']].forEach(([v,t])=>input.append(new Option(t,v)));}
   else {input=document.createElement('input');input.type=kind==='number'?'number':kind==='date'?'date':kind==='datetime-local'?'text':'text';input.placeholder=col.type||'';}
   input.dataset.col=name;input.id=`db-row-${name}`;
   if(record[name]!=null)input.value=String(record[name]);
   if(mode==='update'&&isPrimaryColumn(col)){input.readOnly=true;input.classList.add('is-readonly');}
   if(mode==='insert'&&isAutoColumn(col)&&!record[name])input.placeholder='auto';
   const nullLabel=document.createElement('label');nullLabel.className='dbx-null-toggle';
   const nullBox=document.createElement('input');nullBox.type='checkbox';nullBox.dataset.nullFor=name;
   if(mode==='update'&&(record[name]==null||record[name]==='')){/* keep unchecked by default */}
   nullLabel.append(nullBox,document.createTextNode(' NULL'));
   wrap.append(label,input,nullLabel);host.append(wrap);
   if(isPrimaryColumn(col)&&record[name]!=null)rowEditor.keys[name]=String(record[name]);
  });
  if(mode==='update'&&!Object.keys(rowEditor.keys).length&&pkCols.length){
   pkCols.forEach(name=>{if(record[name]!=null)rowEditor.keys[name]=String(record[name]);});
  }
  get('database-row-dialog').showModal();
 }
 function openMongoEditor(mode,doc={}){
  rowEditor={mode,columns:[],keys:{},values:{}};
  const host=get('database-row-fields');host.replaceChildren();
  get('database-row-title').textContent=mode==='insert'?`Insert into ${explorer.table}`:`Edit ${explorer.table}`;
  get('database-row-hint').textContent='Provide a JSON document. Updates use $set on the selected _id.';
  get('database-row-message').textContent='';
  const wrap=document.createElement('div');wrap.className='form-field form-span';
  const label=document.createElement('label');label.textContent='Document JSON';
  const input=document.createElement('textarea');input.id='db-row-document';input.rows=12;input.spellcheck=false;
  const clone={...doc};if(mode==='update'&&clone._id!=null){rowEditor.keys._id=String(clone._id?.$oid||clone._id);delete clone._id;}
  input.value=mode==='insert'?'{\n  \n}':JSON.stringify(clone,null,2);
  wrap.append(label,input);host.append(wrap);
  get('database-row-dialog').showModal();
 }
 async function submitRowEditor(event){
  event.preventDefault();
  const p=explorerProject();if(!p||!explorer.table)return;
  const msg=get('database-row-message');
  try{
   if(p.spec.engine==='mongodb'){
    const doc=get('db-row-document').value.trim();
    JSON.parse(doc);
    const body={action:rowEditor.mode==='insert'?'insert-row':'update-row',database:explorer.database,table:explorer.table,query:doc,keys:rowEditor.mode==='update'?rowEditor.keys:undefined};
    setExplorerBusy(true,rowEditor.mode==='insert'?'Inserting document…':'Updating document…');
    await runExplorer(body);
   }else{
    const values={};
    (explorer.columns||[]).forEach(col=>{
     const name=col.name;if(!name)return;
     if(rowEditor.mode==='update'&&isPrimaryColumn(col))return;
     const nullBox=document.querySelector(`[data-null-for="${CSS.escape(name)}"]`);
     const input=document.querySelector(`#db-row-${CSS.escape(name)}`);
     if(nullBox?.checked){values[name]=null;return;}
     if(rowEditor.mode==='insert'&&isAutoColumn(col)&&!(input?.value||'').trim())return;
     if(!input)return;
     const value=input.value;
     if(rowEditor.mode==='insert'&&value===''&&String(col.nullable||'').toUpperCase()==='YES'){values[name]=null;return;}
     if(rowEditor.mode==='update'&&value===''&&!nullBox?.checked&&String(col.nullable||'').toUpperCase()==='YES'){values[name]=null;return;}
     values[name]=value;
    });
    if(rowEditor.mode==='update'&&!Object.keys(rowEditor.keys).length)throw Error('This table has no primary key; use SQL to update rows.');
    const body={action:rowEditor.mode==='insert'?'insert-row':'update-row',database:explorer.database,table:explorer.table,values,keys:rowEditor.mode==='update'?rowEditor.keys:undefined};
    setExplorerBusy(true,rowEditor.mode==='insert'?'Inserting row…':'Updating row…');
    await runExplorer(body);
   }
   get('database-row-dialog').close();
   explorerMsg(rowEditor.mode==='insert'?'Row inserted.':'Row updated.');
   await loadExplorerBrowse();
  }catch(error){if(msg)msg.textContent=error.message;explorerMsg(error.message);setExplorerBusy(false,error.message);}
 }
 async function deleteExplorerRow(record,pkCols){
  const keys={};
  (explorer.columns||[]).filter(isPrimaryColumn).forEach(col=>{if(record[col.name]!=null)keys[col.name]=String(record[col.name]);});
  pkCols.forEach(name=>{if(record[name]!=null)keys[name]=String(record[name]);});
  if(!Object.keys(keys).length){explorerMsg('This table has no primary key; use SQL to delete rows.');return;}
  if(await window.cfPrompt('Type DELETE to remove this row','',{title:'Delete row',label:'Confirmation',confirm:'Delete',hint:'Type DELETE exactly.'})!=='DELETE')return;
  setExplorerBusy(true,'Deleting row…');
  try{await runExplorer({action:'delete-row',database:explorer.database,table:explorer.table,keys,confirm:'DELETE'});explorerMsg('Row deleted.');await loadExplorerBrowse();}
  catch(error){explorerMsg(error.message);setExplorerBusy(false,error.message);}
 }
 async function deleteMongoDoc(doc){
  const id=doc?._id?.$oid||doc?._id;if(id==null){explorerMsg('Document has no _id.');return;}
  if(await window.cfPrompt('Type DELETE to remove this document','',{title:'Delete document',label:'Confirmation',confirm:'Delete',hint:'Type DELETE exactly.'})!=='DELETE')return;
  setExplorerBusy(true,'Deleting document…');
  try{await runExplorer({action:'delete-row',database:explorer.database,table:explorer.table,keys:{_id:String(id)},confirm:'DELETE'});explorerMsg('Document deleted.');await loadExplorerBrowse();}
  catch(error){explorerMsg(error.message);setExplorerBusy(false,error.message);}
 }
 async function uploadExplorerSQL(file){
  if(!file)return;
  if(file.size>2*1024*1024){explorerMsg('SQL file must be 2 MB or smaller.');return;}
  const p=explorerProject();
  if(!p||!explorer.database){explorerMsg('Pick an instance and database first.');return;}
  if(p.spec.engine!=='mysql'&&p.spec.engine!=='postgresql'){explorerMsg('SQL import is only for MySQL and PostgreSQL.');return;}
  if(await window.cfPrompt('Type IMPORT to run this SQL file against the selected database','',{title:'Import SQL',label:'Confirmation',confirm:'Import',hint:'Type IMPORT exactly. Multi-statement scripts are allowed.'})!=='IMPORT')return;
  setExplorerBusy(true,`Importing ${file.name}…`);
  try{
   const sql=await file.text();
   await runExplorer({action:'import-sql',database:explorer.database,query:sql,confirm:'IMPORT'},300000);
   explorerMsg(`Imported ${file.name}.`);
   await loadExplorerTables();
  }catch(error){explorerMsg(error.message);setExplorerBusy(false,error.message);}
 }
 async function runExplorerSQL(){
  const sql=get('database-explorer-sql').value.trim();
  if(!sql){explorerMsg('Enter a SQL statement.');return;}
  if(explorer.busy)return;
  const upper=sql.replace(/;+\s*$/,'').trim().toUpperCase();
  const write=/^(INSERT|UPDATE|DELETE|REPLACE|CREATE|ALTER|DROP|TRUNCATE|RENAME|CALL|DO|ANALYZE|OPTIMIZE|REINDEX|VACUUM|COMMENT)\b/.test(upper);
  let confirm='';
  if(write){
   confirm=await window.cfPrompt('Type EXECUTE to run this write statement','',{title:'Run write SQL',label:'Confirmation',confirm:'Execute',hint:'Type EXECUTE exactly.'});
   if(confirm!=='EXECUTE')return;
  }
  setExplorerBusy(true,'Running SQL…',{run:true});
  get('database-explorer-sql-json').hidden=true;
  try{
   const result=await runExplorer({action:'execute-sql',database:explorer.database,query:sql,confirm});
   if(result.columns)fillResultTable(get('database-explorer-sql-result'),result.columns,result.rows||[]);
   else if(result.result!==undefined){row(get('database-explorer-sql-result'),['Result'],true);row(get('database-explorer-sql-result'),[typeof result.result==='string'?result.result:JSON.stringify(result.result)]);get('database-explorer-sql-json').hidden=false;get('database-explorer-sql-json').textContent=JSON.stringify(result.result,null,2);}
   else {row(get('database-explorer-sql-result'),['Status'],true);row(get('database-explorer-sql-result'),[result.ok?'Statement executed.':JSON.stringify(result)]);}
   explorerMsg(write?'Write SQL completed.':'Query completed.');
   if(write){setExplorerBusy(false);await loadExplorerTables();return;}
  }catch(error){explorerMsg(error.message);}
  finally{setExplorerBusy(false,get('database-explorer-message')?.textContent||'');}
 }
 function renderBackups(){const table=get('database-backups');row(table,['Created','Database','Engine','Scope','Destination','Size','State','Actions'],true);if(!state.backups.length)row(table,['No database backups.','','','','','','','']);state.backups.forEach(b=>{const actions=document.createElement('div');actions.className='row-actions';if(b.state==='READY'){actions.append(action('Download',async()=>{const token=await api('GET',`/api/databases/backups/${b.id}/download-token`);location.href=token.url;}),action('Restore',()=>openRestore(b)));}if(b.state==='FAILED'&&b.attempts<3)actions.append(action('Retry',async()=>{await api('POST',`/api/databases/backups/${b.id}/retry`);await refresh();}));actions.append(action('Delete',async()=>{if(!confirm('Delete this encrypted backup?'))return;await api('DELETE',`/api/databases/backups/${b.id}`);await refresh();},'danger small-btn'));const scope=b.scope==='data'?'Data only':b.scope==='schema'?'Schema only':b.tables?.length?b.tables.join(', '):'Full';row(table,[new Date(b.created).toLocaleString(),b.database,b.engine,scope,b.destination,b.bytes?fmtBytes(b.bytes):'Calculating…',badge(b.state),actions]);});}
 function renderSchedules(){const table=get('database-schedules');row(table,['Database','Cron (UTC)','Retention','Destination','Status','Actions'],true);if(!state.schedules.length)row(table,['No automatic database backups.','','','','','']);state.schedules.forEach(s=>{const actions=document.createElement('div');actions.className='row-actions';actions.append(action('Delete',async()=>{if(!confirm('Delete this backup schedule?'))return;await api('DELETE',`/api/databases/schedules/${s.id}`);await refresh();},'danger small-btn'));row(table,[s.database,s.cron,`${s.retention} backups`,s.destination,s.enabled?'Enabled':'Paused',actions]);});}
 function renderJobs(){const table=get('database-jobs');row(table,['Updated','Action','Instance','Progress','State','Error','Actions'],true);if(!state.jobs.length)row(table,['No database jobs.','','','','','','']);state.jobs.slice(0,50).forEach(j=>{const actions=document.createElement('div');actions.className='row-actions';if(!['SUCCEEDED','FAILED','CANCELLED','INTERRUPTED','RETRIED'].includes(j.state))actions.append(action('Cancel',async()=>{await api('POST',`/api/databases/jobs/${j.id}/cancel`);await refresh();},'danger small-btn'));row(table,[new Date(j.updated).toLocaleString(),j.action,j.project_id,`${j.progress||0}%`,badge(j.state),j.error||'-',actions]);});}
 async function refresh(){
  const run=async()=>{if(busy)return;busy=true;try{const calls=[api('GET','/api/databases'),typeof loadVPCCatalog==='function'?loadVPCCatalog().catch(()=>{}):Promise.resolve()];if(!cfMe||cfMe.kind==='admin')calls.push(api('GET','/api/license/usage').catch(()=>({})));const [databaseState,,licenseState]=await Promise.all(calls);state=databaseState;license=licenseState||{};if(selected&&!project())selected='';render();if(get('auth-banner'))get('auth-banner').hidden=true;}catch(error){message(error.message);}finally{busy=false;}};
  return typeof preservePageScroll==='function'?preservePageScroll(run):run();
 }
 function syncVersions(){const engine=get('database-project-engine').value,worker=state.workers.find(w=>w.id===get('database-project-worker').value),version=get('database-project-version');version.replaceChildren();const value=worker?.engines?.[engine];if(value){const option=document.createElement('option');option.value=value;option.textContent=value;version.append(option);}}
 function syncWorkers(){const engine=get('database-project-engine').value,worker=get('database-project-worker');worker.replaceChildren();state.workers.filter(w=>w.engines?.[engine]).forEach(item=>{const option=document.createElement('option');option.value=item.id;option.textContent=`${item.hostname} · ${item.id}`;worker.append(option);});syncVersions();}
 function openDatabase(){showOperation('Create logical database',textField('db-op-name','Database name'),async()=>projectOperation({action:'create-database',database:get('db-op-name').value.trim()}));}
 function openUser(name='',grants={}){const p=project(),options=Object.keys(p.databases||{}).map(database=>`<label class="iam-check-item"><input type="checkbox" data-db="${database}" ${grants[database]?'checked':''}><span>${database}</span><select data-role="${database}"><option value="read" ${grants[database]==='read'?'selected':''}>Read</option><option value="write" ${grants[database]==='write'?'selected':''}>Read / write</option></select></label>`).join('');showOperation(name?'Modify database user':'Create database user',`${textField('db-op-user','Username',name)}${textField('db-op-password',name?'New password (leave blank to retain)':'Password',name?'':'','password')}<div class="form-field"><button class="secondary" id="db-generate-password" type="button">Generate secure password</button></div><div class="form-field form-span"><label>Database permissions</label><div class="iam-check-grid">${options}</div></div>`,async()=>{const permissions={};document.querySelectorAll('#database-operation-fields [data-db]:checked').forEach(box=>permissions[box.dataset.db]=document.querySelector(`[data-role="${box.dataset.db}"]`).value);return projectOperation({action:name?'update-user':'create-user',username:get('db-op-user').value.trim(),password:get('db-op-password').value,grants:permissions});});get('db-generate-password').onclick=()=>{const bytes=new Uint8Array(24);crypto.getRandomValues(bytes);get('db-op-password').value=Array.from(bytes,value=>value.toString(16).padStart(2,'0')).join('');};}
 function openTable(database){const p=project();const types=p.spec.engine==='postgresql'?[['INTEGER','INTEGER'],['BIGINT','BIGINT'],['SMALLINT','SMALLINT'],['BOOLEAN','BOOLEAN'],['TEXT','TEXT'],['VARCHAR(255)','VARCHAR(255)'],['DATE','DATE'],['TIMESTAMP','TIMESTAMP'],['JSON','JSON'],['JSONB','JSONB'],['UUID','UUID'],['BYTEA','BYTEA'],['DOUBLE PRECISION','DOUBLE PRECISION'],['REAL','REAL'],['DECIMAL(18,2)','DECIMAL(18,2)']]:[['INTEGER','INTEGER'],['BIGINT','BIGINT'],['SMALLINT','SMALLINT'],['BOOLEAN','BOOLEAN'],['TEXT','TEXT'],['VARCHAR(255)','VARCHAR(255)'],['DATE','DATE'],['TIMESTAMP','TIMESTAMP'],['JSON','JSON'],['BLOB','BLOB'],['DECIMAL(18,2)','DECIMAL(18,2)']];showOperation(p.spec.engine==='mongodb'?'Create collection':'Create table',textField('db-op-table',p.spec.engine==='mongodb'?'Collection name':'Table name')+(p.spec.engine==='mongodb'?'':`${textField('db-op-column','First column name','id')}${selectField('db-op-type','First column type',types)}<label><input id="db-op-primary" type="checkbox" checked> Primary key</label>`),async()=>projectOperation({action:'create-table',database,table:get('db-op-table').value.trim(),columns:p.spec.engine==='mongodb'?[]:[{name:get('db-op-column').value.trim(),type:get('db-op-type').value,primary:get('db-op-primary').checked,nullable:false}]}));}
 function openIndex(database){showOperation('Create index',`${textField('db-op-table','Table / collection')}${textField('db-op-index','Index name')}${textField('db-op-columns','Columns, comma separated')}<label><input id="db-op-unique" type="checkbox"> Unique index</label>`,async()=>projectOperation({action:'create-index',database,table:get('db-op-table').value.trim(),index:{name:get('db-op-index').value.trim(),columns:get('db-op-columns').value.split(',').map(x=>x.trim()).filter(Boolean),unique:get('db-op-unique').checked}}));}
 function destinationFields(){const options=[['central','Central encrypted storage'],['s3','S3-compatible storage']];return `${selectField('db-op-destination','Destination',options)}${selectField('db-op-connection','S3 connection',[['','Select connection'],...state.connections.map(c=>[c.id,c.name||c.bucket])])}<p class="muted form-span">Backup size is calculated automatically, up to the managed database disk allocation.</p>`;}
 function openBackup(database=''){const p=project();if(p?.spec?.engine==='redis'){message('Redis backup is not implemented.');return;}const databases=database?[[database,database]]:Object.keys(p.databases||{}).map(name=>[name,name]),scopes=[['full','Full backup (schema and data)'],['data','Data only'],...(p.spec.engine==='mongodb'?[]:[['schema','Schema only']])];showOperation('Create encrypted backup',`${selectField('db-op-database','Database',databases)}${selectField('db-op-scope','Backup contents',scopes)}${destinationFields()}`,async()=>api('POST',`/api/databases/projects/${selected}/backups`,{database:get('db-op-database').value,scope:get('db-op-scope').value,destination:get('db-op-destination').value,connection_id:get('db-op-connection').value}));}
 function openRestore(backup){if(!selected){message('Select a destination instance first.');return;}const p=project(),existing=Object.keys(p.databases||{}).sort(),options=existing.map(name=>[name,name]);if(!existing.includes(backup.database))options.unshift([backup.database,backup.database]);showOperation('Restore encrypted backup',`${selectField('db-op-target','Restore into existing database',options)}<p class="muted form-span">Backups created here and completed uploads both appear in Backup Manager and use this restore flow. Replacing an existing database creates a fresh safety backup first. Type RESTORE to confirm.</p>${textField('db-op-confirm','Confirmation')}`,async()=>api('POST',`/api/databases/projects/${selected}/restore`,{backup_id:backup.id,database:get('db-op-target').value,tables:[],confirm:get('db-op-confirm').value}));}
 function openSchedule(){const p=project();if(p?.spec?.engine==='redis'){message('Redis backup is not implemented.');return;}const databases=Object.keys(p.databases||{}).map(name=>[name,name]);showOperation('Add automatic backup schedule',`${selectField('db-op-database','Database',databases)}${textField('db-op-cron','UTC cron schedule','0 2 * * *')}${textField('db-op-retention','Retained backups','7','number')}${destinationFields()}<label><input id="db-op-enabled" type="checkbox" checked> Enabled</label>`,async()=>api('POST','/api/databases/schedules',{project_id:selected,database:get('db-op-database').value,cron:get('db-op-cron').value.trim(),retention:Number(get('db-op-retention').value),destination:get('db-op-destination').value,connection_id:get('db-op-connection').value,enabled:get('db-op-enabled').checked}));}
 async function uploadBackup(file){const p=project();if(!p)throw Error('Select a destination database instance first.');if(p.spec.engine==='redis')throw Error('Redis backup is not implemented.');const databases=Object.keys(p.databases||{}),database=await window.cfPrompt(`Destination database (${databases.join(', ')})`,databases[0]||'',{title:'Upload backup',label:'Destination database',confirm:'Continue'});if(!database||!p.databases?.[database])throw Error('Choose an existing destination database.');const upload=await api('POST','/api/databases/uploads',{project_id:selected,database,filename:file.name,total:file.size,sha256:''});let offset=upload.offset||0;while(offset<file.size){const end=Math.min(file.size,offset+(8<<20)),response=await fetch(`/api/databases/uploads/${upload.id}`,{method:'PATCH',credentials:'include',headers:{'Content-Range':`bytes ${offset}-${end-1}/${file.size}`},body:file.slice(offset,end)});if(!response.ok){let problem={};try{problem=await response.json();}catch{}throw Error(problem.error||`Upload failed (${response.status})`);}const result=await response.json();offset=result.offset;message(`Uploading ${file.name}: ${Math.round(offset/file.size*100)}%`);}await api('POST',`/api/databases/uploads/${upload.id}/complete`);message('Backup upload validated and encrypted.');await refresh();}
 get('database-add-project').onclick=()=>{if(get('database-add-project').disabled){message(get('database-add-project').title);return;}loadVPCOptions('database-project-vpc');get('database-project-dialog').showModal();syncWorkers();};get('database-project-engine').onchange=syncWorkers;get('database-project-worker').onchange=syncVersions;get('database-refresh').onclick=refresh;get('database-add-logical').onclick=openDatabase;get('database-add-user').onclick=()=>openUser();get('database-create-backup').onclick=()=>openBackup();get('database-add-schedule').onclick=openSchedule;
 get('database-explorer-instance').onchange=()=>{selected=get('database-explorer-instance').value;explorer.database='';explorer.table='';explorer.offset=0;loadExplorerTables();};
 get('database-explorer-database').onchange=()=>{explorer.database=get('database-explorer-database').value;explorer.table='';explorer.offset=0;loadExplorerTables();};
 get('database-explorer-refresh').onclick=()=>loadExplorerTables();
 if(get('database-properties-instance'))get('database-properties-instance').onchange=()=>{selected=get('database-properties-instance').value;renderProperties();};
 if(get('database-properties-defaults'))get('database-properties-defaults').onclick=fillRecommendedProperties;
 if(get('database-properties-apply'))get('database-properties-apply').onclick=()=>applyProperties();
 if(get('database-connect-copy'))get('database-connect-copy').onclick=async()=>{try{await navigator.clipboard.writeText(get('database-connect-uri').value);const msg=get('database-properties-message');if(msg){msg.dataset.sticky='1';msg.textContent='Connection URI copied.';}}catch(e){const msg=get('database-properties-message');if(msg){msg.dataset.sticky='1';msg.textContent='Clipboard unavailable. Select and copy the URI.';}}};
 if(get('database-properties-ca'))get('database-properties-ca').onclick=async()=>{const p=propertiesProject();if(!p?.ca)return;selected=p.spec.id;const response=await fetch(`/api/databases/projects/${encodeURIComponent(selected)}/ca.pem`,{credentials:'include'});if(!response.ok){const msg=get('database-properties-message');if(msg)msg.textContent='TLS CA download failed.';return;}const link=document.createElement('a');link.href=URL.createObjectURL(await response.blob());link.download=`${selected}-ca.pem`;link.click();setTimeout(()=>URL.revokeObjectURL(link.href),1000);};
 async function openConnectDialog(){
  const p=propertiesProject();if(!p||p.state!=='READY'){const msg=get('database-properties-message');if(msg){msg.dataset.sticky='1';msg.textContent='Select a READY instance first.';}return;}
  selected=p.spec.id;
  const dbSel=get('database-connect-database'),userSel=get('database-connect-user');
  dbSel.replaceChildren();Object.keys(p.databases||{}).sort().forEach(name=>dbSel.append(new Option(name,name)));
  userSel.replaceChildren();Object.keys(p.users||{}).sort().forEach(name=>userSel.append(new Option(name,name)));
  if(!dbSel.options.length||!userSel.options.length){const msg=get('database-properties-message');if(msg){msg.dataset.sticky='1';msg.textContent='Create a logical database and user under Instances before connecting.';}return;}
  get('database-connect-password').value='';get('database-connect-name').value='';get('database-connect-message').textContent='';get('database-connect-result').hidden=true;get('database-connect-result').textContent='';
  syncConnectTargetFields();
  try{
   const clusters=await api('GET','/api/v1/kubernetes/clusters').catch(()=>({clusters:[]}));
   const clusterSel=get('database-connect-cluster');clusterSel.replaceChildren();
   (clusters.clusters||clusters||[]).filter(c=>['RUNNING','READY'].includes(String(c.state||'').toUpperCase())||c.state==='Running').forEach(c=>clusterSel.append(new Option(c.name||c.id,c.id)));
   if(!clusterSel.options.length)(clusters.clusters||[]).forEach(c=>clusterSel.append(new Option(`${c.name||c.id} · ${c.state}`,c.id)));
  }catch{}
  get('database-connect-dialog').showModal();
 }
 function syncConnectTargetFields(){
  const target=get('database-connect-target').value;
  get('database-connect-cluster-field').hidden=target!=='cks';
  get('database-connect-ns-field').hidden=target!=='cks';
  get('database-connect-cluster').required=target==='cks';
 }
 if(get('database-connect-open'))get('database-connect-open').onclick=()=>openConnectDialog();
 if(get('database-connect-target'))get('database-connect-target').onchange=syncConnectTargetFields;
 if(get('database-connect-form'))get('database-connect-form').onsubmit=async event=>{
  event.preventDefault();
  const p=propertiesProject();if(!p)return;
  const msg=get('database-connect-message'),result=get('database-connect-result'),btn=event.submitter||get('database-connect-form').querySelector('[type=submit]');
  msg.textContent='Connecting…';result.hidden=true;
  if(btn)btn.disabled=true;
  try{
   const payload={
    target:get('database-connect-target').value,
    database:get('database-connect-database').value,
    username:get('database-connect-user').value,
    password:get('database-connect-password').value,
    name:get('database-connect-name').value.trim()||undefined,
    cluster_id:get('database-connect-cluster').value||undefined,
    namespace:get('database-connect-ns').value.trim()||'default'
   };
   const out=await api('POST',`/api/databases/projects/${encodeURIComponent(p.spec.id)}/connect`,payload);
   msg.textContent=out.usage||out.note||'Connected.';
   if(out.env_file){result.hidden=false;result.textContent=out.env_file;}
   else if(out.secret){result.hidden=false;result.textContent=JSON.stringify({secret_id:out.secret.id,secret_name:out.secret.name,usage:out.usage},null,2);}
   if(out.job_id)watch({job_id:out.job_id});
   const sticky=get('database-properties-message');if(sticky){sticky.dataset.sticky='1';sticky.textContent=msg.textContent;}
  }catch(error){msg.textContent=error.message;}
  finally{if(btn)btn.disabled=false;}
 };
 get('database-explorer-new-table').onclick=()=>{const p=explorerProject();if(!p||!explorer.database){explorerMsg('Pick an instance and database first.');return;}selected=p.spec.id;openTable(explorer.database);};
 if(get('database-explorer-add-column'))get('database-explorer-add-column').onclick=()=>{if(explorer.busy||!explorer.table){explorerMsg('Select a table first.');return;}openColumnEditor('add');};
 get('database-explorer-drop-table').onclick=async()=>{if(!explorer.table||explorer.busy)return;if(await window.cfPrompt(`Type DELETE to drop ${explorer.table}`,'',{title:'Drop table',label:'Confirmation',confirm:'Delete',hint:'Type DELETE exactly.'})!=='DELETE')return;setExplorerBusy(true,`Dropping ${explorer.table}…`);try{await runExplorer({action:'delete-table',database:explorer.database,table:explorer.table,confirm:'DELETE'});explorer.table='';setExplorerBusy(false);await loadExplorerTables();explorerMsg('Table dropped.');}catch(error){explorerMsg(error.message);setExplorerBusy(false,error.message);}};
 get('database-explorer-prev').onclick=()=>{if(explorer.busy)return;explorer.offset=Math.max(0,explorer.offset-explorer.limit);loadExplorerBrowse();};
 get('database-explorer-next').onclick=()=>{if(explorer.busy)return;explorer.offset+=explorer.limit;loadExplorerBrowse();};
 get('database-explorer-run').onclick=()=>runExplorerSQL();
 if(get('database-explorer-insert'))get('database-explorer-insert').onclick=async()=>{if(explorer.busy||!explorer.table)return;try{await ensureExplorerColumns();openRowEditor('insert');}catch(error){explorerMsg(error.message);}};
 if(get('database-explorer-sql-upload'))get('database-explorer-sql-upload').onclick=()=>get('database-explorer-sql-file')?.click();
 if(get('database-explorer-sql-file'))get('database-explorer-sql-file').onchange=async event=>{const file=event.target.files?.[0];event.target.value='';if(file)await uploadExplorerSQL(file);};
 if(get('database-row-form'))get('database-row-form').onsubmit=submitRowEditor;
 document.querySelectorAll('[data-dbx-tab]').forEach(btn=>btn.onclick=()=>{if(explorer.busy)return;setExplorerTab(btn.dataset.dbxTab);loadExplorerPanel();});
 if(location.hash==='#database-explorer'){syncExplorerSelectors();loadExplorerTables();}
 get('database-inspect-recovery').onclick=async()=>{const p=project(),job=p&&state.jobs.find(j=>j.project_id===p.spec.id&&j.action==='backup'&&(['INTERRUPTED','CANCELLED'].includes(j.state)||j.state==='FAILED'&&j.error?.includes('database project requires inspection after interrupted work')));if(!p||!job)return;try{watch(await api('POST',`/api/databases/projects/${selected}/recover-backup`,{job_id:job.id}));message('Checking database health and inventory before clearing the interrupted backup fence…');await refresh();}catch(error){message(error.message);}};
 get('database-upload-backup').onclick=()=>{if(!project()){message('Select a destination database instance first.');return;}get('database-upload-file').click();};get('database-upload-file').onchange=async event=>{const file=event.target.files[0];event.target.value='';if(!file)return;try{await uploadBackup(file);}catch(error){message(error.message);}};
 get('database-download-ca').onclick=async()=>{const p=project();if(!p)return;const response=await fetch(`/api/databases/projects/${encodeURIComponent(selected)}/ca.pem`,{credentials:'include'});if(!response.ok)throw Error('TLS CA download failed.');const link=document.createElement('a');link.href=URL.createObjectURL(await response.blob());link.download=`${selected}-ca.pem`;link.click();setTimeout(()=>URL.revokeObjectURL(link.href),1000);};
 get('database-delete-project').onclick=async()=>{const p=project();if(!p)return;if(await window.cfPrompt(`Type DELETE to remove ${p.name} and its database disk`,'',{title:'Delete database instance',label:'Confirmation',confirm:'Delete',hint:'Type DELETE exactly.'})!=='DELETE')return;await api('DELETE',`/api/databases/projects/${selected}`,{confirm:'DELETE'});message('Instance deletion queued.');await refresh();};
 get('database-project-form').onsubmit=async event=>{event.preventDefault();const form=event.currentTarget,finish=beginFormProgress(form,form.querySelector('[type=submit]'),'Creating…');try{watch(await api('POST','/api/databases/projects',{name:get('database-project-name').value.trim(),worker_id:get('database-project-worker').value,vpc_id:get('database-project-vpc')?.value||undefined,spec:{engine:get('database-project-engine').value,version:get('database-project-version').value,ram_mb:Number(get('database-project-ram').value),cpu:Number(get('database-project-cpu').value),disk_mb:Number(get('database-project-disk').value)}}));get('database-project-dialog').close();message('Provisioning request accepted…');await refresh();}catch(error){message(error.message);}finally{finish();}};
 get('database-operation-form').onsubmit=async event=>{event.preventDefault();const form=event.currentTarget,button=form.querySelector('[type=submit]'),finish=beginFormProgress(form,button,'Submitting…');try{const out=await operation();watch(out);get('database-operation-dialog').close();if(out&&out.ok){message('');}else{message('Database request accepted…');await refresh();}}catch(error){get('database-operation-message').textContent=error.message;}finally{finish();}};
 document.querySelectorAll('.dialog-close').forEach(button=>button.onclick=()=>button.closest('dialog').close());get('tok')?.addEventListener('change',refresh);refresh();setInterval(()=>{if(!document.hidden&&!explorer.busy)refresh();},8000);
})();
