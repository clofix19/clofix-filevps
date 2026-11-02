(()=>{
 const input=$('console-search-input'),results=$('console-search-results'),clear=$('console-search-clear'),servicesBtn=$('console-services');
 if(!input||!results)return;
 const icon={
  dashboard:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m3 10 9-7 9 7v10a1 1 0 0 1-1 1h-6v-7h-4v7H4a1 1 0 0 1-1-1z"/></svg>',
  vps:'<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M8 8h8M8 12h8M8 16h4"/></svg>',
  library:'<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="4" width="18" height="6" rx="2"/><rect x="3" y="14" width="18" height="6" rx="2"/><path d="M7 7h.01M7 17h.01M11 7h6M11 17h6"/></svg>',
  jobs:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m8 5 11 7-11 7z"/></svg>',
  volumes:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m12 3 9 5-9 5-9-5zM3 12l9 5 9-5M3 16l9 5 9-5"/></svg>',
  storage:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7.5 12 3l8 4.5v9L12 21l-8-4.5z"/><path d="m4 7.5 8 4.5 8-4.5M12 12v9"/></svg>',
  backups:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16v13H4zM8 7V4h8v3M8 12h8M8 16h5"/></svg>',
  databases:'<svg viewBox="0 0 24 24" aria-hidden="true"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v7c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 12v7c0 1.7 3.6 3 8 3s8-1.3 8-3v-7"/></svg>',
  lb:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 4v7M4 20v-9h16v9M12 11v9"/><circle cx="12" cy="4" r="2"/></svg>',
  vpc:'<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="8" width="7" height="8" rx="1"/><rect x="14" y="8" width="7" height="8" rx="1"/><path d="M10 12h4M7 8V6h10v2"/></svg>',
  dns:'<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18"/></svg>',
  health:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12h5l3-8 4 16 3-8h5"/></svg>',
  firewall:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 6h16v4H4zM4 14h7v4H4zM13 14h7v4h-7z"/></svg>',
  security:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3 20 6v5c0 5-3.4 8.5-8 10-4.6-1.5-8-5-8-10V6z"/><path d="m9 12 2 2 4-4"/></svg>',
  iam:'<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="9" cy="8" r="3"/><path d="M3.5 19c.6-3 2.4-4.5 5.5-4.5s4.9 1.5 5.5 4.5M16 6.5a3 3 0 0 1 0 5.8M17 15c2 .5 3.1 1.8 3.5 4"/></svg>',
  workers:'<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="4" y="3" width="16" height="18" rx="2"/><path d="M8 7h8M8 11h8M8 15h5"/></svg>',
  billing:'<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="6" width="18" height="13" rx="2"/><path d="M3 10h18M7 15h4"/></svg>',
  logs:'<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="5" y="4" width="14" height="16" rx="1"/><path d="M8 8h8M8 12h8M8 16h5"/></svg>',
  settings:'<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1 1.4 1.1-1.4 2.4-1.7-.6a8 8 0 0 1-1.8 1l-.3 1.8h-2.8l-.3-1.8a8 8 0 0 1-1.8-1l-1.7.6-1.4-2.4 1.4-1.1a1.7 1.7 0 0 0 .3-1.8 7 7 0 0 1 0-2 1.7 1.7 0 0 0-.3-1.8l-1.4-1.1 1.4-2.4 1.7.6a8 8 0 0 1 1.8-1l.3-1.8h2.8l.3 1.8a8 8 0 0 1 1.8 1l1.7-.6 1.4 2.4-1.4 1.1a1.7 1.7 0 0 0-.3 1.8 7 7 0 0 1 0 2z"/></svg>'
 };
 const catalog=[
  {name:'Dashboard',href:'/',group:'Overview',keys:'home dashboard overview',icon:icon.dashboard},
  {name:'FileVPS',href:'/instances',group:'Compute',keys:'vps instance compute linux',icon:icon.vps},
  {name:'Library',href:'/services',group:'Compute',keys:'apps catalog software managed',icon:icon.library},
  {name:'Jenkins',href:'/services#library-apps',group:'Compute',keys:'jenkins ci cd pipeline automation',icon:icon.jobs},
  {name:'Grafana',href:'/services#library-apps',group:'Compute',keys:'grafana dashboard metrics observability',icon:icon.jobs},
  {name:'Prometheus',href:'/services#library-apps',group:'Compute',keys:'prometheus metrics monitoring scrape',icon:icon.jobs},
  {name:'Loki',href:'/services#library-apps',group:'Compute',keys:'loki logs aggregation grafana',icon:icon.logs},
  {name:'Jaeger',href:'/services#library-apps',group:'Compute',keys:'jaeger tracing distributed otlp',icon:icon.jobs},
  {name:'OpenTelemetry Collector',href:'/services#library-apps',group:'Compute',keys:'otel opentelemetry collector otlp telemetry',icon:icon.jobs},
  {name:'Certificates',href:'/settings',group:'Networking',keys:'acme tls certificate lets encrypt https domain',icon:icon.security},
  {name:'Floating IPs',href:'/settings#settings-nlb',group:'Networking',keys:'floating ip nlb pool attach',icon:icon.lb},
  {name:'Marketplace',href:'/services#library-apps',group:'Compute',keys:'marketplace sku bdt wordpress one click',icon:icon.library},
  {name:'Jobs & Functions',href:'/applications',group:'Compute',keys:'jobs functions scripts',icon:icon.jobs},
  {name:'CKS',href:'/kubernetes',group:'Compute',keys:'cks k8s eks cluster kubernetes kubeadm helm ingress backup node pool namespace pod deployment service secret configmap pvc volume efs s3 lb',icon:icon.vps},
  {name:'Volumes',href:'/volumes',group:'Storage',keys:'volume disk persistent',icon:icon.volumes},
  {name:'EFS',href:'/efs',group:'Storage',keys:'efs share nfs web root load balancer files',icon:icon.storage},
  {name:'Object Storage',href:'/storage/s3',group:'Storage',keys:'s3 bucket object storage',icon:icon.storage},
  {name:'Backups',href:'/backups',group:'Storage',keys:'backup snapshot restore',icon:icon.backups},
  {name:'Databases',href:'/databases',group:'Storage',keys:'mysql postgres mongodb redis',icon:icon.databases},
  {name:'Load Balancers',href:'/load-balancers',group:'Networking',keys:'nlb alb load balancer',icon:icon.lb},
  {name:'VPC',href:'/vpc',group:'Networking',keys:'vpc cidr subnet ip block network',icon:icon.vpc},
  {name:'DNS Service',href:'/dns',group:'Networking',keys:'dns zone record hostname cks lb load balancer eks',icon:icon.dns},
  {name:'Health Checks',href:'/health-checks',group:'Networking',keys:'health ping monitor',icon:icon.health},
  {name:'Firewall',href:'/firewall',group:'Networking',keys:'firewall security group cidr nftables inbound worker vps',icon:icon.firewall},
  {name:'Security',href:'/security',group:'Security',keys:'protection waf ssh',icon:icon.security},
  {name:'IAM',href:'/iam',group:'Security',keys:'iam users keys policies',icon:icon.iam,kinds:['admin','root','iam']},
  {name:'Users',href:'/users',group:'Security',keys:'users partner client accounts',icon:icon.iam,kinds:['admin','partner']},
  {name:'My Profile',href:'/profile',group:'Account',keys:'profile mfa google authenticator allow ip deny ip password email',icon:icon.iam},
  {name:'Log Service',href:'/logs',group:'Platform',keys:'logs tail alert analysis download nginx syslog',icon:icon.logs},
  {name:'Workers',href:'/workers',group:'Platform',keys:'worker host capacity enroll',icon:icon.workers,kinds:['admin']},
  {name:'Billing & Payments',href:'/billing',group:'Platform',keys:'invoice payment wallet cks eks kubernetes load balancer',icon:icon.billing},
  {name:'Settings',href:'/settings',group:'Platform',keys:'defaults license pricing cks eks kubernetes load balancer',icon:icon.settings}
 ];
 const recentKey='cf_recent_services';
 function service(s){return catalog.find(c=>c.href===s.href)||s;}
 function recent(){try{return JSON.parse(localStorage.getItem(recentKey)||'[]').map(service);}catch(e){return[];}}
 function remember(){
  const path=location.pathname.replace(/\/$/,'')||'/';
  const match=catalog.find(s=>s.href==='/'?path==='/':path===s.href||path.startsWith(s.href+'/'));
  if(!match)return;
  try{localStorage.setItem(recentKey,JSON.stringify([{name:match.name,href:match.href,group:match.group},...recent().filter(x=>x.href!==match.href)].slice(0,6)));}catch(e){}
 }
 function visibleCatalog(){
  const kind=window.cfMe?.kind||document.body.dataset.kind||'';
  return catalog.filter(s=>!s.kinds||!kind||s.kinds.includes(kind));
 }
 function matchQuery(q){
  const list=visibleCatalog();
  if(!q)return list;
  return list.filter(s=>[s.name,s.group,s.keys,s.href].join(' ').toLowerCase().includes(q));
 }
 function groups(list){
  return list.reduce((acc,s)=>{
   const last=acc[acc.length-1];
   if(last&&last.name===s.group)last.items.push(s);
   else acc.push({name:s.group,items:[s]});
   return acc;
  },[]);
 }
 function item(s,active){
  const a=document.createElement('a');
  a.className='console-result'+(active?' is-active':'');
  a.href=s.href;a.role='option';a.setAttribute('aria-selected',active?'true':'false');
  a.innerHTML=`<span class="console-result-icon">${s.icon||icon.dashboard}</span><strong>${s.name}</strong>`;
  return a;
 }
 function heading(text){const h=document.createElement('div');h.className='console-result-label';h.textContent=text;return h;}
 function grid(list,activeFirst){
  const wrap=document.createElement('div');
  wrap.className='console-result-grid';
  list.forEach((s,i)=>wrap.append(item(s,activeFirst&&i===0)));
  return wrap;
 }
 function render(q){
  const query=q.trim().toLowerCase(),list=matchQuery(query);
  results.replaceChildren();
  if(!query){
   const rec=recent();
   if(rec.length){results.append(heading('Recently visited'),grid(rec,true));}
   groups(rec.length?catalog.filter(s=>!rec.some(r=>r.href===s.href)):catalog).forEach((g,i)=>{
    results.append(heading(g.name),grid(g.items,!rec.length&&i===0));
   });
  }else if(!list.length){
   const empty=document.createElement('p');empty.className='console-result-empty';empty.textContent='No matching CloFix service.';results.append(empty);
  }else results.append(grid(list,true));
  if(clear)clear.hidden=!query;
 }
 function fillMenu(){
  const panel=$('console-menu-list');
  if(!panel)return;
  panel.replaceChildren();
  const path=(location.pathname.replace(/\/$/,'')||'/');
  groups(visibleCatalog()).forEach(g=>{
   const label=document.createElement('p');
   label.textContent=g.name;
   panel.append(label);
   g.items.forEach(s=>{
    const a=document.createElement('a');
    a.href=s.href;
    a.setAttribute('role','menuitem');
    a.innerHTML=`<span class="console-result-icon">${s.icon||icon.dashboard}</span><strong>${s.name}</strong>`;
    if(s.href==='/'?path==='/':path===s.href||path.startsWith(s.href+'/'))a.setAttribute('aria-current','page');
    panel.append(a);
   });
  });
 }
 function closeMenu(){$('console-menu')?.removeAttribute('open');}
 function closeTheme(){$('console-theme')?.removeAttribute('open');}
 function openPanel(){closeMenu();closeTheme();results.hidden=false;input.setAttribute('aria-expanded','true');if(servicesBtn)servicesBtn.setAttribute('aria-expanded','true');render(input.value);}
 function closePanel(){results.hidden=true;input.setAttribute('aria-expanded','false');if(servicesBtn)servicesBtn.setAttribute('aria-expanded','false');}
 function options(){return Array.from(results.querySelectorAll('.console-result'));}
 function move(step){
  const items=options();if(!items.length)return;
  const i=items.findIndex(el=>el.classList.contains('is-active'));
  const next=items[(i+step+items.length)%items.length];
  items.forEach(el=>{el.classList.toggle('is-active',el===next);el.setAttribute('aria-selected',el===next?'true':'false');});
  next.scrollIntoView({block:'nearest'});
 }
 input.addEventListener('focus',openPanel);
 input.addEventListener('input',()=>openPanel());
 input.addEventListener('keydown',e=>{
  if(e.key==='ArrowDown'||e.key==='ArrowRight'){e.preventDefault();if(results.hidden)openPanel();move(1);}
  else if(e.key==='ArrowUp'||e.key==='ArrowLeft'){e.preventDefault();if(results.hidden)openPanel();move(-1);}
  else if(e.key==='Enter'){const active=results.querySelector('.console-result.is-active');if(active){e.preventDefault();location.href=active.href;}}
  else if(e.key==='Escape'){closePanel();input.blur();}
 });
 servicesBtn?.addEventListener('click',()=>{if(results.hidden){input.focus();openPanel();}else closePanel();});
 clear?.addEventListener('click',()=>{input.value='';input.focus();openPanel();});
 document.addEventListener('click',e=>{
  if(!e.target.closest('.console-search')&&!e.target.closest('#console-services'))closePanel();
  if(!e.target.closest('.console-more'))document.querySelectorAll('.console-more[open]').forEach(el=>el.removeAttribute('open'));
 });
 document.addEventListener('keydown',e=>{
  if(e.key==='Escape'){closeMenu();closeTheme();}
  if(e.key!=='/'||e.metaKey||e.ctrlKey||e.altKey)return;
  const tag=(e.target.tagName||'').toLowerCase();
  if(tag==='input'||tag==='textarea'||tag==='select'||e.target.isContentEditable)return;
  e.preventDefault();input.focus();openPanel();
 });
 $('console-menu')?.addEventListener('toggle',()=>{if($('console-menu').open){closePanel();closeTheme();fillMenu();}});
 $('console-theme')?.addEventListener('toggle',()=>{if($('console-theme').open){closePanel();closeMenu();}});
 fillMenu();
 remember();
})();
