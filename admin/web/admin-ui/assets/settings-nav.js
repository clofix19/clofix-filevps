(()=>{
 if(!document.querySelector('[data-settings-panel]'))return;
 if('scrollRestoration' in history)history.scrollRestoration='manual';

 const FALLBACK_ICONS=['◎','▣','▦','◇','⬡','☰','⌖','▶','⇄','⚙','◫','⚑','✓','◆','▤','⎋'];
 const NAV_ICONS={
  'security-score':'◎','security-findings':'▦','security-overview':'▣','security-alerts':'⚑','security-audit':'⎘',
  'security-modes':'☰','security-protection':'◈','security-detections':'⌖','security-vulns':'◫','security-autopilot':'▶',
  'security-vps':'◉','security-port-forwards':'⇄','security-guide':'?',
  'settings-defaults':'⚙','settings-pricing':'＄','settings-resource-pricing':'▦','settings-discounts':'％',
  'settings-gateway':'⌁','settings-currency':'¤','settings-billing-notifications':'✉','notification-channels':'⚑',
  'settings-nlb':'⇄','settings-license':'◆',
  'dns-overview':'◎','dns-zone':'◇','dns-records':'▤','dns-certificates':'✓','dns-zonefile':'⎘',
  'billing-overview':'◎','billing-resources':'▦','billing-due':'◷','billing-analysis':'◫',
  'billing-invoices':'▤','billing-wallet':'◇','billing-payments':'⌁','billing-subscriptions':'↻',
  'billing-refunds':'↺','billing-reports':'☰',
  'instances-overview':'◎','instances-list':'▦','instances-keys':'◇',
  'detail-overview':'◎','detail-resources':'▦','detail-services':'⚙','detail-ports':'⇄',
  'detail-security':'◈','detail-access':'⌁','detail-backups':'◫',
  'workers-overview':'◎','workers-list':'▦','workers-capacity':'▣','workers-enroll':'＋',
  'volumes-inventory':'▦','volumes-explorer':'⌕',
  'storage-pool':'◎','storage-files':'▤',
  'backups-list':'▦','backups-scope':'◇',
  'database-instances':'▦','database-backup-manager':'◫','database-schedule-list':'◷','database-job-progress':'▶',
  'vpc-overview':'◎','vpc-blocks':'▣',
  'firewall-workers':'☰','firewall-vps':'◉',
  'lb-overview':'◎','lb-notes':'▤',
  'health-endpoints':'◎','health-current':'⌖','health-history':'◷',
  'efs-inventory':'▦','efs-explorer':'⌕',
  'logs-live':'▶','logs-sources':'▤','logs-alerts':'⚑','logs-analysis':'◫',
  'iam-users-panel':'◎','iam-groups-panel':'☰','iam-policies-panel':'▤','iam-keys-panel':'◇',
  'profile-overview':'◎','profile-details':'▤','profile-mfa':'◆','profile-ips':'⇄',
  'cks-clusters':'◎','cks-workloads':'▦','cks-pools':'▣','cks-addons':'＋','cks-helm':'⬡',
  'cks-events':'⚑','cks-ops':'⚙','cks-integrate':'⇄',
  'library-software':'▦','library-apps':'◇',
  'apps-list':'▦','apps-runs':'▶',
  'docker-overview':'◎','docker-containers':'⬡','docker-apps':'▦','docker-quick':'◇',
  'docker-compose':'☰','docker-images':'▤','docker-networks':'⇄','docker-volumes':'◫',
  'docker-deployments':'▶','docker-monitoring':'⌖','docker-backups':'↻','docker-security':'◈',
  'docker-engine':'⚙'
 };

 function parseHash(){
  const raw=(location.hash||'').replace(/^#/,'');
  if(!raw)return{panel:'',tab:''};
  const slash=raw.indexOf('/');
  if(slash<0)return{panel:raw,tab:''};
  return{panel:raw.slice(0,slash),tab:raw.slice(slash+1)};
 }

 function ensureNavIcons(){
  document.querySelectorAll('.settings-nav > a[data-settings-link]').forEach((a,i)=>{
   if(a.querySelector('.settings-nav-icon'))return;
   const id=a.getAttribute('aria-controls')||(a.hash||'').replace(/^#/,'').split('/')[0]||'';
   const icon=NAV_ICONS[id]||FALLBACK_ICONS[i%FALLBACK_ICONS.length];
   const text=document.createElement('span');
   text.className='settings-nav-text';
   while(a.firstChild)text.append(a.firstChild);
   const mark=document.createElement('span');
   mark.className='settings-nav-icon';
   mark.setAttribute('aria-hidden','true');
   mark.textContent=icon;
   a.append(mark,text);
  });
 }

 const panels=Array.from(document.querySelectorAll('[data-settings-panel]'));
 const links=Array.from(document.querySelectorAll('[data-settings-link]'));

 function activateSectionTabs(root,tabId){
  if(!root)return;
  const tabs=Array.from(root.querySelectorAll('[data-section-tab]'));
  if(!tabs.length)return;
  const wanted=tabId&&tabs.some(t=>t.getAttribute('data-section-tab')===tabId)?tabId:(tabs.find(t=>t.getAttribute('aria-selected')==='true')?.getAttribute('data-section-tab')||tabs[0].getAttribute('data-section-tab'));
  tabs.forEach(tab=>{
   const on=tab.getAttribute('data-section-tab')===wanted;
   tab.setAttribute('aria-selected',on?'true':'false');
   tab.tabIndex=on?0:-1;
  });
  root.querySelectorAll('[data-section-panel]').forEach(panel=>{
   panel.hidden=panel.getAttribute('data-section-panel')!==wanted;
  });
 }

 function activate(focus=false){
  const sx=window.scrollX,sy=window.scrollY;
  if(location.hash==='#settings-access')history.replaceState(null,'','#settings-license');
  const {panel:hashPanel,tab}=parseHash();
  const visible=panels.filter(panel=>!panel.classList.contains('role-nav-hidden'));
  const selected=visible.find(panel=>panel.id===hashPanel)||visible[0]||panels[0];
  const selectedId=selected?.id||'';
  // Strip ids while toggling hidden so the browser does not scroll-to-hash.
  const stripped=[];
  for(const panel of panels){
   if(panel.id){stripped.push([panel,panel.id]);panel.removeAttribute('id');}
  }
  for(const panel of panels)panel.hidden=panel!==selected;
  for(const link of links){
   const inNav=!!link.closest('.settings-nav');
   const target=(link.getAttribute('aria-controls')||'').split('/')[0];
   if(inNav&&target===selectedId)link.setAttribute('aria-current','location');
   else link.removeAttribute('aria-current');
  }
  activateSectionTabs(selected,tab);
  if(focus)selected.querySelector('h2,h3')?.focus({preventScroll:true});
  const restore=()=>window.scrollTo(sx,sy);
  restore();
  for(const [panel,id] of stripped)if(id&&!panel.id)panel.id=id;
  restore();
  requestAnimationFrame(()=>{restore();requestAnimationFrame(restore);});
  setTimeout(restore,0);setTimeout(restore,40);setTimeout(restore,120);
  if(typeof window.enhanceStatWaves==='function')requestAnimationFrame(()=>window.enhanceStatWaves());
 }

 function goHash(hash,push){
  const next=hash.startsWith('#')?hash:'#'+hash;
  if(location.hash!==next){
   if(push)history.pushState(null,'',next);
   else history.replaceState(null,'',next);
  }
  activate(false);
 }

 function keepScroll(fn){
  const x=window.scrollX,y=window.scrollY;
  const restore=()=>{
   window.scrollTo(x,y);
   requestAnimationFrame(()=>{window.scrollTo(x,y);requestAnimationFrame(()=>window.scrollTo(x,y));});
   setTimeout(()=>window.scrollTo(x,y),0);
   setTimeout(()=>window.scrollTo(x,y),40);
   setTimeout(()=>window.scrollTo(x,y),120);
  };
  try{
   const result=fn();
   if(result&&typeof result.then==='function')return result.finally(restore);
   restore();
   return result;
  }catch(err){restore();throw err;}
 }

 window.applySettingsNav=()=>keepScroll(()=>activate());
 window.activateSectionTabs=activateSectionTabs;
 window.preservePageScroll=keepScroll;

 ensureNavIcons();

 for(const link of links)link.addEventListener('click',event=>{
  if(event.button!==0||event.ctrlKey||event.metaKey||event.shiftKey||event.altKey)return;
  event.preventDefault();
  keepScroll(()=>goHash(link.hash||('#'+(link.getAttribute('aria-controls')||'')),true));
 });

 document.querySelectorAll('[data-section-tabs]').forEach(root=>{
  root.querySelectorAll('[data-section-tab]').forEach(tab=>{
   tab.addEventListener('click',()=>{
    const panel=root.closest('[data-settings-panel]');
    if(!panel){activateSectionTabs(root,tab.getAttribute('data-section-tab'));return;}
    const tabId=tab.getAttribute('data-section-tab')||'';
    keepScroll(()=>goHash('#'+panel.id+(tabId?'/'+tabId:''),true));
   });
  });
 });

 addEventListener('hashchange',()=>keepScroll(()=>activate()));
 addEventListener('popstate',()=>keepScroll(()=>activate()));
 activate();
})();
