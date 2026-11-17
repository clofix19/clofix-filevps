(()=>{
const $=id=>document.getElementById(id);
const page=$('page-redis-services');
if(!page)return;

const BUSY=new Set(['PROVISIONING','ATTACHING','CONFIGURING','JOINING','DELETING','PENDING']);
let selected=null;
let msgTimer=0;
let statsSig='';
let listSig='';
let refreshing=false;

function msg(t,isErr){
  const el=$('cs-message'); if(!el)return;
  clearTimeout(msgTimer);
  const text=(t||'').trim();
  el.hidden=!text;
  el.textContent=text;
  el.classList.toggle('error',!!isErr);
  if(text&&!isErr)msgTimer=setTimeout(()=>{el.hidden=true;el.textContent='';},3500);
}
function errCreate(t){
  const el=$('cs-create-error'); if(!el)return;
  const text=(t||'').trim();
  el.hidden=!text;
  el.textContent=text;
}
function topoLabel(t){
  return t==='master_slave'?'Master–slave':'Master–master';
}
function statusBadge(state){
  const key=String(state||'').toUpperCase();
  const cls=key==='READY'?'RUNNING':(BUSY.has(key)||key==='PROVISIONING'?'PROVISIONING':key);
  const e=document.createElement('span');
  e.className='badge '+cls;
  if(BUSY.has(key)||key==='PROVISIONING'){
    const spin=document.createElement('span');
    spin.className='progress-icon';
    spin.setAttribute('aria-hidden','true');
    e.append(spin);
  }
  e.append(document.createTextNode(state||'—'));
  return e;
}
function topologyNote(){
  const topo=$('cs-topology')?.value, nodes=$('cs-nodes'), note=$('cs-topo-note');
  if(!note||!nodes)return;
  if(topo==='master_master'){
    note.textContent='Master–master creates Redis Cluster with all masters (min 3).';
    if(Number(nodes.value)<3)nodes.value=3;
    nodes.min=3;
  }else{
    note.textContent='Master–slave creates 1 primary and (N−1) replicas (min 2).';
    nodes.min=2;
  }
}
function storageToggle(){
  const field=$('cs-efs-field');
  if(field)field.hidden=$('cs-storage')?.value!=='persistent';
}
async function loadEFS(){
  const sel=$('cs-efs'); if(!sel)return;
  try{
    const data=await api('GET','/api/efs');
    const shares=data.shares||[];
    const cur=sel.value;
    sel.replaceChildren(new Option('Select EFS ID',''));
    shares.forEach(s=>sel.add(new Option(`${s.name} (${s.id})`, s.id)));
    if([...sel.options].some(o=>o.value===cur))sel.value=cur;
  }catch(_){}
}
function hashSeed(s){
  let h=2166136261>>>0;
  for(let i=0;i<s.length;i++){h^=s.charCodeAt(i);h=Math.imul(h,16777619);}
  return h>>>0;
}
function barHeights(seed,value,n){
  const count=n||14;
  const out=[];
  let x=seed||1;
  const peak=Math.max(0.18,Math.min(1,(Number(value)||0)>0?0.35+Math.min(Number(value),12)/16:0.12));
  for(let i=0;i<count;i++){
    x=(Math.imul(x,1103515245)+12345)>>>0;
    const wobble=0.22+((x%1000)/1000)*0.55;
    const ramp=0.55+0.45*Math.sin((i/count)*Math.PI);
    const tip=i===count-1?peak:peak*wobble*ramp;
    out.push(Math.max(0.08,Math.min(1,tip)));
  }
  return out;
}
function paintStatBars(force){
  const box=$('cs-stats'); if(!box)return;
  box.querySelectorAll('.stat').forEach((card,idx)=>{
    const valueEl=card.querySelector('.stat-value');
    const label=(card.querySelector('.stat-label')?.textContent||'').toLowerCase();
    const value=Number(String(valueEl?.textContent||'').replace(/[^\d.-]/g,''));
    let tone='muted';
    if(/fail|error|warn/.test(label))tone='warn';
    else if(Number.isFinite(value)&&value>0)tone='ok';
    const barSig=tone+'|'+ (Number.isFinite(value)?value:0);
    if(!force && card.dataset.barSig===barSig && card.querySelector(':scope > .cs-stat-bars')){
      card.dataset.waveTone=tone;
      return;
    }
    card.dataset.waveTone=tone;
    card.dataset.barSig=barSig;
    card.querySelectorAll(':scope > .stat-wave, :scope > .cs-stat-bars').forEach(el=>el.remove());
    const heights=barHeights(hashSeed(label+':'+idx+(Number.isFinite(value)?value:0)),value);
    const svg=document.createElementNS('http://www.w3.org/2000/svg','svg');
    svg.classList.add('cs-stat-bars');
    svg.setAttribute('viewBox','0 0 160 46');
    svg.setAttribute('preserveAspectRatio','none');
    svg.setAttribute('aria-hidden','true');
    const ns='http://www.w3.org/2000/svg';
    const base=document.createElementNS(ns,'line');
    base.setAttribute('x1','0');base.setAttribute('x2','160');base.setAttribute('y1','44.5');base.setAttribute('y2','44.5');
    base.classList.add('cs-stat-bars-base');
    svg.append(base);
    const gap=2.2,bw=(160-(heights.length+1)*gap)/heights.length;
    heights.forEach((h,i)=>{
      const bh=Math.max(3,h*34);
      const x=gap+i*(bw+gap);
      const y=44.5-bh;
      const rect=document.createElementNS(ns,'rect');
      rect.setAttribute('x',x.toFixed(2));
      rect.setAttribute('y',y.toFixed(2));
      rect.setAttribute('width',Math.max(2.4,bw).toFixed(2));
      rect.setAttribute('height',bh.toFixed(2));
      rect.setAttribute('rx','1.6');
      rect.classList.add('cs-stat-bar');
      if(i===heights.length-1)rect.classList.add('cs-stat-bar-tip');
      svg.append(rect);
    });
    card.append(svg);
  });
}
function setStats(items){
  const total=items.length;
  const ready=items.filter(c=>c.status==='READY').length;
  const failed=items.filter(c=>c.status==='FAILED').length;
  const busy=items.filter(c=>BUSY.has(String(c.status||'').toUpperCase())).length;
  const next=total+'|'+ready+'|'+busy+'|'+failed;
  const changed=next!==statsSig;
  statsSig=next;
  if($('cs-stat-total'))$('cs-stat-total').textContent=String(total);
  if($('cs-stat-ready'))$('cs-stat-ready').textContent=String(ready);
  if($('cs-stat-busy'))$('cs-stat-busy').textContent=String(busy);
  if($('cs-stat-failed'))$('cs-stat-failed').textContent=String(failed);
  paintStatBars(changed);
}
function fillDetail(c){
  selected=c;
  $('cs-detail-title').textContent=c.name||c.id;
  $('cs-detail-sub').textContent=`${topoLabel(c.topology)} · ${c.node_count||(c.nodes||[]).length} nodes · ${c.storage==='persistent'?(c.efs_id||'EFS'):'temp storage'}`;
  const meta=$('cs-detail-meta');
  meta.replaceChildren();
  const bits=[
    ['Status', c.status],
    ['Endpoints', (c.endpoints||[]).join(', ')||'—'],
    ['RAM / node', (c.ram_mb||'—')+' MiB'],
    ['CPU / node', String(c.cpu??'—')],
    ['Disk / node', (c.disk_mb||'—')+' MiB'],
    ['ID', c.id]
  ];
  bits.forEach(([k,v])=>{
    const d=document.createElement('div');
    const strong=document.createElement('strong'); strong.textContent=k;
    const span=document.createElement('span');
    if(k==='Status')span.append(statusBadge(v)); else span.textContent=v;
    d.append(strong,span); meta.append(d);
  });
  const err=$('cs-detail-error');
  if(c.error){err.hidden=false;err.textContent=c.error;} else {err.hidden=true;err.textContent='';}
  const nt=$('cs-detail-nodes');
  row(nt,['Node','Role','Status','Instance','Address'],true);
  (c.nodes||[]).forEach(n=>{
    row(nt,[n.id,n.role||'—',statusBadge(n.status),n.instance_id||'—',n.address||'—']);
  });
  const del=$('cs-detail-delete');
  if(del)del.disabled=c.status==='DELETING';
}
function clusterSig(items){
  return items.map(c=>[
    c.id,c.name,c.status,c.topology,c.storage,c.efs_id,c.node_count,
    (c.endpoints||[]).join(','),c.error||'',
    (c.nodes||[]).map(n=>n.id+':'+n.status+':'+n.role).join(';')
  ].join('~')).join('|');
}
function paintList(items){
  const table=$('cs-table'); if(!table)return;
  const next=clusterSig(items);
  if(next===listSig && table.rows.length)return;
  listSig=next;
  if(!items.length){
    table.replaceChildren();
    const head=document.createElement('tr');
    ['Name','Topology','Nodes','Storage','Status','Endpoints','Actions'].forEach(label=>{
      const th=document.createElement('th'); th.textContent=label; head.append(th);
    });
    const tr=document.createElement('tr');
    const td=document.createElement('td');
    td.colSpan=7;
    const empty=document.createElement('div');
    empty.className='s3-empty-state';
    empty.textContent='No Redis clusters yet. Create one to provision member VPS nodes.';
    td.append(empty); tr.append(td);
    table.append(head,tr);
    table.dispatchEvent(new Event('table-painted'));
    return;
  }
  row(table,['Name','Topology','Nodes','Storage','Status','Endpoints','Actions'],true);
  items.slice().sort((a,b)=>String(b.created||'').localeCompare(String(a.created||''))).forEach(c=>{
    const actions=document.createElement('div');
    actions.className='row-actions';
    const open=document.createElement('button');
    open.type='button'; open.className='secondary'; open.textContent='Details';
    open.onclick=()=>{fillDetail(c);$('cs-detail-dialog')?.showModal();};
    const del=document.createElement('button');
    del.type='button'; del.className='danger'; del.textContent='Delete';
    del.disabled=c.status==='DELETING';
    del.onclick=async()=>{
      if(!window.confirm(`Delete Redis cluster “${c.name}”? Member VPS instances will be removed.`))return;
      try{await api('DELETE','/api/cluster-services/'+c.id);listSig='';await refresh();}catch(e){msg(e.message,true);}
    };
    actions.append(open,del);
    const endpoints=(c.endpoints||[]).slice(0,2).join(', ')+((c.endpoints||[]).length>2?'…':'')||'—';
    const storage=c.storage==='persistent'?(c.efs_id||'EFS'):'temp';
    const statusCell=document.createElement('div');
    statusCell.append(statusBadge(c.status));
    if(c.error){
      const tip=document.createElement('div');
      tip.className='muted'; tip.style.maxWidth='220px'; tip.style.whiteSpace='normal';
      tip.textContent=c.error;
      statusCell.append(tip);
    }
    row(table,[c.name,topoLabel(c.topology),String(c.node_count||(c.nodes||[]).length),storage,statusCell,endpoints,actions]);
  });
}
async function refresh(){
  if(refreshing)return;
  refreshing=true;
  const run=async()=>{
    try{
      // Global refresh() skips this page before ensureSession — load identity here.
      const me=typeof ensureSession==='function'?await ensureSession():(window.cfMe||cfMe||null);
      const fetches=[api('GET','/api/cluster-services'),api('GET','/api/instances')];
      const loadWorkers=me?.kind==='admin';
      if(loadWorkers)fetches.push(api('GET','/api/workers'));
      const [data,vps,wks]=await Promise.all(fetches);
      const items=data.clusters||[];
      instances=Array.isArray(vps)?vps:(vps||[]);
      if(loadWorkers)workers=Array.isArray(wks)?wks:(wks||[]);
      lastSuccessfulRefresh=Date.now();
      setStats(items);
      paintList(items);
      if(selected){
        const live=items.find(x=>x.id===selected.id);
        if(live&&$('cs-detail-dialog')?.open)fillDetail(live);
      }
      msg('');
      if(typeof updateNavBadges==='function')updateNavBadges();
      if(typeof updateTopbarMeta==='function')updateTopbarMeta();
    }catch(e){msg(e.message,true);}
  };
  try{
    if(typeof preservePageScroll==='function')await preservePageScroll(run);
    else await run();
  }finally{refreshing=false;}
}

$('cs-create-open')?.addEventListener('click',async()=>{
  errCreate(''); topologyNote(); storageToggle(); await loadEFS();
  $('cs-create-dialog')?.showModal();
});
$('cs-create-close')?.addEventListener('click',()=>$('cs-create-dialog')?.close());
$('cs-detail-close')?.addEventListener('click',()=>$('cs-detail-dialog')?.close());
$('cs-topology')?.addEventListener('change',topologyNote);
$('cs-storage')?.addEventListener('change',storageToggle);
$('cs-detail-delete')?.addEventListener('click',async()=>{
  if(!selected)return;
  if(!window.confirm(`Delete Redis cluster “${selected.name}”? Member VPS instances will be removed.`))return;
  try{
    await api('DELETE','/api/cluster-services/'+selected.id);
    $('cs-detail-dialog')?.close();
    listSig='';
    await refresh();
  }catch(e){msg(e.message,true);}
});
$('cs-create-form')?.addEventListener('submit',async e=>{
  e.preventDefault(); errCreate('');
  const storage=$('cs-storage').value;
  const body={
    name:$('cs-name').value.trim(),
    app:'redis',
    topology:$('cs-topology').value,
    storage,
    efs_id:storage==='persistent'?($('cs-efs').value||''):'',
    node_count:Number($('cs-nodes').value)||0,
    ram_mb:Number($('cs-ram').value)||256,
    cpu:Number($('cs-cpu').value)||0.5,
    disk_mb:Number($('cs-disk').value)||2048
  };
  try{
    await api('POST','/api/cluster-services',body);
    $('cs-create-dialog')?.close();
    $('cs-name').value='';
    listSig='';
    await refresh();
  }catch(err){errCreate(err.message);}
});

window.cfPageRefreshers=window.cfPageRefreshers||{};
window.cfPageRefreshers['redis-services']=refresh;
refresh();
setInterval(()=>{if(!document.hidden && $('page-redis-services')?.classList.contains('active'))refresh();},6000);
})();
