(()=>{
 if(!['efs','details'].includes(page))return;
 let shares=[],path='',editing='',moving='',movingFolder=false,busy=false;
 const message=text=>{if($('efs-message'))$('efs-message').textContent=text||'';};
 const child=name=>path?path.replace(/\/$/,'')+'/'+name:name;
 function fill(id,items,label,keep){
  const sel=$(id);if(!sel)return;
  const prev=keep||sel.value;
  sel.replaceChildren(new Option(label,''));
  (items||[]).forEach(item=>sel.add(new Option(item.label,item.id)));
  if(prev&&[...sel.options].some(o=>o.value===prev))sel.value=prev;
 }
 function renderInventory(){
  const table=$('efs-table');if(!table)return;
  const atts=shares.reduce((n,s)=>n+(s.attachments||[]).length,0);
  if($('efs-count'))$('efs-count').textContent=String(shares.length);
  if($('efs-attached'))$('efs-attached').textContent=String(atts);
  row(table,['Name','ID','Prefix','Attachments','Actions'],true);
  if(!shares.length){row(table,['No EFS shares yet. Create one to share files across VPS and workers.','','','','']);return;}
  shares.forEach(share=>{
   const actions=document.createElement('div');actions.className='row-actions';
   const attach=document.createElement('button');attach.type='button';attach.className='small-btn';attach.textContent='Attach';
   attach.onclick=()=>openAttach(share.id);
   const rename=document.createElement('button');rename.type='button';rename.className='small-btn';rename.textContent='Rename';
   rename.onclick=()=>openEdit(share.id);
   actions.append(attach,rename);
   if(!(share.attachments||[]).length){
    const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';
    del.onclick=async()=>{if(!window.confirm(`Delete share ${share.name}? Objects in the prefix are not deleted.`))return;try{await api('DELETE','/api/efs/'+share.id);message('Share deleted.');await refresh();}catch(e){message(e.message);}};
    actions.append(del);
   }
   const list=document.createElement('div');
   if(!(share.attachments||[]).length)list.textContent='—';
   else (share.attachments||[]).forEach(a=>{
    const line=document.createElement('div');
    const link=document.createElement('a');link.href='/instances/'+encodeURIComponent(a.instance_id);link.textContent=a.instance_id;
    line.append(link,document.createTextNode(' · '+(a.guest_path||'')+' · '+(a.worker_id||'worker')));
    const detach=document.createElement('button');detach.type='button';detach.className='danger small-btn';detach.textContent='Detach';
    detach.onclick=async()=>{try{await api('POST','/api/efs/'+share.id+'/detach',{instance_id:a.instance_id});message('Detach queued.');await refresh();}catch(e){message(e.message);}};
    line.append(detach);
    list.append(line);
   });
   row(table,[share.name,share.id,share.prefix||'',list,actions]);
  });
  fill('efs-edit-select',shares.map(s=>({id:s.id,label:s.name})),'Select a share');
  fill('efs-attach-select',shares.map(s=>({id:s.id,label:s.name})),'Select a share');
  fill('efs-explorer-select',shares.map(s=>({id:s.id,label:s.name})),'Select a share');
  fill('efs-attach-vps',(instances||[]).map(i=>({id:i.id,label:i.id+' · '+(i.worker_id||'')+' · '+(i.state||'')})),'Select a VPS');
 }
 function renderDetails(){
  const table=$('detail-efs');if(!table||!selectedID)return;
  if($('detail-open-efs'))$('detail-open-efs').href='/efs';
  row(table,['Share','Guest path','Prefix'],true);
  const mine=shares.filter(s=>(s.attachments||[]).some(a=>a.instance_id===selectedID));
  if(!mine.length){row(table,['No EFS attachments','','']);return;}
  mine.forEach(s=>{
   const a=(s.attachments||[]).find(x=>x.instance_id===selectedID);
   const link=document.createElement('a');link.href='/efs';link.textContent=s.name;
   row(table,[link,a?.guest_path||'—',s.prefix||'']);
  });
 }
 async function refreshFiles(){
  const id=$('efs-explorer-select')?.value,table=$('efs-explorer-table'),msg=$('efs-explorer-message');
  if(!id){if(table)row(table,['Select a share to browse files.','','','',''],true);if($('efs-explorer-location'))$('efs-explorer-location').textContent='/';return;}
  if(msg)msg.textContent='Loading share contents…';
  try{
   const data=await api('GET','/api/efs/'+id+'/files?path='+encodeURIComponent(path));
   row(table,['Name','Type','Size','Modified','Actions'],true);
   if(!data.entries?.length)row(table,['This folder is empty. Upload a file or create one here.','','','','']);
   (data.entries||[]).forEach(entry=>{
    const name=document.createElement(entry.folder?'button':'span');
    if(entry.folder){name.className='id-link';name.textContent=entry.name+'/';name.onclick=()=>{path=entry.key||entry.name;refreshFiles();};}
    else name.textContent=entry.name;
    const actions=document.createElement('div');actions.className='row-actions';
    const rel=entry.key||entry.name;
    const dl=document.createElement('button');dl.className='small-btn';dl.textContent=entry.folder?'Download zip':'Download';
    dl.onclick=()=>entry.folder?downloadFolder(id,rel):downloadFile(id,rel);actions.append(dl);
    if(!entry.folder&&/\.(txt|html?|css|js|json|xml|md|php|ya?ml|svg|conf|ini|sh)$/i.test(entry.name)){
     const edit=document.createElement('button');edit.className='small-btn';edit.textContent='Edit';
     edit.onclick=()=>openEditor(id,rel);actions.append(edit);
    }
    const mv=document.createElement('button');mv.className='small-btn';mv.textContent='Move';
    mv.onclick=()=>openMove(rel,!!entry.folder);actions.append(mv);
    const del=document.createElement('button');del.className='danger small-btn';del.textContent=entry.folder?'Delete folder':'Delete';
    del.onclick=async()=>{if(!window.confirm(entry.folder?`Delete folder ${rel}?`:`Delete ${rel}?`))return;try{await api('DELETE','/api/efs/'+id+'/file?path='+encodeURIComponent(rel));await refreshFiles();}catch(e){if(msg)msg.textContent=e.message;}};
    actions.append(del);
    row(table,[name,entry.folder?'Folder':'File',entry.folder?'—':fmtBytes(entry.size||0),entry.modified?new Date(entry.modified).toLocaleString():'—',actions]);
   });
   if($('efs-explorer-location'))$('efs-explorer-location').textContent='/'+(path||'');
   if(msg)msg.textContent='';
  }catch(e){if(msg)msg.textContent=e.message;if(table)row(table,['Could not load share contents.','','','',''],true);}
 }
 async function downloadBlob(url,filename){
  const r=await fetch(url,{credentials:'include'});
  if(!r.ok)throw new Error((await r.json().catch(()=>({}))).error||r.status);
  const blob=await r.blob(),href=URL.createObjectURL(blob),a=document.createElement('a');
  a.href=href;a.download=filename;a.click();setTimeout(()=>URL.revokeObjectURL(href),1000);
 }
 async function downloadFile(id,rel){
  try{await downloadBlob('/api/efs/'+id+'/file?path='+encodeURIComponent(rel),rel.split('/').filter(Boolean).pop()||'file');}
  catch(e){if($('efs-explorer-message'))$('efs-explorer-message').textContent=e.message;}
 }
 async function downloadFolder(id,rel){
  const msg=$('efs-explorer-message');
  try{
   if(msg)msg.textContent='Preparing zip…';
   const name=(rel.split('/').filter(Boolean).pop()||'efs')+'.zip';
   await downloadBlob('/api/efs/'+id+'/archive?path='+encodeURIComponent(rel||''),name);
   if(msg)msg.textContent='';
  }catch(e){if(msg)msg.textContent=e.message;}
 }
 function openMove(rel,folder){
  moving=rel;movingFolder=!!folder;
  $('efs-move-from').textContent='/'+rel+(folder?'/':'');
  $('efs-move-to').value=rel+(folder?'/':'');
  $('efs-move-message').textContent='';
  $('efs-move-dialog').showModal();
 }
 async function uploadItems(items,directories=[]){
  const id=$('efs-explorer-select').value,msg=$('efs-explorer-message');
  if(!id){if(msg)msg.textContent='Select a share before uploading.';return;}
  if(!items.length&&!directories.length){if(msg)msg.textContent='No files or folders were found.';return;}
  try{
   const dirs=new Set(directories);
   for(const item of items){
    const relative=item.relativePath||item.file.webkitRelativePath||item.file.name,parts=relative.split('/');
    if(parts.some(part=>!part||part==='.'||part==='..'))throw new Error('Invalid folder path for '+item.file.name+'.');
    if(item.file.size>4*1024*1024)throw new Error(relative+' exceeds the 4 MB upload limit.');
    for(let i=1;i<parts.length;i++)dirs.add(parts.slice(0,i).join('/'));
   }
   const ordered=[...dirs].sort((a,b)=>a.split('/').length-b.split('/').length||a.localeCompare(b));
   for(let i=0;i<ordered.length;i++){
    if(msg)msg.textContent='Creating folder '+(i+1)+' of '+ordered.length+': '+ordered[i];
    await api('POST','/api/efs/'+id+'/folder',{path:child(ordered[i])});
   }
   for(let i=0;i<items.length;i++){
    const file=items[i].file,relative=items[i].relativePath||file.webkitRelativePath||file.name;
    if(msg)msg.textContent='Uploading '+(i+1)+' of '+items.length+': '+relative;
    await apiRaw('PUT','/api/efs/'+id+'/file?path='+encodeURIComponent(child(relative)),file,file.type||'application/octet-stream');
   }
   if(msg)msg.textContent=items.length?'Uploaded '+items.length+' file'+(items.length===1?'':'s')+(ordered.length?' and created/checked '+ordered.length+' folder'+(ordered.length===1?'':'s'):'')+'.':'Created '+ordered.length+' folder'+(ordered.length===1?'':'s')+'.';
   await refreshFiles();
  }catch(e){if(msg)msg.textContent=e.message;}
 }
 async function handleDrop(event){
  try{
   const dropped=await collectDroppedVolumeItems(event.dataTransfer);
   await uploadItems(dropped.files,dropped.directories);
  }catch(e){if($('efs-explorer-message'))$('efs-explorer-message').textContent=e.message||'Could not read the dropped files or folders.';}
 }
 async function openEditor(id,rel){
  try{
   const data=await api('GET','/api/efs/'+id+'/file?path='+encodeURIComponent(rel)+'&text=1');
   editing=rel;$('efs-editor-title').textContent=rel;$('efs-editor-content').value=data.content;$('efs-editor-message').textContent='';$('efs-editor-dialog').showModal();
  }catch(e){if($('efs-explorer-message'))$('efs-explorer-message').textContent=e.message;}
 }
 async function refresh(){
  if(busy||!T&&!$('tok')?.value.trim())return;busy=true;
  try{
   const [data,vps]=await Promise.all([api('GET','/api/efs'),page==='efs'?api('GET','/api/instances'):Promise.resolve(instances||[])]);
   shares=data.shares||[];
   if(page==='efs')instances=vps||[];
   if($('auth-banner'))$('auth-banner').hidden=true;
   if(page==='efs'){renderInventory();await refreshFiles();}
   renderDetails();
  }catch(e){message(e.message);}
  finally{busy=false;}
 }
 function openCreate(){$('efs-create-name').value='';$('efs-create-message').textContent='';$('efs-create-dialog').showModal();}
 function openEdit(id){fill('efs-edit-select',shares.map(s=>({id:s.id,label:s.name})),'Select a share',id);const share=shares.find(s=>s.id===$('efs-edit-select').value);$('efs-edit-name').value=share?.name||'';$('efs-edit-message').textContent='';$('efs-edit-dialog').showModal();}
 function openAttach(id){fill('efs-attach-select',shares.map(s=>({id:s.id,label:s.name})),'Select a share',id);fill('efs-attach-vps',(instances||[]).map(i=>({id:i.id,label:i.id+' · '+(i.worker_id||'')})),'Select a VPS');if(!$('efs-attach-path').value)$('efs-attach-path').value='/var/www/html';$('efs-attach-message').textContent='';$('efs-attach-dialog').showModal();}
 if(page==='efs'){
  $('efs-create-open').onclick=openCreate;$('efs-create-close').onclick=()=>$('efs-create-dialog').close();
  $('efs-edit-open').onclick=()=>openEdit();$('efs-edit-close').onclick=()=>$('efs-edit-dialog').close();
  $('efs-attach-open').onclick=()=>openAttach();$('efs-attach-close').onclick=()=>$('efs-attach-dialog').close();
  $('efs-refresh').onclick=refresh;
  $('efs-create').onclick=async()=>{const name=$('efs-create-name').value.trim(),msg=$('efs-create-message');if(!name){msg.textContent='Enter a share name.';return;}try{const created=await api('POST','/api/efs',{name});msg.textContent='Created '+created.id+'.';$('efs-create-name').value='';await refresh();}catch(e){msg.textContent=e.message;}};
  $('efs-edit').onclick=async()=>{const id=$('efs-edit-select').value,name=$('efs-edit-name').value.trim(),msg=$('efs-edit-message');if(!id||!name){msg.textContent='Select a share and enter a name.';return;}try{await api('PUT','/api/efs/'+id,{name});msg.textContent='Share renamed.';await refresh();}catch(e){msg.textContent=e.message;}};
  $('efs-attach').onclick=async()=>{const id=$('efs-attach-select').value,instance_id=$('efs-attach-vps').value,guest_path=$('efs-attach-path').value.trim(),msg=$('efs-attach-message');if(!id||!instance_id||!guest_path){msg.textContent='Select a share and VPS, then enter a guest path.';return;}try{const result=await api('POST','/api/efs/'+id+'/attach',{instance_id,guest_path});msg.textContent=result.restart_required?'Attach queued; the VPS will restart.':'Attach queued.';await refresh();}catch(e){msg.textContent=e.message;}};
  $('efs-explorer-select').onchange=()=>{path='';refreshFiles();};
  $('efs-explorer-up').onclick=()=>{path=path.replace(/\/?[^/]+\/?$/,'');refreshFiles();};
  $('efs-explorer-refresh').onclick=refreshFiles;
  $('efs-explorer-folder').onclick=async()=>{const id=$('efs-explorer-select').value,name=window.prompt('New folder name');if(!id||!name)return;try{await api('POST','/api/efs/'+id+'/folder',{path:child(name.trim())});await refreshFiles();}catch(e){$('efs-explorer-message').textContent=e.message;}};
  $('efs-explorer-file').onclick=async()=>{const id=$('efs-explorer-select').value,name=window.prompt('New text file name');if(!id||!name)return;const rel=child(name.trim());try{await apiRaw('PUT','/api/efs/'+id+'/file?path='+encodeURIComponent(rel),new Blob([''],{type:'text/plain'}),'text/plain');await refreshFiles();openEditor(id,rel);}catch(e){$('efs-explorer-message').textContent=e.message;}};
  $('efs-explorer-download').onclick=()=>{const id=$('efs-explorer-select').value;if(!id){$('efs-explorer-message').textContent='Select a share first.';return;}downloadFolder(id,path);};
  $('efs-move-close').onclick=()=>$('efs-move-dialog').close();
  $('efs-move').onclick=async()=>{const id=$('efs-explorer-select').value,to=$('efs-move-to').value.trim(),msg=$('efs-move-message');if(!id||!moving||!to){msg.textContent='Enter a destination path.';return;}try{await api('POST','/api/efs/'+id+'/move',{from:moving,to,folder:movingFolder});msg.textContent='Moved.';$('efs-move-dialog').close();await refreshFiles();}catch(e){msg.textContent=e.message;}};
  $('efs-explorer-upload').onclick=()=>$('efs-explorer-input').click();
  $('efs-explorer-upload-folder').onclick=()=>$('efs-explorer-folder-input').click();
  $('efs-explorer-input').onchange=async()=>{try{await uploadItems([...($('efs-explorer-input').files||[])].map(file=>({file,relativePath:file.name})));}finally{$('efs-explorer-input').value='';}};
  $('efs-explorer-folder-input').onchange=async()=>{try{await uploadItems([...($('efs-explorer-folder-input').files||[])].map(file=>({file,relativePath:file.webkitRelativePath||file.name})));}finally{$('efs-explorer-folder-input').value='';}};
  const drop=$('efs-drop-zone');
  if(drop){
   drop.addEventListener('dragenter',e=>{e.preventDefault();drop.classList.add('is-dragover');});
   drop.addEventListener('dragover',e=>{e.preventDefault();e.dataTransfer.dropEffect='copy';drop.classList.add('is-dragover');});
   drop.addEventListener('dragleave',e=>{if(!e.relatedTarget||!drop.contains(e.relatedTarget))drop.classList.remove('is-dragover');});
   drop.addEventListener('drop',e=>{e.preventDefault();drop.classList.remove('is-dragover');handleDrop(e);});
  }
  $('efs-editor-close').onclick=()=>$('efs-editor-dialog').close();
  $('efs-editor-save').onclick=async()=>{const id=$('efs-explorer-select').value,msg=$('efs-editor-message');try{await apiRaw('PUT','/api/efs/'+id+'/file?path='+encodeURIComponent(editing),new Blob([$('efs-editor-content').value],{type:'text/plain; charset=utf-8'}),'text/plain; charset=utf-8');msg.textContent='File saved.';await refreshFiles();}catch(e){msg.textContent=e.message;}};
  $('save')?.addEventListener('click',refresh);$('tok')?.addEventListener('change',refresh);
  setInterval(()=>{if(!document.hidden)refresh();},10000);
 }
 refresh();
})();
