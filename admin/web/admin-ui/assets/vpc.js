(()=>{
 if(page!=='vpc')return;
 let editing=null,items=[];

 function clearForm(){
  editing=null;
  $('vpc-id').value='';
  $('vpc-form').reset();
  $('vpc-cidr').disabled=false;
  $('vpc-save').textContent='Create VPC';
  $('vpc-cancel').hidden=true;
 }

 function editVPC(v){
  editing=v;
  $('vpc-id').value=v.id;
  $('vpc-name').value=v.name||'';
  $('vpc-cidr').value=v.cidr||'';
  $('vpc-cidr').disabled=!!v.default;
  $('vpc-save').textContent='Save VPC';
  $('vpc-cancel').hidden=false;
 }

 function renderStats(){
  const host=$('vpc-overview-stats');
  if(!host)return;
  host.replaceChildren();
  const extra=items.filter(v=>!v.default).length,used=items.reduce((n,v)=>n+(v.used||0),0);
  [['VPCs',String(items.length),'Default plus extra blocks'],['Extra blocks',String(extra),'Can be created, edited and deleted'],['Addresses in use',String(used),'VPS, databases and managed apps']].forEach(([label,value,note])=>{
   const card=document.createElement('div');
   card.className='stat';
   card.innerHTML=`<div class="stat-label"></div><div class="stat-value"></div><div class="stat-note"></div>`;
   card.querySelector('.stat-label').textContent=label;
   card.querySelector('.stat-value').textContent=value;
   card.querySelector('.stat-note').textContent=note;
   host.append(card);
  });
 }

 function renderTable(){
  const table=$('vpc-table');
  table.replaceChildren();
  row(table,['Name','CIDR','Gateway','Used / capacity','Default','Actions'],true);
  if(!items.length)row(table,['No VPC blocks.','','','','','']);
  items.forEach(v=>{
   const actions=document.createElement('div');
   actions.className='row-actions';
   const edit=document.createElement('button');
   edit.type='button';edit.className='small-btn';edit.textContent='Edit';
   edit.onclick=()=>editVPC(v);
   actions.append(edit);
   if(!v.default){
    const del=document.createElement('button');
    del.type='button';del.className='danger small-btn';del.textContent='Delete';
    del.onclick=async()=>{
     if(!confirm(`Delete VPC ${v.name}?`))return;
     try{await api('DELETE','/api/vpcs/'+v.id);$('vpc-message').textContent='VPC deleted.';await load();}
     catch(e){$('vpc-message').textContent=e.message;}
    };
    actions.append(del);
   }
   const badge=document.createElement('span');
   badge.className='badge '+(v.default?'RUNNING':'STOPPED');
   badge.textContent=v.default?'Default':'Extra';
   row(table,[v.name,v.cidr,v.gateway,`${v.used||0} / ${v.capacity||0}`,badge,actions]);
  });
 }

 async function load(){
  const data=await api('GET','/api/vpcs');
  items=data.vpcs||[];
  renderStats();
  renderTable();
 }

 $('vpc-form').onsubmit=async event=>{
  event.preventDefault();
  const body={name:$('vpc-name').value.trim(),cidr:$('vpc-cidr').value.trim()};
  try{
   if(editing){
    await api('PUT','/api/vpcs/'+editing.id,body);
    $('vpc-message').textContent='VPC saved.';
   }else{
    await api('POST','/api/vpcs',body);
    $('vpc-message').textContent='VPC created.';
   }
   clearForm();
   await load();
  }catch(e){$('vpc-message').textContent=e.message;}
 };
 const vpcCancel=$('vpc-cancel');if(vpcCancel)vpcCancel.onclick=clearForm;
 $('save')?.addEventListener('click',()=>load().catch(e=>$('vpc-message').textContent=e.message));
 load().catch(e=>$('vpc-message').textContent=e.message);
})();
