(()=>{
 if(page!=='secrets')return;
 let secrets=[], clusters=[];
 const message=text=>{const el=$('secrets-page-message');if(el)el.textContent=text||'';};
 const formMessage=text=>{const el=$('secrets-form-message');if(el)el.textContent=text||'';};
 const parseKV=text=>{
  const out={};
  String(text||'').split(/\r?\n/).forEach(line=>{
   const trimmed=line.trim();if(!trimmed||trimmed.startsWith('#'))return;
   const i=trimmed.indexOf('=');if(i<=0)return;
   out[trimmed.slice(0,i).trim()]=trimmed.slice(i+1);
  });
  return out;
 };
 function syncTypeFields(){
  const kv=$('secrets-form-type').value==='key_value';
  $('secrets-form-value-wrap').hidden=kv;
  $('secrets-form-values-wrap').hidden=!kv;
 }
 function openCreate(){
  $('secrets-form-id').value='';
  $('secrets-form-title').textContent='New secret';
  $('secrets-form-save').textContent='Create secret';
  $('secrets-form-name').value='';
  $('secrets-form-name').readOnly=false;
  $('secrets-form-type').value='string';
  $('secrets-form-type').disabled=false;
  $('secrets-form-description').value='';
  $('secrets-form-value').value='';
  $('secrets-form-values').value='';
  $('secrets-form-edit-hint').hidden=true;
  formMessage('');
  syncTypeFields();
  $('secrets-form-dialog').showModal();
 }
 function openEdit(sec){
  $('secrets-form-id').value=sec.id;
  $('secrets-form-title').textContent='Edit secret';
  $('secrets-form-save').textContent='Save changes';
  $('secrets-form-name').value=sec.name||'';
  $('secrets-form-type').value=sec.type||'string';
  $('secrets-form-type').disabled=true;
  $('secrets-form-description').value=sec.description||'';
  $('secrets-form-value').value='';
  $('secrets-form-values').value='';
  $('secrets-form-edit-hint').hidden=false;
  formMessage('');
  syncTypeFields();
  $('secrets-form-dialog').showModal();
 }
 async function openSync(sec){
  $('secrets-sync-id').value=sec.id;
  $('secrets-sync-namespace').value='default';
  $('secrets-sync-name').value=(sec.name||'secret').toLowerCase().replace(/[^a-z0-9-]+/g,'-').replace(/^-|-$/g,'')||'secret';
  $('secrets-sync-message').textContent='';
  const sel=$('secrets-sync-cluster');
  sel.replaceChildren(new Option('Select a cluster',''));
  try{
   if(!clusters.length){
    const data=await api('GET','/api/v1/kubernetes/clusters');
    clusters=data.clusters||data||[];
    if(!Array.isArray(clusters))clusters=[];
   }
   clusters.forEach(c=>{
    const id=c.id||c.ID;const name=c.name||id;
    if(id)sel.add(new Option(name+' · '+(c.state||''),id));
   });
  }catch(e){message(e.message);}
  $('secrets-sync-dialog').showModal();
 }
 function hideReveal(){
  const panel=$('secrets-reveal-panel');
  if(panel)panel.hidden=true;
  if($('secrets-reveal-value'))$('secrets-reveal-value').textContent='';
 }
 function showReveal(name,payload){
  $('secrets-reveal-name').textContent=name||'';
  if(payload.values){
   $('secrets-reveal-value').textContent=Object.entries(payload.values).map(([k,v])=>k+'='+v).join('\n');
  }else{
   $('secrets-reveal-value').textContent=payload.value||'';
  }
  $('secrets-reveal-panel').hidden=false;
 }
 function render(){
  const table=$('secrets-table');if(!table)return;
  row(table,['Name','ID','Type','Version','Updated','Actions'],true);
  if(!secrets.length){row(table,['No secrets yet. Create one to inject into Jobs, Docker, or CKS.','','','','','']);return;}
  secrets.forEach(sec=>{
   const actions=document.createElement('div');actions.className='row-actions';
   const reveal=document.createElement('button');reveal.type='button';reveal.className='small-btn';reveal.textContent='Reveal';
   reveal.onclick=async()=>{try{const data=await api('POST','/api/secrets/'+sec.id+'/reveal',{});showReveal(sec.name,data);message('Secret revealed (audited).');}catch(e){message(e.message);}};
   const edit=document.createElement('button');edit.type='button';edit.className='small-btn';edit.textContent='Edit';
   edit.onclick=()=>openEdit(sec);
   const sync=document.createElement('button');sync.type='button';sync.className='small-btn';sync.textContent='Sync to CKS';
   sync.onclick=()=>openSync(sec);
   const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';
   del.onclick=async()=>{
    if(await window.cfPrompt('Type DELETE to remove this secret', '', {
      title: 'Delete secret', label: 'Confirmation', confirm: 'Delete', hint: 'Type DELETE exactly. Runtime refs and CKS syncs that use this secret will fail.'
    })!=='DELETE')return;
    try{await api('DELETE','/api/secrets/'+sec.id);message('Secret deleted.');hideReveal();await refreshSecrets();}catch(e){message(e.message);}
   };
   actions.append(reveal,edit,sync,del);
   row(table,[sec.name,sec.id,sec.type||'string',String(sec.version||1),sec.updated?new Date(sec.updated).toLocaleString():'-',actions]);
  });
 }
 async function refreshSecrets(){
  try{
   const data=await api('GET','/api/secrets');
   secrets=data.secrets||[];
   render();
  }catch(e){message(e.message);}
 }
 $('secrets-create-open').onclick=openCreate;
 $('secrets-refresh').onclick=async()=>{await refreshSecrets();message('Secrets refreshed.');};
 $('secrets-form-type').onchange=syncTypeFields;
 $('secrets-form-close').onclick=()=>$('secrets-form-dialog').close();
 $('secrets-sync-close').onclick=()=>$('secrets-sync-dialog').close();
 $('secrets-reveal-dismiss').onclick=hideReveal;
 $('secrets-reveal-copy').onclick=async()=>{
  try{await navigator.clipboard.writeText($('secrets-reveal-value').textContent||'');message('Copied.');}catch(e){message(e.message||'Copy failed');}
 };
 $('secrets-form').onsubmit=async event=>{
  event.preventDefault();
  const id=$('secrets-form-id').value;
  const type=$('secrets-form-type').value;
  const body={name:$('secrets-form-name').value.trim(),description:$('secrets-form-description').value.trim(),type};
  if(type==='key_value'){
   const values=parseKV($('secrets-form-values').value);
   if(!id&&!Object.keys(values).length){formMessage('Enter at least one KEY=value line.');return;}
   if(Object.keys(values).length)body.values=values;
  }else{
   const value=$('secrets-form-value').value;
   if(!id&&!value){formMessage('Value is required.');return;}
   if(id){if(value)body.value=value;}else body.value=value;
  }
  try{
   const result=await api(id?'PUT':'POST',id?'/api/secrets/'+id:'/api/secrets',body);
   $('secrets-form-dialog').close();
   if(!id&&(result.value||result.values)){
    showReveal(result.name||body.name,result);
    message(result.warning||'Secret created. Copy the value now.');
   }else message(id?'Secret updated.':'Secret created.');
   await refreshSecrets();
  }catch(e){formMessage(e.message);}
 };
 $('secrets-sync-form').onsubmit=async event=>{
  event.preventDefault();
  const id=$('secrets-sync-id').value;
  const body={
   cluster_id:$('secrets-sync-cluster').value,
   namespace:$('secrets-sync-namespace').value.trim()||'default',
   name:$('secrets-sync-name').value.trim()
  };
  try{
   await api('POST','/api/secrets/'+id+'/sync-cks',body);
   $('secrets-sync-dialog').close();
   message('CKS Secret sync queued for '+body.namespace+'/'+body.name+'.');
   await refreshSecrets();
  }catch(e){$('secrets-sync-message').textContent=e.message;}
 };
 function fillUsageExamples(){
  const base=location.origin||'http://localhost:8080';
  const blocks=[
   ['secrets-ex-filevps-name',`export CLOFIX_IAM_BEARER='CFX….secret'\ncurl -sS -H "Authorization: Bearer $CLOFIX_IAM_BEARER" \\\n  ${base}/api/secrets/by-name/db-password/value`],
   ['secrets-ex-filevps-id',`curl -sS -H "Authorization: Bearer $CLOFIX_IAM_BEARER" \\\n  ${base}/api/secrets/sec-a1b2c3d4e5f60708/value`],
   ['secrets-ex-docker-curl',`curl -sS -H "Authorization: Bearer $CLOFIX_IAM_BEARER" \\\n  ${base}/api/secrets/by-name/db-password/value`],
   ['secrets-ex-cks-curl',`curl -sS -H "Authorization: Bearer $CLOFIX_IAM_BEARER" \\\n  ${base}/api/secrets/by-name/db-password/value`]
  ];
  blocks.forEach(([id,text])=>{const el=$(id);if(el)el.textContent=text;});
 }
 fillUsageExamples();
 refreshSecrets();
 window.refreshSecrets=refreshSecrets;
})();
