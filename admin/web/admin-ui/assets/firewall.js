(()=>{
 if(!['firewall','details'].includes(page))return;
 let data=null,busy=false,scope='host',editIndex=-1;
 const message=text=>{if($('fw-message'))$('fw-message').textContent=text||'';};
 const params=new URLSearchParams(location.search);
 function portLabel(rule){
  if((rule.protocol||'')==='icmp'||!rule.port)return 'all';
  return rule.port_end&&rule.port_end>rule.port?rule.port+'-'+rule.port_end:String(rule.port);
 }
 function actionLabel(action){return action==='deny'?'Block':'Allow';}
 function cloneRules(rules){return (rules||[]).map(r=>({action:r.action,protocol:r.protocol,source:r.source,port:Number(r.port)||0,port_end:Number(r.port_end)||0}));}
 function ruleRows(table,rules,onEdit,onRemove){
  row(table,['Action','Protocol','Port','Source','Actions'],true);
  if(!rules||!rules.length){row(table,['No rules yet. With Restrict on, traffic is blocked until you add Allow rules.','','','','']);return;}
  rules.forEach((rule,idx)=>{
   const actions=document.createElement('div');actions.className='row-actions';
   const edit=document.createElement('button');edit.type='button';edit.className='small-btn';edit.textContent='Edit';
   edit.onclick=e=>{e.preventDefault();e.stopPropagation();onEdit(idx);};
   const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';
   del.onclick=e=>{
    e.preventDefault();e.stopPropagation();
    if(!window.confirm(`Delete this ${actionLabel(rule.action).toLowerCase()} rule for ${rule.source}?`))return;
    onRemove(idx);
   };
   actions.append(edit,del);
   const badge=document.createElement('span');badge.className='badge '+(rule.action==='deny'?'FAILED':'RUNNING');badge.textContent=actionLabel(rule.action);
   row(table,[badge,String(rule.protocol||'').toUpperCase(),portLabel(rule),rule.source,actions]);
  });
 }
 function selectedWorker(){return (data?.workers||[]).find(w=>w.id===$('fw-worker')?.value)||null;}
 function selectedInstance(){return (data?.instances||[]).find(i=>i.id===$('fw-instance')?.value)||null;}
 function renderWorker(){
  const w=selectedWorker(),table=$('fw-worker-rules');
  if(!table)return;
  if($('fw-worker-restrict'))$('fw-worker-restrict').checked=!!w?.restrict;
  if($('fw-worker-ports'))$('fw-worker-ports').textContent=(data?.host_ports||[]).map(r=>`${r.name} ${r.from}–${r.to}`).join(' · ');
  const hint=$('fw-worker-hint');
  if(hint)hint.textContent=w?.restrict?'Restrict is on: only listed Allow rules (minus Block) can reach CloFix published ports.':'Restrict is off: published ports stay open; Block rules still deny matching sources.';
  ruleRows(table,w?.rules||[],
   idx=>openDialog('host',idx),
   idx=>saveWorker((w.rules||[]).filter((_,i)=>i!==idx),w?.restrict));
 }
 function renderInstance(){
  const inst=selectedInstance(),table=$('fw-instance-rules'),maps=$('fw-instance-maps');
  if(!table||!maps)return;
  if($('fw-instance-restrict'))$('fw-instance-restrict').checked=!!inst?.restrict;
  if($('fw-instance-meta'))$('fw-instance-meta').textContent=inst?`${inst.ip||'no guest IP'} · ${inst.vpc_id||'vpc-default'} ${inst.vpc_cidr||''} · worker ${inst.worker_id}`:'Select a VPS.';
  const hint=$('fw-instance-hint');
  if(hint)hint.textContent=inst?.restrict?'Restrict is on: add Allow rules for SSH (22), HTTP (80), HTTPS (443), or custom guest ports. Without Allow rules, published ports stay blocked.':'Restrict is off: mapped ports stay open; use Block rules to deny specific sources.';
  const presets=$('fw-instance-presets');
  if(presets){
   presets.hidden=!inst;
   // Keep Add visible even when quick-allows are shown with a selected VPS.
   const add=$('fw-instance-add');
   if(add)add.hidden=false;
  }
  row(maps,['Service','Protocol','Guest port','Host port','Kind'],true);
  if(!inst?.maps?.length)row(maps,['No published maps','','','','']);
  else inst.maps.forEach(m=>{
   const guest=Number(m.guest_port);
   const service=guest===22?'SSH':guest===80?'HTTP':guest===443?'HTTPS':'Custom';
   row(maps,[service,String(m.protocol||'').toUpperCase(),m.guest_port,m.host_port,m.managed?'Managed':'Custom']);
  });
  ruleRows(table,inst?.rules||[],
   idx=>openDialog('vps',idx),
   idx=>saveInstance((inst.rules||[]).filter((_,i)=>i!==idx),inst?.restrict));
 }
 function renderDetailsSG(){
  const table=$('detail-firewall');if(!table||!selectedID)return;
  const inst=(data?.instances||[]).find(i=>i.id===selectedID);
  row(table,['Action','Protocol','Port','Source','Actions'],true);
  if($('detail-open-firewall'))$('detail-open-firewall').href='/firewall?instance='+encodeURIComponent(selectedID)+'#firewall-vps';
  if(!inst){row(table,['No security group loaded','','','','']);return;}
  if(!inst.restrict&&!(inst.rules||[]).length){row(table,['Default allow · '+(inst.vpc_cidr||'VPC'),'','','','']);return;}
  if(inst.restrict)row(table,['Block (default)','any','mapped ports','all sources except Allow rules','']);
  (inst.rules||[]).forEach((rule,idx)=>{
   const actions=document.createElement('div');actions.className='row-actions';
   const manage=document.createElement('a');manage.className='small-btn';manage.href='/firewall?instance='+encodeURIComponent(selectedID)+'#firewall-vps';manage.textContent='Edit';
   const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';
   del.onclick=async e=>{
    e.preventDefault();e.stopPropagation();
    if(!window.confirm(`Delete this ${actionLabel(rule.action).toLowerCase()} rule?`))return;
    const next=(inst.rules||[]).filter((_,i)=>i!==idx);
    try{await api('PUT','/api/firewall/instances/'+inst.id,{restrict:!!inst.restrict,rules:next});await refresh();}
    catch(err){if($('detail-message'))$('detail-message').textContent=err.message;else message(err.message);}
   };
   actions.append(manage,del);
   const badge=document.createElement('span');badge.className='badge '+(rule.action==='deny'?'FAILED':'RUNNING');badge.textContent=actionLabel(rule.action);
   row(table,[badge,String(rule.protocol||'').toUpperCase(),portLabel(rule),rule.source,actions]);
  });
 }
 function fillSelect(id,items,preferred){
  const sel=$(id);if(!sel)return;
  const keep=preferred||params.get(id==='fw-worker'?'worker':'instance')||sel.value;
  sel.replaceChildren();
  (items||[]).forEach(item=>sel.add(new Option(item.hostname?`${item.id} · ${item.hostname}`:`${item.id} · ${item.state||''}`,item.id)));
  if(keep&&[...sel.options].some(o=>o.value===keep))sel.value=keep;
 }
 async function refresh(){
  if(busy||!T&&!$('tok')?.value.trim())return;busy=true;
  try{
   data=await api('GET','/api/firewall');
   if($('auth-banner'))$('auth-banner').hidden=true;
   if(page==='firewall'){
    fillSelect('fw-worker',data.workers);
    fillSelect('fw-instance',data.instances);
    renderWorker();renderInstance();
   }
   renderDetailsSG();
  }catch(e){message(e.message);}
  finally{busy=false;}
 }
 async function saveWorker(rules,restrict){
  const w=selectedWorker();if(!w){message('Select a worker first.');return;}
  try{await api('PUT','/api/firewall/workers/'+w.id,{restrict:!!restrict,rules});message('Worker firewall updated.');await refresh();}
  catch(e){message(e.message);throw e;}
 }
 async function saveInstance(rules,restrict){
  const inst=selectedInstance()||(data?.instances||[]).find(i=>i.id===selectedID);if(!inst){message('Select a VPS first.');return;}
  try{await api('PUT','/api/firewall/instances/'+inst.id,{restrict:!!restrict,rules});message('VPS security group updated.');await refresh();}
  catch(e){message(e.message);throw e;}
 }
 function fillRuleForm(rule){
  if($('fw-action'))$('fw-action').value=rule?.action==='deny'?'deny':'allow';
  if($('fw-proto'))$('fw-proto').value=rule?.protocol||'tcp';
  if($('fw-port'))$('fw-port').value=rule?.port||'';
  if($('fw-port-end'))$('fw-port-end').value=rule?.port_end&&rule.port_end!==rule.port?rule.port_end:'';
  if($('fw-source'))$('fw-source').value=rule?.source||'0.0.0.0/0';
 }
 function openDialog(next,idx=-1){
  const dialog=$('fw-dialog');
  if(!dialog){message('Rule dialog missing. Hard-refresh the page.');return;}
  if(dialog.parentElement!==document.body)document.body.appendChild(dialog);
  scope=next;editIndex=idx;
  const rules=next==='host'?(selectedWorker()?.rules||[]):(selectedInstance()?.rules||[]);
  const editing=idx>=0?rules[idx]:null;
  $('fw-rule-form')?.reset();
  fillRuleForm(editing||{action:'allow',protocol:'tcp',source:'0.0.0.0/0'});
  if($('fw-form-title'))$('fw-form-title').textContent=editing?(next==='host'?'Edit worker rule':'Edit VPS rule'):(next==='host'?'Add worker rule':'Add VPS rule');
  if($('fw-save'))$('fw-save').textContent=editing?'Save rule':'Add rule';
  if($('fw-form-message'))$('fw-form-message').textContent='';
  if($('fw-src-vpc'))$('fw-src-vpc').hidden=next!=='vps';
  if(typeof dialog.showModal==='function')dialog.showModal();
  else dialog.setAttribute('open','');
 }
 async function quickAllow(port,label){
  const inst=selectedInstance();if(!inst){message('Select a VPS first.');return;}
  const rules=cloneRules(inst.rules);
  if(rules.some(r=>r.action==='allow'&&r.protocol==='tcp'&&Number(r.port)===port&&(!r.port_end||Number(r.port_end)===port)&&r.source==='0.0.0.0/0')){
   message(`${label} allow from 0.0.0.0/0 already exists.`);
   return;
  }
  rules.push({action:'allow',protocol:'tcp',source:'0.0.0.0/0',port,port_end:port});
  if($('fw-instance-restrict'))$('fw-instance-restrict').checked=true;
  await saveInstance(rules,true);
  message(`Allowed ${label} (guest ${port}) from anywhere. Restrict is on.`);
 }
 function closeDialog(){
  const dialog=$('fw-dialog');
  if(dialog?.open)dialog.close();
  editIndex=-1;
 }
 if(page==='firewall'){
  const bind=(id,event,fn)=>{const el=$(id);if(el)el[event]=fn;};
  bind('fw-worker','onchange',renderWorker);
  bind('fw-instance','onchange',renderInstance);
  bind('fw-worker-restrict','onchange',()=>saveWorker(selectedWorker()?.rules||[],$('fw-worker-restrict').checked));
  bind('fw-instance-restrict','onchange',()=>saveInstance(selectedInstance()?.rules||[],$('fw-instance-restrict').checked));
  bind('fw-worker-add','onclick',()=>openDialog('host'));
  bind('fw-instance-add','onclick',()=>openDialog('vps'));
  $('fw-allow-ssh')?.addEventListener('click',()=>quickAllow(22,'SSH'));
  $('fw-allow-http')?.addEventListener('click',()=>quickAllow(80,'HTTP'));
  $('fw-allow-https')?.addEventListener('click',()=>quickAllow(443,'HTTPS'));
  bind('fw-close','onclick',closeDialog);
  bind('fw-reset','onclick',closeDialog);
  bind('fw-src-any','onclick',()=>{if($('fw-source'))$('fw-source').value='0.0.0.0/0';});
  bind('fw-src-vpc','onclick',()=>{const inst=selectedInstance();if($('fw-source'))$('fw-source').value=inst?.vpc_cidr||'10.231.0.0/16';});
  $('fw-rule-form')?.addEventListener('submit',async e=>{
   e.preventDefault();
   const rule={action:$('fw-action').value,protocol:$('fw-proto').value,source:$('fw-source').value.trim(),port:Number($('fw-port').value)||0,port_end:Number($('fw-port-end').value)||0};
   if(!rule.source){if($('fw-form-message'))$('fw-form-message').textContent='Source CIDR is required.';return;}
   try{
    if(scope==='host'){
     const next=cloneRules(selectedWorker()?.rules);
     if(editIndex>=0)next[editIndex]=rule;else next.push(rule);
     await saveWorker(next,$('fw-worker-restrict')?.checked);
    }else{
     const next=cloneRules(selectedInstance()?.rules);
     if(editIndex>=0)next[editIndex]=rule;else next.push(rule);
     await saveInstance(next,$('fw-instance-restrict')?.checked);
    }
    closeDialog();
   }catch(err){if($('fw-form-message'))$('fw-form-message').textContent=err.message;}
  });
  $('save')?.addEventListener('click',refresh);$('tok')?.addEventListener('change',refresh);
  setInterval(()=>{if(!document.hidden)refresh();},10000);
 }
 refresh();
})();
