(() => {
 if(document.body.dataset.page!=='services')return;
 let templates=[],selected=null,busy=false,activeLogs=null,dbProjects=[];
 const button=(text,fn)=>{const b=document.createElement('button');b.type='button';b.className='small-btn';b.textContent=text;b.onclick=fn;return b;};
 const message=$('managed-message');
 function showInfo(title,text){$('managed-info-title').textContent=title;$('managed-info-content').textContent=text;$('managed-info-dialog').showModal();}
 async function control(a,action){try{await api('POST',`/api/managed-apps/${a.id}/${action}`,{});await refreshManaged();}catch(e){message.textContent=e.message;}}
 function selectedProject(){return dbProjects.find(p=>p.spec?.id===$('managed-db-project').value)||null;}
 function fillManagedDatabases(){
  const project=$('managed-db-project'),db=$('managed-db-name'),user=$('managed-db-user'),note=$('managed-db-note');
  if(!project)return;
  const current=project.value;
  project.replaceChildren(new Option('None · configure later',''));
  dbProjects.forEach(p=>project.add(new Option(`${p.name} · ${p.spec?.engine||'db'} ${p.spec?.host||''}:${p.spec?.port||''}`,p.spec.id)));
  if([...project.options].some(o=>o.value===current))project.value=current;
  const p=selectedProject(),dbValue=db.value,userValue=user.value;
  db.replaceChildren(new Option(p?'Select a database':'Select a database instance first',''));
  user.replaceChildren(new Option(p?'Select a user':'Select a database instance first',''));
  if(p){Object.keys(p.databases||{}).sort().forEach(name=>db.add(new Option(name,name)));Object.keys(p.users||{}).sort().forEach(name=>user.add(new Option(name,name)));}
  if([...db.options].some(o=>o.value===dbValue))db.value=dbValue;
  if([...user.options].some(o=>o.value===userValue))user.value=userValue;
  if(note)note.textContent=dbProjects.length?'Uses an existing READY managed database. CloFix injects DB_HOST, DB_PORT, DB_NAME, DB_USER, DB_PASSWORD, and DATABASE_URL into the app.':'No READY managed database is available. Create one under Databases, then return here.';
 }
 async function loadManagedDatabases(){
  dbProjects=[];
  try{const data=await api('GET','/api/databases');dbProjects=(data.projects||[]).filter(p=>p.state==='READY'&&p.spec?.host&&p.spec?.port);}catch(e){dbProjects=[];}
  fillManagedDatabases();
 }
 async function refreshManaged(){
  if(busy||!$('tok').value.trim())return;busy=true;
  try{
   const [catalog,apps]=await Promise.all([api('GET','/api/managed-apps/templates'),api('GET','/api/managed-apps')]);templates=catalog;
   const table=$('managed-templates-table');row(table,['Application','Description','Availability','Action'],true);
   for(const template of templates){const deploy=button('Deploy',()=>{selected=template;const runtime=template.id.startsWith('web-')?template.id.slice(4):'';$('managed-deploy-title').textContent='Deploy '+template.name;$('managed-name').value=template.name;$('managed-ram').value=template.ram_mb;$('managed-web-fields').hidden=!runtime;$('managed-zip').required=!!runtime;$('managed-zip').value='';$('managed-entry').value=runtime==='nodejs'||runtime==='nextjs'?'server.js':'';$('managed-env').value='';$('managed-db-project').value='';$('managed-db-name').value='';$('managed-db-user').value='';$('managed-db-password').value='';$('managed-deploy-error').textContent='';if(runtime)loadManagedDatabases();loadVPCOptions('managed-vpc');$('managed-deploy-dialog').showModal();});deploy.disabled=!template.ready_workers;const name=document.createElement('span');name.className='service-name';name.append(serviceBrandIcon(template.id),document.createTextNode(template.name));row(table,[name,template.description,template.ready_workers?`${template.ready_workers} ready worker(s)`:'No ready worker',deploy]);}
   const list=$('managed-apps-table');row(list,['Name','Application','State','URL','Actions'],true);
   for(const a of apps){const actions=document.createElement('div');actions.className='row-actions';const waiting=['DEPLOYING','STARTING','STOPPING','RESTARTING','DELETING'].includes(a.state);
    const open=document.createElement('a');open.className='small-btn';open.textContent=a.ready?'Open app':'Run app';open.target='_blank';open.rel='noopener noreferrer';if(a.url&&a.ready){open.href=a.url;}else{open.href='#';open.setAttribute('aria-disabled','true');open.onclick=event=>{event.preventDefault();message.textContent=a.error||'The application is not ready to open yet. Use Start if it is stopped.';};}actions.append(open);
    actions.append(button('Login',async()=>{try{const credentials=await api('POST',`/api/managed-apps/${a.id}/credentials`,{});activeLogs=null;showInfo(a.name+' · Login',`URL: ${a.url}\nUsername: ${credentials.username}\nPassword: ${credentials.password}\n\nEnter these credentials in the browser login prompt. App-specific first-time setup may follow.`);}catch(e){message.textContent=e.message;}}));
    const toggle=button(a.desired==='running'?'Stop':'Start',()=>control(a,a.desired==='running'?'stop':'start'));toggle.disabled=waiting;actions.append(toggle);const restart=button('Restart',()=>control(a,'restart'));restart.disabled=waiting;actions.append(restart);
    actions.append(button('Logs',async()=>{try{const logs=await api('GET',`/api/managed-apps/${a.id}/logs`);activeLogs=a.id;showInfo(a.name+' · Logs',logs.logs||logs.error||'Waiting for logs…');}catch(e){message.textContent=e.message;}}));
    const backup=document.createElement('a');backup.className='small-btn';backup.href=a.url+'__clofix/backup';backup.target='_blank';backup.rel='noopener noreferrer';backup.textContent='Data export';backup.title='Stop the app first, then sign in with its generated login to download a ZIP.';actions.append(backup);
    const remove=button('Delete',()=>{if(confirm(`Delete ${a.name} and permanently remove its stored data?`))control(a,'delete');});remove.disabled=waiting;actions.append(remove);
    row(list,[a.name,templates.find(t=>t.id===a.template)?.name||a.template,a.error?`${a.state}: ${a.error}`:a.ready?'Ready':a.state,a.url,actions]);
   }
   if(!apps.length)row(list,['No managed apps deployed yet.','','','','']);
   if(activeLogs&&$('managed-info-dialog').open){const logs=await api('GET',`/api/managed-apps/${activeLogs}/logs`);$('managed-info-content').textContent=logs.logs||logs.error||'Waiting for logs…';}
  }catch(e){message.textContent=e.message;}finally{busy=false;}
 }
 $('managed-deploy-form').onsubmit=async event=>{event.preventDefault();if(!selected)return;const b=event.submitter;b.disabled=true;try{const runtime=selected.id.startsWith('web-')?selected.id.slice(4):'';const payload={name:$('managed-name').value.trim(),template:selected.id,ram_mb:+$('managed-ram').value,cpu:+$('managed-cpu').value,vpc_id:$('managed-vpc')?.value||undefined};if(runtime){const file=$('managed-zip').files[0];if(!file)throw new Error('Choose an application ZIP file.');if(file.size>16*1024*1024)throw new Error('The application ZIP must be 16 MiB or smaller.');payload.runtime=runtime;payload.entry=$('managed-entry').value.trim();payload.artifact=await new Promise((resolve,reject)=>{const r=new FileReader();r.onload=()=>resolve(String(r.result).split(',')[1]);r.onerror=()=>reject(new Error('Could not read the ZIP file.'));r.readAsDataURL(file);});payload.env={};for(const line of $('managed-env').value.split(/\r?\n/)){if(!line.trim())continue;const pos=line.indexOf('=');if(pos<1)throw new Error('Environment settings must use NAME=value.');payload.env[line.slice(0,pos).trim()]=line.slice(pos+1);}const project=$('managed-db-project').value,database=$('managed-db-name').value,user=$('managed-db-user').value,password=$('managed-db-password').value;if(project||database||user||password){if(!project||!database||!user||!password)throw new Error('Select the managed database, logical database, user, and enter that user password.');payload.database_project=project;payload.database=database;payload.database_user=user;payload.database_password=password;}}await api('POST','/api/managed-apps',payload);$('managed-deploy-dialog').close();message.textContent='Deployment queued.';await refreshManaged();}catch(e){$('managed-deploy-error').textContent=e.message;}finally{b.disabled=false;}};
 document.querySelectorAll('[data-managed-close]').forEach(b=>b.onclick=()=>b.closest('dialog').close());$('managed-info-dialog').addEventListener('close',()=>{activeLogs=null;$('managed-info-content').textContent='';});$('managed-db-project')?.addEventListener('change',fillManagedDatabases);$('save')?.addEventListener('click',refreshManaged);setInterval(refreshManaged,5000);refreshManaged();
})();
