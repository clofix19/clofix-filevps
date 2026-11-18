(()=>{
 const isList=page==='waf', isDetail=page==='waf-binding';
 if(!isList&&!isDetail)return;
 let data=null, editID=document.body.dataset.instanceId||'', errorStatus='403', errorDirty=false, errorSource='';
 const statuses=['403','404','406','429','500'];
 const wafRangeKey='cf_waf_traffic_range_v1', wafCustomKey='cf_waf_traffic_custom_v1';
 const wafRangeMs={'1h':3600000,'6h':21600000,'1d':86400000,'7d':604800000,'30d':2592000000,'all':0,'custom':0};
 let wafTrafficCustom={from:0,to:0};
 try{const saved=JSON.parse(localStorage.getItem(wafCustomKey)||'{}');if(saved&&Number(saved.from)&&Number(saved.to))wafTrafficCustom={from:+saved.from,to:+saved.to};}catch(e){}
 let wafTrafficRange=localStorage.getItem(wafRangeKey)||'1d';
 if(!Object.prototype.hasOwnProperty.call(wafRangeMs,wafTrafficRange))wafTrafficRange='1d';
 if(wafTrafficRange==='custom'&&!(wafTrafficCustom.from&&wafTrafficCustom.to))wafTrafficRange='1d';
 const msg=t=>{if($('waf-page-message'))$('waf-page-message').textContent=t||'';};
 const formMsg=t=>{if($('waf-binding-form-message'))$('waf-binding-form-message').textContent=t||'';};
 const detailMsg=t=>{if($('wafb-message'))$('wafb-message').textContent=t||'';};
 const items=()=>window.cfWAFItems||[];
 const defaultHTML=s=>typeof window.cfDefaultWAFErrorHTML==='function'?window.cfDefaultWAFErrorHTML(s):`<!DOCTYPE html><html><body><h1>${s}</h1><p>Request blocked by CloFix WAF.</p></body></html>`;
 const stateBadge=st=>{
  const b=document.createElement('span');
  const u=String(st||'INACTIVE').toUpperCase();
  b.className='badge '+(u==='ACTIVE'||u==='READY'?'RUNNING':u==='FAILED'?'FAILED':u==='ACTIVATING'||u==='DEACTIVATING'?'PENDING':'STOPPED');
  b.textContent=u;
  return b;
 };
 function binding(id){return (data?.bindings||[]).find(b=>b.id===id)||null;}
 function fillTargets(){
  const kind=$('waf-binding-kind')?.value||'vps', sel=$('waf-binding-target');
  if(!sel)return;
  const keep=sel.value;
  sel.textContent='';
  sel.add(new Option('Select target…',''));
  (data?.targets||[]).filter(t=>t.kind===kind&&!t.bound).forEach(t=>{
   const label=`${t.name||t.id} · ${t.worker_id||'-'}${t.waf_state?` · ${t.waf_state}`:''}`;
   sel.add(new Option(label,t.id));
  });
  if([...sel.options].some(o=>o.value===keep))sel.value=keep;
 }
 function renderEngines(){
  const table=$('waf-engines-table');if(!table)return;
  row(table,['Worker','Hostname','Status','Engine','Version / error'],true);
  const list=data?.engines||[];
  if(!list.length){row(table,['No workers enrolled','','','','']);return;}
  list.forEach(e=>{
   const ready=!!e.ready;
   const badge=document.createElement('span');
   badge.className='badge '+(ready?'RUNNING':(e.error?'FAILED':'STOPPED'));
   badge.textContent=ready?'READY':(e.error?'ERROR':'NOT READY');
   const detail=ready?(e.version||'owasp/modsecurity-crs:nginx'):(e.error||(e.docker_ready===false?'Docker engine required':'Waiting for worker prepare…'));
   const live=document.createElement('span');
   live.className='badge '+(e.live?'RUNNING':'STOPPED');
   live.textContent=e.live?'Live':(e.status||'offline');
   row(table,[e.worker_id,e.hostname||'-',live,badge,detail]);
  });
 }
 function renderBindings(){
  const table=$('waf-bindings-table');if(!table)return;
  row(table,['Name','Target','Worker','Hits','Blocks','State','Actions'],true);
  const list=data?.bindings||[];
  if(!list.length){row(table,['No bindings yet. Create one to activate WAF for a VPS or Docker target.','','','','','','']);return;}
  list.forEach(b=>{
   const actions=document.createElement('div');actions.className='row-actions';
   const open=document.createElement('a');open.className='small-btn';open.href='/waf/bindings/'+encodeURIComponent(b.id);open.textContent='Open';
   const act=document.createElement('button');act.type='button';act.className='small-btn';
   act.textContent=b.state==='ACTIVE'||b.state==='ACTIVATING'?'Deactivate':'Activate';
   act.onclick=async()=>{
    try{
     if(b.state==='ACTIVE'||b.state==='ACTIVATING'){
      if(!confirm(`Deactivate WAF for ${b.name||b.target_id}?`))return;
      await api('POST',`/api/waf/bindings/${b.id}/deactivate`,{});
      msg('Deactivate queued.');
     }else{
      await api('POST',`/api/waf/bindings/${b.id}/activate`,{});
      msg('Activate queued.');
     }
     await refreshWAF();
    }catch(e){msg(e.message);}
   };
   const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';
   del.onclick=async()=>{
    if(!confirm(`Delete binding ${b.name||b.id}? Active WAF will be deactivated.`))return;
    try{await api('DELETE',`/api/waf/bindings/${b.id}`);msg('Binding deleted.');await refreshWAF();}catch(e){msg(e.message);}
   };
   actions.append(open,act,del);
   const nameLink=document.createElement('a');
   nameLink.className='waf-name-btn';nameLink.href='/waf/bindings/'+encodeURIComponent(b.id);
   nameLink.textContent=b.name||b.id;nameLink.title='Open binding details';
   const target=`${b.target_kind} · ${b.target_id}`;
   const st=stateBadge(b.state);
   if(b.error){st.title=b.error;st.textContent=(b.state||'FAILED')+' · error';}
   const hits=b.stats!=null?String(b.stats.hits??0):'-';
   const blocks=b.stats!=null?String(b.stats.blocks??0):'-';
   row(table,[nameLink,target,b.worker_id||'-',hits,blocks,st,actions]);
  });
 }
 const wafHistKey=id=>'cf_waf_stats_hist_v1_'+id;
 function loadWAFHistory(id){
  try{const raw=JSON.parse(localStorage.getItem(wafHistKey(id))||'[]');return Array.isArray(raw)?raw:[];}catch(e){return [];}
 }

 async function loadWAFServerHistory(id){
  if(!id||typeof fetchMetricsSeries!=='function'||typeof mergeMetricsPoints!=='function')return loadWAFHistory(id);
  const w=wafTrafficBounds();
  const from=Math.max(0,w.from||(Date.now()-wafRangeMs['1d']));
  const to=Math.min(Number.isFinite(w.to)?w.to:Date.now(),Date.now()+60000);
  const pts=await fetchMetricsSeries('waf:'+id,from,to);
  let hist=loadWAFHistory(id);
  if(pts.length){
   hist=mergeMetricsPoints(hist,pts);
   try{localStorage.setItem(wafHistKey(id),JSON.stringify(hist));}catch(e){}
  }
  return hist;
 }
 function pushWAFHistory(id,s){
  if(!id||!s)return loadWAFHistory(id);
  const at=Date.parse(s.at)||Date.now();
  const point={at,hits:Number(s.hits)||0,blocks:Number(s.blocks)||0,attacks:Number(s.attacks)||0,unique_ips:Number(s.unique_ips)||0};
  let hist=loadWAFHistory(id);
  const last=hist[hist.length-1];
  // Avoid duplicate samples from the same stats snapshot.
  if(last&&Math.abs(last.at-point.at)<2000&&last.hits===point.hits&&last.blocks===point.blocks)return hist;
  hist.push(point);
  if(hist.length>120)hist=hist.slice(-120);
  try{localStorage.setItem(wafHistKey(id),JSON.stringify(hist));}catch(e){}
  return hist;
 }
 function sparkline(values,tone){
  const svg=document.createElementNS('http://www.w3.org/2000/svg','svg');
  svg.setAttribute('class','waf-stat-spark'+(tone?' spark-'+tone:''));
  svg.setAttribute('viewBox','0 0 120 36');
  svg.setAttribute('preserveAspectRatio','none');
  svg.setAttribute('aria-hidden','true');
  const pts=values.length?values:[0];
  const max=Math.max(1,...pts);
  const step=pts.length<2?120:120/(pts.length-1);
  const coords=pts.map((v,i)=>{
   const x=i*step,y=34-(v/max)*30;
   return [x,Math.max(2,Math.min(34,y))];
  });
  const line=coords.map((p,i)=>(i?'L':'M')+p[0].toFixed(1)+' '+p[1].toFixed(1)).join(' ');
  const area=line+' L'+coords[coords.length-1][0].toFixed(1)+' 36 L0 36 Z';
  const fill=document.createElementNS('http://www.w3.org/2000/svg','path');
  fill.setAttribute('d',area);fill.setAttribute('class','spark-fill');
  const path=document.createElementNS('http://www.w3.org/2000/svg','path');
  path.setAttribute('d',line);path.setAttribute('class','spark-line');
  svg.append(fill,path);
  return svg;
 }
 function wafPct(part,whole){
  const p=Number(part)||0,w=Number(whole)||0;
  return w>0?Math.max(0,Math.min(100,(p/w)*100)):0;
 }
 function setWAFRateDonut(arcID,numID,pct){
  const arc=$(arcID),num=$(numID);
  const v=Math.max(0,Math.min(100,Number(pct)||0));
  if(arc)arc.style.strokeDasharray=v.toFixed(1)+' 100';
  if(num)num.textContent=Number.isFinite(pct)?Math.round(v)+'%':'-';
 }
 function renderWAFMixPie(hits,blocks,attacks){
  const svg=$('wafb-pie-mix'),legend=$('wafb-pie-mix-legend');
  if(!svg)return;
  const h=Math.max(0,Number(hits)||0);
  const b=Math.max(0,Number(blocks)||0);
  const a=Math.max(0,Number(attacks)||0);
  const clean=Math.max(0,h-b);
  const slices=[
   {key:'clean',label:'Clean',value:clean,color:'#43d6c5'},
   {key:'blocks',label:'Blocks',value:b,color:'#f4c56a'},
   {key:'attacks',label:'Attacks',value:a,color:'#ff7a8c'},
  ];
  const total=slices.reduce((n,s)=>n+s.value,0)||1;
  svg.replaceChildren();
  const track=document.createElementNS('http://www.w3.org/2000/svg','circle');
  track.setAttribute('class','waf-donut-track');
  track.setAttribute('cx','60');track.setAttribute('cy','60');track.setAttribute('r','44');
  svg.append(track);
  let offset=0;
  slices.forEach(s=>{
   if(s.value<=0)return;
   const len=(s.value/total)*100;
   const c=document.createElementNS('http://www.w3.org/2000/svg','circle');
   c.setAttribute('class','waf-donut-slice');
   c.setAttribute('cx','60');c.setAttribute('cy','60');c.setAttribute('r','44');
   c.setAttribute('pathLength','100');
   c.style.stroke=s.color;
   c.style.strokeDasharray=`${len.toFixed(2)} ${(100-len).toFixed(2)}`;
   c.style.strokeDashoffset=String((-offset).toFixed(2));
   svg.append(c);
   offset+=len;
  });
  const center=document.createElementNS('http://www.w3.org/2000/svg','text');
  center.setAttribute('class','waf-donut-num');
  center.setAttribute('x','60');center.setAttribute('y','58');center.setAttribute('text-anchor','middle');
  center.textContent=String(h||0);
  const cap=document.createElementNS('http://www.w3.org/2000/svg','text');
  cap.setAttribute('class','waf-donut-caption');
  cap.setAttribute('x','60');cap.setAttribute('y','74');cap.setAttribute('text-anchor','middle');
  cap.textContent='hits';
  svg.append(center,cap);
  if(legend){
   legend.textContent='';
   slices.forEach(s=>{
    const li=document.createElement('li');
    const pct=total?Math.round((s.value/total)*100):0;
    li.innerHTML=`<i style="background:${s.color}"></i><span>${s.label}</span><strong>${s.value}</strong><em>${pct}%</em>`;
    legend.append(li);
   });
  }
 }
 function renderWAFInsights(s){
  const hits=Number(s?.hits)||0;
  const blocks=Number(s?.blocks)||0;
  const attacks=Number(s?.attacks)||0;
  const ips=Number(s?.unique_ips)||0;
  const sampled=Number(s?.sampled)||0;
  const clean=Math.max(0,hits-blocks);
  const blockPct=wafPct(blocks,hits);
  const attackPct=wafPct(attacks,hits);
  renderWAFMixPie(hits,blocks,attacks);
  setWAFRateDonut('wafb-pie-block-arc','wafb-pie-block-num',s?blockPct:NaN);
  setWAFRateDonut('wafb-pie-attack-arc','wafb-pie-attack-num',s?attackPct:NaN);
  const blockFacts=$('wafb-pie-block-facts');
  if(blockFacts)blockFacts.innerHTML=`<div><span>Blocks</span><strong>${s?blocks:'-'}</strong></div><div><span>Clean</span><strong>${s?clean:'-'}</strong></div><div><span>Hits</span><strong>${s?hits:'-'}</strong></div>`;
  const attackFacts=$('wafb-pie-attack-facts');
  if(attackFacts)attackFacts.innerHTML=`<div><span>Attacks</span><strong>${s?attacks:'-'}</strong></div><div><span>Unique IPs</span><strong>${s?ips:'-'}</strong></div><div><span>Sampled</span><strong>${s?sampled:'-'}</strong></div>`;
  if($('wafb-num-sampled'))$('wafb-num-sampled').textContent=s?String(sampled):'-';
  if($('wafb-num-clean'))$('wafb-num-clean').textContent=s?String(clean):'-';
  if($('wafb-num-blocks'))$('wafb-num-blocks').textContent=s?String(blocks):'-';
  if($('wafb-num-attacks'))$('wafb-num-attacks').textContent=s?String(attacks):'-';
  if($('wafb-num-ips'))$('wafb-num-ips').textContent=s?String(ips):'-';
  if($('wafb-num-proxy'))$('wafb-num-proxy').textContent=s?.proxy||'-';
 }
 function renderWAFCharts(id,hist){
  if(typeof drawCloudChart!=='function')return;
  const bounds=wafTrafficBounds();
  const seriesTraffic=[
   {key:'hits',label:'Hits',color:'#43d6c5',area:true},
   {key:'blocks',label:'Blocks',color:'#f4c56a',area:true},
   {key:'attacks',label:'Attacks',color:'#ff7a8c',area:true},
  ];
  drawCloudChart('chart-waf-traffic',{
   history:hist,
   windowBounds:bounds,
   series:seriesTraffic,
   currentID:'chart-waf-traffic-current',
   formatCurrent:last=>last?`${last.hits||0} hits · ${last.blocks||0} blocks · ${last.attacks||0} attacks`:'-',
  });
  drawCloudChart('chart-waf-ips',{
   history:hist,
   windowBounds:bounds,
   series:[{key:'unique_ips',label:'Unique IPs',color:'#77baff',area:true}],
   currentID:'chart-waf-ips-current',
   formatCurrent:last=>last?String(last.unique_ips||0)+' IPs':'-',
  });
  const rateHist=(hist||[]).map(p=>{
   const hits=Number(p.hits)||0;
   return {
    ...p,
    block_pct:wafPct(p.blocks,hits),
    attack_pct:wafPct(p.attacks,hits),
   };
  });
  drawCloudChart('chart-waf-rates',{
   history:rateHist,
   windowBounds:bounds,
   minMax:100,
   series:[
    {key:'block_pct',label:'Block %',color:'#f4c56a',area:true},
    {key:'attack_pct',label:'Attack %',color:'#ff7a8c',area:true},
   ],
   currentID:'chart-waf-rates-current',
   formatCurrent:last=>last?`${Math.round(last.block_pct||0)}% block · ${Math.round(last.attack_pct||0)}% attack`:'-',
   formatValue:v=>Math.round(Number(v)||0)+'%',
  });
 }
 function withScroll(run){
  if(typeof preservePageScroll==='function')return preservePageScroll(run);
  const x=window.scrollX,y=window.scrollY;
  const out=run();
  const restore=()=>{window.scrollTo(x,y);requestAnimationFrame(()=>window.scrollTo(x,y));};
  if(out&&typeof out.then==='function')return out.finally(restore);
  restore();
  return out;
 }
 function renderStats(b){
  const kpis=$('wafb-stats-kpis'), note=$('wafb-stats-note');
  const s=b?.stats;
  let hist=s?pushWAFHistory(b.id,s):loadWAFHistory(b?.id||editID);
  const wid=b?.id||editID;if(wid){loadWAFServerHistory(wid).then(h=>{if(Array.isArray(h)&&h.length){hist=h;renderWAFCharts(wid,hist);}}).catch(()=>{});}
  const series=key=>hist.map(p=>Number(p[key])||0);
  const cards=[
   ['Hits',s?String(s.hits??0):'-','hits','teal'],
   ['Blocks',s?String(s.blocks??0):'-','blocks','amber'],
   ['Attacks',s?String(s.attacks??0):'-','attacks','rose'],
   ['Unique IPs',s?String(s.unique_ips??0):'-','unique_ips','blue'],
  ];
  if(kpis){
   // Update cards in place so refresh does not jump scroll.
   if(kpis.children.length===cards.length){
    [...kpis.children].forEach((d,i)=>{
     const [,val,key,tone]=cards[i];
     d.dataset.tone=tone;
     const v=d.querySelector('.waf-stat-value');
     if(v)v.textContent=val;
     const old=d.querySelector('.waf-stat-spark');
     const next=sparkline(series(key),tone==='teal'?'':tone);
     if(old)old.replaceWith(next);else d.append(next);
    });
   }else{
    kpis.textContent='';
    cards.forEach(([label,val,key,tone])=>{
     const d=document.createElement('div');d.className='waf-stat';d.dataset.tone=tone;
     const l=document.createElement('span');l.className='waf-stat-label';l.textContent=label;
     const v=document.createElement('div');v.className='waf-stat-value';v.textContent=val;
     d.append(l,v,sparkline(series(key),tone==='teal'?'':tone));kpis.append(d);
    });
   }
  }
  renderWAFCharts(b?.id||editID,hist);
  renderWAFInsights(s);
  if(note){
   if(!s)note.textContent='No samples yet - click Refresh stats to pull edge proxy logs.';
   else{
    const when=s.at?new Date(s.at).toLocaleString():'-';
    const blockPct=wafPct(s.blocks,s.hits),attackPct=wafPct(s.attacks,s.hits);
    note.textContent=`Sampled ${s.sampled||0} events from ${s.proxy||'proxy'} · ${Math.round(blockPct)}% blocked · ${Math.round(attackPct)}% attacks · updated ${when}${s.error?' · '+s.error:''}`;
   }
  }
  renderTrafficTables(s,b?.id||editID);
 }
 const wafEventsKey=id=>'cf_waf_events_v1_'+id;
 const wafMonths={Jan:0,Feb:1,Mar:2,Apr:3,May:4,Jun:5,Jul:6,Aug:7,Sep:8,Oct:9,Nov:10,Dec:11};
 function toWAFLocal(ms){
  if(!ms)return '';
  const d=new Date(ms),p=n=>String(n).padStart(2,'0');
  return `${d.getFullYear()}-${p(d.getMonth()+1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
 }
 function formatWAFEventTime(ms,fallback){
  if(!Number.isFinite(ms))return fallback||'-';
  try{return new Date(ms).toLocaleString();}catch(e){return fallback||'-';}
 }
 function wafTrafficBounds(){
  const now=Date.now();
  if(wafTrafficRange==='all')return {from:now-wafRangeMs['30d'],to:now,all:true};
  if(wafTrafficRange==='custom'&&wafTrafficCustom.from&&wafTrafficCustom.to){
   return {from:Math.min(wafTrafficCustom.from,wafTrafficCustom.to),to:Math.max(wafTrafficCustom.from,wafTrafficCustom.to)};
  }
  const span=wafRangeMs[wafTrafficRange]||wafRangeMs['1d'];
  return {from:now-span,to:now};
 }
 // ModSecurity time_stamp is UTC with no zone suffix ("Wed Nov 18 17:31:45 2026").
 // Date.parse() would treat that as local time and skew filters by the browser offset.
 function parseWAFEventTime(raw){
  if(raw==null||raw==='')return NaN;
  if(typeof raw==='number'&&Number.isFinite(raw))return raw>1e12?raw:raw*1000;
  const s=String(raw).trim();
  if(!s)return NaN;
  let m=s.match(/^[A-Za-z]{3}\s+([A-Za-z]{3})\s+(\d{1,2})\s+(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?\s+(\d{4})$/);
  if(m){
   const mon=wafMonths[m[1]];
   if(mon==null)return NaN;
   return Date.UTC(+m[6],mon,+m[2],+m[3],+m[4],+m[5]);
  }
  // nginx access: 18/Nov/2026:17:31:45 +0000
  m=s.match(/^\[?(\d{1,2})\/([A-Za-z]{3})\/(\d{4}):(\d{2}):(\d{2}):(\d{2})\s*([+-]\d{4})\]?$/);
  if(m){
   const mon=wafMonths[m[2]];
   if(mon==null)return NaN;
   const off=m[7],sign=off[0]==='-'?-1:1,oh=+off.slice(1,3),om=+off.slice(3,5);
   const utc=Date.UTC(+m[3],mon,+m[1],+m[4],+m[5],+m[6])-sign*((oh*60+om)*60000);
   return utc;
  }
  if(/^\d{4}-\d{2}-\d{2}/.test(s)){
   const t=Date.parse(s);
   if(Number.isFinite(t))return t;
  }
  return NaN;
 }
 function eventFingerprint(e,t){
  return [t||0,e.ip||'',e.method||'',e.uri||'',e.status||'',e.attack||'',e.rule_id||'',e.message||''].join('|');
 }
 function loadWAFEvents(id){
  if(!id)return [];
  try{
   const raw=JSON.parse(localStorage.getItem(wafEventsKey(id))||'[]');
   return Array.isArray(raw)?raw.filter(e=>e&&typeof e==='object'):[];
  }catch(e){return [];}
 }
 function mergeWAFEvents(id,recent){
  if(!id)return Array.isArray(recent)?recent:[];
  const cutoff=Date.now()-wafRangeMs['30d'];
  const map=new Map();
  loadWAFEvents(id).forEach(e=>{
   const t=Number(e._at)||parseWAFEventTime(e.time);
   if(Number.isFinite(t)&&t<cutoff)return;
   map.set(eventFingerprint(e,t),{...e,_at:Number.isFinite(t)?t:0});
  });
  (recent||[]).forEach(e=>{
   const t=parseWAFEventTime(e.time);
   map.set(eventFingerprint(e,t),{...e,_at:Number.isFinite(t)?t:0});
  });
  const merged=[...map.values()].sort((a,b)=>(b._at||0)-(a._at||0)).slice(0,2500);
  try{localStorage.setItem(wafEventsKey(id),JSON.stringify(merged));}catch(e){}
  return merged;
 }
 function topFromMap(map,n){
  return Object.entries(map).sort((a,b)=>b[1]-a[1]||a[0].localeCompare(b[0])).slice(0,n).map(([key,count])=>({key,count}));
 }
 function filterWAFTraffic(s,bindingID){
  if(!s&&!bindingID)return {stats:null,recent:[],filtered:false,total:0};
  const snapshot=Array.isArray(s?.recent)?s.recent:[];
  const all=mergeWAFEvents(bindingID||editID,snapshot);
  const w=wafTrafficBounds();
  const timed=all.map(e=>{
   const t=Number(e._at)||parseWAFEventTime(e.time);
   return {e,t:Number.isFinite(t)?t:NaN};
  });
  const dated=timed.filter(x=>Number.isFinite(x.t));
  if(!dated.length){
   const fallback=snapshot.length?snapshot:all;
   return {stats:s||null,recent:fallback,filtered:false,total:fallback.length,undated:true};
  }
  const kept=[];
  const ipN={},uriN={},atkN={};
  timed.forEach(({e,t})=>{
   if(!Number.isFinite(t)||t<w.from||t>w.to)return;
   kept.push(e);
   if(e.ip)ipN[e.ip]=(ipN[e.ip]||0)+1;
   if(e.uri)uriN[e.uri]=(uriN[e.uri]||0)+1;
   if(e.attack&&e.attack!=='attack-protocol')atkN[e.attack]=(atkN[e.attack]||0)+1;
   else if(e.blocked)atkN.blocked=(atkN.blocked||0)+1;
  });
  kept.sort((a,b)=>(Number(b._at)||parseWAFEventTime(b.time)||0)-(Number(a._at)||parseWAFEventTime(a.time)||0));
  return {
   stats:{
    ...(s||{}),
    top_ips:topFromMap(ipN,10),
    top_uris:topFromMap(uriN,10),
    top_attacks:topFromMap(atkN,10),
   },
   recent:kept,
   filtered:wafTrafficRange!=='all',
   total:dated.length,
   matched:kept.length,
  };
 }
 function updateWAFRangeButtons(){
  document.querySelectorAll('[data-waf-range]').forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.wafRange===wafTrafficRange)));
  const custom=$('wafb-traffic-custom');
  if(custom)custom.hidden=wafTrafficRange!=='custom';
  if(wafTrafficRange==='custom'){
   if($('wafb-traffic-from'))$('wafb-traffic-from').value=toWAFLocal(wafTrafficCustom.from);
   if($('wafb-traffic-to'))$('wafb-traffic-to').value=toWAFLocal(wafTrafficCustom.to);
  }
 }
 function saveWAFTrafficRange(next){
  wafTrafficRange=next;
  localStorage.setItem(wafRangeKey,wafTrafficRange);
  updateWAFRangeButtons();
  const b=binding(editID);
  renderTrafficTables(b?.stats,b?.id||editID);
  if(b?.id){
   loadWAFServerHistory(b.id).then(h=>{if(Array.isArray(h))renderWAFCharts(b.id,h);}).catch(()=>{});
  }
 }
 function renderTrafficTables(s,bindingID){
  const view=filterWAFTraffic(s,bindingID||editID);
  const note=$('wafb-traffic-note');
  if(note){
   if(!s&&!(view.total))note.textContent='No samples yet - refresh stats from Overview.';
   else if(view.undated)note.textContent=`Showing ${view.total} events (no parseable timestamps).`;
   else if(wafTrafficRange==='all')note.textContent=`Showing ${view.matched||0} retained events (up to 30 days).`;
   else note.textContent=`Showing ${view.matched||0} of ${view.total} events in ${wafTrafficRange==='custom'?'custom range':wafTrafficRange}.`;
  }
  const fill=(id,rows,empty)=>{
   const t=$(id);if(!t)return;
   row(t,['Key','Count'],true);
   if(!rows||!rows.length){row(t,[empty,'']);return;}
   rows.forEach(r=>row(t,[r.key,String(r.count)]));
  };
  const stats=view.stats;
  fill('wafb-stats-ips',stats?.top_ips,'No IPs in range');
  fill('wafb-stats-uris',stats?.top_uris,'No URIs in range');
  fill('wafb-stats-attacks',stats?.top_attacks,'No attacks in range');
  const recent=$('wafb-stats-recent');
  if(recent){
   row(recent,['Time','IP','Method','URI','Status','Attack','Detail'],true);
   const list=view.recent||[];
   if(!list.length){row(recent,['No events in selected range','','','','','','']);}
   else list.slice(0,100).forEach(e=>{
    const badge=document.createElement('span');
    badge.className=e.blocked?'badge FAILED':'badge RUNNING';
    badge.textContent=e.status||(e.blocked?'block':'ok');
    const when=formatWAFEventTime(Number(e._at)||parseWAFEventTime(e.time),e.time||'-');
    row(recent,[when,e.ip||'-',e.method||'-',e.uri||'-',badge,e.attack||'-',e.message||e.rule_id||'-']);
   });
  }
 }
 async function refreshStats(id){
  if(!id)return;
  try{
   await api('POST',`/api/waf/bindings/${id}/stats/refresh`,{});
   if($('wafb-stats-note'))$('wafb-stats-note').textContent='Refreshing from edge proxy…';
  }catch(e){detailMsg(e.message);}
 }
 function renderEditRules(b){
  const controls=$('wafb-controls');if(!controls)return;
  const active=b.state==='ACTIVE';
  controls.textContent='';
  items().forEach(([key,label,tag])=>{
   const item=document.createElement('div');item.className='waf-rule';
   const info=document.createElement('div');info.className='waf-rule-info';
   const title=document.createElement('div');title.className='waf-rule-title';title.textContent=label;
   const code=document.createElement('div');code.className='waf-rule-tag';code.textContent=tag;
   info.append(title,code);
   const toggle=document.createElement('button');toggle.type='button';toggle.className='waf-toggle';toggle.setAttribute('role','switch');
   const enabled=!b||b.owasp_protection?.[key]!==false;
   toggle.setAttribute('aria-checked',String(enabled));
   toggle.disabled=!active;
   toggle.onclick=async()=>{
    toggle.disabled=true;detailMsg('Queueing rule update…');
    try{
     await api('POST',`/api/waf/bindings/${b.id}/category`,{category:key,enabled:!enabled});
     detailMsg('Rule update queued.');
     await refreshWAF();
    }catch(e){detailMsg(e.message);toggle.disabled=false;}
   };
   item.append(info,toggle);controls.append(item);
  });
  const enabledCount=items().filter(([key])=>!b||b.owasp_protection?.[key]!==false).length;
  if($('wafb-rule-summary'))$('wafb-rule-summary').textContent=active?`${enabledCount} of ${items().length} rule groups enabled`:'Activate the binding to manage rules.';
 }
 function renderErrorTabs(b){
  const tabs=$('wafb-error-tabs');if(!tabs)return;
  if(tabs.children.length!==statuses.length){
   tabs.textContent='';
   statuses.forEach(code=>{
    const btn=document.createElement('button');btn.type='button';btn.className='waf-status-tab';btn.dataset.status=code;btn.textContent=code;btn.setAttribute('role','tab');
    btn.onclick=()=>{if(errorDirty&&!confirm('Discard unsaved changes?'))return;errorStatus=code;errorDirty=false;errorSource='';renderErrorEditor(b);};
    tabs.append(btn);
   });
  }
  [...tabs.children].forEach(btn=>btn.setAttribute('aria-selected',String(btn.dataset.status===errorStatus)));
 }
 function renderErrorEditor(b){
  renderErrorTabs(b);
  if($('wafb-error-label'))$('wafb-error-label').textContent=errorStatus;
  const html=$('wafb-error-html');
  const key=(b?.id||'')+':'+errorStatus;
  if(html&&errorSource!==key&&!errorDirty){
   const custom=(b?.waf_error_pages||{})[errorStatus];
   html.value=(custom&&String(custom).trim())||defaultHTML(errorStatus);
   errorSource=key;
  }
 }
 let detailHydrated=false, lastDetailState='';
 function renderDetail(b, opts){
  opts=opts||{};
  withScroll(()=>{
   if(!b){
    if($('wafb-title'))$('wafb-title').textContent='Binding not found';
    detailMsg('This WAF binding no longer exists.');
    return;
   }
   if($('wafb-title'))$('wafb-title').textContent=b.name||b.id;
   if($('wafb-meta'))$('wafb-meta').textContent=`${b.target_kind} · ${b.target_id} · worker ${b.worker_id||'-'}${b.error?' · '+b.error:''}`;
   const st=$('wafb-state');
   if(st){st.className=stateBadge(b.state).className;st.textContent=b.state||'INACTIVE';}
   if($('wafb-activate'))$('wafb-activate').disabled=b.state==='ACTIVE'||b.state==='ACTIVATING';
   if($('wafb-deactivate'))$('wafb-deactivate').disabled=b.state==='INACTIVE'||b.state==='DEACTIVATING';
   renderStats(b);
   // Poll refresh: skip rebuilding rules/error editor (causes scroll jump).
   if(!opts.statsOnly){
    renderEditRules(b);
    renderErrorEditor(b);
   }
   const title=$('page-title');
   if(title)title.textContent=b.name||'WAF Binding';
  });
 }
 async function refreshWAF(){
  data=await api('GET','/api/waf');
  if(isList){
   withScroll(()=>{renderEngines();renderBindings();fillTargets();});
  }
  if(isDetail&&editID){
   const b=binding(editID);
   const stateChanged=!!b&&b.state!==lastDetailState;
   renderDetail(b,{statsOnly:detailHydrated&&!stateChanged});
   lastDetailState=b?.state||'';
   detailHydrated=true;
  }
 }
 function openCreate(){
  if($('waf-binding-id'))$('waf-binding-id').value='';
  if($('waf-binding-name'))$('waf-binding-name').value='';
  if($('waf-binding-activate'))$('waf-binding-activate').checked=true;
  if($('waf-binding-title'))$('waf-binding-title').textContent='New WAF binding';
  if($('waf-binding-save'))$('waf-binding-save').textContent='Create binding';
  formMsg('');
  fillTargets();
  $('waf-binding-dialog')?.showModal();
 }

 if(isList){
  if($('waf-binding-add'))$('waf-binding-add').onclick=openCreate;
  if($('waf-binding-close'))$('waf-binding-close').onclick=()=>$('waf-binding-dialog')?.close();
  if($('waf-binding-cancel'))$('waf-binding-cancel').onclick=()=>$('waf-binding-dialog')?.close();
  if($('waf-binding-kind'))$('waf-binding-kind').onchange=()=>fillTargets();
  if($('waf-binding-form'))$('waf-binding-form').onsubmit=async e=>{
   e.preventDefault();
   const kind=$('waf-binding-kind').value, target_id=$('waf-binding-target').value;
   if(!target_id){formMsg('Select a target.');return;}
   const body={name:($('waf-binding-name').value||'').trim(),target_kind:kind,target_id,activate:!!$('waf-binding-activate')?.checked};
   formMsg('Creating…');
   try{
    const created=await api('POST','/api/waf/bindings',body);
    formMsg('');
    $('waf-binding-dialog')?.close();
    msg('Binding created.');
    if(created?.id){location.assign('/waf/bindings/'+encodeURIComponent(created.id));return;}
    await refreshWAF();
   }catch(err){formMsg(err.message);}
  };
 }

 if(isDetail){
  updateWAFRangeButtons();
  document.querySelectorAll('[data-waf-range]').forEach(btn=>{
   btn.addEventListener('click',()=>{
    if(btn.dataset.wafRange==='custom'){
     if(!(wafTrafficCustom.from&&wafTrafficCustom.to)){
      wafTrafficCustom={from:Date.now()-86400000,to:Date.now()};
      localStorage.setItem(wafCustomKey,JSON.stringify(wafTrafficCustom));
     }
     saveWAFTrafficRange('custom');
     return;
    }
    saveWAFTrafficRange(btn.dataset.wafRange);
   });
  });
  if($('wafb-traffic-apply'))$('wafb-traffic-apply').onclick=()=>{
   const from=Date.parse($('wafb-traffic-from')?.value||'');
   const to=Date.parse($('wafb-traffic-to')?.value||'');
   if(!Number.isFinite(from)||!Number.isFinite(to)||from===to){
    if($('wafb-traffic-note'))$('wafb-traffic-note').textContent='Pick a valid From and To date.';
    return;
   }
   wafTrafficCustom={from:Math.min(from,to),to:Math.max(from,to)};
   localStorage.setItem(wafCustomKey,JSON.stringify(wafTrafficCustom));
   saveWAFTrafficRange('custom');
  };
  if($('wafb-stats-refresh'))$('wafb-stats-refresh').onclick=()=>{if(editID)refreshStats(editID);};
  if($('wafb-activate'))$('wafb-activate').onclick=async()=>{
   if(!editID)return;
   try{await api('POST',`/api/waf/bindings/${editID}/activate`,{});detailMsg('Activate queued.');await refreshWAF();}catch(e){detailMsg(e.message);}
  };
  if($('wafb-deactivate'))$('wafb-deactivate').onclick=async()=>{
   if(!editID)return;
   if(!confirm('Deactivate WAF for this binding?'))return;
   try{await api('POST',`/api/waf/bindings/${editID}/deactivate`,{});detailMsg('Deactivate queued.');await refreshWAF();}catch(e){detailMsg(e.message);}
  };
  if($('wafb-delete'))$('wafb-delete').onclick=async()=>{
   if(!editID)return;
   if(!confirm('Delete this binding? Active WAF will be deactivated.'))return;
   try{await api('DELETE',`/api/waf/bindings/${editID}`);location.assign('/waf#waf-bindings');}catch(e){detailMsg(e.message);}
  };
  if($('wafb-error-html'))$('wafb-error-html').oninput=()=>{errorDirty=true;};
  if($('wafb-error-preview'))$('wafb-error-preview').onclick=()=>{
   const html=$('wafb-error-html')?.value||defaultHTML(errorStatus);
   const blob=new Blob([html],{type:'text/html'});const url=URL.createObjectURL(blob);window.open(url,'_blank','noopener');setTimeout(()=>URL.revokeObjectURL(url),60000);
  };
  if($('wafb-error-default'))$('wafb-error-default').onclick=()=>{
   if($('wafb-error-html')){$('wafb-error-html').value=defaultHTML(errorStatus);errorDirty=true;if($('wafb-error-message'))$('wafb-error-message').textContent='Default HTML loaded - click Save.';}
  };
  if($('wafb-error-save'))$('wafb-error-save').onclick=async()=>{
   if(!editID)return;
   const html=($('wafb-error-html')?.value||'').trim(),m=$('wafb-error-message');
   if(m)m.textContent='Saving…';
   try{
    await api('PUT',`/api/waf/bindings/${editID}/error-pages`,{status:errorStatus,html});
    errorDirty=false;errorSource=editID+':'+errorStatus;
    if(m)m.textContent=`${errorStatus} error page saved.`;
    await refreshWAF();
   }catch(e){if(m)m.textContent=e.message;}
  };
 }

 async function loop(){
  try{
   await refreshWAF();
   if(isList)msg('');
   if(isDetail&&editID&&!binding(editID)?.stats)refreshStats(editID);
   else if(isDetail&&editID){/* keep polling via refreshWAF */}
   if(isDetail&&editID){
    // first load: always kick a refresh once
    if(!loop._statsOnce){loop._statsOnce=true;refreshStats(editID);}
   }
  }catch(e){if(isList)msg(e.message);else detailMsg(e.message);}
 }
 loop();
 setInterval(()=>{if(document.hidden)return;loop();},15000);
})();
