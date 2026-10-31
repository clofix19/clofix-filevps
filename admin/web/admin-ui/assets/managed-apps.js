(() => {
 if(document.body.dataset.page!=='services')return;
 let templates=[],selected=null,busy=false,activeLogs=null;
 const button=(text,fn)=>{const b=document.createElement('button');b.type='button';b.className='small-btn';b.textContent=text;b.onclick=fn;return b;};
 const message=$('managed-message');
 function showInfo(title,text){$('managed-info-title').textContent=title;$('managed-info-content').textContent=text;$('managed-info-dialog').showModal();}
 async function control(a,action){try{await api('POST',`/api/managed-apps/${a.id}/${action}`,{});await refreshManaged();}catch(e){message.textContent=e.message;}}
 async function refreshManaged(){
  if(busy||!$('tok').value.trim())return;busy=true;
  try{
   const [catalog,apps]=await Promise.all([api('GET','/api/managed-apps/templates'),api('GET','/api/managed-apps')]);templates=catalog;
   const table=$('managed-templates-table');table.replaceChildren();row(table,['Application','Description','Availability','Action'],true);
   for(const template of templates){const deploy=button('Deploy',()=>{selected=template;$('managed-deploy-title').textContent='Deploy '+template.name;$('managed-name').value=template.name;$('managed-ram').value=template.ram_mb;$('managed-deploy-error').textContent='';$('managed-deploy-dialog').showModal();});deploy.disabled=!template.ready_workers;const name=document.createElement('span');name.className='service-name';name.append(serviceBrandIcon(template.id),document.createTextNode(template.name));row(table,[name,template.description,template.ready_workers?`${template.ready_workers} ready worker(s)`:'No ready worker',deploy]);}
   const list=$('managed-apps-table');list.replaceChildren();row(list,['Name','Application','State','URL','Actions'],true);
   for(const a of apps){const actions=document.createElement('div');actions.className='row-actions';const waiting=['DEPLOYING','STARTING','STOPPING','RESTARTING','DELETING'].includes(a.state);
    const open=document.createElement('a');open.className='small-btn';open.href=a.url;open.target='_blank';open.rel='noopener noreferrer';open.textContent='Open app';actions.append(open);
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
 $('managed-deploy-form').onsubmit=async event=>{event.preventDefault();if(!selected)return;const b=event.submitter;b.disabled=true;try{await api('POST','/api/managed-apps',{name:$('managed-name').value.trim(),template:selected.id,ram_mb:+$('managed-ram').value,cpu:+$('managed-cpu').value});$('managed-deploy-dialog').close();message.textContent='Deployment queued.';await refreshManaged();}catch(e){$('managed-deploy-error').textContent=e.message;}finally{b.disabled=false;}};
 document.querySelectorAll('[data-managed-close]').forEach(b=>b.onclick=()=>b.closest('dialog').close());$('managed-info-dialog').addEventListener('close',()=>{activeLogs=null;$('managed-info-content').textContent='';});$('save').addEventListener('click',refreshManaged);setInterval(refreshManaged,5000);refreshManaged();
})();
