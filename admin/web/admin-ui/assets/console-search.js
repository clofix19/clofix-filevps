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
 // CloFix platform services only — no third-party app names (Jenkins, Grafana, …).
 const catalog=[
  {name:'Dashboard',href:'/',group:'Overview',hint:'Fleet overview',keys:'home dashboard overview clofix cloud',icon:icon.dashboard,core:true},
  {name:'FileVPS',href:'/instances',group:'Compute',hint:'Linux VPS instances',keys:'vps instance compute linux filevps server vm ssh',icon:icon.vps,core:true},
  {name:'Create FileVPS',href:'/instances?create=1',group:'Compute',hint:'Launch a new VPS',keys:'create new launch provision',icon:icon.vps},
  {name:'CloFix Library',href:'/services',group:'Compute',hint:'Managed apps & VPS packages',keys:'library catalog software package install',icon:icon.library,core:true},
  {name:'Quick Apps',href:'/services#library-apps',group:'Compute',hint:'CloFix managed deploys',keys:'quick apps managed deploy sandbox zip',icon:icon.library},
  {name:'VPS software',href:'/services#library-software',group:'Compute',hint:'Install into a FileVPS',keys:'vps software packages nginx apache php mysql',icon:icon.library},
  {name:'Jobs & Functions',href:'/applications',group:'Compute',hint:'Scripts and jobs',keys:'jobs functions scripts serverless',icon:icon.jobs,core:true},
  {name:'CKS',href:'/kubernetes',group:'Compute',hint:'CloFix Kubernetes',keys:'cks k8s kubernetes cluster kubernetes kubeadm helm ingress node pool namespace pod',icon:icon.vps,core:true},
  {name:'Volumes',href:'/volumes',group:'Storage',hint:'Persistent disks',keys:'volume disk persistent block',icon:icon.volumes,core:true},
  {name:'EFS',href:'/efs',group:'Storage',hint:'Shared NFS files',keys:'efs share nfs web root files',icon:icon.storage,core:true},
  {name:'Object Storage',href:'/storage/s3',group:'Storage',hint:'S3-compatible buckets',keys:'s3 bucket object storage',icon:icon.storage,core:true},
  {name:'Backups',href:'/backups',group:'Storage',hint:'Snapshots & restore',keys:'backup snapshot restore',icon:icon.backups,core:true},
  {name:'Databases',href:'/databases',group:'Storage',hint:'Managed databases',keys:'database mysql postgres mongodb redis',icon:icon.databases,core:true},
  {name:'Load Balancers',href:'/load-balancers',group:'Networking',hint:'NLB / ALB',keys:'nlb alb load balancer traffic',icon:icon.lb,core:true},
  {name:'Floating IPs',href:'/settings#settings-nlb',group:'Networking',hint:'Public IPv4 pool',keys:'floating ip nlb pool attach public ipv4',icon:icon.lb},
  {name:'VPC',href:'/vpc',group:'Networking',hint:'Private networks',keys:'vpc cidr subnet ip block network',icon:icon.vpc,core:true},
  {name:'DNS Service',href:'/dns',group:'Networking',hint:'Zones & records',keys:'dns zone record hostname',icon:icon.dns,core:true},
  {name:'Certificates',href:'/dns#dns-certificates',group:'Networking',hint:'TLS / ACME',keys:'acme tls certificate lets encrypt https domain',icon:icon.security},
  {name:'Health Checks',href:'/health-checks',group:'Networking',hint:'Endpoint monitors',keys:'health ping monitor uptime',icon:icon.health,core:true},
  {name:'Firewall',href:'/firewall',group:'Networking',hint:'Security groups',keys:'firewall security group cidr nftables inbound',icon:icon.firewall,core:true},
  {name:'Security Center',href:'/security',group:'Security',hint:'Posture & controls',keys:'security protection waf ssh score audit findings autopilot fim cve compliance',icon:icon.security,core:true},
  {name:'Security score',href:'/security#security-score',group:'Security',hint:'Live posture score',keys:'score posture findings delta',icon:icon.security},
  {name:'Findings',href:'/security#security-findings',group:'Security',hint:'Score ledger',keys:'findings ledger delta inventory',icon:icon.security},
  {name:'Advanced Protection',href:'/security#security-protection',group:'Security',hint:'Catalog & unlock',keys:'advanced protection unlock quarantine approval filevps network worker storage',icon:icon.security},
  {name:'Security Modes',href:'/security#security-modes',group:'Security',hint:'Policy templates',keys:'modes policy harden dry-run apply',icon:icon.security},
  {name:'Security Guide',href:'/security#security-guide',group:'Security',hint:'Control map',keys:'guide catalog control map deeplink',icon:icon.security},
  {name:'Security audit',href:'/security#security-audit',group:'Security',hint:'Mutation trail',keys:'audit trail activity',icon:icon.security},
  {name:'IAM',href:'/iam',group:'Security',hint:'Keys & policies',keys:'iam users keys policies access',icon:icon.iam,kinds:['admin','root','iam'],core:true},
  {name:'Users',href:'/users',group:'Security',hint:'Accounts',keys:'users partner client accounts',icon:icon.iam,kinds:['admin','partner'],core:true},
  {name:'My Profile',href:'/profile',group:'Account',hint:'MFA & allowlist',keys:'profile mfa authenticator allow ip password',icon:icon.iam,core:true},
  {name:'Log Service',href:'/logs',group:'Platform',hint:'Tail & alerts',keys:'logs tail alert analysis download nginx syslog',icon:icon.logs,core:true},
  {name:'Workers',href:'/workers',group:'Platform',hint:'Host capacity',keys:'worker host capacity enroll lab',icon:icon.workers,kinds:['admin'],core:true},
  {name:'Billing & Payments',href:'/billing',group:'Platform',hint:'Plans & invoices',keys:'invoice payment wallet billing plan',icon:icon.billing,core:true},
  {name:'Settings',href:'/settings',group:'Platform',hint:'Defaults & license',keys:'defaults license pricing settings',icon:icon.settings,core:true}
 ];
 const recentKey='cf_recent_services';
 const aliases={
  home:'dashboard',cloud:'dashboard',overview:'dashboard',
  vps:'filevps',vm:'filevps',instance:'filevps',server:'filevps',linux:'filevps',
  k8s:'cks',kubernetes:'cks',eks:'cks',kube:'cks',
  s3:'object storage',bucket:'object storage',
  nfs:'efs',share:'efs',
  db:'databases',sql:'databases',postgres:'databases',mysql:'databases',redis:'databases',mongo:'databases',
  nlb:'load balancers',alb:'load balancers',lb:'load balancers',
  waf:'security center',ssh:'security center',fim:'advanced protection',cve:'security center',
  protect:'advanced protection',protection:'advanced protection',
  package:'vps software',packages:'vps software',install:'vps software',
  deploy:'quick apps',managed:'quick apps',
  snapshot:'backups',restore:'backups',
  invoice:'billing & payments',wallet:'billing & payments',
  host:'workers',node:'workers'
 };
 function service(s){return catalog.find(c=>c.href===s.href)||s;}
 function recent(){try{return JSON.parse(localStorage.getItem(recentKey)||'[]').map(service).filter(s=>catalog.some(c=>c.href===s.href));}catch(e){return[];}}
 function remember(){
  const path=location.pathname.replace(/\/$/,'')||'/';
  const hash=location.hash||'';
  const withHash=catalog.find(s=>s.href.includes('#')&&s.href===path+hash);
  const match=withHash||catalog.find(s=>s.core&&(s.href==='/'?path==='/':path===s.href||path.startsWith(s.href+'/')));
  if(!match)return;
  try{localStorage.setItem(recentKey,JSON.stringify([{name:match.name,href:match.href,group:match.group},...recent().filter(x=>x.href!==match.href)].slice(0,6)));}catch(e){}
 }
 function visibleCatalog(){
  const kind=window.cfMe?.kind||document.body.dataset.kind||'';
  return catalog.filter(s=>!s.kinds||!kind||s.kinds.includes(kind));
 }
 function tokens(q){
  return q.toLowerCase().split(/[\s,/|+.]+/).map(t=>t.trim()).filter(Boolean);
 }
 function expand(tok){
  const a=aliases[tok];
  return a?[tok,...tokens(a)]:[tok];
 }
 function scoreService(s,queryTokens){
  const name=s.name.toLowerCase(),group=(s.group||'').toLowerCase(),keys=(s.keys||'').toLowerCase(),hint=(s.hint||'').toLowerCase(),href=s.href.toLowerCase();
  let score=0,hits=0;
  for(const raw of queryTokens){
   const variants=expand(raw);
   let best=0;
   for(const t of variants){
    if(!t)continue;
    if(name===t)best=Math.max(best,120);
    else if(name.startsWith(t))best=Math.max(best,95);
    else if(name.split(/\s+/).some(w=>w.startsWith(t)))best=Math.max(best,80);
    else if(name.includes(t))best=Math.max(best,55);
    if(keys.split(/\s+/).includes(t))best=Math.max(best,70);
    else if(keys.includes(t))best=Math.max(best,40);
    if(hint.includes(t))best=Math.max(best,35);
    if(group.startsWith(t)||group.includes(t))best=Math.max(best,25);
    if(href.includes(t))best=Math.max(best,15);
   }
   if(best){score+=best;hits++;}
   else return 0;
  }
  if(s.core)score+=4;
  if(hits===queryTokens.length)score+=10;
  return score;
 }
 function matchQuery(q){
  const list=visibleCatalog();
  const toks=tokens(q);
  if(!toks.length)return list.filter(s=>s.core);
  return list.map(s=>({s,score:scoreService(s,toks)})).filter(x=>x.score>0).sort((a,b)=>b.score-a.score||a.s.name.localeCompare(b.s.name)).map(x=>x.s);
 }
 function item(s,active){
  const a=document.createElement('a');
  a.className='console-result'+(active?' is-active':'');
  a.href=s.href;a.role='option';a.setAttribute('aria-selected',active?'true':'false');
  a.title=s.hint?`${s.name} · ${s.hint}`:s.name;
  a.innerHTML=`<span class="console-result-icon">${s.icon||icon.dashboard}</span><strong>${s.name}</strong>`;
  return a;
 }
 function grid(list,activeFirst){
  const wrap=document.createElement('div');
  wrap.className='console-result-grid';
  list.forEach((s,i)=>wrap.append(item(s,activeFirst&&i===0)));
  return wrap;
 }
 function render(q){
  const query=q.trim().toLowerCase(),visible=visibleCatalog(),list=matchQuery(query);
  results.replaceChildren();
  if(!query){
   const rec=recent().filter(s=>visible.some(v=>v.href===s.href));
   const browse=visible.filter(s=>s.core&&!rec.some(r=>r.href===s.href));
   const flat=[...rec,...browse];
   if(!flat.length){
    const empty=document.createElement('p');empty.className='console-result-empty';empty.textContent='No CloFix services available.';results.append(empty);
   }else results.append(grid(flat,true));
  }else if(!list.length){
   const empty=document.createElement('p');empty.className='console-result-empty';empty.textContent='No matching CloFix service. Try FileVPS, Security, Storage, or Networking.';results.append(empty);
  }else results.append(grid(list,true));
  if(clear)clear.hidden=!query;
 }
 function fillMenu(){
  const panel=$('console-menu-list');
  if(!panel)return;
  panel.replaceChildren();
  const path=(location.pathname.replace(/\/$/,'')||'/');
  const wrap=document.createElement('div');
  wrap.className='console-menu-grid';
  visibleCatalog().filter(s=>s.core).forEach(s=>{
   const a=document.createElement('a');
   a.href=s.href;
   a.setAttribute('role','menuitem');
   a.title=s.hint||s.name;
   a.innerHTML=`<span class="console-result-icon">${s.icon||icon.dashboard}</span><strong>${s.name}</strong>`;
   if(s.href==='/'?path==='/':path===s.href||path.startsWith(s.href+'/'))a.setAttribute('aria-current','page');
   wrap.append(a);
  });
  panel.append(wrap);
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
