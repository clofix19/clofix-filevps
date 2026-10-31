(() => {
 if(document.body.dataset.page!=='applications')return;
 let apps=[],selected=null,editing=null,webhookApp=null,output=null,busy=false;
 const message=$('application-message'),active=state=>['QUEUED','RUNNING'].includes(state);
 const action=(text,fn)=>{const b=document.createElement('button');b.type='button';b.className='small-btn';b.textContent=text;b.onclick=fn;return b;};
 const parseJSON=(id,fallback)=>$(id).value.trim()?JSON.parse($(id).value):fallback;
 function editApplication(a){
  editing=a;$('application-create-form').reset();$('application-create-error').textContent='';$('application-create-title').textContent=a?'New version · '+a.name:'Add application';
  if(a){$('application-name').value=a.name;$('application-kind').value=a.kind;$('application-runtime').value=a.execution.runtime;$('application-format').value=a.execution.format||'file';$('application-entry').value=a.execution.entry||'';$('application-args').value=JSON.stringify(a.execution.args||[]);$('application-env').value=JSON.stringify(a.execution.env||{},null,2);$('application-ram').value=a.execution.ram_mb;$('application-cpu').value=a.execution.cpu;$('application-timeout').value=a.execution.timeout_seconds;$('application-schedule').value=a.schedule||'';$('application-deps').checked=!!a.execution.install_dependencies;}
  $('application-create-dialog').showModal();
 }
 function showVersions(a){
  const table=$('application-versions-table');table.replaceChildren();row(table,['Version','Created','Runtime','Actions'],true);$('application-versions-message').textContent='';
  for(const v of a.versions||[]){row(table,[`v${v.number}${v.number===a.version?' · current':''}`,new Date(v.created).toLocaleString(),v.execution.runtime,action('Restore',async()=>{try{await api('POST',`/api/applications/${a.id}/rollback/${v.number}`,{});$('application-versions-dialog').close();await refreshApps();}catch(e){$('application-versions-message').textContent=e.message;}})]);}
  $('application-versions-dialog').showModal();
 }
 function render(){
  const table=$('applications-table');table.replaceChildren();row(table,['Application','Type','Runtime','Version','Schedule (UTC)','Actions'],true);
  const query=$('application-search').value.toLowerCase(),matches=apps.filter(a=>(a.name+' '+a.kind+' '+a.execution.runtime).toLowerCase().includes(query));
  for(const a of matches){const buttons=document.createElement('div');buttons.className='row-actions';buttons.append(action(a.kind==='function'?'Invoke':'Run',()=>{selected=a;$('application-run-title').textContent=a.name;$('application-invoke-message').textContent='';$('application-endpoint').textContent=a.kind==='function'?`POST /api/functions/${a.id}/invoke · bearer authentication · JSON body: {"input":"..."}`:'Input is passed to the process through stdin.';$('application-run-dialog').showModal();}),action('Edit',()=>editApplication(a)),action('Versions',()=>showVersions(a)),action('Webhook',()=>{webhookApp=a;$('application-webhook-details').textContent=location.origin+'/hooks/applications/'+a.id;$('application-webhook-message').textContent='Create a secret to enable this endpoint. Rotating invalidates the previous secret.';$('application-webhook-dialog').showModal();}),action('Delete',async()=>{if(!confirm(`Delete ${a.name}? Run history is retained.`))return;try{await api('DELETE',`/api/applications/${a.id}`);await refreshApps();}catch(e){message.textContent=e.message;}}));row(table,[a.name,a.kind,a.execution.runtime,'v'+(a.version||1),a.schedule_error?`${a.schedule||''} · ${a.schedule_error}`:a.schedule||'Manual',buttons]);}
  if(!matches.length)row(table,['No applications found.','','','','','']);
 }
 async function updateOutput(id){
  output=await api('GET',`/api/application-runs/${id}`);
  $('application-log-status').textContent=`${output.state} · v${output.version||1}${output.result?` · exit ${output.result.exit_code}`:''}${output.error?' · '+output.error:''}${output.result?.truncated?' · logs truncated':''}${output.result?.output_error?' · '+output.result.output_error:''}`;
  $('application-stdout').textContent=output.result?.stdout||'Waiting for stdout…';$('application-stderr').textContent=output.result?.stderr||'';$('application-files-download').disabled=!(output.result?.output_available||output.result?.output_zip);
 }
 function runButtons(run){const group=document.createElement('div');group.className='row-actions';group.append(action(active(run.state)?'Live logs':'Output',async()=>{try{await updateOutput(run.id);$('application-log-dialog').showModal();}catch(e){message.textContent=e.message;}}));if(active(run.state))group.append(action('Stop',async()=>{try{await api('POST',`/api/application-runs/${run.id}/cancel`,{});message.textContent='Cancellation requested.';}catch(e){message.textContent=e.message;}}));else group.append(action('Delete',async()=>{try{await api('DELETE',`/api/application-runs/${run.id}`);await refreshApps();}catch(e){message.textContent=e.message;}}));return group;}
 async function refreshApps(){
  if(busy||!$('tok').value.trim())return;busy=true;
  try{const [newApps,runs,runtimes]=await Promise.all([api('GET','/api/applications'),api('GET','/api/application-runs'),api('GET','/api/applications/runtimes')]);apps=newApps;render();$('application-runtime-status').textContent=runtimes.map(r=>`${r.runtime}: ${r.workers} ready`).join(' · ');const table=$('application-runs-table');table.replaceChildren();row(table,['Application','Version','State','Created','Worker','Actions'],true);for(const run of runs)row(table,[apps.find(a=>a.id===run.app_id)?.name||run.app_id,'v'+(run.version||1),run.state,new Date(run.created).toLocaleString(),run.worker_id,runButtons(run)]);if(!runs.length)row(table,['No runs yet.','','','','','']);message.textContent='';if($('application-log-dialog').open&&output&&active(output.state))await updateOutput(output.id);}catch(e){message.textContent=e.message;}finally{busy=false;}
 }
 $('application-search').oninput=render;$('application-refresh').onclick=refreshApps;$('application-new').onclick=()=>editApplication(null);
 document.querySelectorAll('[data-app-close]').forEach(b=>b.onclick=()=>b.closest('dialog').close());
 $('application-create-form').onsubmit=async event=>{
  event.preventDefault();const button=event.submitter;button.disabled=true;
  try{
   const file=$('application-file').files[0];if(!editing&&!file)throw Error('Select an application file.');let artifact;
   if(file){if(file.size>4*1024*1024)throw Error('Maximum upload is 4 MiB.');const bytes=new Uint8Array(await file.arrayBuffer());let binary='';for(let i=0;i<bytes.length;i+=8192)binary+=String.fromCharCode(...bytes.subarray(i,i+8192));artifact=btoa(binary);}
   const args=parseJSON('application-args',[]),env=parseJSON('application-env',{}),secrets=parseJSON('application-secrets',null);
   if(!Array.isArray(args)||args.some(a=>typeof a!=='string'))throw Error('Arguments must be a JSON array of strings.');for(const values of [env,secrets])if(values!==null&&(typeof values!=='object'||Array.isArray(values)||Object.values(values).some(v=>typeof v!=='string')))throw Error('Environment and secrets must be JSON objects with string values.');
   const body={name:$('application-name').value.trim(),kind:$('application-kind').value,schedule:$('application-schedule').value.trim(),execution:{runtime:$('application-runtime').value,format:$('application-format').value,entry:$('application-entry').value.trim(),args,env,artifact,install_dependencies:$('application-deps').checked,ram_mb:+$('application-ram').value,cpu:+$('application-cpu').value,timeout_seconds:+$('application-timeout').value}};if(secrets!==null)body.secrets=secrets;
   await api(editing?'PUT':'POST',editing?`/api/applications/${editing.id}`:'/api/applications',body);$('application-create-dialog').close();event.target.reset();await refreshApps();
  }catch(e){$('application-create-error').textContent=e.message;}finally{button.disabled=false;}
 };
 $('application-run-submit').onclick=async()=>{if(!selected)return;const b=$('application-run-submit');b.disabled=true;$('application-invoke-message').textContent='Submitting…';try{const url=selected.kind==='function'?`/api/functions/${selected.id}/invoke`:`/api/applications/${selected.id}/run`;const run=await api('POST',url,{input:$('application-input').value});$('application-invoke-message').textContent=`${run.id}: ${run.state}. View run history for output.`;await refreshApps();}catch(e){$('application-invoke-message').textContent=e.message;}finally{b.disabled=false;}};
 function download(blob,name){const url=URL.createObjectURL(blob),a=document.createElement('a');a.href=url;a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}
 $('application-output-download').onclick=()=>{if(!output)return;const copy={...output,result:output.result?{...output.result,output_zip:undefined}:undefined};download(new Blob([JSON.stringify(copy,null,2)],{type:'application/json'}),output.id+'.json');};
 $('application-files-download').onclick=async()=>{if(!output)return;try{const response=await apiRaw('GET',`/api/application-runs/${output.id}/output`);download(await response.blob(),output.id+'-output.zip');}catch(e){$('application-log-status').textContent=e.message;}};
 $('application-webhook-enable').onclick=async()=>{if(!webhookApp)return;try{const value=await api('POST',`/api/applications/${webhookApp.id}/webhook`,{});$('application-webhook-details').textContent=`${location.origin}${value.path}\n\n${value.header}: ${value.secret}`;$('application-webhook-message').textContent='Copy this secret now. It will not be shown again.';}catch(e){$('application-webhook-message').textContent=e.message;}};
 $('application-webhook-disable').onclick=async()=>{if(!webhookApp)return;try{await api('DELETE',`/api/applications/${webhookApp.id}/webhook`);$('application-webhook-details').textContent='Webhook disabled.';$('application-webhook-message').textContent='';}catch(e){$('application-webhook-message').textContent=e.message;}};
 $('save').addEventListener('click',refreshApps);setInterval(refreshApps,3000);refreshApps();
})();
