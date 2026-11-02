(()=>{
 if(!['firewall','details'].includes(page))return;
 let data=null,busy=false,scope='host';
 const message=text=>{if($('fw-message'))$('fw-message').textContent=text||'';};
 const params=new URLSearchParams(location.search);
 function portLabel(rule){
  if((rule.protocol||'')==='icmp'||!rule.port)return 'all';
  return rule.port_end&&rule.port_end>rule.port?rule.port+'-'+rule.port_end:String(rule.port);
 }
 function ruleRows(table,rules,onRemove){
  row(table,['Action','Protocol','Port','Source','Actions'],true);
  if(!rules||!rules.length){row(table,['Default allow. No extra rules.','','','','']);return;}
  rules.forEach((rule,idx)=>{
   const actions=document.createElement('div');actions.className='row-actions';
   const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Remove';
   del.onclick=()=>onRemove(idx);
   actions.append(del);
   row(table,[rule.action,String(rule.protocol||'').toUpperCase(),portLabel(rule),rule.source,actions]);
  });
 }
 function selectedWorker(){return (data?.workers||[]).find(w=>w.id===$('fw-worker')?.value)||null;}
 function selectedInstance(){return (data?.instances||[]).find(i=>i.id===$('fw-instance')?.value)||null;}
 function renderWorker(){
  const w=selectedWorker(),table=$('fw-worker-rules');
  if(!table)return;
  $('fw-worker-restrict').checked=!!w?.restrict;
  $('fw-worker-ports').textContent=(data?.host_ports||[]).map(r=>`${r.name} ${r.from}–${r.to}`).join(' · ');
  ruleRows(table,w?.rules||[],idx=>saveWorker((w.rules||[]).filter((_,i)=>i!==idx),w?.restrict));
 }
 function renderInstance(){
  const inst=selectedInstance(),table=$('fw-instance-rules'),maps=$('fw-instance-maps');
  if(!table||!maps)return;
  $('fw-instance-restrict').checked=!!inst?.restrict;
  $('fw-instance-meta').textContent=inst?`${inst.ip||'no guest IP'} · ${inst.vpc_id||'vpc-default'} ${inst.vpc_cidr||''} · worker ${inst.worker_id}`:'Select a VPS.';
  row(maps,['Protocol','Guest port','Host port','Kind'],true);
  if(!inst?.maps?.length)row(maps,['No published maps','','','']);
  else inst.maps.forEach(m=>row(maps,[String(m.protocol||'').toUpperCase(),m.guest_port,m.host_port,m.managed?'Managed':'Custom']));
  ruleRows(table,inst?.rules||[],idx=>saveInstance((inst.rules||[]).filter((_,i)=>i!==idx),inst?.restrict));
 }
 function renderDetailsSG(){
  const table=$('detail-firewall');if(!table||!selectedID)return;
  const inst=(data?.instances||[]).find(i=>i.id===selectedID);
  row(table,['Action','Protocol','Port','Source'],true);
  if($('detail-open-firewall'))$('detail-open-firewall').href='/firewall?instance='+encodeURIComponent(selectedID)+'#firewall-vps';
  if(!inst){row(table,['No security group loaded','','','']);return;}
  if(!inst.restrict&&!(inst.rules||[]).length){row(table,['Default allow · '+ (inst.vpc_cidr||'VPC'),'','', '']);return;}
  if(inst.restrict)row(table,['restrict','any','mapped ports','default deny']);
  (inst.rules||[]).forEach(rule=>row(table,[rule.action,String(rule.protocol||'').toUpperCase(),portLabel(rule),rule.source]));
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
  const w=selectedWorker();if(!w)return;
  try{await api('PUT','/api/firewall/workers/'+w.id,{restrict:!!restrict,rules});message('Worker firewall queued.');await refresh();}
  catch(e){message(e.message);}
 }
 async function saveInstance(rules,restrict){
  const inst=selectedInstance();if(!inst)return;
  try{await api('PUT','/api/firewall/instances/'+inst.id,{restrict:!!restrict,rules});message('VPS security group queued.');await refresh();}
  catch(e){message(e.message);}
 }
 function openDialog(next){
  scope=next;$('fw-rule-form').reset();$('fw-form-title').textContent=next==='host'?'Add worker rule':'Add VPS rule';
  $('fw-form-message').textContent='';$('fw-src-vpc').hidden=next!=='vps';$('fw-dialog').showModal();
 }
 if(page==='firewall'){
  $('fw-worker').onchange=renderWorker;
  $('fw-instance').onchange=renderInstance;
  $('fw-worker-restrict').onchange=()=>saveWorker(selectedWorker()?.rules||[],$('fw-worker-restrict').checked);
  $('fw-instance-restrict').onchange=()=>saveInstance(selectedInstance()?.rules||[],$('fw-instance-restrict').checked);
  $('fw-worker-add').onclick=()=>openDialog('host');
  $('fw-instance-add').onclick=()=>openDialog('vps');
  $('fw-close').onclick=$('fw-reset').onclick=()=>$('fw-dialog').close();
  $('fw-src-any').onclick=()=>{$('fw-source').value='0.0.0.0/0';};
  $('fw-src-vpc').onclick=()=>{const inst=selectedInstance();$('fw-source').value=inst?.vpc_cidr||'10.231.0.0/16';};
  $('fw-rule-form').onsubmit=async e=>{
   e.preventDefault();
   const rule={action:$('fw-action').value,protocol:$('fw-proto').value,source:$('fw-source').value.trim(),port:Number($('fw-port').value)||0,port_end:Number($('fw-port-end').value)||0};
   try{
    if(scope==='host')await saveWorker([...(selectedWorker()?.rules||[]),rule],$('fw-worker-restrict').checked);
    else await saveInstance([...(selectedInstance()?.rules||[]),rule],$('fw-instance-restrict').checked);
    $('fw-dialog').close();
   }catch(err){$('fw-form-message').textContent=err.message;}
  };
  $('save')?.addEventListener('click',refresh);$('tok')?.addEventListener('change',refresh);
  setInterval(()=>{if(!document.hidden)refresh();},10000);
 }
 refresh();
})();
