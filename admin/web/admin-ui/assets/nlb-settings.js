(()=>{
 if(page!=='settings')return;
 const node=name=>$('settings-nlb-'+name);let balancers=[],instances=[];
 const message=text=>node('message').textContent=text;
 const floatMsg=text=>{if($('settings-floating-message'))$('settings-floating-message').textContent=text;};
 const errorText=e=>String(e.message).includes('requested API route')?'Running controller is older than this UI. Restart the controller with the updated binary to enable IP pool and NLB settings.':e.message;
 function fillFloatingResources(){
  const sel=$('settings-floating-resource'),target=$('settings-floating-target')?.value||'vps';
  if(!sel)return;
  const keep=sel.value;
  sel.replaceChildren();
  if(target==='nlb'){
   const nlbs=balancers.filter(b=>b.mode==='nlb'&&!b.deleting);
   if(!nlbs.length){sel.add(new Option('No NLB yet — convert a balancer below first',''));return;}
   nlbs.forEach(b=>sel.add(new Option(`${b.name} · ${b.public_ip||'no IP'} · ${b.id}`,b.id)));
  }else{
   if(!instances.length){sel.add(new Option('No FileVPS instances',''));return;}
   instances.forEach(i=>sel.add(new Option(`${i.id} · ${i.state}`,i.id)));
  }
  if([...sel.options].some(o=>o.value===keep))sel.value=keep;
 }
 function renderFloating(items,note){
  const table=$('settings-floating-table');if(!table)return;
  if($('settings-floating-note')&&note)$('settings-floating-note').textContent=note;
  table.replaceChildren();
  row(table,['IP','Target','Resource','Firewall','Attached','Note'],true);
  if(!items.length){row(table,['No floating IPs attached yet.','','','','','']);return;}
  items.forEach(a=>{
   const target=a.instance_id?'FileVPS':(a.nlb_id?'NLB':'—');
   const resource=a.instance_id||a.nlb_id||'—';
   row(table,[a.ip,target,resource,a.firewall||'—',a.attached?new Date(a.attached).toLocaleString():'—',a.note||'—']);
  });
 }
 async function loadFloating(){
  if(!$('settings-floating-table'))return;
  try{
   const data=await api('GET','/api/floating-ips');
   renderFloating(data.items||[],data.note||'');
  }catch(e){floatMsg(errorText(e));}
 }
 async function load(){if(!T&&!$('tok').value.trim())return;if(cfMe&&cfMe.kind!=='admin')return;try{const [config,items,vps]=await Promise.all([api('GET','/api/load-balancers/settings'),api('GET','/api/load-balancers'),api('GET','/api/instances')]);node('pool').value=(config.ip_ranges||[]).join('\n');balancers=items;instances=vps||[];const previous=node('lb').value;node('lb').replaceChildren(new Option('Select load balancer…',''));for(const b of items.filter(x=>!x.deleting))node('lb').add(new Option(`${b.name} · ${b.id}`,b.id));node('lb').value=items.some(x=>x.id===previous)?previous:(items.length===1?items[0].id:'');showCurrent();fillFloatingResources();await loadFloating();}catch(e){message(errorText(e));}}
 function showCurrent(){const b=balancers.find(x=>x.id===node('lb').value);if(!b)return;node('port').value=b.listen_port||80;node('backend').value=b.backend_port||80;node('current').textContent=`Current type: ${b.mode==='nlb'?'NLB · TCP':'ALB · HTTP/HTTPS'} · Allocated IP: ${b.public_ip||'none'}${b.listener_error?' · '+b.listener_error:''}`;}
 node('lb').onchange=showCurrent;
 $('settings-floating-target')?.addEventListener('change',fillFloatingResources);
 node('form').onsubmit=async e=>{e.preventDefault();if(node('form').getAttribute('aria-busy')==='true')return;const finish=beginFormProgress(node('form'),node('save'),'Saving IP pool…');message('Saving IP pool. Please wait…');try{await api('PUT','/api/load-balancers/settings',{ip_ranges:node('pool').value.split(/[\n,]+/).map(x=>x.trim()).filter(Boolean)});message('IP pool saved. Apply NLB settings below to convert an existing HTTP balancer.');await load();}catch(e){message(errorText(e));}finally{finish();}};
 node('lb-form').onsubmit=async e=>{e.preventDefault();const b=balancers.find(x=>x.id===node('lb').value);if(!b||node('lb-form').getAttribute('aria-busy')==='true')return;const finish=beginFormProgress(node('lb-form'),node('apply'),'Updating NLB…');message('Saving NLB changes. Please wait…');try{const updated=await api('PUT','/api/load-balancers/'+b.id,{...b,domains:[],mode:'nlb',listen_port:Number(node('port').value),backend_port:Number(node('backend').value),algorithm:['uri_hash','least_response_time'].includes(b.algorithm)?'round_robin':b.algorithm});await load();message(`✓ NLB settings saved: ${updated.public_ip}:${updated.listen_port}. Listener and TCP health checks update on the next scheduler pass. Changes may interrupt traffic; update DNS if the IP changed.`);}catch(e){message(errorText(e));}finally{finish();}};
 $('settings-floating-form')?.addEventListener('submit',async e=>{
  e.preventDefault();
  const target=$('settings-floating-target').value,id=$('settings-floating-resource').value,ip=$('settings-floating-ip').value.trim();
  if(!id){floatMsg('Select a FileVPS or NLB.');return;}
  const body={ip,note:$('settings-floating-note-input').value.trim()};
  if(target==='nlb')body.nlb_id=id;else body.instance_id=id;
  try{
   const out=await api('POST','/api/floating-ips/attach',body);
   floatMsg((out.firewall||'Floating IP attached.')+' '+((out.assignment&&out.assignment.ip)||ip));
   $('settings-floating-ip').value='';
   await loadFloating();
  }catch(err){floatMsg(errorText(err));}
 });
 $('save')?.addEventListener('click',load);$('tok')?.addEventListener('change',load);load();
})();
