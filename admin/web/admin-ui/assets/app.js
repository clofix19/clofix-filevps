const $=id=>document.getElementById(id);if(!$('tok')){const hiddenTok=document.createElement('input');hiddenTok.type='hidden';hiddenTok.id='tok';hiddenTok.value='session';document.body.appendChild(hiddenTok);}const page=document.body.dataset.page;let T='',cfMe=null,instances=[],cksNodes=[],workers=[],volumes=[],backups=[],volumeExplorerPath='',volumeEditingPath='',volumeMoving='',volumeMovingFolder=false,s3Connections=[],s3BrowserPrefix='',s3CurrentConnection='',s3EditingConnectionID='',s3EditingKey='',s3Moving='',s3MovingFolder=false,selectedID=document.body.dataset.instanceId||'',selectedServiceLog='',selectedErrorService='';const usageHistory=[];let lastSuccessfulRefresh=0;let selectedServiceInstall='',selectedServiceAction='install',serviceInstallSubmitting=false,serviceInstallNotice='',serviceInstallError='';
const usageHistoryKey='cf_usage_history_v1',instanceHistoryKey='cf_instance_net_v1',usageRangeKey='cf_usage_range_v1',usageRangeMs={'1h':3600000,'6h':21600000,'1d':86400000,'7d':604800000,'30d':2592000000,'custom':0};const usageCustomKey='cf_usage_custom_v1';let usageCustom={from:0,to:0};try{const savedCustom=JSON.parse(localStorage.getItem(usageCustomKey)||'{}');if(savedCustom&&Number(savedCustom.from)&&Number(savedCustom.to))usageCustom={from:+savedCustom.from,to:+savedCustom.to};}catch(e){}let usageRange=localStorage.getItem(usageRangeKey)||'1h';if(!Object.prototype.hasOwnProperty.call(usageRangeMs,usageRange))usageRange='1h';if(usageRange==='custom'&&!(usageCustom.from&&usageCustom.to))usageRange='1h';const instanceHistory={};try{const saved=JSON.parse(localStorage.getItem(usageHistoryKey)||'[]');if(Array.isArray(saved))saved.forEach(p=>{if(Array.isArray(p)&&p.length>=5&&Number.isFinite(+p[0]))usageHistory.push({at:+p[0],cpuUsed:+p[1]||0,cpuLimit:+p[2]||0,ramUsed:+p[3]||0,ramLimit:+p[4]||0,netIn:+p[5]||0,netOut:+p[6]||0,netBw:+p[7]||0,hostNetIn:+p[8]||0,hostNetOut:+p[9]||0,hostNetBw:+p[10]||0,swapUsed:+p[11]||0,swapLimit:+p[12]||0,diskUsed:+p[13]||0,diskLimit:+p[14]||0});});}catch(e){}try{const saved=JSON.parse(localStorage.getItem(instanceHistoryKey)||'{}');if(saved&&typeof saved==='object')Object.entries(saved).forEach(([id,rows])=>{if(!Array.isArray(rows))return;instanceHistory[id]=rows.filter(p=>Array.isArray(p)&&Number.isFinite(+p[0])).map(p=>({at:+p[0],netIn:+p[1]||0,netOut:+p[2]||0,netBw:+p[3]||0,cpuUsed:+p[4]||0,cpuLimit:+p[5]||0,ramUsed:+p[6]||0,ramLimit:+p[7]||0,swapUsed:+p[8]||0,swapLimit:+p[9]||0,diskUsed:+p[10]||0,diskLimit:+p[11]||0}));});}catch(e){}while(usageHistory.length&&usageHistory[0].at<Date.now()-usageRangeMs['30d'])usageHistory.shift();
const defaultUISettings={image:'ubuntu',version:'24.04',ram:512,cpu:1,disk:4096,swap:512,protection:true};let uiSettings={...defaultUISettings};try{uiSettings={...uiSettings,...JSON.parse(localStorage.getItem('cf_ui_settings')||'{}')};}catch(e){}
let iamUsers=[],iamGroups=[],iamPolicies=[],iamPermissions=[],iamPermissionDetails={},iamEditingUser='',iamEditingGroup='',iamEditingPolicy='';
function roleLabel(me){if(!me)return '-';if(me.kind==='admin')return 'Admin · CloFix owner';if(me.kind==='partner')return 'Partner';if(me.kind==='root')return 'Root · account owner';return me.role||'IAM user';}
function applyAccountMenu(me){const email=$('account-email'),role=$('account-role'),acc=$('account-id'),uid=$('account-user-id'),bar=$('console-account-id');if(email)email.textContent=me.email||me.username||'Signed in';if(role)role.textContent=roleLabel(me);if(acc)acc.textContent=me.account_id||'-';if(uid)uid.textContent=me.id||'-';if(bar){bar.textContent=me.account_id||'-';bar.title='Account ID (shared by Root and IAM users of this account). User ID: '+(me.id||'-');}document.body.dataset.kind=me.kind||'';}
function applyRoleNav(me){const kind=me.kind||'';const hideWorkers=kind==='partner';const hideUsers=kind!=='admin'&&kind!=='partner';const hideIAM=kind==='partner';const hidePricing=kind!=='admin';const hideWorkerAdmin=kind!=='admin';document.querySelectorAll('[href="/workers"]').forEach(el=>el.classList.toggle('role-nav-hidden',hideWorkers));document.querySelectorAll('[href="/users"]').forEach(el=>el.classList.toggle('role-nav-hidden',hideUsers));document.querySelectorAll('[href="/iam"]').forEach(el=>el.classList.toggle('role-nav-hidden',hideIAM));document.querySelectorAll('[href="#settings-pricing"],[href="#settings-resource-pricing"],[href="#settings-discounts"],[href="#settings-gateway"],[href="#settings-currency"],[href="#settings-nlb"],[href="#settings-license"]').forEach(el=>el.classList.toggle('role-nav-hidden',hidePricing));document.querySelectorAll('#settings-pricing,#settings-resource-pricing,#settings-discounts,#settings-gateway,#settings-currency,#settings-nlb,#settings-license').forEach(el=>el.classList.toggle('role-nav-hidden',hidePricing));document.querySelectorAll('#mktok,[href="#workers-enroll"],#workers-enroll,[href="/firewall#firewall-workers"]').forEach(el=>el.classList.toggle('role-nav-hidden',hideWorkerAdmin));const warn=$('license-warning');if(warn&&hidePricing)warn.hidden=true;if(typeof window.applySettingsNav==='function')window.applySettingsNav();}
async function ensureSession(){if(cfMe){T='session';return cfMe;}const r=await fetch('/api/auth/me',{credentials:'include',cache:'no-store'});if(r.status===401){location.assign('/login');throw new Error('Please sign in.');}const me=await r.json();cfMe=me;window.cfMe=me;T='session';applyAccountMenu(me);applyRoleNav(me);return me;}
async function api(method,path,body){const r=await fetch(path,{method,cache:'no-store',credentials:'include',headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const ct=r.headers.get('content-type')||'';let j={};if(ct.includes('json'))j=await r.json().catch(()=>({}));else{const raw=await r.text();if(raw.trim().startsWith('<!doctype')||raw.trim().startsWith('<html'))throw new Error('This controller does not have the requested API route. Rebuild and restart the admin container, then reload the page.');}if(r.status===401){location.assign('/login');throw Object.assign(new Error('Please sign in.'),{data:j});}if(!r.ok)throw Object.assign(new Error(j.error||r.status),{data:j});return j;}
async function apiRaw(method,path,body,contentType='application/octet-stream'){const r=await fetch(path,{method,cache:'no-store',credentials:'include',headers:{'Content-Type':contentType},body});if(r.status===401){location.assign('/login');throw new Error('Please sign in.');}if(!r.ok){let j={};try{j=await r.json();}catch(e){}throw new Error(j.error||r.status);}return r;}
if('scrollRestoration' in history)history.scrollRestoration='manual';
function preservePageScroll(run){
 if(typeof window.preservePageScroll==='function'&&window.preservePageScroll!==preservePageScroll)return window.preservePageScroll(run);
 const x=window.scrollX,y=window.scrollY;
 const restore=()=>{
  window.scrollTo(x,y);
  requestAnimationFrame(()=>{window.scrollTo(x,y);requestAnimationFrame(()=>window.scrollTo(x,y));});
  setTimeout(()=>window.scrollTo(x,y),0);
  setTimeout(()=>window.scrollTo(x,y),40);
  setTimeout(()=>window.scrollTo(x,y),120);
 };
 try{
  const result=run();
  if(result&&typeof result.then==='function')return result.finally(restore);
  restore();
  return result;
 }catch(err){restore();throw err;}
}
window.preservePageScroll=preservePageScroll;
const tablePaints=new WeakMap();
function tableDataCells(tr){return Array.from(tr.cells).filter(c=>!c.classList.contains('table-select'));}
function nodeFingerprint(el){return Array.from(el.querySelectorAll('button,a,span.muted,span.badge,code')).map(n=>[n.tagName,n.className,(n.textContent||'').replace(/\s+/g,' ').trim(),n.disabled?'1':'0'].join(':')).join('|');}
function applyTableCell(td,c){
  if(c instanceof Node){
    if(c.classList.contains('badge')){
      const b=td.querySelector(':scope > .badge');
      if(b){
        if(b.className!==c.className)b.className=c.className;
        const text=(c.lastChild&&c.lastChild.nodeType===3?c.lastChild.textContent:c.textContent)||'';
        const last=b.lastChild;
        if(last&&last.nodeType===3){if(last.textContent!==text)last.textContent=text;}
        else b.append(document.createTextNode(text));
        const wantSpin=!!c.querySelector('.progress-icon'),haveSpin=b.querySelector('.progress-icon');
        if(wantSpin&&!haveSpin){const spin=document.createElement('span');spin.className='progress-icon';spin.setAttribute('aria-hidden','true');b.insertBefore(spin,b.firstChild);}
        else if(!wantSpin&&haveSpin)haveSpin.remove();
        return;
      }
    }
    if(c.classList.contains('id-link')){
      const b=td.querySelector(':scope > .id-link');
      if(b){if(b.textContent!==c.textContent)b.textContent=c.textContent;b.onclick=c.onclick;if(c.title!=null)b.title=c.title;return;}
    }
    if(c.classList.contains('row-actions')){
      const existing=td.querySelector(':scope > .row-actions');
      if(existing&&nodeFingerprint(existing)===nodeFingerprint(c))return;
    }
    if(c.tagName==='BUTTON'){
      const b=td.querySelector(':scope > button');
      if(b&&(b.textContent||'').trim()===(c.textContent||'').trim()&&b.className===c.className){b.onclick=c.onclick;b.disabled=c.disabled;return;}
    }
    if(c.tagName==='A'){
      const a=td.querySelector(':scope > a');
      if(a){if(a.textContent!==c.textContent)a.textContent=c.textContent;if(c.getAttribute('href'))a.setAttribute('href',c.getAttribute('href'));a.className=c.className;a.onclick=c.onclick;return;}
    }
    if(c.tagName==='CODE'){
      const code=td.querySelector(':scope > code');
      if(code){if(code.textContent!==c.textContent)code.textContent=c.textContent;return;}
    }
    if(c.classList.contains('service-name')){
      const s=td.querySelector(':scope > .service-name');
      if(s){const next=c.lastChild&&c.lastChild.nodeType===3?c.lastChild.textContent:c.textContent;const label=s.lastChild;if(label&&label.nodeType===3){if(label.textContent!==next)label.textContent=next;}return;}
    }
    if(c.tagName==='SPAN'&&!c.children.length){
      const s=td.querySelector(':scope > span');
      if(s&&s.className===c.className&&!s.children.length){if(s.textContent!==c.textContent)s.textContent=c.textContent;if(c.title)s.title=c.title;return;}
    }
    if(c.tagName==='INPUT'&&c.type==='checkbox'){
      const box=td.querySelector('input[type=checkbox]');
      if(box){box.className=c.className;if(c.dataset.key)box.dataset.key=c.dataset.key;return;}
    }
    td.replaceChildren(c);return;
  }
  const text=String(c??'');
  if(!td.children.length){if(td.textContent!==text)td.textContent=text;return;}
  td.textContent=text;
}
function row(t,cells,head){
  if(!t)return;
  let paint=tablePaints.get(t);
  if(!paint){
    paint={i:0};
    tablePaints.set(t,paint);
    queueMicrotask(()=>{
      const done=tablePaints.get(t);
      if(!done)return;
      while(t.rows.length>done.i)t.deleteRow(-1);
      tablePaints.delete(t);
      t.dispatchEvent(new Event('table-painted'));
    });
  }
  let tr=t.rows[paint.i];
  if(!tr){tr=document.createElement('tr');t.appendChild(tr);}
  const tag=head?'th':'td';
  let data=tableDataCells(tr);
  while(data.length<cells.length){tr.appendChild(document.createElement(tag));data=tableDataCells(tr);}
  while(data.length>cells.length){data.pop().remove();data=tableDataCells(tr);}
  data.forEach((td,i)=>{
    if(td.tagName!==tag.toUpperCase()){
      const next=document.createElement(tag);
      next.className=td.className;
      td.replaceWith(next);
      td=next;
    }
    applyTableCell(td,cells[i]);
  });
  paint.i++;
}
function button(label,fn,kind='small-btn'){const b=document.createElement('button');b.className=kind;b.textContent=label;b.onclick=async()=>{b.disabled=true;const phase={Start:'Starting',Restart:'Restarting',Stop:'Stopping',Delete:'Deleting',Retry:'Starting'}[label]||'Working';b.textContent='';const spin=document.createElement('span');spin.className='progress-icon';spin.setAttribute('aria-hidden','true');b.append(spin,document.createTextNode(phase+'…'));try{const result=await fn();await refresh();showMessage(result?.queued?`${label} queued.`:`${label} request sent.`);}catch(e){await refresh();showMessage(e.message);}finally{b.disabled=false;}};return b;}
function showMessage(s){$('msg').textContent=s||'';}
function routePath(page,id=''){return page==='home'?'/':page==='create'?'/instances?create=1':page==='services'?'/services':page==='security'?'/security':page==='workers'?'/workers':page==='storage'?'/storage/s3':page==='volumes'?'/volumes':page==='backups'?'/backups':page==='settings'?'/settings':page==='billing'?'/billing':page==='details'?'/instances/'+encodeURIComponent(id||selectedID):'/instances';}
function go(nextPage,id=''){location.assign(routePath(nextPage,id));}
function pageParentHref(){
 const path=(location.pathname.replace(/\/$/,'')||'/'),parts=path.split('/').filter(Boolean);
 if(parts[0]==='instances'&&parts[1])return '/instances';
 if(parts[0]==='load-balancers'&&parts[1])return '/load-balancers';
 if(parts[0]==='kubernetes'&&parts[1])return '/kubernetes';
 if(parts[0]==='waf'&&parts[1]==='bindings'&&parts[2])return '/waf#waf-bindings';
 if(parts[0]==='storage'&&parts[1])return '/';
 if(path==='/'||page==='home')return '';
 return '/';
}
function navigateBack(event){
 if(event){event.preventDefault();event.stopPropagation();}
 const hash=(location.hash||'').replace(/^#/,'');
 const first=document.querySelector('.settings-nav a[data-settings-link]:not(.role-nav-hidden)')?.getAttribute('aria-controls')||'';
 if(hash&&first){
  const panel=hash.split('/')[0],tab=hash.includes('/')?hash.slice(hash.indexOf('/')+1):'';
  if(panel!==first||tab){location.assign(location.pathname+location.search+'#'+first);return;}
 }
 const parent=pageParentHref();
 const here=location.pathname.replace(/\/$/,'')||'/';
 if(parent&&parent!==here){location.assign(parent);return;}
 if(window.history.length>1){history.back();return;}
 location.assign('/');
}
function syncTopbarBack(){
 const btn=$('topbar-back');if(!btn)return;
 const parent=pageParentHref();
 const hash=(location.hash||'').replace(/^#/,'');
 const first=document.querySelector('.settings-nav a[data-settings-link]:not(.role-nav-hidden)')?.getAttribute('aria-controls')||'';
 const deepHash=!!(hash&&first&&(hash.split('/')[0]!==first||hash.includes('/')));
 const show=page!=='home'&&(!!parent||deepHash||!!document.body.dataset.instanceId);
 btn.hidden=!show;
 if(parent)btn.setAttribute('data-parent',parent);else btn.removeAttribute('data-parent');
}
window.navigateBack=navigateBack;
window.syncTopbarBack=syncTopbarBack;
document.addEventListener('click',e=>{
 const link=e.target.closest('[data-cf-back]');
 if(!link)return;
 navigateBack(e);
});
addEventListener('hashchange',()=>syncTopbarBack());
addEventListener('popstate',()=>syncTopbarBack());
let vpcCatalog=[];
async function loadVPCCatalog(){try{const data=await api('GET','/api/vpcs');vpcCatalog=data.vpcs||[];}catch(e){vpcCatalog=[];}}
function vpcByID(id){return vpcCatalog.find(v=>v.id===id)||vpcCatalog.find(v=>v.default)||null;}
function vpcAddress(ip,id){const v=vpcByID(id);if(!v)return ip||'-';return ip?`${ip} · ${v.name} (${v.cidr})`:`${v.name} (${v.cidr})`;}
async function loadVPCOptions(id){const sel=$(id);if(!sel)return;await loadVPCCatalog();const keep=sel.value;sel.replaceChildren();(vpcCatalog.length?vpcCatalog:[{id:'vpc-default',name:'Default VPC',cidr:'10.231.0.0/16',default:true}]).forEach(v=>sel.add(new Option(v.default?`${v.name} · ${v.cidr} (default)`:`${v.name} · ${v.cidr}`,v.id)));if(keep&&[...sel.options].some(o=>o.value===keep))sel.value=keep;else{const def=vpcCatalog.find(v=>v.default);sel.value=def?def.id:'vpc-default';}}
function syncImagePresetUI(){
  const preset=$('img-preset');
  const custom=preset&&preset.value==='custom';
  if($('img-custom-wrap'))$('img-custom-wrap').hidden=!custom;
  if($('ver-custom-wrap'))$('ver-custom-wrap').hidden=!custom;
  if(!preset)return;
  if(custom){
    if($('img-name'))$('img-name').value=$('img')?.value||'ubuntu';
    if($('img-version'))$('img-version').value=$('ver')?.value||'24.04';
  }else{
    const [name,version]=(preset.value||'ubuntu:24.04').split(':');
    if($('img-name'))$('img-name').value=name||'ubuntu';
    if($('img-version'))$('img-version').value=version||'24.04';
    if($('img'))$('img').value=name||'ubuntu';
    if($('ver'))$('ver').value=version||'24.04';
  }
}
function applySettingsToCreate(){
  if($('ram'))$('ram').value=uiSettings.ram;
  if($('cpu'))$('cpu').value=uiSettings.cpu;
  if($('disk'))$('disk').value=uiSettings.disk;
  if($('swap'))$('swap').value=uiSettings.swap;
  const key=`${uiSettings.image||'ubuntu'}:${uiSettings.version||'24.04'}`;
  const preset=$('img-preset');
  if(preset){
    const known=[...preset.options].some(o=>o.value===key);
    preset.value=known?key:'custom';
    if($('img'))$('img').value=uiSettings.image||'ubuntu';
    if($('ver'))$('ver').value=uiSettings.version||'24.04';
    syncImagePresetUI();
  }else if($('img')){
    $('img').value=uiSettings.image;$('ver').value=uiSettings.version;
  }
}
function createImageSpec(){
  syncImagePresetUI();
  const name=($('img-name')?.value||$('img')?.value||'ubuntu').trim();
  const version=($('img-version')?.value||$('ver')?.value||'24.04').trim();
  return {name,version,url:$('url')?.value||undefined,sha256:$('sha')?.value||undefined};
}
function populateSettings(){if($('settings-image')){$('settings-image').value=uiSettings.image;$('settings-version').value=uiSettings.version;$('settings-ram').value=uiSettings.ram;$('settings-cpu').value=uiSettings.cpu;$('settings-disk').value=uiSettings.disk;$('settings-swap').value=uiSettings.swap;$('settings-protection').checked=uiSettings.protection!==false;}applySettingsToCreate();}
populateSettings();
let sshPEMKeys=[], pendingCredentials={pem:'',password:'',id:'',name:''};
function safeDownloadBase(name,fallback='clofix'){return String(name||fallback).trim().replace(/[\/\\?%*:|"<>]+/g,'_').replace(/\s+/g,'-').replace(/-+/g,'-').replace(/^[.-]+|[.-]+$/g,'').slice(0,80)||fallback;}
function downloadText(name,text,type='application/x-pem-file'){const a=document.createElement('a');a.href=URL.createObjectURL(new Blob([text],{type}));a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(a.href),1000);}
function instanceDisplayName(i){const n=String(i?.spec?.hostname||'').trim();return n||i?.id||'-';}
function validVPSName(name){return /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/.test(String(name||'').trim());}
async function editInstanceName(i){
  if(!i?.id)return;
  const current=String(i.spec?.hostname||'').trim();
  const ask=typeof window.cfPrompt==='function'?window.cfPrompt:null;
  const next=ask
    ? await ask(`VPS name for ${i.id}`, current, {
        title:'Edit VPS name',
        eyebrow:'Instances',
        label:'VPS name',
        confirm:'Save name',
        hint:'Letters, digits, and hyphens only (1–63 characters).',
        pattern:/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/,
      })
    : null;
  if(next===null)return;
  const name=String(next).trim().toLowerCase();
  if(!name){showMessage('VPS name is required.');return;}
  if(!validVPSName(name)){showMessage('VPS name must be letters, digits, and hyphens (1–63 chars).');return;}
  if(name===current){showMessage('Name unchanged.');return;}
  try{
    const r=await api('POST',`/api/instances/${i.id}/hostname`,{hostname:name});
    showMessage(r.unchanged?'Name unchanged.':`Renamed to ${r.hostname||name}.`);
    await refresh();
  }catch(e){showMessage(e.message);}
}
function instanceNameLink(i){
  const name=String(i?.spec?.hostname||'').trim();
  const btn=document.createElement('button');
  btn.type='button';
  btn.className='id-link';
  btn.textContent=name||'-';
  btn.title=name?`Open ${name}`:'No name set yet';
  btn.onclick=()=>go('details',i.id);
  return btn;
}
function instanceIDLink(i){
  const btn=document.createElement('button');
  btn.type='button';
  btn.className='id-link';
  btn.textContent=i?.id||'-';
  btn.title=i?.id||'';
  btn.onclick=()=>go('details',i.id);
  return btn;
}
function syncCreateAuthFields(){const method=$('auth-method')?.value||'key',pam=method==='pam'||method==='both',key=method==='key'||method==='both';if($('password-fields'))$('password-fields').hidden=!pam;if($('pem-fields'))$('pem-fields').hidden=!key;if($('sshkey-wrap'))$('sshkey-wrap').hidden=!key;}
function fillSSHKeySelect(){const sel=$('ssh-key-id');if(!sel)return;const old=sel.value;sel.length=1;sshPEMKeys.forEach(k=>sel.add(new Option(`${k.name} · ${k.id}`,k.id)));if([...sel.options].some(o=>o.value===old))sel.value=old;}
function renderPEMKeys(){const t=$('pem-table');if(!t)return;row(t,['Name','ID','Public key','Created','Actions'],true);if(!sshPEMKeys.length)row(t,['No saved PEM keys. Create one here or generate one when you create a VPS.','','','','']);sshPEMKeys.forEach(k=>{const actions=document.createElement('div');actions.className='row-actions';const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';del.onclick=async()=>{if(!window.confirm(`Delete PEM key ${k.name}? Existing VPS keep the public key already installed.`))return;try{await api('DELETE',`/api/ssh-keys/${k.id}`);await loadPEMKeys();$('pem-message').textContent='PEM key deleted.';}catch(e){$('pem-message').textContent=e.message;}};actions.append(del);const pub=document.createElement('code');pub.textContent=(k.public_key||'').slice(0,48)+((k.public_key||'').length>48?'…':'');row(t,[k.name,k.id,pub,k.created?new Date(k.created).toLocaleString():'-',actions]);});fillSSHKeySelect();}
async function loadPEMKeys(){if(!$('pem-table')&&!$('ssh-key-id'))return;try{const data=await api('GET','/api/ssh-keys');sshPEMKeys=Array.isArray(data)?data:(data.keys||[]);renderPEMKeys();}catch(e){if($('pem-message'))$('pem-message').textContent=e.message;}}
function showCredentialDialog(r,password){const name=r.key?.name||r.name||r.instance?.spec?.hostname||r.instance?.id||r.key?.id||'';pendingCredentials={pem:r.pem||'',password:password||'',id:r.instance?.id||r.key?.id||'',name};const hasPEM=!!pendingCredentials.pem,hasPW=!!pendingCredentials.password;if(!hasPEM&&!hasPW)return;if($('pem-ready-note'))$('pem-ready-note').textContent=hasPEM&&hasPW?'Download the private PEM and the password file now. The private key is shown once.':hasPEM?'Download the private PEM now. CloFix keeps only the public key.':'Download the root password file. CloFix does not store the plaintext password.';if($('pem-download')){$('pem-download').hidden=!hasPEM;$('pem-download').textContent=hasPEM?`Download ${safeDownloadBase(pendingCredentials.name)}.pem`:'Download PEM';}if($('password-download'))$('password-download').hidden=!hasPW;$('pem-ready-dialog')?.showModal();}
function fmtBytes(n){n=Math.max(0,Number(n)||0);const units=['B','KB','MB','GB','TB','PB'];let unit=0;while(n>=1024&&unit<units.length-1){n/=1024;unit++;}return `${unit===0?Math.round(n):n.toFixed(1)} ${units[unit]}`;}
function fmtBps(n){n=Math.max(0,Number(n)||0);if(n<1000)return Math.round(n)+' B/s';if(n<1000000)return (n/1000).toFixed(1)+' KB/s';if(n<1000000000)return (n/1000000).toFixed(2)+' MB/s';return (n/1000000000).toFixed(2)+' GB/s';}
function fmtMetricBytes(n){n=Number(n)||0;if(n<1024)return n+' B';if(n<1048576)return (n/1024).toFixed(1)+' KB';return (n/1048576).toFixed(1)+' MB';}
function stateLabel(state){const key=String(state||'').toUpperCase();return ({RUNNING:'Running',STOPPED:'Stopped',STARTING:'Starting',STOPPING:'Stopping',RESTARTING:'Restarting',DELETING:'Deleting',PROVISIONING:'Provisioning',PENDING:'Pending',FAILED:'Failed',PROCESSING:'In progress',IN_PROGRESS:'In progress','IN-PROGRESS':'In progress',CREATING:'Creating',ATTACHING:'Attaching',DETACHING:'Detaching'})[key]||state||'Unknown';}
function badge(state){const e=document.createElement('span');e.className='badge '+(state||'');const key=String(state||'').toUpperCase();if(['STARTING','STOPPING','RESTARTING','DELETING','PROVISIONING','PENDING','PROCESSING','IN_PROGRESS','IN-PROGRESS','CREATING','ATTACHING','DETACHING'].includes(key)){const spin=document.createElement('span');spin.className='progress-icon';spin.setAttribute('aria-hidden','true');e.append(spin);}e.append(document.createTextNode(stateLabel(state)));return e;}
function openTerminal(i){if(!i?.id)return;if(page==='details'&&selectedID===i.id){if(typeof openVPSTerminalNav==='function')openVPSTerminalNav();else location.hash='detail-terminal';return;}go('details',i.id);setTimeout(()=>{if(typeof openVPSTerminalNav==='function')openVPSTerminalNav();else location.hash='detail-terminal';},0);}
function terminalButton(i,kind){const b=document.createElement('button');b.type='button';b.className=kind;b.textContent='Terminal';b.onclick=()=>openTerminal(i);return b;}
function appendInstanceControls(target,i,detail=false){const kind=detail?'secondary':'small-btn',add=(label,action,css=kind)=>target.append(button(label,action,css));if(['STARTING','STOPPING','RESTARTING','PROVISIONING','PENDING','PROCESSING','IN_PROGRESS','IN-PROGRESS'].includes(String(i.state||'').toUpperCase())){const wait=document.createElement('span');wait.className='muted';wait.textContent='';const spin=document.createElement('span');spin.className='progress-icon';spin.setAttribute('aria-hidden','true');wait.append(spin,document.createTextNode('Action in progress…'));target.append(wait);}if(i.state==='RUNNING'){target.append(terminalButton(i,kind));add('Restart',()=>api('POST',`/api/instances/${i.id}/restart`));add('Stop',()=>api('POST',`/api/instances/${i.id}/stop`));}else if(i.state==='STOPPED'){add('Start',()=>api('POST',`/api/instances/${i.id}/start`));}else if(i.state==='FAILED'){add('Retry',()=>api('POST',`/api/instances/${i.id}/retry`,{ssh_authorized_key:$('sshkey')?.value?.trim()||''}),detail?'primary':'small-btn');}if(i.state!=='DELETING'){const edit=document.createElement('button');edit.type='button';edit.className=kind;edit.textContent='Edit';edit.setAttribute('aria-label',`Edit name for ${i.id}`);edit.onclick=()=>editInstanceName(i);target.append(edit);if(i.spec?.delete_protection){const del=document.createElement('button');del.className='danger';del.textContent='Delete protected';del.disabled=true;del.setAttribute('aria-label',`Delete protection is enabled for ${i.id}`);target.append(del);}else add('Delete',()=>api('DELETE',`/api/instances/${i.id}`),'danger');}}
function meta(key,value){const d=document.createElement('div');d.className='meta-item';const k=document.createElement('div');k.className='meta-key';k.textContent=key;const v=document.createElement('div');v.className='meta-value';v.textContent=value??'-';d.append(k,v);return d;}
function instanceLive(i){const running=i.state==='RUNNING',m=i.metrics||{},limit=Number(m.memory_limit_bytes)||Number(i.spec?.ram_mb||0)*1048576,inn=Number(m.net_rx_bps)||0,out=Number(m.net_tx_bps)||0;return{cpu:running?`${(Number(m.cpu_percent)||0).toFixed(1)}%`:'-',ram:running?`${fmtBytes(m.memory_current_bytes)} / ${fmtBytes(limit)}`:'-',inn:running?fmtBps(inn):'-',out:running?fmtBps(out):'-',bw:running?fmtBps(inn+out):'-',disk:`${fmtBytes(m.disk_usage_bytes||0)} used`};}
function loadCKSNodesFrom(data){
  const out=[];
  (data?.clusters||[]).forEach(c=>{
    const pools=c.node_pools&&c.node_pools.length?c.node_pools:[c.node_pool].filter(Boolean);
    (c.nodes||[]).forEach(n=>{
      if(String(n.state||'').toUpperCase()==='DELETED')return;
      const pool=pools.find(p=>p.id===n.pool_id)||c.node_pool||{};
      out.push({id:n.id,cluster_id:c.id,cluster_name:c.name||c.id,role:n.role||'worker',pool:pool.name||'',pool_kind:pool.kind||'',state:n.state,worker_id:n.worker_id||n.hostname||'',cpu:n.cpu||pool.cpu||0,ram_mb:n.ram_mb||pool.ram_mb||0,disk_mb:n.disk_mb||pool.disk_mb||0,created:c.created});
    });
  });
  cksNodes=out;
}
function cksNodeLabel(n){const group=n.role==='control-plane'?'control plane':(n.pool||'VPS');const short=(n.id||'').replace(/^ckn-/,'').slice(0,8);return short?`${n.cluster_name} · ${group} · ${short}`:`${n.cluster_name} · ${group}`;}
function openCKSNode(n){location.assign(`/kubernetes/${encodeURIComponent(n.cluster_id)}${n.pool_kind==='vps-group'?'#cks-pools':''}`);}
function renderInstances(){
  const t=$('instances');if(!t)return;
  const q=$('instance-search').value.trim().toLowerCase(),stateFilter=new URLSearchParams(location.search).get('state')||'';
  const vps=instances.filter(i=>(!stateFilter||i.state===stateFilter)&&[i.id,instanceDisplayName(i),'filevps','vps',i.worker_id,i.state,i.spec?.hostname,i.spec?.ip_address,vpcAddress(i.spec?.ip_address,i.spec?.vpc_id)].join(' ').toLowerCase().includes(q));
  const cks=(cksNodes||[]).filter(n=>(!stateFilter||n.state===stateFilter)&&[n.id,n.cluster_id,n.cluster_name,n.role,n.pool,'cks','eks','k8s','kubernetes',n.worker_id,n.state].join(' ').toLowerCase().includes(q));
  row(t,['Name','VPS ID','Kind','State','Worker','CPU','RAM','IN','OUT','Bandwidth','Disk','IP address','Created','Actions'],true);
  if(!vps.length&&!cks.length){row(t,[stateFilter==='FAILED'?'No failed FileVPS or CKS nodes.':q?'No FileVPS or CKS nodes match this search.':'No FileVPS or CKS nodes yet. Use Create FileVPS or CKS to get started.','','','','','','','','','','','','','']);}
  vps.forEach(i=>{
    const live=instanceLive(i),actions=document.createElement('div');actions.className='row-actions';appendInstanceControls(actions,i);
    const disk=document.createElement('span');disk.textContent=live.disk;disk.title=`${i.spec.disk_mb||0} MB allocated for placement; disk quota is not enforced`;
    row(t,[instanceNameLink(i),instanceIDLink(i),'FileVPS',badge(i.state),i.worker_id,live.cpu,live.ram,live.inn,live.out,live.bw,disk,vpcAddress(i.spec.ip_address,i.spec.vpc_id),i.created?new Date(i.created).toLocaleString():'-',actions]);
  });
  cks.forEach(n=>{
    const name=document.createElement('button');name.type='button';name.className='id-link';name.textContent=cksNodeLabel(n);name.title=n.id;name.onclick=()=>openCKSNode(n);
    const id=document.createElement('button');id.type='button';id.className='id-link';id.textContent=n.id;id.title=n.id;id.onclick=()=>openCKSNode(n);
    const actions=document.createElement('div');actions.className='row-actions';
    const open=document.createElement('button');open.type='button';open.className='small-btn';open.textContent='Open CKS';open.onclick=()=>openCKSNode(n);actions.append(open);
    const cpu=n.cpu?`${n.cpu} vCPU`:'-',ram=n.ram_mb?`${n.ram_mb} MB`:'-',disk=n.disk_mb?`${n.disk_mb} MB allocated`:'-';
    row(t,[name,id,'CKS',badge(n.state),n.worker_id||'-',cpu,ram,'-','-','-',disk,'-',n.created?new Date(n.created).toLocaleString():'-',actions]);
  });
  $('stat-total').textContent=String(instances.length+cksNodes.length);
  if($('stat-filevps'))$('stat-filevps').textContent=String(instances.length);
  if($('stat-cks'))$('stat-cks').textContent=String(cksNodes.length);
  $('stat-running').textContent=String(instances.filter(i=>i.state==='RUNNING').length+cksNodes.filter(n=>n.state==='RUNNING').length);
  if($('stat-stopped'))$('stat-stopped').textContent=String(instances.filter(i=>i.state==='STOPPED').length+cksNodes.filter(n=>n.state==='STOPPED').length);
  const running=instances.filter(i=>i.state==='RUNNING');
  const ramUsed=running.reduce((a,i)=>a+(Number(i.metrics?.memory_current_bytes)||0),0),ramLimit=running.reduce((a,i)=>a+(Number(i.metrics?.memory_limit_bytes)||Number(i.spec?.ram_mb||0)*1048576),0);
  const swapUsed=running.reduce((a,i)=>a+(Number(i.metrics?.swap_current_bytes)||0),0),swapLimit=running.reduce((a,i)=>a+(Number(i.metrics?.swap_limit_bytes??((i.spec?.swap_mb??512)*1048576))),0);
  const diskUsed=running.reduce((a,i)=>a+(Number(i.metrics?.disk_usage_bytes)||0),0),diskLimit=running.reduce((a,i)=>a+(Number(i.spec?.disk_mb||0)*1048576),0);
  $('stat-ram').textContent=fmtBytes(ramUsed)+' used';
  if($('stat-ram-note'))$('stat-ram-note').textContent=`${fmtBytes(ramUsed)} of ${fmtBytes(ramLimit)} across running VPS`;
  if($('stat-swap')){$('stat-swap').textContent=fmtBytes(swapUsed)+' used';if($('stat-swap-note'))$('stat-swap-note').textContent=`${fmtBytes(swapUsed)} of ${fmtBytes(swapLimit)} across running VPS`;}
  if($('stat-disk')){$('stat-disk').textContent=fmtBytes(diskUsed)+' used';if($('stat-disk-note'))$('stat-disk-note').textContent=`${fmtBytes(diskUsed)} of ${fmtBytes(diskLimit)} placement · no hard quota`;}
  $('stat-cpu').textContent=running.length?`${(running.reduce((a,i)=>a+(Number(i.metrics?.cpu_percent)||0),0)/running.length).toFixed(1)}% avg`:`${instances.reduce((a,i)=>a+(+i.spec.cpu||0),0).toFixed(2)} cores`;
  const netSum=(fn)=>running.reduce((n,i)=>n+(Number(fn(i))||0),0);
  if($('stat-net-in')){$('stat-net-in').textContent=fmtBps(netSum(i=>i.metrics?.net_rx_bps));$('stat-net-out').textContent=fmtBps(netSum(i=>i.metrics?.net_tx_bps));$('stat-net-bw').textContent=fmtBps(netSum(i=>i.metrics?.net_rx_bps)+netSum(i=>i.metrics?.net_tx_bps));}
  const cpuAvg=running.length?(running.reduce((a,i)=>a+(Number(i.metrics?.cpu_percent)||0),0)/running.length):0;
  const vpsIn=netSum(i=>i.metrics?.net_rx_bps),vpsOut=netSum(i=>i.metrics?.net_tx_bps);
  if(typeof setBitPie==='function'){
    setBitPie('pie-inst-cpu',cpuAvg,100,cpuAvg.toFixed(1)+'%',Math.max(0,100-cpuAvg).toFixed(1)+'%',cpuAvg.toFixed(1)+'% avg');
    setBitPie('pie-inst-ram',ramUsed,ramLimit,fmtBytes(ramUsed),fmtBytes(Math.max(0,ramLimit-ramUsed)),fmtBytes(ramLimit));
    setBitPie('pie-inst-swap',swapUsed,swapLimit,fmtBytes(swapUsed),fmtBytes(Math.max(0,swapLimit-swapUsed)),fmtBytes(swapLimit));
    setBitPie('pie-inst-disk',diskUsed,diskLimit,fmtBytes(diskUsed),fmtBytes(Math.max(0,diskLimit-diskUsed)),fmtBytes(diskLimit));
    setBitPie('pie-inst-net',vpsIn,vpsIn+vpsOut,fmtBps(vpsIn),fmtBps(vpsOut),fmtBps(vpsIn+vpsOut));
    if($('pie-inst-net-alloc'))$('pie-inst-net-alloc').textContent=fmtBps(vpsIn+vpsOut);
    setBitPie('pie-inst-bw',vpsIn,vpsIn+vpsOut,fmtBps(vpsIn),fmtBps(vpsOut),fmtBps(vpsIn+vpsOut));
  }
}

function setHomeMeter(prefix,used,total,label){const safeTotal=Math.max(0,Number(total)||0),safeUsed=Math.max(0,Number(used)||0),pct=safeTotal?Math.max(0,Math.min(100,safeUsed/safeTotal*100)):0,bar=$(prefix+'-bar'),meter=$(prefix+'-meter'),chip=$(prefix+'-pct');if(!bar||!meter)return;bar.style.width=pct.toFixed(1)+'%';bar.className='meter-fill '+(pct>=90?'level-bad':pct>=70?'level-warn':'level-good');meter.setAttribute('aria-valuenow',pct.toFixed(1));meter.setAttribute('aria-valuetext',`${pct.toFixed(1)}% ${label}`);meter.closest('.stat')?.setAttribute('data-util',pct>=90?'high':pct>=70?'mid':'low');if(chip){chip.textContent=safeTotal?pct.toFixed(0)+'%':'-';chip.dataset.level=pct>=90?'bad':pct>=70?'warn':'good';}}
function setBitPie(id,used,total,fmtUsed,fmtFree,fmtAlloc){
  const safeTotal=Math.max(0,Number(total)||0),safeUsed=Math.max(0,Number(used)||0);
  const pct=safeTotal?Math.max(0,Math.min(100,safeUsed/safeTotal*100)):0;
  const arc=$(id+'-arc'),num=$(id+'-pct'),u=$(id+'-used'),f=$(id+'-free'),a=$(id+'-alloc');
  if(arc){
    const level=pct>=90?'bad':pct>=70?'warn':'good';
    arc.style.strokeDasharray=pct.toFixed(1)+' 100';
    arc.dataset.level=level;
    arc.closest('.bit-card')?.setAttribute('data-util',pct>=90?'high':pct>=70?'mid':'low');
  }
  if(num)num.textContent=safeTotal?Math.round(pct)+'%':'-';
  if(u)u.textContent=fmtUsed!=null?fmtUsed:(safeTotal?safeUsed.toFixed(2):'-');
  if(f)f.textContent=fmtFree!=null?fmtFree:(safeTotal?Math.max(0,safeTotal-safeUsed).toFixed(2):'-');
  if(a)a.textContent=fmtAlloc!=null?fmtAlloc:(safeTotal?safeTotal.toFixed(2):'-');
}

function renderHome(){$('home-loading').hidden=true;$('home-loading').classList.remove('load-error');const running=instances.filter(i=>i.state==='RUNNING'),failed=instances.filter(i=>i.state==='FAILED'),pending=instances.filter(i=>i.state==='PENDING'),activeWorkers=workers.filter(w=>w.status==='active'),sum=(list,fn)=>list.reduce((n,x)=>n+(Number(fn(x))||0),0),ramUsed=sum(running,i=>i.metrics?.memory_current_bytes),ramLimit=sum(running,i=>i.metrics?.memory_limit_bytes||Number(i.spec?.ram_mb)*1048576),swapUsed=sum(running,i=>i.metrics?.swap_current_bytes),swapLimit=sum(running,i=>i.metrics?.swap_limit_bytes??Number(i.spec?.swap_mb??512)*1048576),hostRAM=sum(activeWorkers,w=>w.capacity?.ram_total),hostFree=sum(activeWorkers,w=>w.capacity?.mem_available),hostDiskTotal=sum(activeWorkers,w=>w.capacity?.disk_total),hostDiskFree=sum(activeWorkers,w=>w.capacity?.disk_free),hostCores=sum(activeWorkers,w=>w.capacity?.cpu_cores),hostCPUUsed=sum(activeWorkers,w=>(Number(w.capacity?.cpu_util)||0)*(Number(w.capacity?.cpu_cores)||0)),hostSwapTotal=sum(activeWorkers,w=>w.capacity?.swap_total),hostSwapFree=sum(activeWorkers,w=>w.capacity?.swap_free),zramTotal=sum(activeWorkers,w=>w.capacity?.zram_logical),zramOriginal=sum(activeWorkers,w=>w.capacity?.zram_original),zramUsed=sum(activeWorkers,w=>w.capacity?.zram_used),zramCompressed=sum(activeWorkers,w=>w.capacity?.zram_compressed),zramWorkers=activeWorkers.filter(w=>w.capacity?.zram_active).length,protectedCount=instances.filter(i=>i.spec?.protection_mode!==false).length,wafCount=instances.filter(i=>i.service_installs?.['owasp-waf']==='INSTALLED').length,sample=usageHistory[usageHistory.length-1]||{cpuUsed:0,cpuLimit:0},cpuPct=sample.cpuLimit?sample.cpuUsed/sample.cpuLimit*100:0,hostCpuPct=hostCores?hostCPUUsed/hostCores*100:0,psiWorkers=activeWorkers.map(w=>({id:w.id,value:Number(w.capacity?.psi_mem_some10)||0})),maxPsi=psiWorkers.reduce((best,w)=>w.value>best.value?w:best,{id:'',value:-1});$('home-total').textContent=String(instances.length);$('home-running').textContent=`${running.length} / ${instances.length}`;$('home-failed').textContent=String(failed.length);$('home-failed-link').classList.toggle('attention-clear',failed.length===0);$('home-failed-link').classList.toggle('attention-required',failed.length>0);$('home-failed-note').textContent=failed.length?'Failed VPS instances · click to review':'No failed VPS instances';$('home-workers').textContent=`${activeWorkers.length} / ${workers.length}`;$('home-ram').textContent=`${fmtBytes(ramUsed)} / ${fmtBytes(ramLimit)}`;$('home-cpu').textContent=`${sample.cpuUsed.toFixed(2)} / ${sample.cpuLimit.toFixed(2)} cores`;$('home-host-ram').textContent=`${fmtBytes(hostFree)} / ${fmtBytes(hostRAM)} free`;$('home-host-cpu').textContent=`${hostCpuPct.toFixed(1)}%`;if($('home-host-cpu-card'))$('home-host-cpu-card').textContent=`${hostCpuPct.toFixed(1)}%`;if($('home-host-cpu-pct')){$('home-host-cpu-pct').textContent=hostCores?`${hostCpuPct.toFixed(0)}%`:'-';$('home-host-cpu-pct').dataset.level=hostCpuPct>=90?'bad':hostCpuPct>=70?'warn':'good';}$('home-host-disk').textContent=`${fmtBytes(hostDiskFree)} / ${fmtBytes(hostDiskTotal)} free`;$('home-swap').textContent=`${fmtBytes(swapUsed)} / ${fmtBytes(swapLimit)}`;$('home-host-swap').textContent=`${fmtBytes(hostSwapTotal-hostSwapFree)} / ${fmtBytes(hostSwapTotal)}`;$('home-zram-state').textContent=zramWorkers?'Active · '+zramWorkers:'Off';$('home-zram-state').className='badge '+(zramWorkers?'RUNNING':'STOPPED');const zramMetricsComplete=activeWorkers.filter(w=>w.capacity?.zram_active).every(w=>Object.prototype.hasOwnProperty.call(w.capacity||{},'zram_original'));$('home-zram').textContent=!zramWorkers?'No active worker reports ZRAM':zramMetricsComplete?`${fmtBytes(Math.max(0,zramTotal-zramOriginal))} free of ${fmtBytes(zramTotal)} · ${fmtBytes(zramUsed)} RAM used · ${fmtBytes(zramCompressed)} compressed`:'Logical usage unavailable · update worker';$('home-security').textContent=`Protection ${protectedCount}/${instances.length} · WAF ${wafCount}/${instances.length}`;$('home-security-note').textContent='Protection Mode is filesystem-level; OWASP CRS WAF protects HTTP via Nginx.';$('home-pending').textContent=String(pending.length);$('home-memory-psi').textContent=maxPsi.value>=0?`${maxPsi.value.toFixed(1)}% max`:'-';if($('home-memory-psi-pct')){$('home-memory-psi-pct').textContent=maxPsi.value>=0?`${maxPsi.value.toFixed(0)}%`:'-';$('home-memory-psi-pct').dataset.level=maxPsi.value>=80?'bad':maxPsi.value>=50?'warn':'good';}setHomeMeter('home-memory-psi',Math.max(0,maxPsi.value),100,'memory PSI');setBitPie('pie-psi',Math.max(0,maxPsi.value),100,(maxPsi.value>=0?maxPsi.value.toFixed(1)+'%':'-'),(maxPsi.value>=0?Math.max(0,100-maxPsi.value).toFixed(1)+'%':'-'),'100%');setHomeMeter('home-memory-psi',Math.max(0,maxPsi.value),100,'memory PSI');const vpsIn=sum(running,i=>i.metrics?.net_rx_bps),vpsOut=sum(running,i=>i.metrics?.net_tx_bps),hostIn=sum(activeWorkers,w=>w.capacity?.net_rx_bps),hostOut=sum(activeWorkers,w=>w.capacity?.net_tx_bps);if($('home-net-in')){$('home-net-in').textContent=fmtBps(vpsIn);$('home-net-out').textContent=fmtBps(vpsOut);$('home-net-bw').textContent=fmtBps(vpsIn+vpsOut);$('home-net-in-note').textContent=fmtBytes(sum(running,i=>i.metrics?.net_rx_bytes))+' received';$('home-net-out-note').textContent=fmtBytes(sum(running,i=>i.metrics?.net_tx_bytes))+' sent';$('home-net-bw-note').textContent='IN + OUT across running VPS';$('home-host-net-in').textContent=fmtBps(hostIn);$('home-host-net-out').textContent=fmtBps(hostOut);$('home-host-net-bw').textContent=fmtBps(hostIn+hostOut);setBitPie('pie-vps-net',vpsIn,vpsIn+vpsOut,fmtBps(vpsIn),fmtBps(vpsOut),fmtBps(vpsIn+vpsOut));if($('pie-vps-net-alloc'))$('pie-vps-net-alloc').textContent=fmtBps(vpsIn+vpsOut);setBitPie('pie-vps-bw',vpsIn,vpsIn+vpsOut,fmtBps(vpsIn),fmtBps(vpsOut),fmtBps(vpsIn+vpsOut));setBitPie('pie-worker-net',hostIn,hostIn+hostOut,fmtBps(hostIn),fmtBps(hostOut),fmtBps(hostIn+hostOut));if($('pie-worker-net-alloc'))$('pie-worker-net-alloc').textContent=fmtBps(hostIn+hostOut);setBitPie('pie-worker-bw',hostIn,hostIn+hostOut,fmtBps(hostIn),fmtBps(hostOut),fmtBps(hostIn+hostOut));$('home-host-net-in-note').textContent=fmtBytes(sum(activeWorkers,w=>w.capacity?.net_rx_bytes))+' received';$('home-host-net-out-note').textContent=fmtBytes(sum(activeWorkers,w=>w.capacity?.net_tx_bytes))+' sent';$('home-host-net-bw-note').textContent='IN + OUT on worker uplink NICs';}setHomeMeter('home-ram',ramUsed,ramLimit,'guest RAM used');setHomeMeter('home-cpu',sample.cpuUsed,sample.cpuLimit,'guest CPU used');setBitPie('pie-cpu',sample.cpuUsed,sample.cpuLimit,(sample.cpuUsed||0).toFixed(2)+' c',Math.max(0,(sample.cpuLimit||0)-(sample.cpuUsed||0)).toFixed(2)+' c',(sample.cpuLimit||0).toFixed(2)+' c');setBitPie('pie-ram',ramUsed,ramLimit,fmtBytes(ramUsed),fmtBytes(Math.max(0,ramLimit-ramUsed)),fmtBytes(ramLimit));setBitPie('pie-swap',swapUsed,swapLimit,fmtBytes(swapUsed),fmtBytes(Math.max(0,swapLimit-swapUsed)),fmtBytes(swapLimit));setBitPie('pie-host-ram',hostRAM-hostFree,hostRAM,fmtBytes(hostRAM-hostFree),fmtBytes(hostFree),fmtBytes(hostRAM));setBitPie('pie-host-swap',hostSwapTotal-hostSwapFree,hostSwapTotal,fmtBytes(hostSwapTotal-hostSwapFree),fmtBytes(hostSwapFree),fmtBytes(hostSwapTotal));setBitPie('pie-host-disk',hostDiskTotal-hostDiskFree,hostDiskTotal,fmtBytes(hostDiskTotal-hostDiskFree),fmtBytes(hostDiskFree),fmtBytes(hostDiskTotal));setBitPie('pie-host-cpu',hostCPUUsed,hostCores,(hostCPUUsed||0).toFixed(2)+' c',Math.max(0,(hostCores||0)-(hostCPUUsed||0)).toFixed(2)+' c',(hostCores||0).toFixed(2)+' c');setBitPie('pie-health-cpu',hostCPUUsed,hostCores,hostCpuPct.toFixed(1)+'%',Math.max(0,100-hostCpuPct).toFixed(1)+'%',(hostCores||0).toFixed(0)+' c');setHomeMeter('home-host-ram',hostRAM-hostFree,hostRAM,'worker RAM used');setHomeMeter('home-host-cpu',hostCPUUsed,hostCores,'host CPU used');setHomeMeter('home-host-disk',hostDiskTotal-hostDiskFree,hostDiskTotal,'worker disk used');setHomeMeter('home-swap',swapUsed,swapLimit,'guest swap used');setHomeMeter('home-host-swap',hostSwapTotal-hostSwapFree,hostSwapTotal,'host swap used');if(zramMetricsComplete)setHomeMeter('home-zram',zramOriginal,zramTotal,'ZRAM logical capacity used');setBitPie('pie-zram',zramOriginal,zramTotal,fmtBytes(zramOriginal),fmtBytes(Math.max(0,zramTotal-zramOriginal)),fmtBytes(zramTotal));const empty=instances.length===0&&workers.length===0;$('home-empty').hidden=!empty;const vps=$('home-vps-table');row(vps,['VPS','State','Worker','RAM','CPU','Actions'],true);if(!instances.length)row(vps,['No VPS instances yet. Create one to get started.','','','','']);instances.forEach(i=>{const open=document.createElement('button');open.className='id-link';open.textContent=instanceDisplayName(i);open.title=i.id;open.onclick=()=>go('details',i.id);const m=i.metrics||{},cpu=i.state==='RUNNING'?`${(Number(m.cpu_percent)||0).toFixed(1)}%`:'-',ram=i.state==='RUNNING'?`${fmtBytes(m.memory_current_bytes)} / ${fmtBytes(m.memory_limit_bytes)}`:'-',actions=document.createElement('div');actions.className='row-actions';const details=document.createElement('button');details.className='small-btn';details.textContent='Details';details.setAttribute('aria-label',`View details for ${instanceDisplayName(i)}`);details.onclick=()=>go('details',i.id);actions.append(details);row(vps,[open,badge(i.state),i.worker_id,ram,cpu,actions]);});const wt=$('home-worker-table');row(wt,['Worker','Status','Free RAM','CPU','Memory PSI','Host swap'],true);if(!workers.length)row(wt,['No workers enrolled yet.','','','','','']);workers.forEach(w=>{const c=w.capacity||{};row(wt,[w.id,badge(w.status),`${fmtBytes(c.mem_available)} / ${fmtBytes(c.ram_total)}`,`${Math.round((Number(c.cpu_util)||0)*100)}%`,`${Number(c.psi_mem_some10)||0}%`,`${fmtBytes(c.swap_free)} / ${fmtBytes(c.swap_total)}`]);});}
function captureUsageSample(){const now=Date.now(),active=instances.filter(i=>i.state==='RUNNING'),sample={at:now,cpuUsed:0,cpuLimit:0,ramUsed:0,ramLimit:0,swapUsed:0,swapLimit:0,diskUsed:0,diskLimit:0,netIn:0,netOut:0,netBw:0,hostNetIn:0,hostNetOut:0,hostNetBw:0};active.forEach(i=>{const cpu=Number(i.spec?.cpu)||0,m=i.metrics||{},inn=Number(m.net_rx_bps)||0,out=Number(m.net_tx_bps)||0;sample.cpuLimit+=cpu;sample.cpuUsed+=cpu*(Math.max(0,Number(m.cpu_percent)||0)/100);sample.ramUsed+=(Number(m.memory_current_bytes)||0)/1073741824;sample.ramLimit+=(Number(m.memory_limit_bytes)||Number(i.spec?.ram_mb||0)*1048576)/1073741824;sample.swapUsed+=(Number(m.swap_current_bytes)||0)/1073741824;sample.swapLimit+=(Number(m.swap_limit_bytes??((i.spec?.swap_mb??512)*1048576)))/1073741824;sample.diskUsed+=(Number(m.disk_usage_bytes)||0)/1073741824;sample.diskLimit+=(Number(i.spec?.disk_mb||0)*1048576)/1073741824;sample.netIn+=inn;sample.netOut+=out;sample.netBw+=inn+out;});workers.filter(w=>w.status==='active').forEach(w=>{const c=w.capacity||{},inn=Number(c.net_rx_bps)||0,out=Number(c.net_tx_bps)||0;sample.hostNetIn+=inn;sample.hostNetOut+=out;sample.hostNetBw+=inn+out;});const usageSampleEvery=usageHistory.length<24?5000:60000;if(!usageHistory.length||now-usageHistory[usageHistory.length-1].at>=usageSampleEvery){usageHistory.push(sample);while(usageHistory.length&&usageHistory[0].at<now-usageRangeMs['30d'])usageHistory.shift();try{localStorage.setItem(usageHistoryKey,JSON.stringify(usageHistory.map(p=>[p.at,p.cpuUsed,p.cpuLimit,p.ramUsed,p.ramLimit,p.netIn||0,p.netOut||0,p.netBw||0,p.hostNetIn||0,p.hostNetOut||0,p.hostNetBw||0,p.swapUsed||0,p.swapLimit||0,p.diskUsed||0,p.diskLimit||0])));}catch(e){}}else{usageHistory[usageHistory.length-1]=sample;}instances.forEach(i=>{const m=i.metrics||{},inn=Number(m.net_rx_bps)||0,out=Number(m.net_tx_bps)||0,cores=Number(i.spec?.cpu)||0,point={at:now,netIn:inn,netOut:out,netBw:inn+out,cpuUsed:cores*(Math.max(0,Number(m.cpu_percent)||0)/100),cpuLimit:cores,ramUsed:(Number(m.memory_current_bytes)||0)/1073741824,ramLimit:(Number(m.memory_limit_bytes)||Number(i.spec?.ram_mb||0)*1048576)/1073741824,swapUsed:(Number(m.swap_current_bytes)||0)/1073741824,swapLimit:(Number(m.swap_limit_bytes??((i.spec?.swap_mb??512)*1048576)))/1073741824,diskUsed:(Number(m.disk_usage_bytes)||0)/1073741824,diskLimit:(Number(i.spec?.disk_mb||0)*1048576)/1073741824},hist=instanceHistory[i.id]||(instanceHistory[i.id]=[]);if(!hist.length||now-hist[hist.length-1].at>=(hist.length<24?5000:60000)){hist.push(point);while(hist.length&&hist[0].at<now-usageRangeMs['30d'])hist.shift();}else hist[hist.length-1]=point;});try{const packed={};Object.entries(instanceHistory).forEach(([id,rows])=>{packed[id]=rows.map(p=>[p.at,p.netIn,p.netOut,p.netBw,p.cpuUsed||0,p.cpuLimit||0,p.ramUsed||0,p.ramLimit||0,p.swapUsed||0,p.swapLimit||0,p.diskUsed||0,p.diskLimit||0]);});localStorage.setItem(instanceHistoryKey,JSON.stringify(packed));}catch(e){}renderUsageCharts();}
function chartNode(name,attrs={},label=''){const n=document.createElementNS('http://www.w3.org/2000/svg',name);Object.entries(attrs).forEach(([k,v])=>n.setAttribute(k,String(v)));if(label)n.textContent=label;return n;}
function chartAxisValue(v){const n=Math.abs(v);if(n>=1000000000)return (v/1000000000).toFixed(1)+'G';if(n>=1000000)return (v/1000000).toFixed(1)+'M';if(n>=1000)return (v/1000).toFixed(1)+'k';return v!==0&&v<10?v.toFixed(1):String(Math.round(v));}
function usageWindowBounds(){if(usageRange==='custom'&&usageCustom.from&&usageCustom.to){const from=Math.min(usageCustom.from,usageCustom.to),to=Math.max(usageCustom.from,usageCustom.to);return {from,to};}const span=usageRangeMs[usageRange]||usageRangeMs['1h'];return {from:Date.now()-span,to:Date.now()};}
function usageHistoryWindow(source,bounds){const src=source||usageHistory,w=bounds||usageWindowBounds();let all=src.filter(p=>p.at>=w.from&&p.at<=w.to);if(!all.length&&src.length){const span=Math.max(60000,(w.to-w.from)||usageRangeMs['1h']),to=src[src.length-1].at,from=to-span;all=src.filter(p=>p.at>=from&&p.at<=to);if(!all.length)all=src.slice(-Math.min(src.length,120));}const step=Math.max(1,Math.ceil(all.length/360));let history=all.filter((_,i)=>i%step===0);if(all.length&&history[history.length-1]!==all[all.length-1])history.push(all[all.length-1]);if(history.length===1){const only=history[0],anchor={...only,at:Math.min(only.at-1000,w.from||only.at-1000)};history=[anchor,only];}return history;}
function niceChartMax(raw){const v=Math.max(Number(raw)||0,1e-9);const exp=Math.floor(Math.log10(v)),f=v/Math.pow(10,exp),nf=f<=1?1:f<=2?2:f<=5?5:10;return nf*Math.pow(10,exp);}
function chartSmoothPath(points){if(!points.length)return '';if(points.length===1)return 'M'+points[0][0].toFixed(1)+','+points[0][1].toFixed(1);let d='M'+points[0][0].toFixed(1)+','+points[0][1].toFixed(1);for(let i=0;i<points.length-1;i++){const p0=points[i-1]||points[i],p1=points[i],p2=points[i+1],p3=points[i+2]||p2;const c1x=p1[0]+(p2[0]-p0[0])/6,c1y=p1[1]+(p2[1]-p0[1])/6,c2x=p2[0]-(p3[0]-p1[0])/6,c2y=p2[1]-(p3[1]-p1[1])/6;d+=' C'+c1x.toFixed(1)+','+c1y.toFixed(1)+' '+c2x.toFixed(1)+','+c2y.toFixed(1)+' '+p2[0].toFixed(1)+','+p2[1].toFixed(1);}return d;}
function bindChartHover(svg,meta){const overlay=chartNode('rect',{x:meta.left,y:meta.top,width:meta.plotW,height:meta.plotH,fill:'transparent',class:'chart-hit'});const guide=chartNode('line',{x1:0,y1:meta.top,x2:0,y2:meta.top+meta.plotH,class:'chart-crosshair',visibility:'hidden'});const tip=chartNode('g',{class:'chart-tooltip',visibility:'hidden'});const tipBg=chartNode('rect',{rx:8,ry:8,class:'chart-tooltip-bg'});const tipText=chartNode('text',{class:'chart-tooltip-text',x:10,y:15});tip.append(tipBg,tipText);const marks=meta.series.map(s=>{const g=chartNode('g',{class:'chart-hover-mark',visibility:'hidden'});g.append(chartNode('circle',{r:9,class:'chart-hover-ring',style:'stroke:'+s.color}),chartNode('circle',{r:3.4,class:'chart-hover-dot',style:'stroke:'+s.color+';fill:'+s.color}));return g;});svg.append(guide,...marks,tip,overlay);const hide=()=>{guide.setAttribute('visibility','hidden');tip.setAttribute('visibility','hidden');marks.forEach(m=>m.setAttribute('visibility','hidden'));};overlay.addEventListener('mouseleave',hide);overlay.addEventListener('mousemove',e=>{const rect=svg.getBoundingClientRect(),px=(e.clientX-rect.left)*(meta.W/Math.max(rect.width,1));let best=0,bestDist=Infinity;for(let i=0;i<meta.history.length;i++){const dx=Math.abs(meta.x(i)-px);if(dx<bestDist){bestDist=dx;best=i;}}const p=meta.history[best],cx=meta.x(best);guide.setAttribute('x1',cx);guide.setAttribute('x2',cx);guide.setAttribute('visibility','visible');const rows=[chartTimeLabel(p.at,meta.spanMs)];meta.series.forEach((s,idx)=>{const val=Number(p[s.key])||0;rows.push((s.label||s.key)+': '+(meta.formatValue?meta.formatValue(val):val.toFixed(2)));const cy=meta.y(val);marks[idx].setAttribute('transform','translate('+cx+','+cy+')');marks[idx].setAttribute('visibility','visible');});tipText.textContent='';rows.forEach((line,i)=>{const tspan=chartNode('tspan',{x:10,dy:i?13:0},line);tipText.append(tspan);});requestAnimationFrame(()=>{try{const box=tipText.getBBox();tipBg.setAttribute('width',Math.max(78,box.width+20));tipBg.setAttribute('height',box.height+14);}catch(_){tipBg.setAttribute('width',168);tipBg.setAttribute('height',rows.length*13+14);}const tw=Number(tipBg.getAttribute('width')||168);const tx=Math.min(Math.max(meta.left+4,cx+12),meta.W-meta.right-tw-4);const ty=Math.max(meta.top+4,meta.top+8);tip.setAttribute('transform','translate('+tx+','+ty+')');tip.setAttribute('visibility','visible');});});}
function drawCloudChart(svgID,opts){const svg=$(svgID);if(!svg)return;const windowBounds=opts.windowBounds||null,history=usageHistoryWindow(opts.history,windowBounds),series=opts.series||[],last=history[history.length-1],sig=JSON.stringify([windowBounds||usageRange,windowBounds?windowBounds.from:usageCustom.from,windowBounds?windowBounds.to:usageCustom.to,history.length,last&&series.map(s=>Number(last[s.key])||0),'ai-v1']);if(svg.dataset.drawSig===sig){if(opts.currentID&&$(opts.currentID))$(opts.currentID).textContent=last&&opts.formatCurrent?opts.formatCurrent(last):(last?'-':'-');return;}svg.dataset.drawSig=sig;svg.replaceChildren();svg.classList.add('chart-svg','chart-ai');const W=640,H=220,left=50,right=16,top=16,bottom=32,plotW=W-left-right,plotH=H-top-bottom,values=history.flatMap(p=>series.map(s=>Number(p[s.key])||0)),rawMax=Math.max(opts.minMax||1,...values),maxValue=niceChartMax(rawMax*1.14),y=v=>top+plotH-(Math.max(0,v)/maxValue)*plotH,x=i=>left+(history.length<=1?plotW/2:(i/(history.length-1))*plotW),defs=chartNode('defs');
const softGlow=chartNode('filter',{id:svgID+'-glow',x:'-80%',y:'-80%',width:'260%',height:'260%'});softGlow.append(chartNode('feGaussianBlur',{'in':'SourceGraphic',stdDeviation:'2.4',result:'blur'}));const softMerge=chartNode('feMerge');softMerge.append(chartNode('feMergeNode',{'in':'blur'}),chartNode('feMergeNode',{'in':'SourceGraphic'}));softGlow.append(softMerge);defs.append(softGlow);
const hardGlow=chartNode('filter',{id:svgID+'-neon',x:'-100%',y:'-100%',width:'300%',height:'300%'});hardGlow.append(chartNode('feGaussianBlur',{'in':'SourceGraphic',stdDeviation:'3.6',result:'b1'}));hardGlow.append(chartNode('feGaussianBlur',{'in':'SourceGraphic',stdDeviation:'1.2',result:'b2'}));const hardMerge=chartNode('feMerge');hardMerge.append(chartNode('feMergeNode',{'in':'b1'}),chartNode('feMergeNode',{'in':'b2'}),chartNode('feMergeNode',{'in':'SourceGraphic'}));hardGlow.append(hardMerge);defs.append(hardGlow);
const plotGrad=chartNode('linearGradient',{id:svgID+'-plot',x1:'0',y1:'0',x2:'1',y2:'1'});plotGrad.append(chartNode('stop',{offset:'0%','stop-color':'#0a1520'}),chartNode('stop',{offset:'45%','stop-color':'#0d1a28'}),chartNode('stop',{offset:'100%','stop-color':'#101828'}));defs.append(plotGrad);
const vignette=chartNode('radialGradient',{id:svgID+'-vig',cx:'50%',cy:'42%',r:'72%'});vignette.append(chartNode('stop',{offset:'55%','stop-color':'#000','stop-opacity':'0'}),chartNode('stop',{offset:'100%','stop-color':'#000','stop-opacity':'.35'}));defs.append(vignette);
series.forEach((s,si)=>{const g=chartNode('linearGradient',{id:svgID+'-'+s.key,x1:'0',x2:'0',y1:'0',y2:'1'});const topOp=si===0?'.42':'.22';g.append(chartNode('stop',{offset:'0%','stop-color':s.color,'stop-opacity':topOp}),chartNode('stop',{offset:'55%','stop-color':s.color,'stop-opacity':'.12'}),chartNode('stop',{offset:'100%','stop-color':s.color,'stop-opacity':'0'}));defs.append(g);const hx=chartNode('linearGradient',{id:svgID+'-'+s.key+'-stroke',x1:'0',y1:'0',x2:'1',y2:'0'});hx.append(chartNode('stop',{offset:'0%','stop-color':s.color,'stop-opacity':'.35'}),chartNode('stop',{offset:'35%','stop-color':s.color,'stop-opacity':'1'}),chartNode('stop',{offset:'100%','stop-color':s.color,'stop-opacity':'1'}));defs.append(hx);});
svg.append(defs);
svg.append(chartNode('rect',{x:left,y:top,width:plotW,height:plotH,rx:12,class:'chart-plot',fill:'url(#'+svgID+'-plot)'}));
const mesh=chartNode('g',{class:'chart-mesh',opacity:'.55'});
for(let gx=0;gx<=12;gx++){const xx=left+(plotW*gx/12);mesh.append(chartNode('line',{x1:xx,y1:top,x2:xx,y2:top+plotH,class:'chart-mesh-line'}));}
for(let gy=0;gy<=6;gy++){const yy=top+(plotH*gy/6);mesh.append(chartNode('line',{x1:left,y1:yy,x2:left+plotW,y2:yy,class:'chart-mesh-line'}));}
for(let gx=0;gx<=12;gx++){for(let gy=0;gy<=6;gy++){if((gx+gy)%2)continue;mesh.append(chartNode('circle',{cx:left+(plotW*gx/12),cy:top+(plotH*gy/6),r:1.05,class:'chart-mesh-dot'}));}}
svg.append(mesh);
svg.append(chartNode('rect',{x:left,y:top,width:plotW,height:plotH,rx:12,fill:'url(#'+svgID+'-vig)',class:'chart-vignette'}));
svg.append(chartNode('line',{x1:left,y1:top+plotH,x2:left+plotW,y2:top+plotH,class:'chart-baseline'}));
for(let n=0;n<=4;n++){const val=maxValue*n/4,yy=y(val);svg.append(chartNode('text',{x:left-8,y:yy+3,'text-anchor':'end',class:'chart-axis'},chartAxisValue(val)));}
if(opts.currentID&&$(opts.currentID)&&!history.length){$(opts.currentID).textContent='-';svg.append(chartNode('text',{x:W/2,y:H/2,'text-anchor':'middle',class:'chart-empty'},'Waiting for live metrics…'));return;}
if(!history.length){svg.append(chartNode('text',{x:W/2,y:H/2,'text-anchor':'middle',class:'chart-empty'},'Waiting for live metrics…'));return;}
series.forEach((s,si)=>{const points=history.map((p,i)=>[x(i),y(Number(p[s.key])||0)]);const path=chartSmoothPath(points);if(s.area){svg.append(chartNode('path',{d:path+' L'+x(history.length-1).toFixed(1)+','+(top+plotH).toFixed(1)+' L'+x(0).toFixed(1)+','+(top+plotH).toFixed(1)+' Z',class:'chart-area'+(si?' chart-area-secondary':''),style:'fill:url(#'+svgID+'-'+s.key+')'}));}svg.append(chartNode('path',{d:path,class:'chart-line chart-line-glow '+(s.cls||''),style:'stroke:'+s.color,filter:'url(#'+svgID+'-neon)','stroke-linecap':'round','stroke-linejoin':'round'}));svg.append(chartNode('path',{d:path,class:'chart-line '+(s.cls||''),style:'stroke:url(#'+svgID+'-'+s.key+'-stroke)','stroke-linecap':'round','stroke-linejoin':'round'}));const lv=Number(history[history.length-1][s.key])||0,cx=x(history.length-1),cy=y(lv);const node=chartNode('g',{class:'chart-live-node'});node.append(chartNode('circle',{cx,cy,r:10,class:'chart-live-ping',style:'stroke:'+s.color}));node.append(chartNode('circle',{cx,cy,r:5.2,class:'chart-dot',style:'stroke:'+s.color,filter:'url(#'+svgID+'-glow)'}));node.append(chartNode('circle',{cx,cy,r:2.1,class:'chart-dot-core',style:'fill:'+s.color}));node.append(chartNode('title',{},(s.label||s.key)+': '+(opts.formatValue?opts.formatValue(lv):lv.toFixed(2))));svg.append(node);});
const spanMs=Math.max(60000,(history[history.length-1].at-history[0].at)||0),startLabel=chartTimeLabel(history[0].at,spanMs),endLabel=chartTimeLabel(history[history.length-1].at,spanMs),midLabel=chartTimeLabel(history[Math.floor(history.length/2)].at,spanMs);svg.append(chartNode('text',{x:left,y:H-8,class:'chart-axis'},startLabel),chartNode('text',{x:W/2,y:H-8,'text-anchor':'middle',class:'chart-axis'},midLabel),chartNode('text',{x:W-right,y:H-8,'text-anchor':'end',class:'chart-axis'},endLabel));bindChartHover(svg,{W,H,left,right,top,plotW,plotH,history,series,x,y,spanMs,formatValue:opts.formatValue});if(opts.currentID&&$(opts.currentID))$(opts.currentID).textContent=opts.formatCurrent?opts.formatCurrent(history[history.length-1]):'-';}

function drawUsageChart(svgID,usedKey,limitKey,unit,currentID,history){drawCloudChart(svgID,{history,series:[{key:usedKey,label:'Used',color:'#5cf2d6',area:true},{key:limitKey,label:'Allocated',color:'#8b7bff',cls:'secondary-line'}],currentID,formatCurrent:last=>(Number(last[usedKey])||0).toFixed(2)+' / '+(Number(last[limitKey])||0).toFixed(2)+' '+unit});}
function drawRatePair(svgID,inKey,outKey,currentID,history){drawCloudChart(svgID,{history,series:[{key:inKey,label:'IN',color:'#5cf2d6',cls:'in-line',area:true},{key:outKey,label:'OUT',color:'#ff9f6b',cls:'out-line',area:true}],currentID,formatValue:fmtBps,formatCurrent:last=>fmtBps(last[inKey])+' in · '+fmtBps(last[outKey])+' out'});}
function drawRateSingle(svgID,key,currentID,history){drawCloudChart(svgID,{history,series:[{key,label:'Bandwidth',color:'#7aa7ff',cls:'bw-line',area:true}],currentID,formatValue:fmtBps,formatCurrent:last=>fmtBps(last[key])});}

function chartTimeLabel(at,spanMs){if(!at)return '';const d=new Date(at);if(spanMs<=3600000)return d.toLocaleTimeString([],{hour:'2-digit',minute:'2-digit',second:'2-digit'});if(spanMs<=86400000)return d.toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'});return d.toLocaleString([],{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'});}
function mergeMetricsPoints(target,points){if(!Array.isArray(points)||!points.length)return target;const byAt=new Map();(target||[]).forEach(p=>{if(p&&Number.isFinite(+p.at))byAt.set(+p.at,p);});points.forEach(p=>{if(!p||!Number.isFinite(+p.at))return;const at=+p.at,prev=byAt.get(at)||{at};byAt.set(at,Object.assign({},prev,p,{at}));});return [...byAt.values()].sort((a,b)=>a.at-b.at);}
async function fetchMetricsSeries(series,from,to){if(!T||!series)return[];try{const q=new URLSearchParams({series,from:String(from||0),to:String(to||Date.now())});const out=await api('GET','/api/metrics/history?'+q.toString());return Array.isArray(out?.points)?out.points:[];}catch(e){return[];}}
async function loadServerMetricsHistory(){if(!T)return;const w=usageWindowBounds(),from=w.from,to=w.to;const fleet=await fetchMetricsSeries('fleet',from,to);if(fleet.length){const merged=mergeMetricsPoints(usageHistory,fleet);usageHistory.splice(0,usageHistory.length,...merged);while(usageHistory.length&&usageHistory[0].at<Date.now()-usageRangeMs['30d'])usageHistory.shift();try{localStorage.setItem(usageHistoryKey,JSON.stringify(usageHistory.map(p=>[p.at,p.cpuUsed,p.cpuLimit,p.ramUsed,p.ramLimit,p.netIn||0,p.netOut||0,p.netBw||0,p.hostNetIn||0,p.hostNetOut||0,p.hostNetBw||0,p.swapUsed||0,p.swapLimit||0,p.diskUsed||0,p.diskLimit||0])));}catch(e){}}const ids=new Set(Object.keys(instanceHistory));if(selectedID)ids.add(selectedID);(instances||[]).forEach(i=>ids.add(i.id));for(const id of ids){if(!id)continue;const pts=await fetchMetricsSeries('instance:'+id,from,to);if(!pts.length)continue;instanceHistory[id]=mergeMetricsPoints(instanceHistory[id]||[],pts);}try{const packed={};Object.entries(instanceHistory).forEach(([id,rows])=>{packed[id]=rows.map(p=>[p.at,p.netIn||0,p.netOut||0,p.netBw||0,p.cpuUsed||0,p.cpuLimit||0,p.ramUsed||0,p.ramLimit||0,p.swapUsed||0,p.swapLimit||0,p.diskUsed||0,p.diskLimit||0]);});localStorage.setItem(instanceHistoryKey,JSON.stringify(packed));}catch(e){}invalidateUsageCharts();renderUsageCharts();}
function invalidateUsageCharts(){document.querySelectorAll('svg.chart-svg[data-draw-sig],svg.chart-svg').forEach(svg=>{delete svg.dataset.drawSig;});}
function renderUsageCharts(){drawUsageChart('chart-cpu','cpuUsed','cpuLimit','cores','chart-cpu-current');drawUsageChart('chart-memory','ramUsed','ramLimit','GB','chart-memory-current');drawUsageChart('chart-swap','swapUsed','swapLimit','GB','chart-swap-current');drawUsageChart('chart-disk','diskUsed','diskLimit','GB','chart-disk-current');drawRatePair('chart-net','netIn','netOut','chart-net-current');drawRateSingle('chart-bw','netBw','chart-bw-current');drawRatePair('chart-host-net','hostNetIn','hostNetOut','chart-host-net-current');drawRateSingle('chart-host-bw','hostNetBw','chart-host-bw-current');const detail=instanceHistory[selectedID]||[];drawUsageChart('chart-detail-cpu','cpuUsed','cpuLimit','cores','chart-detail-cpu-current',detail);drawUsageChart('chart-detail-memory','ramUsed','ramLimit','GB','chart-detail-memory-current',detail);drawUsageChart('chart-detail-swap','swapUsed','swapLimit','GB','chart-detail-swap-current',detail);drawUsageChart('chart-detail-disk','diskUsed','diskLimit','GB','chart-detail-disk-current',detail);drawRatePair('chart-detail-net','netIn','netOut','chart-detail-net-current',detail);drawRateSingle('chart-detail-bw','netBw','chart-detail-bw-current',detail);if(typeof window.renderLBCharts==='function')window.renderLBCharts();if(typeof window.renderDockerCharts==='function')window.renderDockerCharts();}
function toChartLocal(ms){if(!ms)return '';const d=new Date(ms),p=n=>String(n).padStart(2,'0');return `${d.getFullYear()}-${p(d.getMonth()+1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;}
function fillChartCustomInputs(){document.querySelectorAll('.chart-custom').forEach(box=>{const from=box.querySelector('[data-chart-from]'),to=box.querySelector('[data-chart-to]');if(from)from.value=toChartLocal(usageCustom.from);if(to)to.value=toChartLocal(usageCustom.to);});}
function saveUsageRange(next){usageRange=next;localStorage.setItem(usageRangeKey,usageRange);updateChartRangeButtons();loadServerMetricsHistory().finally(()=>{invalidateUsageCharts();renderUsageCharts();});}
function applyChartCustom(box){const from=Date.parse(box.querySelector('[data-chart-from]')?.value||''),to=Date.parse(box.querySelector('[data-chart-to]')?.value||'');if(!Number.isFinite(from)||!Number.isFinite(to)||from===to)return;usageCustom={from:Math.min(from,to),to:Math.max(from,to)};localStorage.setItem(usageCustomKey,JSON.stringify(usageCustom));saveUsageRange('custom');}
function updateChartRangeButtons(){document.querySelectorAll('[data-chart-range]').forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.chartRange===usageRange)));document.querySelectorAll('.chart-custom').forEach(box=>box.hidden=usageRange!=='custom');if(usageRange==='custom')fillChartCustomInputs();}
document.querySelectorAll('[data-chart-range]').forEach(b=>b.addEventListener('click',()=>{if(b.dataset.chartRange==='custom'){if(!(usageCustom.from&&usageCustom.to)){usageCustom={from:Date.now()-86400000,to:Date.now()};localStorage.setItem(usageCustomKey,JSON.stringify(usageCustom));}saveUsageRange('custom');return;}saveUsageRange(b.dataset.chartRange);}));
document.querySelectorAll('[data-chart-custom-apply]').forEach(b=>b.addEventListener('click',()=>applyChartCustom(b.closest('.chart-custom'))));
updateChartRangeButtons();
function renderWorkerS3Summary(){const nodes=workers.filter(w=>w.status==='active'&&w.capacity?.s3_node&&Date.now()-new Date(w.last_heartbeat||0).getTime()<60000),total=nodes.reduce((n,w)=>n+(Number(w.capacity.s3_disk_total)||0),0),free=nodes.reduce((n,w)=>n+(Number(w.capacity.s3_disk_free)||0),0);if($('worker-stat-s3')){$('worker-stat-s3').textContent=nodes.length?fmtBytes(free)+' free':'-';if($('worker-stat-s3-note'))$('worker-stat-s3-note').textContent=nodes.length?`${nodes.length} online · ${fmtBytes(total)} total`:'Waiting for storage nodes';}const target=$('worker-s3-summary');if(!target)return;target.hidden=true;}
function renderWorkers(){
  renderWorkerS3Summary();
  const activeWorkers=workers.filter(w=>w.status==='active'),sum=(list,fn)=>list.reduce((n,x)=>n+(Number(fn(x))||0),0),hostRAM=sum(activeWorkers,w=>w.capacity?.ram_total),hostFree=sum(activeWorkers,w=>w.capacity?.mem_available),hostDiskTotal=sum(activeWorkers,w=>w.capacity?.disk_total),hostDiskFree=sum(activeWorkers,w=>w.capacity?.disk_free),hostCores=sum(activeWorkers,w=>w.capacity?.cpu_cores),hostCPUUsed=sum(activeWorkers,w=>(Number(w.capacity?.cpu_util)||0)*(Number(w.capacity?.cpu_cores)||0));
  if($('worker-stat-cpu')){$('worker-stat-cpu').textContent=hostCores?`${(hostCPUUsed/hostCores*100).toFixed(1)}%`:'-';$('worker-stat-cpu-note').textContent=`${hostCPUUsed.toFixed(2)} of ${hostCores.toFixed(2)} cores · ${activeWorkers.length} active`;$('worker-stat-ram').textContent=`${fmtBytes(Math.max(0,hostRAM-hostFree))} used`;$('worker-stat-ram-note').textContent=`${fmtBytes(hostFree)} free of ${fmtBytes(hostRAM)}`;$('worker-stat-disk').textContent=`${fmtBytes(Math.max(0,hostDiskTotal-hostDiskFree))} used`;$('worker-stat-disk-note').textContent=`${fmtBytes(hostDiskFree)} free of ${fmtBytes(hostDiskTotal)}`;}const hostRamUsed=Math.max(0,hostRAM-hostFree),hostDiskUsed=Math.max(0,hostDiskTotal-hostDiskFree),hostCpuPct=hostCores?hostCPUUsed/hostCores*100:0,hostIn=sum(activeWorkers,w=>w.capacity?.net_rx_bps),hostOut=sum(activeWorkers,w=>w.capacity?.net_tx_bps);if($('worker-host-cpu')){$('worker-host-cpu').textContent=hostCores?`${hostCpuPct.toFixed(1)}%`:'-';$('worker-host-cpu-note').textContent=`${hostCPUUsed.toFixed(2)} / ${hostCores.toFixed(2)} cores`;$('worker-host-ram').textContent=fmtBytes(hostRamUsed)+' used';$('worker-host-ram-note').textContent=`${fmtBytes(hostFree)} free of ${fmtBytes(hostRAM)}`;$('worker-host-disk').textContent=fmtBytes(hostDiskUsed)+' used';$('worker-host-disk-note').textContent=`${fmtBytes(hostDiskFree)} free of ${fmtBytes(hostDiskTotal)}`;}if(typeof setBitPie==='function'){setBitPie('pie-worker-cpu',hostCPUUsed,hostCores,hostCPUUsed.toFixed(2)+' c',Math.max(0,hostCores-hostCPUUsed).toFixed(2)+' c',hostCores.toFixed(2)+' c');setBitPie('pie-worker-ram',hostRamUsed,hostRAM,fmtBytes(hostRamUsed),fmtBytes(hostFree),fmtBytes(hostRAM));setBitPie('pie-worker-disk',hostDiskUsed,hostDiskTotal,fmtBytes(hostDiskUsed),fmtBytes(hostDiskFree),fmtBytes(hostDiskTotal));setBitPie('pie-worker-net',hostIn,hostIn+hostOut,fmtBps(hostIn),fmtBps(hostOut),fmtBps(hostIn+hostOut));if($('pie-worker-net-alloc'))$('pie-worker-net-alloc').textContent=fmtBps(hostIn+hostOut);setBitPie('pie-worker-bw',hostIn,hostIn+hostOut,fmtBps(hostIn),fmtBps(hostOut),fmtBps(hostIn+hostOut));}
  const cards=$('worker-memory-cards');
  if(cards){
    const capId=id=>'cap-'+String(id||'').replace(/[^a-zA-Z0-9_-]/g,'-');
    const makeCapPie=(bit,kicker,pid,aLabel,bLabel,cLabel)=>{
      const card=document.createElement('section');card.className='bit-card';card.dataset.bit=bit;
      card.innerHTML=`<div class="bit-side"><div class="bit-kicker">${kicker}</div><div class="bit-value" id="${pid}-val">-</div><div class="bit-sub" id="${pid}-note">-</div><div class="bit-pie-row"><svg class="bit-pie" id="${pid}" viewBox="0 0 72 72" role="img" aria-label="${kicker} pie"><circle class="bit-pie-track" cx="36" cy="36" r="26"></circle><circle class="bit-pie-arc" id="${pid}-arc" cx="36" cy="36" r="26" pathLength="100"></circle><text class="bit-pie-num" id="${pid}-pct" x="36" y="39" text-anchor="middle">-</text></svg><div class="bit-nums"><div><span>${aLabel}</span><strong id="${pid}-used">-</strong></div><div><span>${bLabel}</span><strong id="${pid}-free">-</strong></div><div><span>${cLabel}</span><strong id="${pid}-alloc">-</strong></div></div></div></div>`;
      return card;
    };
    const fillCapBoard=(group,w)=>{
      const c=w.capacity||{},pid=capId(w.id);
      const cpuPct=Math.max(0,Math.min(100,(Number(c.cpu_util)||0)*100)),cores=Number(c.cpu_cores)||0,cpuUsed=cores*(Number(c.cpu_util)||0);
      const ramUsed=Math.max(0,(Number(c.ram_total)||0)-(Number(c.mem_available)||0));
      const diskUsed=Math.max(0,(Number(c.disk_total)||0)-(Number(c.disk_free)||0));
      const swapUsed=Math.max(0,(Number(c.swap_total)||0)-(Number(c.swap_free)||0));
      const hostIn=Number(c.net_rx_bps)||0,hostOut=Number(c.net_tx_bps)||0;
      const devices=Array.isArray(c.gpus)?c.gpus:[];
      const note=group.querySelector('.ov-board-note');
      if(note)note.textContent=`${w.status||'-'} · ${w.hostname||w.id}`;
      if($(pid+'-cpu-val')){$(pid+'-cpu-val').textContent=cpuPct.toFixed(1)+'%';$(pid+'-cpu-note').textContent=`${(Number(c.cpu_allocated)||0).toFixed(2)} of ${cores.toFixed(2)} cores allocated`;}
      if($(pid+'-ram-val')){$(pid+'-ram-val').textContent=fmtBytes(ramUsed)+' used';$(pid+'-ram-note').textContent=`${fmtBytes(c.mem_available)} free of ${fmtBytes(c.ram_total)}`;}
      if($(pid+'-disk-val')){$(pid+'-disk-val').textContent=fmtBytes(diskUsed)+' used';$(pid+'-disk-note').textContent=`${fmtBytes(c.disk_free)} free of ${fmtBytes(c.disk_total)}`;}
      if($(pid+'-swap-val')){$(pid+'-swap-val').textContent=fmtBytes(c.swap_free)+' free';$(pid+'-swap-note').textContent=`${fmtBytes(c.swap_total)} total · ${fmtBytes(c.swap_committed)} reserved`;}
      if($(pid+'-zram-val')){$(pid+'-zram-val').textContent=c.zram_active?fmtBytes(c.zram_logical)+' active':'Off / unavailable';$(pid+'-zram-note').textContent=c.zram_active?`${fmtBytes(c.zram_used)} RAM used · ${fmtBytes(c.zram_compressed)} compressed`:'Enable on a Linux worker with ZRAM support';}
      if($(pid+'-gpu-val')){$(pid+'-gpu-val').textContent=devices.length?`${devices.length} detected`:'None detected';$(pid+'-gpu-note').textContent=devices.length?devices.map(d=>`${d.name||'NVIDIA GPU'} · ${fmtBytes(d.memory_free_bytes)} free / ${fmtBytes(d.memory_total_bytes)} VRAM · ${Number(d.utilization_percent)||0}%`).join(' · '):'GPU discovery only; assignment not enabled';}
      if($(pid+'-net-val')){$(pid+'-net-val').textContent=fmtBps(hostIn+hostOut);$(pid+'-net-note').textContent=`${fmtBps(hostIn)} in · ${fmtBps(hostOut)} out`;}
      if(typeof setBitPie==='function'){
        setBitPie(pid+'-cpu',cpuUsed,cores,cpuUsed.toFixed(2)+' c',Math.max(0,cores-cpuUsed).toFixed(2)+' c',cores.toFixed(2)+' c');
        setBitPie(pid+'-ram',ramUsed,c.ram_total,fmtBytes(ramUsed),fmtBytes(c.mem_available),fmtBytes(c.ram_total));
        setBitPie(pid+'-disk',diskUsed,c.disk_total,fmtBytes(diskUsed),fmtBytes(c.disk_free),fmtBytes(c.disk_total));
        setBitPie(pid+'-swap',swapUsed,c.swap_total,fmtBytes(swapUsed),fmtBytes(c.swap_free),fmtBytes(c.swap_total));
        const zUsed=Number(c.zram_original)||Number(c.zram_used)||0,zTotal=Number(c.zram_logical)||0;
        setBitPie(pid+'-zram',c.zram_active?zUsed:0,c.zram_active?zTotal:0,c.zram_active?fmtBytes(zUsed):'-',c.zram_active?fmtBytes(Math.max(0,zTotal-zUsed)):'-',c.zram_active?fmtBytes(zTotal):'-');
        const gUsed=devices.reduce((n,d)=>n+(Number(d.memory_total_bytes)||0)-(Number(d.memory_free_bytes)||0),0);
        const gTotal=devices.reduce((n,d)=>n+(Number(d.memory_total_bytes)||0),0);
        setBitPie(pid+'-gpu',gUsed,gTotal,devices.length?fmtBytes(gUsed):'-',devices.length?fmtBytes(Math.max(0,gTotal-gUsed)):'-',devices.length?fmtBytes(gTotal):'-');
        setBitPie(pid+'-net',hostIn,hostIn+hostOut,fmtBps(hostIn),fmtBps(hostOut),fmtBps(hostIn+hostOut));
      }
    };
    const existing=Array.from(cards.children);
    const same=existing.length===workers.length&&existing.every((el,i)=>el.dataset.workerId===workers[i].id);
    if(same){
      workers.forEach((w,index)=>fillCapBoard(existing[index],w));
    } else {
    cards.textContent='';
      if(!workers.length){
        const empty=document.createElement('p');empty.className='muted';empty.textContent='No workers enrolled yet. Use Enroll worker to add one.';cards.append(empty);
      }
    workers.forEach(w=>{
        const pid=capId(w.id);
        const group=document.createElement('section');
        group.className='ov-board worker-cap-board';group.dataset.workerId=w.id;
        group.setAttribute('aria-labelledby',pid+'-heading');
        const head=document.createElement('header');head.className='ov-board-head';
        head.innerHTML=`<h3 id="${pid}-heading">${w.id}</h3><span class="ov-board-note">${w.status||'-'} · ${w.hostname||w.id}</span>`;
        group.append(head);
        const grid=document.createElement('div');grid.className='hard-bit-grid ov-graph-grid workers-cap-grid';
        grid.append(
          makeCapPie('host-cpu','CPU · WORKER',pid+'-cpu','USED','FREE','CORES'),
          makeCapPie('host-ram','RAM · WORKER',pid+'-ram','USED','FREE','TOTAL'),
          makeCapPie('host-disk','DISK · WORKER',pid+'-disk','USED','FREE','TOTAL'),
          makeCapPie('swap','SWAP · HOST',pid+'-swap','USED','FREE','TOTAL'),
          makeCapPie('zram','ZRAM',pid+'-zram','USED','FREE','TOTAL'),
          makeCapPie('gpu','GPU · VRAM',pid+'-gpu','USED','FREE','TOTAL'),
          makeCapPie('worker-net','NET · WORKER',pid+'-net','IN','OUT','BW')
        );
        group.append(grid);cards.append(group);fillCapBoard(group,w);
    });
    }
  }
  const t=$('workers');
  if(t){
    row(t,['Worker','Host','Status','CPU','RAM','Disk','Host swap free / total','ZRAM','GPU inventory','Memory pressure','Last heartbeat','Actions'],true);
    if(!workers.length)row(t,['No workers enrolled yet. Use Enroll worker to add one.','','','','','','','','','','','']);
    workers.forEach(w=>{const c=w.capacity||{},devices=Array.isArray(c.gpus)?c.gpus:[],gpuSummary=devices.length?devices.map(d=>`${d.name||'NVIDIA GPU'} (${fmtBytes(d.memory_free_bytes)} free / ${fmtBytes(d.memory_total_bytes)})`).join(', '):'None detected',ramUsed=Math.max(0,(Number(c.ram_total)||0)-(Number(c.mem_available)||0)),diskUsed=Math.max(0,(Number(c.disk_total)||0)-(Number(c.disk_free)||0));const actions=cfMe?.kind==='admin'?button(w.status==='draining'?'Activate':'Drain',()=>api('POST','/api/workers/'+w.id+(w.status==='draining'?'/activate':'/drain'))):document.createTextNode('Placement ready');row(t,[w.id,w.hostname,badge(w.status),Math.round((Number(c.cpu_util)||0)*100)+'%',`${fmtBytes(ramUsed)} / ${fmtBytes(c.ram_total)}`,`${fmtBytes(diskUsed)} / ${fmtBytes(c.disk_total)}`,`${fmtBytes(c.swap_free)} / ${fmtBytes(c.swap_total)}`,c.zram_active?`${fmtBytes(c.zram_logical)} logical · ${fmtBytes(c.zram_used)} used`:'Off / unavailable',gpuSummary,`${Number(c.psi_mem_some10)||0}%`,w.last_heartbeat?new Date(w.last_heartbeat).toLocaleString():'-',actions]);});
  }
  populateWorkerSelect();
}
function populateWorkerSelect(){const s=$('wsel');if(!s)return;const placeable=workers.filter(w=>w.status==='active'||w.status==='draining'||!w.status);const ids=placeable.map(w=>w.id),current=[...s.options].slice(1).map(o=>o.value);if(ids.join('\0')===current.join('\0'))return;const v=s.value;s.length=1;placeable.forEach(w=>{const label=w.hostname?`${w.id} · ${w.hostname}`:w.id;s.add(new Option(label,w.id));});s.value=placeable.some(w=>w.id===v)?v:'';}

const serviceLogoFiles={wildfly:'wildfly.ico',supervisor:'supervisor.png',rsync:'rsync.jpg',adminer:'adminer.png'};
function serviceBrandIcon(id){const tile=document.createElement('span');tile.className='service-brand';tile.setAttribute('aria-hidden','true');const image=document.createElement('img');image.src='/assets/service-logos/'+(serviceLogoFiles[id]||id+'.svg');image.alt='';image.width=28;image.height=28;image.loading='lazy';image.decoding='async';image.onerror=()=>{image.hidden=true;tile.textContent=id.slice(0,2).toUpperCase();};tile.append(image);return tile;}
const serviceItems=[
['nginx','Nginx','Web server','Installs the Nginx package.'],
['apache','Apache HTTP Server','Web server','Installs Apache 2.'],
['caddy','Caddy','Web server','Installs the Caddy web server from Ubuntu repositories.'],
['varnish','Varnish Cache','Web server','HTTP accelerator and caching reverse proxy.'],
['squid','Squid','Networking','Caching HTTP proxy for outbound or edge traffic control.'],
['haproxy','HAProxy','Networking','Load balancer and reverse proxy. Configure listeners and backends after installation.'],
['owasp-waf','OWASP CRS WAF','Security','Installs ModSecurity and OWASP CRS for Nginx. Inspects HTTP/HTTPS requests handled by this Nginx server; requires Protection Mode disabled for installation.'],
['fail2ban','Fail2ban','Security','Bans hosts that show malicious signs such as repeated failed logins.'],
['ufw','UFW','Security','Uncomplicated Firewall front-end for host packet filtering rules.'],
['clamav','ClamAV','Security','Open-source antivirus engine and on-access/on-demand scanning tools.'],
['certbot','Certbot','Security','ACME certificate client. Domain validation and renewal scheduling must be configured separately.'],
['wireguard','WireGuard','Networking','Modern VPN tunnel tools (wg / wg-quick).'],
['openvpn','OpenVPN','Networking','OpenVPN client/server packages for VPN endpoints.'],
['nmap','Nmap','Networking','Network discovery and security auditing scanner.'],
['tcpdump','tcpdump','Networking','Packet capture utility for troubleshooting network traffic.'],
['mtr','MTR','Networking','Combined traceroute and ping network diagnostic tool.'],
['iperf3','iPerf3','Networking','Bandwidth measurement tool for network performance tests.'],
['dns-tools','DNS tools','Networking','dig, nslookup, whois, and traceroute for DNS and path diagnostics.'],
['docker','Docker Engine','Containers','Installs docker.io and Compose v2. Nested daemon support depends on the guest environment.'],
['podman','Podman','Containers','Daemonless OCI container engine for building and running containers.'],
['buildah','Buildah','Containers','OCI image build tool commonly used with Podman workflows.'],
['skopeo','Skopeo','Containers','Copy, inspect, and mirror container images between registries.'],
['kubectl','kubectl','Cloud native','Pinned kubectl 1.31.4 CLI for Kubernetes cluster operations.'],
['helm','Helm','Cloud native','Pinned Helm 3.16.3 package manager for Kubernetes charts.'],
['terraform','Terraform','IaC','Pinned Terraform 1.9.8 binary for infrastructure as code.'],
['ansible','Ansible','IaC','Configuration management and automation toolkit.'],
['wordpress','WordPress','CMS','Installs WordPress and Apache, PHP, and MariaDB packages. Complete database/site setup through WordPress in your browser.'],
['joomla','Joomla','CMS','Installs Apache, PHP, and MariaDB, then deploys the official Joomla 6.2.0 package to /var/www/html/joomla. Finish setup in your browser.'],
['mysql','MySQL Server','Database','Installs MySQL Server.'],
['mariadb','MariaDB Server','Database','Installs MariaDB Server.'],
['postgresql','PostgreSQL','Database','Installs PostgreSQL.'],
['redis','Redis','Database','Installs Redis Server.'],
['sqlite','SQLite','Database','SQLite command-line tools for local application databases.'],
['rabbitmq','RabbitMQ','Messaging','AMQP message broker for application queues and events.'],
['memcached','Memcached','Cache','In-memory application cache. Configure access and memory limits before use.'],
['php','PHP runtime','Runtime','Installs PHP-FPM, CLI, and common extensions.'],
['nodejs','Node.js + npm','Runtime','Installs Node.js and npm from Ubuntu repositories.'],
['python','Python','Runtime','Installs Python 3, venv, and pip.'],
['golang','Go toolchain','Runtime','Go compiler and development tools from the guest Ubuntu repositories.'],
['java','Java JDK','Runtime','Default OpenJDK development kit for Java applications.'],
['ruby','Ruby','Runtime','Ruby runtime and development tools from the guest Ubuntu repositories.'],
['wildfly','WildFly','Java application server','Installs verified WildFly 42.0.0.Final and Java 21. Application port 8180; management remains localhost-only. Recommended RAM: 1 GiB.'],
['tomcat','Apache Tomcat','Java application server','Installs Apache Tomcat 10 and Java. Application port 8080. Recommended RAM: 1 GiB.'],
['supervisor','Supervisor','Process management','Process manager for application workers and background jobs. Add your process configuration after installation.'],
['git','Git','Development','Version control tools for cloning and deploying application repositories.'],
['composer','Composer','Development','PHP dependency manager. Run Composer in your application directory.'],
['build-tools','Build tools','Development','GCC, G++, Make and pkg-config for compiling applications and native dependencies.'],
['cmake','CMake','Development','Cross-platform build system generator for C/C++ and native projects.'],
['rsync','Rsync','Operations','File synchronization and transfer utility for deployments and backups.'],
['rclone','rclone','Operations','Cloud storage sync CLI for S3, GCS, Azure Blob, and many providers.'],
['restic','restic','Operations','Encrypted backup tool for local and remote repositories.'],
['awscli','AWS CLI','Cloud','Amazon Web Services command-line interface from Ubuntu repositories.'],
['netdata','Netdata','Observability','Real-time host metrics dashboard and collectors.'],
['node-exporter','Node Exporter','Observability','Prometheus node exporter for host metrics scraping.'],
['collectd','collectd','Observability','System statistics collection daemon with plugin-based metrics.'],
['jq','jq','CLI tools','Lightweight JSON processor for scripts and pipelines.'],
['httpie','HTTPie','CLI tools','User-friendly HTTP client for API testing and debugging.'],
['htop','htop','CLI tools','Interactive process viewer for CPU and memory inspection.'],
['tmux','tmux','CLI tools','Terminal multiplexer for persistent remote sessions.'],
['vim','Vim','CLI tools','Classic modal text editor.'],
['neovim','Neovim','CLI tools','Modern Vim-compatible editor.'],
['zip-tools','zip / unzip','CLI tools','Archive creation and extraction utilities.'],
['tree','tree','CLI tools','Recursive directory listing as a tree.'],
['ripgrep','ripgrep','CLI tools','Fast recursive search tool (rg) for code and logs.'],
['fd','fd','CLI tools','Fast find alternative (fd-find package provides fdfind).'],
['bat','bat','CLI tools','cat alternative with syntax highlighting.'],
['ncdu','ncdu','CLI tools','NCurses disk usage analyzer.'],
['sysstat','sysstat','CLI tools','sar, iostat, mpstat, and related performance utilities.'],
['apache-utils','Apache utils','CLI tools','htpasswd, ab, and related HTTP utility commands.'],
['zsh','Zsh','Shell','Z shell with advanced interactive features.'],
['fish','fish','Shell','Friendly interactive shell.']
];

const wafErrorStatuses=['403','404','406','429','500'];
let wafErrorStatus='403',wafErrorDirty=false,wafErrorSourceID='';
function defaultWAFErrorHTML(status){const titles={403:'Request blocked',404:'Page not found',406:'Not acceptable',429:'Too many requests',500:'Server error'},msgs={403:'This request was blocked by the web application firewall protecting this site.',404:'The page you requested could not be found.',406:'The server cannot produce a response matching the request.',429:'Too many requests were sent in a short time. Please try again later.',500:'The server encountered an error while handling this request.'};const title=titles[status]||('Error '+status),msg=msgs[status]||'An error occurred while processing this request.';return`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>${status} · ${title}</title><style>:root{color-scheme:light dark}body{margin:0;min-height:100vh;display:grid;place-items:center;font-family:ui-sans-serif,system-ui,-apple-system,sans-serif;background:#0b1218;color:#e8eef4}main{max-width:34rem;padding:2rem 1.5rem;text-align:center}code{font-size:.85em;color:#87dcd1}h1{margin:.35rem 0 .65rem;font-size:1.45rem;letter-spacing:-.02em}p{margin:0;color:#9aabbd;line-height:1.55}</style></head><body><main><p><code>${status}</code></p><h1>${title}</h1><p>${msg}</p></main></body></html>
`;}
function currentWAFErrorHTML(i){const custom=(i?.spec?.waf_error_pages||{})[wafErrorStatus];return (custom&&String(custom).trim())||defaultWAFErrorHTML(wafErrorStatus);}
function renderWAFErrorTabs(i,canEdit){const tabs=$('waf-error-status-tabs');if(!tabs)return;if(tabs.children.length!==wafErrorStatuses.length){tabs.textContent='';wafErrorStatuses.forEach(code=>{const b=document.createElement('button');b.type='button';b.className='waf-status-tab';b.dataset.status=code;b.textContent=code;b.setAttribute('role','tab');b.onclick=()=>{if(wafErrorDirty&&!window.confirm('Discard unsaved changes for this error page?'))return;wafErrorStatus=code;wafErrorDirty=false;wafErrorSourceID='';renderWAF(i);};tabs.append(b);});}[...tabs.children].forEach(b=>{b.setAttribute('aria-selected',String(b.dataset.status===wafErrorStatus));b.disabled=!canEdit&&false;});}
function syncWAFErrorEditor(i,canEdit){const panel=$('waf-error-pages'),html=$('waf-error-html'),label=$('waf-error-status-label'),save=$('waf-error-save'),preview=$('waf-error-preview'),def=$('waf-error-default');if(!panel||!html)return;panel.hidden=false;renderWAFErrorTabs(i,canEdit);if(label)label.textContent=wafErrorStatus;const sourceKey=i.id+':'+wafErrorStatus;if(wafErrorSourceID!==sourceKey&&!wafErrorDirty){html.value=currentWAFErrorHTML(i);wafErrorSourceID=sourceKey;}html.disabled=!canEdit;if(save)save.disabled=!canEdit;if(preview)preview.disabled=false;if(def)def.disabled=!canEdit;}
const wafItems=[['protocol','HTTP protocol attacks','attack-protocol'],['protocol_enforcement','HTTP protocol enforcement','CRS rules 920000–920999'],['method','Allowed HTTP methods','CRS rules 911000–911999'],['scanner','Scanners & hacking tools','CRS rules 913000–913999'],['iprep','IP reputation tracking','CRS rules 910000–910999'],['dos','Layer 7 DoS detection','CRS rules 912000–912999'],['multipart','Multipart / upload checks','CRS rules 922000–922999'],['lfi','Local file inclusion','attack-lfi'],['rfi','Remote file inclusion','attack-rfi'],['rce','OS / command injection (shell, python, go, etc.)','attack-rce'],['shell','Shell / scripting language payloads','language-shell'],['powershell','PowerShell language payloads','language-powershell'],['php','PHP injection','attack-php'],['nodejs','Node.js / JavaScript server attacks','CRS rules 934000–934999'],['javascript','JavaScript language payloads','language-javascript'],['java','Java application attacks','CRS rules 944000–944999'],['aspnet','ASP.NET language payloads','language-aspnet'],['generic','Generic application attacks','attack-generic'],['xss','Cross-site scripting (XSS)','attack-xss'],['sqli','SQL injection','attack-sqli'],['fixation','Session fixation','attack-fixation'],['disclosure','Response data leakage','attack-disclosure'],['leakage_sql','SQL response data leakage','CRS rules 951000–951999'],['leakage_java','Java response data leakage','CRS rules 952000–952999'],['leakage_php','PHP response data leakage','CRS rules 953000–953999'],['leakage_iis','IIS response data leakage','CRS rules 954000–954999']];
window.cfWAFItems=wafItems;window.cfDefaultWAFErrorHTML=defaultWAFErrorHTML;
function renderWAF(i){if(!$('waf-status')||!$('waf-install')){if($('detail-open-waf')&&i?.id)$('detail-open-waf').href='/waf?target=vps:'+encodeURIComponent(i.id)+'#waf-bindings';return;}const state=i.service_installs?.['owasp-waf']||'',installed=state==='INSTALLED',installing=state==='INSTALLING',uninstalling=state==='UNINSTALLING',failed=state==='FAILED',uninstallFailed=state==='UNINSTALL_FAILED',protectedMode=i.spec?.protection_mode!==false,status=$('waf-status'),install=$('waf-install'),uninstall=$('waf-uninstall'),controls=$('waf-controls'),logButton=$('waf-error-log');status.textContent=installed?'Installed':installing?'Installing':uninstalling?'Uninstalling':failed?'Install failed':uninstallFailed?'Uninstall failed':'Not installed';status.className='badge '+(installed?'RUNNING':failed||uninstallFailed?'FAILED':'STOPPED');install.hidden=installed||installing||uninstalling||uninstallFailed;const busy=installing||uninstalling,notRunning=i.state!=='RUNNING';install.disabled=busy||notRunning;install.title=protectedMode?'Protection Mode is on - click to disable it and install':(notRunning?'Start the VPS first':'');install.textContent=installing?'Installing…':failed?'Retry WAF install':'Install OWASP CRS WAF';uninstall.hidden=!(installed||uninstalling||uninstallFailed);uninstall.disabled=busy||notRunning;uninstall.title=protectedMode?'Protection Mode is on - click to disable it and uninstall':(notRunning?'Start the VPS first':'');uninstall.textContent=uninstalling?'Uninstalling…':uninstallFailed?'Retry WAF uninstall':'Uninstall OWASP CRS WAF';logButton.disabled=i.state!=='RUNNING';if(controls&&controls.children.length===wafItems.length){wafItems.forEach(([key,label],idx)=>{const toggle=controls.children[idx].querySelector('.waf-toggle');if(!toggle)return;const enabled=i.spec?.owasp_protection?.[key]!==false;toggle.setAttribute('aria-checked',String(enabled));toggle.setAttribute('aria-label',(enabled?'Disable ':'Enable ')+label);toggle.title=(enabled?'Disable ':'Enable ')+label;toggle.disabled=!installed||protectedMode||i.state!=='RUNNING';toggle.onclick=async()=>{toggle.disabled=true;$('waf-message').textContent='Queueing rule-group update…';try{const r=await api('POST',`/api/instances/${i.id}/waf`,{category:key,enabled:!enabled});$('waf-message').textContent=r.unchanged?'This rule group already has that setting.':'Rule-group update queued. The worker will validate and reload Nginx.';await refresh();}catch(e){$('waf-message').textContent=e.message;await refresh();}};});}else{controls.textContent='';wafItems.forEach(([key,label,tag])=>{const item=document.createElement('div');item.className='waf-rule';const info=document.createElement('div');info.className='waf-rule-info';const title=document.createElement('div');title.className='waf-rule-title';title.textContent=label;const code=document.createElement('div');code.className='waf-rule-tag';code.textContent=tag;info.append(title,code);const toggle=document.createElement('button');toggle.type='button';toggle.className='waf-toggle';toggle.setAttribute('role','switch');const enabled=i.spec?.owasp_protection?.[key]!==false;toggle.setAttribute('aria-checked',String(enabled));toggle.setAttribute('aria-label',(enabled?'Disable ':'Enable ')+label);toggle.title=(enabled?'Disable ':'Enable ')+label;toggle.disabled=!installed||protectedMode||i.state!=='RUNNING';toggle.onclick=async()=>{toggle.disabled=true;$('waf-message').textContent='Queueing rule-group update…';try{const r=await api('POST',`/api/instances/${i.id}/waf`,{category:key,enabled:!enabled});$('waf-message').textContent=r.unchanged?'This rule group already has that setting.':'Rule-group update queued. The worker will validate and reload Nginx.';await refresh();}catch(e){$('waf-message').textContent=e.message;await refresh();}};item.append(info,toggle);controls.append(item);});}if(i.error?.startsWith('WAF update failed:')||i.error?.startsWith('service uninstall failed:')){$('waf-message').textContent=i.error;}else if(uninstalling){$('waf-message').textContent='Removing ModSecurity and CRS packages; Nginx remains installed.';}else if(!installed){$('waf-message').textContent=protectedMode?'Disable Protection Mode to install or uninstall the WAF.':i.state!=='RUNNING'?'Start the VPS to manage the WAF.':uninstallFailed?(i.error||'Uninstall failed. Retry the uninstall.'):'Install the WAF to enable these HTTP rule controls.';}else if(protectedMode){$('waf-message').textContent='Protection Mode is on. Use Install/Uninstall to disable it automatically, or turn it off above first. Rule toggles and error-page saves stay locked until Protection Mode is off.';}else if(i.state!=='RUNNING'){$('waf-message').textContent='Start the VPS to manage WAF rule groups.';}const enabledCount=wafItems.filter(([key])=>i.spec?.owasp_protection?.[key]!==false).length;if($('waf-rule-summary'))$('waf-rule-summary').textContent=installed?`${enabledCount} of ${wafItems.length} rule groups enabled`:'Install the WAF to manage rule groups.';syncWAFErrorEditor(i,installed&&!protectedMode&&i.state==='RUNNING');}
async function refreshWAFErrorLog(id){try{const result=await api('GET',`/api/instances/${id}/waf/logs`);$('waf-log-output').textContent=result.logs||'No WAF error log entries.';$('waf-log-output').scrollTop=$('waf-log-output').scrollHeight;}catch(e){$('waf-log-output').textContent=e.message;}}
function serviceInstallEligible(i,key){const state=i.service_installs?.[key]||'';return i.state==='RUNNING'&&i.spec?.protection_mode===false&&!['INSTALLED','INSTALLING','UNINSTALLING','UNINSTALL_FAILED'].includes(state);}
function serviceUninstallEligible(i,key){const state=i.service_installs?.[key]||'';return i.state==='RUNNING'&&i.spec?.protection_mode===false&&key!=='python'&&['INSTALLED','FAILED','UNINSTALL_FAILED'].includes(state);}
function filterServiceCatalog(){
  const query=($('library-search')?.value||'').trim().toLowerCase();
  let count=0;
  document.querySelectorAll('#service-catalog .service-card').forEach(card=>{
    const visible=!query||card.dataset.search.includes(query);
    card.hidden=!visible;if(visible)count++;
  });
  if($('library-count'))$('library-count').textContent=`${count} of ${serviceItems.length} services`;
  if($('library-empty'))$('library-empty').hidden=count!==0;
}
$('library-search')?.addEventListener('input',filterServiceCatalog);
function renderServiceCatalog(){const cards=$('service-catalog');if(!cards)return;const existing=cards.querySelectorAll('.service-card');if(existing.length===serviceItems.length){serviceItems.forEach(([key,name,group,description],idx)=>{const card=existing[idx],installed=instances.filter(i=>i.service_installs?.[key]==='INSTALLED').length,installing=instances.filter(i=>i.service_installs?.[key]==='INSTALLING').length,uninstalling=instances.filter(i=>i.service_installs?.[key]==='UNINSTALLING').length,failed=instances.filter(i=>i.service_installs?.[key]==='FAILED'||i.service_installs?.[key]==='UNINSTALL_FAILED').length,summary=card.querySelector('.service-card-state'),next=failed?`${failed} failed`:installing||uninstalling?`${installing+uninstalling} in progress`:installed?`${installed} installed`:'Not installed';if(summary&&summary.textContent!==next)summary.textContent=next;summary?.classList.toggle('error',failed>0);if(summary)summary.title=`Installed: ${installed} · Installing: ${installing} · Removing: ${uninstalling} · Failed: ${failed}`;const actions=card.querySelector('.service-card-actions'),install=actions?.querySelector('.primary');if(install)install.disabled=instances.length===0;const canUninstall=key!=='python'&&instances.some(i=>['INSTALLED','FAILED','UNINSTALL_FAILED','UNINSTALLING'].includes(i.service_installs?.[key]||''));let uninstall=actions?.querySelector('.danger');if(canUninstall&&!uninstall&&actions){uninstall=document.createElement('button');uninstall.className='danger';uninstall.textContent='Uninstall';uninstall.setAttribute('aria-label',`Choose a VPS to uninstall ${name}`);uninstall.onclick=()=>openServiceUninstall(key,name,description);actions.append(uninstall);}if(!canUninstall&&uninstall)uninstall.remove();});filterServiceCatalog();if($('service-install-dialog')?.open)renderServiceInstallDialog();return;}cards.textContent='';serviceItems.forEach(([key,name,group,description])=>{const card=document.createElement('article');card.className='panel service-card';card.dataset.search=[key,name,group,description].join(' ').toLowerCase();const g=document.createElement('div');g.className='eyebrow';g.textContent=group;const h=document.createElement('h2');h.textContent=name;h.style.margin='0 0 8px';const p=document.createElement('p');p.className='muted';p.textContent=description;const installed=instances.filter(i=>i.service_installs?.[key]==='INSTALLED').length,installing=instances.filter(i=>i.service_installs?.[key]==='INSTALLING').length,uninstalling=instances.filter(i=>i.service_installs?.[key]==='UNINSTALLING').length,failed=instances.filter(i=>i.service_installs?.[key]==='FAILED'||i.service_installs?.[key]==='UNINSTALL_FAILED').length,summary=document.createElement('p');summary.className='service-card-state muted';summary.textContent=failed?`${failed} failed`:installing||uninstalling?`${installing+uninstalling} in progress`:installed?`${installed} installed`:'Not installed';summary.classList.toggle('error',failed>0);summary.title=`Installed: ${installed} · Installing: ${installing} · Removing: ${uninstalling} · Failed: ${failed}`;const actions=document.createElement('div');actions.className='service-card-actions';const install=document.createElement('button');install.className='primary';install.textContent='Install';install.disabled=instances.length===0;install.setAttribute('aria-label',`Choose a VPS to install ${name}`);install.onclick=()=>openServiceInstall(key,name,description);actions.append(install);const canUninstall=key!=='python'&&instances.some(i=>['INSTALLED','FAILED','UNINSTALL_FAILED','UNINSTALLING'].includes(i.service_installs?.[key]||''));if(canUninstall){const uninstall=document.createElement('button');uninstall.className='danger';uninstall.textContent='Uninstall';uninstall.setAttribute('aria-label',`Choose a VPS to uninstall ${name}`);uninstall.onclick=()=>openServiceUninstall(key,name,description);actions.append(uninstall);}const info=document.createElement('div');info.className='service-info';const copy=document.createElement('div');copy.className='service-copy';copy.append(h,p);info.append(serviceBrandIcon(key),copy);card.append(info,summary,actions);cards.append(card);});filterServiceCatalog();if($('service-install-dialog')?.open)renderServiceInstallDialog();}
function openServiceInstall(key,name,description){selectedServiceInstall=key;selectedServiceAction='install';serviceInstallNotice='';serviceInstallError='';$('service-install-title').textContent='Install '+name;$('service-install-description').textContent=description;$('service-install-note').textContent='Installation uses the VPS package manager and may take several minutes.';renderServiceInstallDialog();$('service-install-dialog').showModal();}
function openServiceUninstall(key,name,description){selectedServiceInstall=key;selectedServiceAction='uninstall';serviceInstallNotice='';serviceInstallError='';$('service-install-title').textContent='Uninstall '+name;$('service-install-description').textContent=description;$('service-install-note').textContent=key==='joomla'?'Uninstall removes the Joomla site files from /var/www/html/joomla.':key==='python'?'Uninstall is disabled because Ubuntu components depend on Python.':'Uninstall removes catalog packages and retains database files. Shared dependencies may be protected.';renderServiceInstallDialog();$('service-install-dialog').showModal();}
function renderServiceInstallDialog(){const select=$('service-install-vps');if(!select)return;const old=select.value;select.replaceChildren(new Option('Choose a VPS',''));instances.forEach(i=>{const state=i.service_installs?.[selectedServiceInstall]||'',baseReason=i.state!=='RUNNING'?'not running':i.spec?.protection_mode!==false?'Protection Mode is enabled':'',note=baseReason||state.toLowerCase().replaceAll('_',' ')||(selectedServiceAction==='uninstall'?'not installed':''),option=new Option(`${i.id} · ${stateLabel(i.state)}${note?' · '+note:''}`,i.id),monitoring=selectedServiceAction==='install'?['INSTALLING','UNINSTALLING','INSTALLED','FAILED','UNINSTALLED'].includes(state):['UNINSTALLING','UNINSTALLED'].includes(state);option.disabled=!!baseReason||(!serviceInstallEligible(i,selectedServiceInstall)&&!serviceUninstallEligible(i,selectedServiceInstall)&&!monitoring);select.add(option);});if(instances.some(i=>i.id===old))select.value=old;else select.value='';const id=select.value,inst=instances.find(i=>i.id===id),status=$('service-install-status'),run=$('service-install-run'),done=$('service-install-done');status.replaceChildren();status.className='service-install-status';done.hidden=true;run.className=selectedServiceAction==='uninstall'?'danger':'primary';if(!inst){status.textContent=instances.length?'Select an eligible VPS. Other VPSs show their current service status.':'No VPS instances are available.';run.disabled=true;run.textContent=selectedServiceAction==='uninstall'?'Uninstall':'Install';return;}const state=inst.service_installs?.[selectedServiceInstall]||'';if(state==='INSTALLING'||state==='UNINSTALLING'){const spin=document.createElement('span');spin.className='progress-icon';spin.setAttribute('aria-hidden','true');status.append(spin,document.createTextNode(`${state==='INSTALLING'?'Installing':'Uninstalling'} on ${inst.id}. Package manager is working; this may take several minutes.`));status.classList.add('is-progress');}else if(state==='UNINSTALLED'&&selectedServiceAction==='uninstall'){status.textContent=`Uninstall complete on ${inst.id}.`;status.classList.add('is-success');done.hidden=false;}else if(state==='INSTALLED'&&selectedServiceAction==='install'){status.textContent=`Already installed on ${inst.id}.`;status.classList.add('is-success');done.hidden=false;}else if(state==='UNINSTALL_FAILED'){status.textContent=inst.error?.startsWith('service uninstall failed:')?inst.error:`Uninstall failed on ${inst.id}. Review VPS logs or retry.`;status.classList.add('is-error');}else if(state==='FAILED'&&selectedServiceAction==='install'){status.textContent=inst.error?.startsWith('service installation failed:')?inst.error:`Installation failed on ${inst.id}. Review VPS logs or retry.`;status.classList.add('is-error');}else if(serviceInstallError){status.textContent=serviceInstallError;status.classList.add('is-error');}else if(serviceInstallSubmitting){status.textContent=serviceInstallNotice||'Sending service request…';status.classList.add('is-progress');}else if(serviceInstallNotice){status.textContent=serviceInstallNotice;status.classList.add('is-progress');}else if(selectedServiceAction==='uninstall'&&(state==='INSTALLED'||state==='FAILED')){status.textContent=state==='FAILED'?`A partial install exists on ${inst.id}. Uninstall will remove catalog packages.`:`Ready to uninstall from ${inst.id}. Database files are retained.`;status.classList.add('is-progress');}else if(state==='FAILED'&&selectedServiceAction==='uninstall'){status.textContent=`Partial installation on ${inst.id} can be removed.`;}else if(inst.state!=='RUNNING'){status.textContent='Start this VPS before changing services.';status.classList.add('is-error');}else if(inst.spec?.protection_mode!==false){status.textContent='Disable Protection Mode in VPS Details before changing services.';status.classList.add('is-error');}else{status.textContent='Ready to install on '+inst.id+'.';}
const eligible=selectedServiceAction==='uninstall'?serviceUninstallEligible(inst,selectedServiceInstall):serviceInstallEligible(inst,selectedServiceInstall);run.disabled=serviceInstallSubmitting||!eligible;run.textContent=serviceInstallSubmitting?'Queuing…':selectedServiceAction==='uninstall'?(state==='UNINSTALL_FAILED'?'Retry uninstall':'Uninstall'):state==='FAILED'?'Retry install':'Install';}
async function runServiceInstall(){const id=$('service-install-vps').value,inst=instances.find(i=>i.id===id);if(!inst)return;const eligible=selectedServiceAction==='uninstall'?serviceUninstallEligible(inst,selectedServiceInstall):serviceInstallEligible(inst,selectedServiceInstall);if(!eligible)return;if(selectedServiceAction==='uninstall'){const siteWarning=selectedServiceInstall==='joomla'?' This removes the Joomla site files at /var/www/html/joomla.':' Database files are retained, but applications using this service may stop working.';if(!window.confirm(`Uninstall ${selectedServiceInstall} from ${id}?${siteWarning}`))return;}serviceInstallSubmitting=true;serviceInstallError='';serviceInstallNotice=`Queuing ${selectedServiceAction} on ${id}…`;renderServiceInstallDialog();try{const route=selectedServiceAction==='uninstall'?'uninstall-service':'install-service';await api('POST',`/api/instances/${id}/${route}`,{service_id:selectedServiceInstall});serviceInstallNotice=`${selectedServiceAction==='uninstall'?'Uninstall':'Install'} request accepted for ${id}. Waiting for worker progress…`;await refresh();}catch(e){serviceInstallError=e.message;serviceInstallNotice='';}finally{serviceInstallSubmitting=false;renderServiceInstallDialog();}}
let detailSecurityModesCache=null,detailSecurityModesSig='',detailSecurityModesReq=0;
function detailModeCurrentControls(sp){
 return {
  protection_mode:sp.protection_mode!==false,
  ssh_login_blocked:sp.ssh_login_blocked===true,
  delete_protection:sp.delete_protection===true
 };
}
function detailModeControlSummary(controls){
 const labels={protection_mode:'Protection',ssh_login_blocked:'SSH Block',delete_protection:'Delete Protection'};
 const parts=[];
 for(const key of Object.keys(labels)){
  if(typeof controls?.[key]!=='boolean')continue;
  parts.push(labels[key]+'='+(controls[key]?'on':'off'));
 }
 return parts.length?parts.join(' · '):'No live controls';
}
function detailModeProtectLines(p){
 const c=p.controls||{}, lines=[];
 if(c.protection_mode===true)lines.push('Protects guest root - remounted read-only (blocks persistent malware writes / apt changes)');
 else if(c.protection_mode===false)lines.push('Protection off - root writable for installs, updates, and WAF changes');
 if(c.ssh_login_blocked===true)lines.push('Blocks new SSH logins (running sessions may remain until restart)');
 else if(c.ssh_login_blocked===false)lines.push('SSH login stays allowed for admin access');
 if(c.delete_protection===true)lines.push('Blocks VPS deletion from the CloFix admin API/UI');
 else if(c.delete_protection===false)lines.push('VPS can still be deleted from admin after confirm');
 if(c.waf==='planned')lines.push('HTTP Web Block (SQLi/XSS/RCE) needs OWASP CRS WAF installed separately');
 if(c.firewall_cidr==='planned')lines.push('Firewall CIDR restrict is planned - use Network Quarantine for inbound lock');
 if(!lines.length)lines.push('No live protect controls in this mode');
 return lines;
}
function detailModeMatchScore(controls,cur){
 let score=0;
 for(const key of ['protection_mode','ssh_login_blocked','delete_protection']){
  if(typeof controls?.[key]!=='boolean')continue;
  if(controls[key]!==cur[key])return -1;
  score+=1;
 }
 return score;
}
function detailModeBestMatchID(policies,sp){
 const cur=detailModeCurrentControls(sp);
 let bestID='',bestScore=0;
 for(const p of policies||[]){
  const score=detailModeMatchScore(p.controls||{},cur);
  if(score>bestScore){bestScore=score;bestID=p.id;}
 }
 return bestID;
}
function detailSecurityModesSignature(i,workerLive,policies,best){
 return [i.id,workerLive?'1':'0',best||'',(policies||[]).map(p=>p.id+':'+(p.version||1)+':'+(p.description||'').slice(0,40)).join('|')].join('#');
}
function syncDetailSecurityModeCards(best,workerLive){
 const grid=$('security-modes-grid');if(!grid)return;
 grid.querySelectorAll('.security-mode-card').forEach(card=>{
  const id=card.dataset.modeId||'';
  const active=id===best;
  card.classList.toggle('is-match',active);
  card.querySelectorAll('button').forEach(btn=>{
   if(btn.dataset.busy)return;
   btn.disabled=!workerLive;
   const want=active?'Disable':'Enable';
   if(btn.textContent!==want)btn.textContent=want;
   btn.className=active?'secondary':'primary';
  });
 });
}
function isDetailSecurityModePolicy(p){
 return p&&p.id!=='tpl-quarantine'&&p.template!=='quarantine'&&p.id!=='tpl-database_protection'&&p.template!=='database_protection';
}
async function loadDetailSecurityModes(){
 if(detailSecurityModesCache)return detailSecurityModesCache;
 const data=await api('GET','/api/security/modes');
 detailSecurityModesCache=(data.policies||[]).filter(isDetailSecurityModePolicy).slice().sort((a,b)=>String(a.name||'').localeCompare(String(b.name||'')));
 return detailSecurityModesCache;
}
function detailModeDisableTargetID(activeID){
 return activeID==='tpl-standard'?'tpl-maintenance':'tpl-standard';
}
function paintDetailSecurityModes(i,workerLive,policies,best){
 const grid=$('security-modes-grid'),msg=$('security-modes-message'),matchEl=$('security-modes-match');
 if(!grid)return;
 if(matchEl){
  const hit=policies.find(p=>p.id===best);
  const next=hit?`Current posture matches ${hit.name}.`:'Choose a template to apply Protection, SSH Login Block, and Delete Protection together.';
  if(matchEl.textContent!==next)matchEl.textContent=next;
 }
 if(!policies.length){
  if(grid.dataset.empty!=='1'){
   grid.replaceChildren();
   const empty=document.createElement('p');empty.className='muted';empty.textContent='No Security Modes are available.';
   grid.append(empty);grid.dataset.empty='1';
  }
  return;
 }
 grid.dataset.empty='0';
 grid.replaceChildren();
 const byID=Object.fromEntries(policies.map(p=>[p.id,p]));
 for(const p of policies){
  const active=p.id===best;
  const card=document.createElement('article');
  card.className='security-mode-card'+(active?' is-match':'');
  card.dataset.modeId=p.id;
  const title=document.createElement('strong');title.textContent=p.name||p.id;
  const desc=document.createElement('p');desc.textContent=p.description||'Built-in Security Mode';
  const protect=document.createElement('ul');protect.className='mode-protect-summary';
  detailModeProtectLines(p).forEach(line=>{const li=document.createElement('li');li.textContent=line;protect.append(li);});
  const ctrls=document.createElement('div');ctrls.className='mode-controls';ctrls.textContent=detailModeControlSummary(p.controls||{});
  const actions=document.createElement('div');actions.className='inline-fields';
  const toggle=document.createElement('button');
  toggle.type='button';
  toggle.className=active?'secondary':'primary';
  toggle.textContent=active?'Disable':'Enable';
  toggle.disabled=!workerLive;
  toggle.onclick=async()=>{
   const wk=workers.find(w=>w.id===i.worker_id),nowLive=!!wk&&wk.status==='active'&&(Date.now()-new Date(wk.last_heartbeat||0).getTime()<60000);
   if(!nowLive){if(msg)msg.textContent='Worker is offline. Wait for heartbeat, then retry.';return;}
   const targetID=active?detailModeDisableTargetID(p.id):p.id;
   const target=byID[targetID]||{id:targetID,name:targetID};
   const action=active?'Disable':'Enable';
   if(!window.confirm(`${action} Security Mode “${p.name||p.id}” on ${i.id}?${active?` This switches to ${target.name||target.id}.`:''}`))return;
   if(msg)msg.textContent=(active?'Disabling ':'Enabling ')+(p.name||p.id)+'…';
   toggle.dataset.busy='1';toggle.disabled=true;
   try{
    await api('POST',`/api/security/modes/${encodeURIComponent(targetID)}/apply`,{instance_ids:[i.id]});
    if(msg)msg.textContent=active?`Disabled ${p.name||p.id} → ${target.name||target.id}.`:`Enabled ${p.name||p.id}.`;
    detailSecurityModesSig='';
    await refresh();
   }catch(e){if(msg)msg.textContent=e.message;}
   finally{delete toggle.dataset.busy;toggle.disabled=!nowLive;}
  };
  actions.append(toggle);card.append(title,desc,protect,ctrls,actions);grid.append(card);
 }
}
async function renderDetailSecurityModes(i,workerLive){
 const grid=$('security-modes-grid'),msg=$('security-modes-message'),matchEl=$('security-modes-match');
 if(!grid)return;
 const req=++detailSecurityModesReq;
 try{
  const policies=await loadDetailSecurityModes();
  if(req!==detailSecurityModesReq)return;
  const best=detailModeBestMatchID(policies,i.spec||{});
  const sig=detailSecurityModesSignature(i,workerLive,policies,best);
  if(sig===detailSecurityModesSig&&grid.children.length){
   syncDetailSecurityModeCards(best,workerLive);
   if(matchEl){
    const hit=policies.find(p=>p.id===best);
    const next=hit?`Current posture matches ${hit.name}.`:'Choose a template to apply Protection, SSH Login Block, and Delete Protection together.';
    if(matchEl.textContent!==next)matchEl.textContent=next;
   }
   return;
  }
  paintDetailSecurityModes(i,workerLive,policies,best);
  detailSecurityModesSig=sig;
 }catch(e){
  if(req!==detailSecurityModesReq)return;
  detailSecurityModesSig='';
  if(!grid.querySelector('.load-error')){
   grid.replaceChildren();
   const err=document.createElement('p');err.className='load-error';err.textContent=e.message;grid.append(err);
  }else if(msg&&!msg.textContent)msg.textContent=e.message;
 }
}
function renderDetails(){const i=instances.find(x=>x.id===selectedID);if(!i){if(instances.length)go('instances');return;}const sp=i.spec||{},protectedMode=sp.protection_mode!==false,u=i.metrics||{},cpu=Math.max(0,Math.min(100,+u.cpu_percent||0)),mem=+u.memory_current_bytes||0,limit=+u.memory_limit_bytes||(+sp.ram_mb||0)*1048576,memPct=limit?Math.max(0,Math.min(100,mem/limit*100)):0;$('detail-title').textContent=instanceDisplayName(i);$('detail-sub').textContent=`${i.id} · Created ${new Date(i.created).toLocaleString()} · ${i.worker_id} · ${i.placement_method} placement`;if($('detail-hostname')&&document.activeElement!==$('detail-hostname'))$('detail-hostname').value=String(sp.hostname||'').trim();$('detail-state').className='badge '+i.state;$('detail-state').textContent=stateLabel(i.state);const sshPort=Number(sp.ssh_port)||0;if($('terminal-inline-command')){if(!sshPort)$('terminal-inline-command').textContent='SSH port unavailable';else if(sp.password_auth)$('terminal-inline-command').textContent=`ssh -p ${sshPort} -o PreferredAuthentications=password -o PubkeyAuthentication=no root@localhost`;else $('terminal-inline-command').textContent=`ssh -p ${sshPort} -i /path/to/${i.id}.pem root@localhost`;}if($('terminal-inline-label'))$('terminal-inline-label').textContent=sp.password_auth?'Host SSH (password - paste into Mac Terminal)':'Host SSH (PEM)';if($('terminal-inline-pem-command')&&$('terminal-inline-pem-label')){const showPem=!!(sp.password_auth&&(sp.ssh_authorized_key||'').trim()&&sshPort);$('terminal-inline-pem-label').hidden=!showPem;$('terminal-inline-pem-command').hidden=!showPem;if(showPem)$('terminal-inline-pem-command').textContent=`ssh -p ${sshPort} -i /path/to/${i.id}.pem root@localhost`;}if($('terminal-inline-help'))$('terminal-inline-help').textContent=sp.password_auth?'Your Mac default SSH key will not match this VPS. Use the password command (disables pubkey), then type the root password from Access. Or use the PEM downloaded at create time.':'Use the private PEM downloaded when this VPS was created: ssh -p PORT -i /path/to/file.pem root@localhost';$('detail-cpu').textContent=cpu.toFixed(1)+'%';if($('detail-cpu-bar'))$('detail-cpu-bar').style.width=cpu+'%';$('detail-memory').textContent=fmtBytes(mem);$('detail-memory-note').textContent=`${fmtBytes(mem)} of ${fmtBytes(limit)} used`;if($('detail-memory-bar'))$('detail-memory-bar').style.width=memPct+'%';$('detail-cpu-limit').textContent=sp.cpu+' cores';$('detail-memory-limit').textContent=sp.ram_mb+' MB';if($('detail-disk-limit'))$('detail-disk-limit').textContent=(sp.disk_mb||0)+' MB';if($('detail-swap-limit'))$('detail-swap-limit').textContent=(sp.swap_mb??512)+' MB';const swapUsed=+u.swap_current_bytes||0,swapLimit=Number(u.swap_limit_bytes??((sp.swap_mb??512)*1048576)),swapPct=swapLimit?Math.max(0,Math.min(100,swapUsed/swapLimit*100)):0;$('detail-swap').textContent=fmtBytes(swapUsed);$('detail-swap-note').textContent=`${fmtBytes(swapUsed)} of ${fmtBytes(swapLimit)} used`;if($('detail-swap-bar'))$('detail-swap-bar').style.width=swapPct+'%';const diskUsed=Number(u.disk_usage_bytes)||0,diskLimit=Number(sp.disk_mb||0)*1048576,diskPct=diskLimit?Math.max(0,Math.min(100,diskUsed/diskLimit*100)):0;$('detail-disk').textContent=fmtBytes(diskUsed);$('detail-disk-note').textContent=`${fmtBytes(diskUsed)} of ${fmtBytes(diskLimit)} placement · no hard quota`;if($('detail-disk-bar'))$('detail-disk-bar').style.width=diskPct+'%';const netIn=Number(u.net_rx_bps)||0,netOut=Number(u.net_tx_bps)||0;if($('detail-net-in')){$('detail-net-in').textContent=fmtBps(netIn);$('detail-net-out').textContent=fmtBps(netOut);$('detail-net-bw').textContent=fmtBps(netIn+netOut);if($('detail-net-sum'))$('detail-net-sum').textContent=fmtBps(netIn+netOut);$('detail-net-in-note').textContent=`${fmtBytes(u.net_rx_bytes||0)} received`;$('detail-net-out-note').textContent=`${fmtBytes(u.net_tx_bytes||0)} sent`;$('detail-net-bw-note').textContent=i.state==='RUNNING'?'Guest eth0 counters from the worker':'Start the VPS to sample network';}if(typeof setBitPie==='function'){setBitPie('pie-detail-cpu',cpu,100,cpu.toFixed(1)+'%',Math.max(0,100-cpu).toFixed(1)+'%',(Number(sp.cpu)||0).toFixed(2)+' c');setBitPie('pie-detail-ram',mem,limit,fmtBytes(mem),fmtBytes(Math.max(0,limit-mem)),fmtBytes(limit));setBitPie('pie-detail-swap',swapUsed,swapLimit,fmtBytes(swapUsed),fmtBytes(Math.max(0,swapLimit-swapUsed)),fmtBytes(swapLimit));setBitPie('pie-detail-disk',diskUsed,diskLimit,fmtBytes(diskUsed),fmtBytes(Math.max(0,diskLimit-diskUsed)),fmtBytes(diskLimit));setBitPie('pie-detail-net',netIn,netIn+netOut,fmtBps(netIn),fmtBps(netOut),fmtBps(netIn+netOut));if($('pie-detail-net-alloc'))$('pie-detail-net-alloc').textContent=fmtBps(netIn+netOut);setBitPie('pie-detail-bw',netIn,netIn+netOut,fmtBps(netIn),fmtBps(netOut),fmtBps(netIn+netOut));}if(document.activeElement!==$('resource-ram'))$('resource-ram').value=sp.ram_mb||512;if(document.activeElement!==$('resource-cpu'))$('resource-cpu').value=sp.cpu||1;if(document.activeElement!==$('resource-disk'))$('resource-disk').value=sp.disk_mb||4096;if(document.activeElement!==$('resource-swap'))$('resource-swap').value=sp.swap_mb??512; const m=$('metadata');const metaRows=[["Instance ID",i.id],["Image",`${sp.image?.name||'-'}:${sp.image?.version||'-'}`],["Worker",i.worker_id],["Private IP",vpcAddress(sp.ip_address,sp.vpc_id)],["SSH",sp.ssh_port?`ssh -p ${sp.ssh_port} root@localhost`:'-'],["HTTP access",sp.ssh_port?`http://localhost:${hostHTTPPort(sp.ssh_port)}`:'-'],["HTTPS access",sp.ssh_port?`https://localhost:${hostHTTPSPort(sp.ssh_port)}`:'-'],["RAM limit",`${sp.ram_mb} MB`],["Swap limit",`${sp.swap_mb??512} MB`],["CPU limit",`${sp.cpu} cores`],["Disk",`${sp.disk_mb} MB`],["PID limit",sp.pids||512],["Hostname",sp.hostname||i.id],["Protection Mode",protectedMode?'Enabled':'Disabled'],["SSH Login Block",sp.ssh_login_blocked?'Enabled':'Disabled'],["VPS Delete Protection",sp.delete_protection?'Enabled':'Disabled'],["PAM password login",sp.password_auth?'Enabled':'Disabled'],["Created",new Date(i.created).toLocaleString()]];if(m.children.length===metaRows.length)metaRows.forEach((x,idx)=>{const v=m.children[idx].querySelector('.meta-value');if(v&&v.textContent!==String(x[1]??'-'))v.textContent=x[1]??'-';});else{m.textContent='';metaRows.forEach(x=>m.appendChild(meta(x[0],x[1])));}if($('password-auth-status'))$('password-auth-status').textContent=sp.password_auth?'PAM password login is enabled for root. Updating the password applies it on the VPS.':'PAM password login is off. Set a root password here to enable it.';const wk=workers.find(w=>w.id===i.worker_id),workerLive=!!wk&&wk.status==='active'&&(Date.now()-new Date(wk.last_heartbeat||0).getTime()<60000);if($('security-worker-status')){$('security-worker-status').textContent=workerLive?'Worker is online · Protection, SSH, Key-Only, Modes, and Quarantine apply while the worker is live.':'Worker is offline or not heartbeating · toggles are disabled until it reconnects.';$('security-worker-status').className='message '+(workerLive?'':'load-error');}$('protection-badge').textContent=protectedMode?'Enabled':'Disabled';$('protection-badge').className='badge '+(protectedMode?'RUNNING':'STOPPED');$('protection-status').textContent=protectedMode?'Persistent guest root is read-only':'Guest root is writable';const pb=$('protection-toggle');pb.textContent=protectedMode?'Disable Protection':'Enable Protection';pb.disabled=!workerLive;pb.onclick=async()=>{if(!workerLive){$('protection-message').textContent='Worker is offline. Wait for heartbeat, then retry.';return;}if(protectedMode&&!window.confirm('Disable Protection Mode? The VPS root filesystem will become writable after the guest restarts.'))return;const msg=$('protection-message');msg.textContent='Applying Protection Mode…';pb.disabled=true;try{const result=await api('POST',`/api/instances/${i.id}/protection`,{enabled:!protectedMode});msg.textContent=result.unchanged?'Protection Mode was already in this state.':(result.restart_required?'Change queued. Restarting the VPS to apply Protection Mode…':'Protection Mode updated.');await refresh();}catch(e){msg.textContent=e.message;}finally{pb.disabled=false;}};const sshBlocked=sp.ssh_login_blocked===true;$('ssh-block-badge').textContent=sshBlocked?'Enabled':'Disabled';$('ssh-block-badge').className='badge '+(sshBlocked?'STOPPED':'RUNNING');$('ssh-block-status').textContent=sshBlocked?'New SSH logins are blocked':'SSH login is allowed';const sb=$('ssh-block-toggle');sb.textContent=sshBlocked?'Allow SSH logins':'Block SSH logins';sb.disabled=!workerLive;sb.onclick=async()=>{if(!workerLive){$('ssh-block-message').textContent='Worker is offline. Wait for heartbeat, then retry.';return;}if(!sshBlocked&&!window.confirm('Block new SSH logins? A running VPS will restart to apply the setting.'))return;const msg=$('ssh-block-message');msg.textContent='Applying SSH login policy…';sb.disabled=true;try{const result=await api('POST',`/api/instances/${i.id}/ssh-login`,{enabled:!sshBlocked});msg.textContent=result.unchanged?'SSH login policy was already in this state.':(result.restart_required?'Change queued. Restarting the VPS to apply SSH policy…':'SSH login policy updated.');await refresh();}catch(e){msg.textContent=e.message;}finally{sb.disabled=false;}};const deleteProtected=sp.delete_protection===true;$('delete-protection-badge').textContent=deleteProtected?'Enabled':'Disabled';$('delete-protection-badge').className='badge '+(deleteProtected?'RUNNING':'STOPPED');$('delete-protection-status').textContent=deleteProtected?'Deletion is blocked until protection is disabled':'Deletion is allowed from the VPS actions';const db=$('delete-protection-toggle');db.textContent=deleteProtected?'Disable Delete Protection':'Enable Delete Protection';db.disabled=!workerLive;db.onclick=async()=>{if(!workerLive){$('delete-protection-message').textContent='Worker is offline. Wait for heartbeat, then retry.';return;}if(deleteProtected&&!window.confirm('Disable VPS Delete Protection? The VPS can then be deleted from the admin UI.'))return;const msg=$('delete-protection-message');msg.textContent='Applying Delete Protection…';db.disabled=true;try{const result=await api('POST',`/api/instances/${i.id}/delete-protection`,{enabled:!deleteProtected});msg.textContent=result.unchanged?'Delete Protection was already in this state.':'Delete Protection updated.';await refresh();}catch(e){msg.textContent=e.message;}finally{db.disabled=false;}};const hasSSHKey=!!(sp.ssh_authorized_key||'').trim();const keyOnly=!sp.password_auth&&hasSSHKey;if($('ssh-key-only-badge')){$('ssh-key-only-badge').textContent=keyOnly?'Enabled':'Disabled';$('ssh-key-only-badge').className='badge '+(keyOnly?'RUNNING':'STOPPED');}$('ssh-key-only-status')&&($('ssh-key-only-status').textContent=keyOnly?'Password authentication is disabled · key-only':'Password authentication is allowed');const kb=$('ssh-key-only-toggle');if(kb){kb.textContent=keyOnly?'Allow password login':'Enable SSH Key-Only';kb.disabled=!workerLive||(!keyOnly&&!hasSSHKey);if($('ssh-key-only-message')&&!hasSSHKey&&!keyOnly)$('ssh-key-only-message').textContent='Add an SSH authorized key before enabling Key-Only.';kb.onclick=async()=>{if(!workerLive){$('ssh-key-only-message').textContent='Worker is offline. Wait for heartbeat, then retry.';return;}if(!keyOnly&&!hasSSHKey){$('ssh-key-only-message').textContent='An SSH public key must be set on this VPS first.';return;}if(!keyOnly&&!window.confirm('Enable SSH Key-Only? Root password authentication will be disabled.'))return;const msg=$('ssh-key-only-message');msg.textContent='Applying SSH Key-Only…';kb.disabled=true;try{await api('POST',`/api/instances/${i.id}/ssh-key-only`,{enabled:!keyOnly});msg.textContent='SSH Key-Only updated.';await refresh();}catch(e){msg.textContent=e.message;}finally{kb.disabled=false;}};}const quarantined=protectedMode&&sshBlocked&&deleteProtected;if($('quarantine-badge')){$('quarantine-badge').textContent=quarantined?'Active':'Off';$('quarantine-badge').className='badge '+(quarantined?'STOPPED':'RUNNING');}$('quarantine-status')&&($('quarantine-status').textContent=quarantined?'Protection + SSH Block + Delete Protection are all on':'Not matching quarantine posture');const qb=$('quarantine-toggle');if(qb){qb.disabled=!workerLive||quarantined;qb.textContent=quarantined?'Quarantined':'Quarantine VPS';qb.onclick=async()=>{if(!workerLive){$('quarantine-message').textContent='Worker is offline. Wait for heartbeat, then retry.';return;}if(quarantined)return;if(!window.confirm(`Quarantine ${i.id}? This enables Protection Mode, SSH Login Block, Delete Protection, and restricts inbound firewall rules.`))return;const msg=$('quarantine-message');msg.textContent='Applying quarantine…';qb.disabled=true;try{await api('POST',`/api/security/quarantine/${i.id}`);msg.textContent='Quarantine applied.';await refresh();}catch(e){msg.textContent=e.message;}finally{qb.disabled=false;}};}renderDetailSecurityModes(i,workerLive);const a=$('detail-actions'),probe=document.createElement('div');appendInstanceControls(probe,i,true);if(nodeFingerprint(a)!==nodeFingerprint(probe)){a.textContent='';appendInstanceControls(a,i,true);}renderServices(i);renderPorts(i);renderWAF(i);if(typeof refreshVPSTerminalPanel==='function')refreshVPSTerminalPanel(i);}
function openServiceLogs(i,name){selectedServiceLog=name;$('service-log-title').textContent=name+' logs';$('service-log-dialog').showModal();renderServices(i);}
function serviceCatalogForProcess(name){const n=String(name||'').toLowerCase();if(n==='wildfly')return 'wildfly';if(n==='tomcat10'||n==='tomcat')return 'tomcat';if(n==='haproxy'||n==='memcached')return n;if(n==='supervisor'||n==='supervisord')return 'supervisor';if(n==='nginx')return 'nginx';if(n==='apache2')return 'apache';if(n==='mariadb'||n==='mysqld'||n==='mariadbd')return 'mariadb';if(n==='mysql')return 'mysql';if(n==='postgresql')return 'postgresql';if(n==='redis'||n==='redis-server')return 'redis';if(n==='php-fpm'||n.startsWith('php-fpm'))return 'php';return '';}
async function uninstallDetailService(id,key,name){if(!window.confirm(`Uninstall ${name} from this VPS? Database files are retained. Applications that depend on this service may stop working.`))return;const msg=$('service-action-message');msg.textContent=`Queueing ${name} uninstall…`;try{await api('POST',`/api/instances/${id}/uninstall-service`,{service_id:key});msg.textContent=`${name} uninstall queued. Status will update from the worker.`;await refresh();}catch(e){msg.textContent=e.message;await refresh();}}
function formatListenPorts(ports){if(!Array.isArray(ports)||!ports.length)return '-';return ports.filter(p=>Number(p)>0).map(p=>String(p)).join(', ')||'-';}
function renderServices(i){const t=$('service-table');row(t,['Service','Status','Ports','CPU','RAM (PSS estimate)','Swap','ZRAM','Actions'],true);const services=Array.isArray(i.services)?i.services:[];$('services-note').textContent=i.state==='RUNNING'?'Resource samples refresh on worker heartbeats':'Start the VPS to scan its services';$('service-action-message').textContent=(i.error||'').startsWith('service action failed:')||(i.error||'').startsWith('service uninstall failed:')?i.error:'';if(i.state!=='RUNNING'){row(t,['Services are available while the VPS is running','','','','','','','']);}else if(!services.length){row(t,['No enabled application services detected','','','','','','','']);}else services.forEach(s=>{const state=document.createElement('span');state.className='badge '+(s.state==='RUNNING'?'RUNNING':'STOPPED');state.textContent=s.state==='RUNNING'?'Running':'Enabled · not running';const actions=document.createElement('div');actions.className='row-actions';const control=(action)=>actions.append(button(action,()=>api('POST',`/api/instances/${i.id}/services/${encodeURIComponent(s.name)}`,{action:action.toLowerCase()})));if(s.state==='RUNNING'){control('Stop');control('Restart');}else control('Start');const logs=document.createElement('button');logs.type='button';logs.className='small-btn';logs.textContent='Live logs';logs.onclick=()=>openServiceLogs(i,s.name);const errorLog=document.createElement('button');errorLog.type='button';errorLog.className='small-btn';errorLog.textContent='Error log';errorLog.onclick=()=>openServiceErrorLog(i,s.name);actions.append(logs,errorLog);const catalogID=serviceCatalogForProcess(s.name),installState=catalogID?i.service_installs?.[catalogID]:'';if(catalogID&&catalogID!=='python'&&['INSTALLED','FAILED','UNINSTALL_FAILED','UNINSTALLING'].includes(installState)){const uninstall=document.createElement('button');uninstall.type='button';uninstall.className='danger';uninstall.textContent=installState==='UNINSTALLING'?'Uninstalling…':installState==='UNINSTALL_FAILED'?'Retry uninstall':'Uninstall';uninstall.disabled=installState==='UNINSTALLING';uninstall.setAttribute('aria-label',`${installState==='UNINSTALL_FAILED'?'Retry uninstall':'Uninstall'} ${s.name} from ${i.id}`);uninstall.onclick=()=>uninstallDetailService(i.id,catalogID,s.name);actions.append(uninstall);}row(t,[s.name,state,formatListenPorts(s.listen_ports),`${(Number(s.cpu_percent)||0).toFixed(1)}%`,fmtMetricBytes(s.memory_bytes),fmtMetricBytes(s.swap_bytes),'Host-level only',actions]);});if(selectedServiceLog&&$('service-log-dialog').open){const selected=services.find(s=>s.name===selectedServiceLog);$('service-log-output').textContent=selected?.logs||'No log output collected yet for this service.';$('service-log-output').scrollTop=$('service-log-output').scrollHeight;}if($('service-error-dialog')?.open&&selectedErrorService)refreshServiceErrorLog(i.id,selectedErrorService);if($('waf-log-dialog')?.open)refreshWAFErrorLog(i.id);}
function openServiceErrorLog(i,name){selectedErrorService=name;$('service-error-title').textContent=name+' error log';$('service-error-output').textContent='Loading error log…';$('service-error-dialog').showModal();refreshServiceErrorLog(i.id,name);}
async function refreshServiceErrorLog(id,name){try{const result=await api('GET',`/api/instances/${id}/services/${encodeURIComponent(name)}/error-log`);$('service-error-output').textContent=result.logs||'No error log entries.';$('service-error-output').scrollTop=$('service-error-output').scrollHeight;}catch(e){$('service-error-output').textContent=e.message;}}
function hostHTTPPort(ssh){return Number(ssh)+5000;}
function hostHTTPSPort(ssh){return Number(ssh)+10000;}
function managedPublished(spec,guest){const key=guest===22?'publish_ssh':guest===80?'publish_http':guest===443?'publish_https':'';if(!key)return false;const v=spec?.[key];return v===undefined||v===null?true:!!v;}
function clearPortForm(){if(!$('port-form'))return;if($('port-edit-host'))$('port-edit-host').value='';if($('port-edit-managed'))$('port-edit-managed').value='';if($('guest-port')){$('guest-port').value='';$('guest-port').readOnly=false;}$('port-protocol').value='tcp';$('port-protocol').disabled=false;if($('port-save'))$('port-save').textContent='Add port';if($('port-cancel'))$('port-cancel').hidden=true;}
function editPortMapping(mapping){if(!$('port-form')||!mapping)return;const managed=!!mapping.managed;if($('port-edit-managed'))$('port-edit-managed').value=managed?'1':'';$('port-edit-host').value=managed?'':String(mapping.host_port);$('guest-port').value=String(mapping.guest_port);$('guest-port').readOnly=managed;$('port-protocol').value=String(mapping.protocol||'tcp').toLowerCase();$('port-protocol').disabled=managed;$('port-save').textContent=managed?'Publish port':'Save port';$('port-cancel').hidden=false;$('port-message').textContent=managed?`Managed ${mapping.service||'port'} uses fixed guest ${mapping.guest_port} → localhost:${mapping.host_port}. Save publishes it again.`:`Editing custom map localhost:${mapping.host_port}.`;$('guest-port').focus();}
function renderPorts(i){const t=$('port-mappings');if(!t)return;row(t,['Service','Protocol','Guest port','Local host access','Actions'],true);const ssh=Number(i.spec?.ssh_port)||0,sp=i.spec||{};const managedRows=ssh?[{service:'SSH',protocol:'tcp',guest_port:22,host_port:ssh,managed:true,published:managedPublished(sp,22)},{service:'HTTP',protocol:'tcp',guest_port:80,host_port:hostHTTPPort(ssh),managed:true,published:managedPublished(sp,80)},{service:'HTTPS',protocol:'tcp',guest_port:443,host_port:hostHTTPSPort(ssh),managed:true,published:managedPublished(sp,443)}]:[];const customs=(sp.port_mappings||[]).map(p=>({service:'Custom',...p,managed:false,published:true}));[...managedRows,...customs].filter(p=>p.host_port).forEach(p=>{const address=`localhost:${p.host_port}`;const guest=Number(p.guest_port);const link=document.createElement('a');link.className='port-address';link.textContent=p.published?address:`unpublished · ${address}`;if(!p.published){link.href='#';link.onclick=e=>e.preventDefault();}else if(p.protocol==='tcp'&&guest===80){link.href=`http://${address}/`;link.target='_blank';link.rel='noopener';}else if(p.protocol==='tcp'&&guest===443){link.href=`https://${address}/`;link.target='_blank';link.rel='noopener';}else{link.href='#';link.onclick=async e=>{e.preventDefault();const text=guest===22?`ssh -p ${p.host_port} root@localhost`:address;try{await navigator.clipboard.writeText(text);$('port-message').textContent=`Copied: ${text}`;}catch(_){$('port-message').textContent=text;}};}const actions=document.createElement('div');actions.className='row-actions';const edit=document.createElement('button');edit.type='button';edit.className='small-btn';edit.textContent=p.managed&&!p.published?'Publish':'Edit';edit.onclick=()=>editPortMapping(p);const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';del.disabled=p.managed&&!p.published;del.onclick=async()=>{const label=p.managed?`${p.service} (guest ${guest})`:`custom ${String(p.protocol||'tcp').toUpperCase()} ${guest}`;if(!window.confirm(p.managed?`Unpublish ${label}? Host DNAT for localhost:${p.host_port} will be removed.`:`Delete ${label} → localhost:${p.host_port}?`))return;try{if(p.managed)await api('DELETE',`/api/instances/${i.id}/ports/managed/${guest}`);else await api('DELETE',`/api/instances/${i.id}/ports/${p.host_port}`);$('port-message').textContent=p.managed?`Unpublished ${p.service}.`:`Deleted localhost:${p.host_port}.`;clearPortForm();await refresh();}catch(e){$('port-message').textContent=e.message;}};actions.append(edit,del);row(t,[p.service||'Custom',String(p.protocol||'tcp').toUpperCase(),p.guest_port,link,actions]);});if(t.rows.length===1)row(t,['No ports mapped','','','','']);}
function updateNavBadges(){const v=$('nav-vps-count'),w=$('nav-worker-count'),s=$('nav-security-count');if(v)v.textContent=String(instances.length);if(w)w.textContent=String(workers.length);if(s)s.textContent=String(instances.filter(i=>i.spec?.protection_mode===false).length);}
function securityStatus(text,enabled){const e=document.createElement('span');e.className='badge '+(enabled?'RUNNING':'STOPPED');e.textContent=text;return e;}
function renderSecurity(){const scrollX=window.scrollX,scrollY=window.scrollY;const protectedCount=instances.filter(i=>i.spec?.protection_mode!==false).length,sshBlocked=instances.filter(i=>i.spec?.ssh_login_blocked===true).length,deleteProtected=instances.filter(i=>i.spec?.delete_protection===true).length,wafInstalled=instances.filter(i=>i.service_installs?.['owasp-waf']==='INSTALLED').length,forwardCount=instances.reduce((n,i)=>n+(i.spec?.port_mappings?.length||0),0),activeWorkers=workers.filter(w=>w.status==='active').length;$('security-protected').textContent=String(protectedCount);$('security-unprotected').textContent=String(instances.length-protectedCount);$('security-ssh-blocked').textContent=String(sshBlocked);$('security-delete-protected').textContent=String(deleteProtected);$('security-waf').textContent=String(wafInstalled);$('security-forwards').textContent=String(forwardCount);$('security-workers').textContent=String(activeWorkers);$('security-loading').hidden=true;$('security-empty').hidden=instances.length>0;$('security-message').textContent=instances.length-protectedCount?`${instances.length-protectedCount} VPS${instances.length-protectedCount===1?' has':'s have'} Protection Mode disabled.`:'';const table=$('security-instances');row(table,['VPS','State','Worker','Protection Mode','SSH Login Block','Delete Protection','OWASP CRS WAF','Custom forwards','Action'],true);if(!instances.length)row(table,['No VPS instances are available.','','','','','','','','']);instances.forEach(i=>{const sp=i.spec||{},protectedMode=sp.protection_mode!==false,ssh=sp.ssh_login_blocked===true,deleteGuard=sp.delete_protection===true,waf=i.service_installs?.['owasp-waf']||'',wafLabel=({INSTALLED:'Installed',INSTALLING:'Installing',UNINSTALLING:'Uninstalling',FAILED:'Install failed',UNINSTALL_FAILED:'Uninstall failed',UNINSTALLED:'Not installed'})[waf]||'Not installed',wafBadge=securityStatus(wafLabel,waf==='INSTALLED');if(['INSTALLING','UNINSTALLING'].includes(waf))wafBadge.className='badge STARTING';const manage=document.createElement('a');manage.className='small-btn';manage.href='/instances/'+encodeURIComponent(i.id);manage.textContent='Manage';manage.setAttribute('aria-label',`Manage security for ${i.id}`);row(table,[i.id,badge(i.state),i.worker_id,securityStatus(protectedMode?'Enabled':'Disabled',protectedMode),securityStatus(ssh?'Blocked':'Allowed',ssh),securityStatus(deleteGuard?'Enabled':'Disabled',deleteGuard),wafBadge,String(sp.port_mappings?.length||0),manage]);});const ports=$('security-ports');row(ports,['VPS','Protocol','Guest port','Host port','Action'],true);let found=false;instances.forEach(i=>(i.spec?.port_mappings||[]).forEach(p=>{found=true;const manage=document.createElement('a');manage.className='small-btn';manage.href='/instances/'+encodeURIComponent(i.id);manage.textContent='Review';manage.setAttribute('aria-label',`Review port mappings for ${i.id}`);row(ports,[i.id,String(p.protocol||'tcp').toUpperCase(),String(p.guest_port),String(p.host_port),manage]);}));if(!found)row(ports,['No custom port forwards','','','','']);const keepScroll=()=>{window.scrollTo(scrollX,scrollY);};if(typeof window.renderSecurityCenter==='function')Promise.resolve(window.renderSecurityCenter()).finally(keepScroll);else keepScroll();}
function iamCheckbox(container,label,value,checked){const item=document.createElement('label');item.className='iam-check-item';const input=document.createElement('input');input.type='checkbox';input.value=value;input.checked=checked;const text=document.createElement('span');text.textContent=label;item.append(input,text);container.append(item);}
function renderIAMAssignments(){const groups=$('iam-user-groups'),policies=$('iam-user-policies'),groupIDs=iamUsers.find(u=>u.id===iamEditingUser)?.group_ids||[],policyIDs=iamUsers.find(u=>u.id===iamEditingUser)?.policy_ids||[];groups.textContent='';policies.textContent='';iamGroups.forEach(g=>iamCheckbox(groups,g.name,g.id,groupIDs.includes(g.id)));iamPolicies.forEach(p=>iamCheckbox(policies,`${p.name} · ${p.permissions.join(', ')}`,p.id,policyIDs.includes(p.id)));if(!iamGroups.length)groups.textContent='No groups created yet.';if(!iamPolicies.length)policies.textContent='No custom policies created yet.';}
function checkedIAMValues(id){return [...$(id).querySelectorAll('input:checked')].map(x=>x.value);}
function openIAMUserDialog(){const dlg=$('iam-user-dialog');if(!dlg)return;renderIAMAssignments();if(!dlg.open)dlg.showModal();}
function closeIAMUserDialog(){const dlg=$('iam-user-dialog');if(dlg?.open)dlg.close();}
function clearIAMUserForm(){iamEditingUser='';$('iam-user-id').value='';$('iam-username').value='';if($('iam-email'))$('iam-email').value='';if($('iam-password'))$('iam-password').value='';$('iam-role').value='IAM User';$('iam-status').value='active';$('iam-user-form-title').textContent='Create IAM user';$('iam-user-save').textContent='Create user';$('iam-user-form-message').textContent='';renderIAMAssignments();}
function editIAMUser(id){const u=iamUsers.find(x=>x.id===id);if(!u)return;if(u.kind!=='iam'&&cfMe?.kind!=='admin'){$('iam-users-message').textContent='Account root is managed from Profile (password) and Access keys - not Edit.';return;}iamEditingUser=id;$('iam-user-id').value=id;$('iam-username').value=u.username;if($('iam-email'))$('iam-email').value=u.email||'';if($('iam-password'))$('iam-password').value='';$('iam-role').value=u.role;$('iam-status').value=u.status;$('iam-user-form-title').textContent='Edit IAM user';$('iam-user-save').textContent='Save user';$('iam-user-form-message').textContent=`Editing ${u.username}.`;openIAMUserDialog();}
function renderIAMGroupPolicies(selected){const box=$('iam-group-policies');if(!box)return;const picked=new Set(selected||[]);box.textContent='';if(!iamPolicies.length){const empty=document.createElement('p');empty.className='muted';empty.innerHTML='No custom policies yet. Open <a href="#iam-policies-panel">Policies</a>, create one (or Use a built-in template), then attach it here.';box.append(empty);return;}iamPolicies.forEach(p=>iamCheckbox(box,`${p.name} · ${(p.permissions||[]).join(', ')}`,p.id,picked.has(p.id)));}
function openIAMGroupDialog(){const dlg=$('iam-group-dialog');if(!dlg)return;renderIAMGroupPolicies(iamEditingGroup?(iamGroups.find(g=>g.id===iamEditingGroup)?.policy_ids||[]):[]);if(!dlg.open)dlg.showModal();}
function closeIAMGroupDialog(){const dlg=$('iam-group-dialog');if(dlg?.open)dlg.close();}
function clearIAMGroupForm(){iamEditingGroup='';$('iam-group-id').value='';$('iam-group-name').value='';$('iam-group-description').value='';$('iam-group-form-title').textContent='Create group';$('iam-group-save').textContent='Create group';if($('iam-group-form-message'))$('iam-group-form-message').textContent='';renderIAMGroupPolicies([]);}
function editIAMGroup(id){const g=iamGroups.find(x=>x.id===id);if(!g)return;iamEditingGroup=id;$('iam-group-id').value=id;$('iam-group-name').value=g.name;$('iam-group-description').value=g.description||'';$('iam-group-form-title').textContent='Edit group';$('iam-group-save').textContent='Save group';if($('iam-group-form-message'))$('iam-group-form-message').textContent=`Editing ${g.name}.`;openIAMGroupDialog();}
function renderIAMPermissionOptions(selected){
 const box=$('iam-policy-permissions');box.replaceChildren();
 const order=['Global','Compute','Storage','Networking','Security','Platform'];
 const groups={};
 for(const permission of iamPermissions){
  const detail=iamPermissionDetails[permission]||{label:permission,description:'',category:'Other'};
  const cat=detail.category||'Other';
  (groups[cat]||(groups[cat]=[])).push({permission,detail});
 }
 const cats=[...order.filter(c=>groups[c]),...Object.keys(groups).filter(c=>!order.includes(c)).sort()];
 for(const cat of cats){
  const section=document.createElement('section');section.className='iam-permission-category';
  const heading=document.createElement('h4');heading.textContent=cat;
  const list=document.createElement('div');list.className='iam-permission-category-list';
  for(const {permission,detail} of groups[cat]){
   const item=document.createElement('label');item.className='iam-check-item iam-permission-item';
   const input=document.createElement('input');input.type='checkbox';input.value=permission;input.checked=selected.includes(permission);
   const text=document.createElement('span'),title=document.createElement('strong'),code=document.createElement('code'),description=document.createElement('small');
   title.textContent=detail.label;code.textContent=permission;description.textContent=detail.description;text.append(title,code,description);item.append(input,text);list.append(item);
  }
  section.append(heading,list);box.append(section);
 }
 filterIAMPermissions();
}
function filterIAMPermissions(){const query=($('iam-permission-search')?.value||'').trim().toLowerCase();document.querySelectorAll('#iam-policy-permissions .iam-permission-category').forEach(section=>{let any=false;section.querySelectorAll('.iam-permission-item').forEach(item=>{const show=!query||item.textContent.toLowerCase().includes(query);item.hidden=!show;if(show)any=true;});section.hidden=!any;});}
$('iam-permission-search')?.addEventListener('input',filterIAMPermissions);
function applyBuiltInIAMPolicy(p){if(!p)return;clearIAMPolicyForm();$('iam-policy-name').value=(p.name||'CustomPolicy').replace(/^CloFix/,'')+'Copy';$('iam-policy-description').value=p.description||'';renderIAMPermissionOptions(p.permissions||[]);$('iam-policy-form-title').textContent='Create policy from template';$('iam-policy-message').textContent='Built-in scopes loaded. Rename and save to create a custom policy.';$('iam-policy-name').scrollIntoView({behavior:'smooth',block:'center'});}
function clearIAMPolicyForm(){iamEditingPolicy='';$('iam-policy-id').value='';$('iam-policy-name').value='';$('iam-policy-description').value='';$('iam-policy-form-title').textContent='Create policy';$('iam-policy-save').textContent='Create policy';$('iam-policy-cancel').hidden=true;$('iam-policy-message').textContent='';const box=$('iam-policy-permissions');box.textContent='';renderIAMPermissionOptions([]);}
function editIAMPolicy(id){const p=iamPolicies.find(x=>x.id===id);if(!p)return;iamEditingPolicy=id;$('iam-policy-id').value=id;$('iam-policy-name').value=p.name;$('iam-policy-description').value=p.description;$('iam-policy-form-title').textContent='Edit policy';$('iam-policy-save').textContent='Save policy';$('iam-policy-cancel').hidden=false;const box=$('iam-policy-permissions');box.textContent='';renderIAMPermissionOptions(p.permissions);$('iam-policy-name').scrollIntoView({behavior:'smooth',block:'center'});}
function syncIAMKeyUserActions(){const id=$('iam-key-user')?.value,summary=$('iam-keys-user-summary'),heading=$('iam-keys-list-heading');const u=iamUsers.find(x=>x.id===id);if(heading)heading.textContent=u?`Keys for ${u.username}`:'Keys for selected user';if(!summary)return;if(!u){summary.hidden=true;summary.textContent='';return;}const kind=u.kind==='iam'?'IAM user':(u.kind==='root'||u.kind==='admin'?'Account owner':u.kind);summary.hidden=false;summary.textContent=`${u.username} · ${kind} · ${u.role} · ${u.status}`;}
async function refreshIAMKeys(){const id=$('iam-key-user').value,table=$('iam-keys-table'),msg=$('iam-keys-message');syncIAMKeyUserActions();if(!id){row(table,['Access key ID','Status','Created','Actions'],true);row(table,['Select a user above to list and manage keys.','','','']);return;}try{const keys=await api('GET',`/api/iam/users/${id}/keys`);row(table,['Access key ID','Status','Created','Actions'],true);if(!keys.length)row(table,['No access keys for this user.','','','']);keys.forEach(k=>{const status=document.createElement('span');status.className='badge'+(k.active?' active':'');status.textContent=k.active?'Active':'Inactive';const actions=document.createElement('div');actions.className='row-actions';const suspend=document.createElement('button');suspend.type='button';suspend.className=k.active?'danger small-btn':'small-btn';suspend.textContent=k.active?'Suspend':'Enable';suspend.title=k.active?'Set key inactive without deleting it':'Set key active again';suspend.onclick=async()=>{const act=k.active?'suspend':'resume';if(!window.confirm(`${act==='suspend'?'Suspend':'Enable'} access key ${k.access_key_id}?`))return;try{await api('POST',`/api/iam/users/${id}/keys/${k.access_key_id}/${act}`);await refreshIAMKeys();msg.textContent=act==='suspend'?'Access key suspended (inactive).':'Access key enabled (active).';}catch(e){msg.textContent=e.message;}};const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent='Delete';del.title='Permanently delete this access key';del.onclick=async()=>{if(!window.confirm(`Delete access key ${k.access_key_id}? This cannot be undone.`))return;try{await api('DELETE',`/api/iam/users/${id}/keys/${k.access_key_id}`);await refreshIAMKeys();msg.textContent='Access key deleted.';}catch(e){msg.textContent=e.message;}};actions.append(suspend,del);row(table,[k.access_key_id,status,new Date(k.created).toLocaleString(),actions]);});}catch(e){msg.textContent=e.message;}}
async function refreshIAM(){const [me,users,groups,policyData]=await Promise.all([api('GET','/api/iam/me'),api('GET','/api/iam/users'),api('GET','/api/iam/groups'),api('GET','/api/iam/policies')]);iamUsers=users;iamGroups=groups;iamPolicies=policyData.policies||[];iamPermissions=policyData.permissions||[];iamPermissionDetails=policyData.permission_details||{};applyAccountMenu(me);const userTable=$('iam-users-table');row(userTable,['Username','User ID','Account ID','Email','Role','Status','Groups','Policies','Created','Actions'],true);if(!iamUsers.length)row(userTable,['No IAM users yet. Use Create IAM user.','','','','','','','','','']);iamUsers.forEach(u=>{const actions=document.createElement('div');actions.className='row-actions';const isSelf=me&&u.id===me.id;const manageRow=!isSelf&&(u.kind==='iam'||(me&&me.kind==='admin'));if(manageRow){const edit=document.createElement('button');edit.className='small-btn';edit.textContent='Edit';edit.onclick=()=>editIAMUser(u.id);const keys=document.createElement('button');keys.className='small-btn';keys.textContent='Keys';keys.onclick=()=>{location.hash='iam-keys-panel';$('iam-key-user').value=u.id;refreshIAMKeys();};const del=document.createElement('button');del.className='danger small-btn';del.textContent='Delete';del.onclick=async()=>{if(!window.confirm(`Delete IAM user ${u.username} and revoke all of its access keys?`))return;try{await api('DELETE',`/api/iam/users/${u.id}`);await refreshIAM();}catch(e){$('iam-users-message').textContent=e.message;}};const statusBtn=document.createElement('button');statusBtn.className=u.status==='active'?'danger small-btn':'small-btn';statusBtn.textContent=u.status==='active'?'Suspend':'Enable';statusBtn.onclick=async()=>{const act=u.status==='active'?'suspend':'resume';if(!window.confirm(`${act==='suspend'?'Suspend':'Enable'} IAM user ${u.username}?`))return;try{await api('POST',`/api/iam/users/${u.id}/${act}`);await refreshIAM();}catch(e){$('iam-users-message').textContent=e.message;}};actions.append(edit,keys,statusBtn,del);}else if(isSelf){const keys=document.createElement('button');keys.className='small-btn';keys.textContent='Keys';keys.title='Create, suspend, or delete your access keys';keys.onclick=()=>{location.hash='iam-keys-panel';$('iam-key-user').value=u.id;refreshIAMKeys();};const pw=document.createElement('a');pw.className='small-btn';pw.href='/profile#profile-details';pw.textContent='Password';pw.title='Change password in Profile';const note=document.createElement('span');note.className='muted';note.textContent='You · cannot delete / suspend / deactivate yourself';actions.append(keys,pw,note);}else{const note=document.createElement('span');note.className='muted';note.textContent='Account owner';actions.append(note);}const groupNames=(u.group_ids||[]).map(id=>iamGroups.find(g=>g.id===id)?.name||id).join(', ')||'-',policyNames=(u.policy_ids||[]).map(id=>iamPolicies.find(p=>p.id===id)?.name||id).join(', ')||'-';row(userTable,[u.username,u.id||'-',u.account_id||'-',u.email||'-',u.role,badge(u.status),groupNames,policyNames,new Date(u.created).toLocaleDateString(),actions]);});const userSelect=$('iam-key-user'),old=userSelect.value;userSelect.length=1;iamUsers.forEach(u=>{const allowKey=u.kind==='iam'||(me&&u.id===me.id)||(me&&me.kind==='admin');if(!allowKey)return;const base=u.kind!=='iam'&&me&&u.id===me.id?`${u.username} · account root (you)`:`${u.username} · ${u.role}`;const label=u.status==='active'?base:`${base} · ${u.status}`;userSelect.add(new Option(label,u.id));});userSelect.value=iamUsers.some(u=>u.id===old)?old:'';syncIAMKeyUserActions();const groupsTable=$('iam-groups-table');row(groupsTable,['Group','Description','Users','Policies','Actions'],true);if(!iamGroups.length)row(groupsTable,['No groups yet. Use Create group.','','','','']);iamGroups.forEach(g=>{const actions=document.createElement('div');actions.className='row-actions';const edit=document.createElement('button');edit.className='small-btn';edit.textContent='Edit';edit.onclick=()=>editIAMGroup(g.id);const del=document.createElement('button');del.className='danger small-btn';del.textContent='Delete';del.onclick=async()=>{if(!window.confirm(`Delete group ${g.name}? Users will be removed from it.`))return;try{await api('DELETE',`/api/iam/groups/${g.id}`);await refreshIAM();}catch(e){$('iam-group-message').textContent=e.message;}};actions.append(edit,del);row(groupsTable,[g.name,g.description,(g.user_ids||[]).length,(g.policy_ids||[]).map(id=>iamPolicies.find(p=>p.id===id)?.name||id).join(', ')||'-',actions]);});const policiesTable=$('iam-policies-table');row(policiesTable,['Policy','Category','Description','Permissions','Actions'],true);const builtIn=policyData.built_in||[];builtIn.forEach(p=>{const actions=document.createElement('div');actions.className='row-actions';const use=document.createElement('button');use.className='small-btn';use.textContent='Use';use.title='Copy these scopes into the form as a new custom policy';use.onclick=()=>applyBuiltInIAMPolicy(p);actions.append(use);row(policiesTable,[p.name,p.category||'-',p.description||'Built-in template',(p.permissions||[]).join(', '),actions]);});if(!iamPolicies.length)row(policiesTable,['No custom policies','','','','']);iamPolicies.forEach(p=>{const actions=document.createElement('div');actions.className='row-actions';const edit=document.createElement('button');edit.className='small-btn';edit.textContent='Edit';edit.onclick=()=>editIAMPolicy(p.id);const del=document.createElement('button');del.className='danger small-btn';del.textContent='Delete';del.onclick=async()=>{if(!window.confirm(`Delete policy ${p.name}? It will be detached from users and groups.`))return;try{await api('DELETE',`/api/iam/policies/${p.id}`);await refreshIAM();}catch(e){$('iam-policy-message').textContent=e.message;}};actions.append(edit,del);row(policiesTable,[p.name,'Custom',p.description||'-',p.permissions.join(', '),actions]);});if(!(document.activeElement&&document.activeElement.closest('#iam-user-groups,#iam-user-policies,#iam-group-policies,#iam-policy-permissions'))){renderIAMAssignments();if(!iamEditingGroup)renderIAMGroupPolicies([]);if(!iamEditingPolicy){renderIAMPermissionOptions(checkedIAMValues('iam-policy-permissions'));}}await refreshIAMKeys();}
function isClofixS3(c){try{return new URL(c.endpoint).hostname.toLowerCase()==='s3pool'||c.provider==='clofix';}catch(e){return c?.provider==='clofix';}}
function s3Provider(c){if(c.provider)return c.provider;try{const host=new URL(c.endpoint).hostname.toLowerCase();if(host.includes('amazonaws.com'))return 'aws';if(host==='storage.googleapis.com'||host.endsWith('.storage.googleapis.com'))return 'gcs';}catch(e){}return 'custom';}
function s3ProviderLabel(c){if(isClofixS3(c))return 'CloFix S3';const labels={aws:'Amazon S3',gcs:'Google Cloud Storage',azure_s3:'Azure S3 gateway',custom:'S3-compatible'};return labels[s3Provider(c)]||'S3-compatible';}
function s3ConnectionLabel(c){return `${isClofixS3(c)?'CloFix S3':c.name} · ${c.bucket}${isClofixS3(c)?' · default':''}`;}
function setS3ConnectionForm(c){s3EditingConnectionID=c?.id||'';$('s3-provider').value=c&& !isClofixS3(c)?s3Provider(c):'aws';$('s3-name').value=c?.name||'';$('s3-endpoint').value=c?.endpoint||'';$('s3-bucket').value=c?.bucket||'';$('s3-region').value=c?.region||'';$('s3-prefix').value=c?.prefix||'';$('s3-access-key').value='';$('s3-secret-key').value='';$('s3-access-key').placeholder=c?'Leave blank to keep saved key':'Access key ID';$('s3-secret-key').placeholder=c?'Leave blank to keep saved secret':'Secret access key';$('s3-save').textContent=c?'Save changes':'Add S3 connection';$('s3-connection-title').textContent=c?'Edit S3 connection':'Add S3 connection';$('s3-message').textContent='';}
function renderS3Connections(){const table=$('s3-connections-table');if(!table)return;row(table,['Storage','Provider','Bucket','Endpoint','Action'],true);const sorted=[...s3Connections].sort((a,b)=>Number(isClofixS3(b))-Number(isClofixS3(a)));if(!sorted.length)row(table,['No S3 connections yet.','','','','']);for(const c of sorted){const actions=document.createElement('div');actions.className='row-actions';if(isClofixS3(c)){const tag=document.createElement('span');tag.className='badge active';tag.textContent='Default';actions.append(tag);}else{const edit=document.createElement('button');edit.className='small-btn';edit.textContent='Edit';edit.onclick=()=>{ $('s3-all-dialog').close();$('s3-connection-select').value=c.id;s3CurrentConnection=c.id;setS3ConnectionForm(c);$('s3-connection-dialog').showModal();};const del=document.createElement('button');del.className='danger small-btn';del.textContent='Delete';del.onclick=()=>deleteS3Connection(c.id);actions.append(edit,del);}row(table,[isClofixS3(c)?'CloFix S3':c.name,s3ProviderLabel(c),c.bucket,c.endpoint,actions]);}}
async function deleteS3Connection(id){const c=s3Connections.find(x=>x.id===id);if(!id||!c||isClofixS3(c))return;if(!window.confirm(`Delete ${c.name} (${s3ProviderLabel(c)})? This does not delete bucket contents.`))return;try{await api('DELETE',`/api/storage/connections/${id}`);$('s3-all-message').textContent='S3 connection deleted. Bucket contents were not deleted.';if(s3CurrentConnection===id){s3CurrentConnection='';s3BrowserPrefix='';}await refreshS3();}catch(e){$('s3-all-message').textContent=e.message;}}
async function refreshS3(){if(!T)return;try{const liveNodes=workers.filter(w=>w.status==='active'&&w.capacity?.s3_node&&Date.now()-new Date(w.last_heartbeat||0).getTime()<60000),poolTotal=liveNodes.reduce((n,w)=>n+(Number(w.capacity.s3_disk_total)||0),0),poolFree=liveNodes.reduce((n,w)=>n+(Number(w.capacity.s3_disk_free)||0),0);$('s3-pool-nodes').textContent=String(liveNodes.length);$('s3-pool-total').textContent=fmtBytes(poolTotal);$('s3-pool-free').textContent=fmtBytes(poolFree);$('s3-pool-worker-note').textContent=liveNodes.length===1?'1 worker contributing storage':`${liveNodes.length} workers contributing storage`;$('s3-pool-capacity-note').textContent=poolTotal?`${((poolFree/poolTotal)*100).toFixed(1)}% of backing filesystem available to S3`:'Waiting for worker disk metrics';const capacityPct=poolTotal?Math.max(0,Math.min(100,poolFree/poolTotal*100)):0;$('s3-pool-capacity-bar').style.width=capacityPct+'%';$('s3-pool-capacity-meter').setAttribute('aria-valuenow',capacityPct.toFixed(1));const state=$('s3-pool-state');state.textContent=liveNodes.length?'Online':'Waiting for storage nodes';state.className='badge '+(liveNodes.length?'active':'pending');s3Connections=await api('GET','/api/storage/connections');const select=$('s3-connection-select'),old=select.value;select.textContent='';const ordered=[...s3Connections].sort((a,b)=>Number(isClofixS3(b))-Number(isClofixS3(a)));ordered.forEach(c=>select.add(new Option(s3ConnectionLabel(c),c.id)));const selected=s3Connections.find(c=>c.id===old)||s3Connections.find(isClofixS3)||s3Connections[0]||null;select.value=selected?.id||'';const newID=selected?.id||'';if(newID!==s3CurrentConnection){s3CurrentConnection=newID;s3BrowserPrefix='';setS3ConnectionForm(selected);refreshS3Objects();}renderS3Connections();const mountSelect=$('s3-mount-connection'),mountOld=mountSelect.value;mountSelect.length=1;s3Connections.forEach(c=>mountSelect.add(new Option(s3ConnectionLabel(c),c.id)));mountSelect.value=mountOld||newID;const v=$('s3-mount-vps'),oldV=v.value;v.length=1;instances.forEach(i=>v.add(new Option(i.id,i.id)));v.value=oldV;const mounts=$('s3-mounts');row(mounts,['VPS','Connection','Guest path','State','Action'],true);const mounted=instances.filter(i=>i.spec?.s3_mount_path);mounted.forEach(i=>{const c=s3Connections.find(x=>x.id===i.spec?.s3_connection_id);row(mounts,[i.id,c?s3ConnectionLabel(c):i.spec?.s3_connection_id||'Legacy connection',i.spec.s3_mount_path,badge(i.state),button('Unmount',()=>api('DELETE',`/api/instances/${i.id}/s3-mount`))]);});if(!mounted.length)row(mounts,['No S3 mounts','','','','']);}catch(e){$('s3-message').textContent=e.message;}}
async function refreshS3Objects(cursor=''){
  const id=$('s3-connection-select')?.value,msg=$('s3-browser-message'),table=$('s3-objects');
  if(!id){row(table,['Create or select an S3 connection first.','','','','']);$('s3-browser-location').textContent='No connection selected.';return;}
  msg.textContent='Loading bucket objects…';
  try{
    const data=await api('GET',`/api/storage/connections/${id}/objects?prefix=${encodeURIComponent(s3BrowserPrefix)}&cursor=${encodeURIComponent(cursor)}`),connection=s3Connections.find(c=>c.id===id);
    const crumbs=$('s3-browser-location');crumbs.textContent='';
    const addCrumb=(label,prefix,last)=>{if(crumbs.childNodes.length){const sep=document.createElement('span');sep.className='s3-crumb-separator';sep.textContent='›';crumbs.append(sep);}const item=document.createElement(last?'span':'button');item.textContent=label;item.className=last?'s3-crumb-current':'s3-crumb';if(!last)item.onclick=()=>{s3BrowserPrefix=prefix;refreshS3Objects();};crumbs.append(item);};
    addCrumb(connection?.bucket||'Bucket','',!s3BrowserPrefix);let pathParts=s3BrowserPrefix.split('/').filter(Boolean),running='';pathParts.forEach((part,index)=>{running+=part+'/';addCrumb(part,running,index===pathParts.length-1);});
    const allBox=document.createElement('input');allBox.type='checkbox';allBox.setAttribute('aria-label','Select all visible objects');row(table,[allBox,'Name','Type','Size','Last modified',''],true);
    const term=($('s3-search')?.value||'').trim().toLowerCase(),entries=(data.entries||[]).filter(e=>!term||e.name.toLowerCase().includes(term));
    if(!entries.length){const empty=document.createElement('div');empty.className='s3-empty-state';empty.textContent=term?'No files or folders match this search.':'This folder is empty. Upload files or create a folder to get started.';const tr=document.createElement('tr'),td=document.createElement('td');td.colSpan=6;td.append(empty);tr.append(td);table.append(tr);}
    entries.forEach(entry=>{
      const checkbox=document.createElement('input');checkbox.type='checkbox';checkbox.className='s3-select-object';checkbox.dataset.key=entry.key;checkbox.setAttribute('aria-label',`Select ${entry.name}`);
      const name=document.createElement(entry.folder?'button':'span');name.className=entry.folder?'id-link s3-entry-name':'s3-entry-name';name.textContent=(entry.folder?'▰  ':'▤  ')+entry.name+(entry.folder?'/':'');if(entry.folder)name.onclick=()=>{s3BrowserPrefix=entry.key;refreshS3Objects();};
      const actions=document.createElement('div');actions.className='row-actions';
      const download=document.createElement('button');download.className='small-btn';download.textContent=entry.folder?'Download zip':'Download';download.onclick=()=>entry.folder?downloadS3Folder(id,entry.key):downloadS3Object(id,entry.key);actions.append(download);if(!entry.folder&&/\.(txt|html?|css|js|json|xml|md|php|ya?ml|svg|conf|ini|sh)$/i.test(entry.name)){const edit=document.createElement('button');edit.className='small-btn';edit.textContent='Edit';edit.onclick=()=>openS3Editor(id,entry.key);actions.append(edit);}const move=document.createElement('button');move.className='small-btn';move.textContent='Move';move.onclick=()=>openS3Move(entry.key,!!entry.folder);actions.append(move);
      const del=document.createElement('button');del.className='danger small-btn';del.textContent='Delete';del.onclick=async()=>{if(!window.confirm(entry.folder?`Delete folder ${entry.key} and all objects inside it?`:`Delete ${entry.key}?`))return;try{await api('DELETE',`/api/storage/connections/${id}/object?key=${encodeURIComponent(entry.key)}`);await refreshS3Objects();}catch(e){msg.textContent=e.message;}};actions.append(del);
      row(table,[checkbox,name,entry.folder?'Folder':'File',entry.folder?'-':fmtBytes(entry.size),entry.modified?new Date(entry.modified).toLocaleString():'-',actions]);
    });
    allBox.onchange=()=>{document.querySelectorAll('.s3-select-object').forEach(box=>box.checked=allBox.checked);$('s3-delete-selected').hidden=!allBox.checked;};
    const next=$('s3-next');next.hidden=!data.cursor;next.dataset.cursor=data.cursor||'';next.textContent='Next page';msg.textContent='';
  }catch(e){msg.textContent=e.message;table.textContent='';const tr=document.createElement('tr'),td=document.createElement('td');td.colSpan=6;td.className='s3-empty-state';td.textContent='Could not load bucket objects.';tr.append(td);table.append(tr);}
}
async function downloadAuthorized(url,filename,msgId){try{const r=await fetch(url,{credentials:'include'});if(!r.ok)throw new Error((await r.json().catch(()=>({}))).error||r.status);const blob=await r.blob(),href=URL.createObjectURL(blob),a=document.createElement('a');a.href=href;a.download=filename;a.click();setTimeout(()=>URL.revokeObjectURL(href),1000);}catch(e){if($(msgId))$(msgId).textContent=e.message;}}
async function downloadS3Object(id,key){await downloadAuthorized(`/api/storage/connections/${id}/object?key=${encodeURIComponent(key)}`,key.split('/').filter(Boolean).pop()||'file','s3-browser-message');}
async function downloadS3Folder(id,prefix){const msg=$('s3-browser-message');if(msg)msg.textContent='Preparing zip…';await downloadAuthorized(`/api/storage/connections/${id}/archive?prefix=${encodeURIComponent(prefix||'')}`,(String(prefix||'').split('/').filter(Boolean).pop()||'s3')+'.zip','s3-browser-message');if(msg&&msg.textContent==='Preparing zip…')msg.textContent='';}
function openS3Move(key,folder){s3Moving=key;s3MovingFolder=!!folder;$('s3-move-from').textContent='/'+key;$('s3-move-to').value=key;$('s3-move-message').textContent='';$('s3-move-dialog').showModal();}
async function openS3Editor(id,key){try{const data=await api('GET',`/api/storage/connections/${id}/object?key=${encodeURIComponent(key)}&text=1`);s3EditingKey=key;$('s3-editor-title').textContent=key;$('s3-editor-content').value=data.content;$('s3-editor-message').textContent='';$('s3-editor-dialog').showModal();}catch(e){$('s3-browser-message').textContent=e.message;}}
async function refreshVolumes(){if(!T)return;try{volumes=await api('GET','/api/volumes');const active=workers.filter(w=>w.status==='active'),workerSelect=$('volume-create-worker'),oldWorker=workerSelect.value;workerSelect.length=1;active.forEach(w=>workerSelect.add(new Option(`${w.id} · ${w.hostname||'worker'}`,w.id)));workerSelect.value=oldWorker;const edit=$('volume-edit-select'),oldEdit=edit.value;edit.length=1;volumes.forEach(v=>edit.add(new Option(`${v.name} · ${v.state}`,v.id)));edit.value=oldEdit;const attach=$('volume-attach-select'),oldAttach=attach.value,readyVolumes=volumes.filter(v=>v.state==='READY'&&!v.instance_id);attach.length=1;readyVolumes.forEach(v=>attach.add(new Option(`${v.name} · ${v.worker_id}`,v.id)));attach.value=readyVolumes.some(v=>v.id===oldAttach)?oldAttach:(readyVolumes[0]?.id||'');populateVolumeVPS();$('vol-count').textContent=String(volumes.length);$('vol-attached').textContent=String(volumes.filter(v=>v.state==='ATTACHED'||v.state==='ATTACHING'||v.state==='DETACHING').length);const totalUsage=volumes.reduce((n,v)=>n+(Number(v.usage_bytes)||0),0),cappedVolumes=volumes.filter(v=>Number(v.limit_mb)>0),totalCap=cappedVolumes.reduce((n,v)=>n+(Number(v.limit_mb)*1048576),0);$('vol-used').textContent=fmtBytes(totalUsage);$('vol-used-note').textContent=cappedVolumes.length?`${cappedVolumes.length} capped volume${cappedVolumes.length===1?'':'s'} · combined hard cap ${fmtBytes(totalCap)} · unlimited excluded`:'No hard caps configured; usage is reported by workers.';const t=$('volume-table');row(t,['Name','ID','Worker','State','Error','Usage / hard limit','Attached VPS','Guest path','Actions'],true);if(!volumes.length)row(t,['No volumes yet. Create one above.','','','','','','','','']);volumes.forEach(v=>{const actions=document.createElement('div');actions.className='row-actions';const selectAction=(label,fn)=>{const b=document.createElement('button');b.className='small-btn';b.textContent=label;b.onclick=fn;return b;};actions.append(selectAction('Edit',()=>openVolumeEdit(v.id)));if(v.state==='ATTACHED')actions.append(button('Detach',()=>api('POST',`/api/volumes/${v.id}/detach`,{})));if(v.state==='READY'&&!v.instance_id)actions.append(selectAction('Attach',()=>openVolumeAttach(v.id)));if(['READY','FAILED'].includes(v.state)&&!v.instance_id)actions.append(button('Delete',()=>{if(!window.confirm(`Permanently delete ${v.name} and all its data? This cannot be undone.`))return Promise.reject(new Error('Volume deletion cancelled.'));return api('DELETE',`/api/volumes/${v.id}`);},'danger'));row(t,[v.name,v.id,v.worker_id,badge(v.state),v.error||'-',`${fmtBytes(v.usage_bytes||0)} / ${v.limit_mb?fmtBytes(Number(v.limit_mb)*1048576):'Unlimited'}`,v.instance_id||'-',v.guest_path||'-',actions]);});refreshVolumeExplorerSelect();}catch(e){$('volume-message').textContent=e.message;}}
function refreshVolumeExplorerSelect(){const select=$('volume-explorer-select');if(!select)return;const old=select.value,available=volumes.filter(v=>['READY','ATTACHED'].includes(v.state));select.length=1;available.forEach(v=>select.add(new Option(`${v.name} · ${v.state}${v.guest_path?' · '+v.guest_path:''}${v.instance_id?' · '+v.instance_id:''}`,v.id)));const preferred=available.find(v=>v.state==='ATTACHED'&&v.guest_path==='/var/www/html');const next=available.some(v=>v.id===old)?old:(preferred?.id||available[0]?.id||'');select.value=next;if(next!==old){volumeExplorerPath='';refreshVolumeFiles();}if(!next){$('volume-explorer-location').textContent='/';$('volume-explorer-table')&&row($('volume-explorer-table'),['No ready or attached volumes available.','','',''],true);$('volume-explorer-message').textContent='Create or attach a volume first.';}}
async function refreshVolumeFiles(){const id=$('volume-explorer-select')?.value,table=$('volume-explorer-table'),msg=$('volume-explorer-message');if(!id){if(table)row(table,['Select a volume to browse files.','','',''],true);return;}msg.textContent='Loading volume contents…';try{const data=await api('GET',`/api/volumes/${id}/files?path=${encodeURIComponent(volumeExplorerPath)}`);row(table,['Name','Type','Size','Modified','Actions'],true);if(!data.entries?.length)row(table,['This folder is empty. Upload a file or create one here.','','','','']);for(const entry of data.entries||[]){const name=document.createElement(entry.kind==='folder'?'button':'span');if(entry.kind==='folder'){name.className='id-link';name.textContent=entry.name+'/';name.onclick=()=>{volumeExplorerPath=entry.path;refreshVolumeFiles();};}else{name.textContent=entry.name;if(entry.kind==='symlink')name.title='Symlinks are listed but never followed by the explorer.';}const actions=document.createElement('div');actions.className='row-actions';if(entry.kind==='file'||entry.kind==='folder'){const dl=document.createElement('button');dl.className='small-btn';dl.textContent=entry.kind==='folder'?'Download zip':'Download';dl.onclick=()=>entry.kind==='folder'?downloadVolumeFolder(id,entry.path):downloadVolumeFile(id,entry.path);actions.append(dl);if(entry.kind==='file'&&/\.(txt|html?|css|js|json|xml|md|php|ya?ml|svg|conf|ini|sh)$/i.test(entry.name)){const edit=document.createElement('button');edit.className='small-btn';edit.textContent='Edit';edit.onclick=()=>openVolumeEditor(id,entry.path);actions.append(edit);}const mv=document.createElement('button');mv.className='small-btn';mv.textContent='Move';mv.onclick=()=>openVolumeMove(entry.path,entry.kind==='folder');actions.append(mv);}const del=document.createElement('button');del.className='danger small-btn';del.textContent=entry.kind==='folder'?'Delete folder':'Delete';del.onclick=async()=>{const recursive=entry.kind==='folder';if(!window.confirm(recursive?`Delete folder ${entry.path} and everything inside it?`:`Delete ${entry.path}?`))return;try{await api('DELETE',`/api/volumes/${id}/file?path=${encodeURIComponent(entry.path)}&recursive=${recursive?'1':'0'}`);await refreshVolumeFiles();await refreshVolumes();}catch(e){msg.textContent=e.message;}};actions.append(del);row(table,[name,entry.kind,entry.kind==='folder'?'-':fmtBytes(entry.size),entry.modified?new Date(entry.modified).toLocaleString():'-',actions]);}$('volume-explorer-location').textContent=`/${volumeExplorerPath}`;msg.textContent='';}catch(e){msg.textContent=e.message;row(table,['Could not load volume contents.','','','','']);}}
async function downloadVolumeFile(id,path){await downloadAuthorized(`/api/volumes/${id}/file?path=${encodeURIComponent(path)}`,path.split('/').filter(Boolean).pop()||'file','volume-explorer-message');}
async function downloadVolumeFolder(id,path){const msg=$('volume-explorer-message');if(msg)msg.textContent='Preparing zip…';await downloadAuthorized(`/api/volumes/${id}/archive?path=${encodeURIComponent(path||'')}`,(String(path||'').split('/').filter(Boolean).pop()||'volume')+'.zip','volume-explorer-message');if(msg&&msg.textContent==='Preparing zip…')msg.textContent='';}
function openVolumeMove(path,folder){volumeMoving=path;volumeMovingFolder=!!folder;$('volume-move-from').textContent='/'+path;$('volume-move-to').value=path;$('volume-move-message').textContent='';$('volume-move-dialog').showModal();}
async function openVolumeEditor(id,path){try{const data=await api('GET',`/api/volumes/${id}/file?path=${encodeURIComponent(path)}&text=1`);volumeEditingPath=path;$('volume-editor-title').textContent=path;$('volume-editor-content').value=data.content;$('volume-editor-message').textContent='';$('volume-editor-dialog').showModal();}catch(e){$('volume-explorer-message').textContent=e.message;}}
function volumeChildPath(name){return volumeExplorerPath?`${volumeExplorerPath}/${name}`:name;}
async function uploadVolumeFiles(items,directories=[]){const id=$('volume-explorer-select').value,msg=$('volume-explorer-message');if(!id){msg.textContent='Select a volume before uploading.';return;}if(!items.length&&!directories.length){msg.textContent='No files or folders were found.';return;}try{const dirs=new Set(directories);for(const item of items){const relative=item.relativePath||item.file.webkitRelativePath||item.file.name,parts=relative.split('/');if(parts.some(part=>!part||part==='.'||part==='..'))throw new Error(`Invalid folder path for ${item.file.name}.`);if(item.file.size>4*1024*1024)throw new Error(`${relative} exceeds the 4 MB upload limit.`);for(let i=1;i<parts.length;i++)dirs.add(parts.slice(0,i).join('/'));}const ordered=[...dirs].sort((a,b)=>a.split('/').length-b.split('/').length||a.localeCompare(b));for(let i=0;i<ordered.length;i++){const path=volumeChildPath(ordered[i]);msg.textContent=`Creating folder ${i+1} of ${ordered.length}: ${ordered[i]}`;await api('POST',`/api/volumes/${id}/folder`,{path});}for(let i=0;i<items.length;i++){const {file}=items[i],relative=items[i].relativePath||file.webkitRelativePath||file.name,path=volumeChildPath(relative);msg.textContent=`Uploading ${i+1} of ${items.length}: ${relative}`;await apiRaw('PUT',`/api/volumes/${id}/file?path=${encodeURIComponent(path)}`,file,file.type||'application/octet-stream');}msg.textContent=items.length?`Uploaded ${items.length} file${items.length===1?'':'s'}${ordered.length?` and created/checked ${ordered.length} folder${ordered.length===1?'':'s'}`:''}.`:`Created ${ordered.length} folder${ordered.length===1?'':'s'}.`;await refreshVolumeFiles();await refreshVolumes();}catch(e){msg.textContent=e.message;}}
async function collectDroppedVolumeItems(dataTransfer){const files=[],directories=new Set(),items=Array.from(dataTransfer.items||[]);async function visit(entry,parent=''){const relative=parent?`${parent}/${entry.name}`:entry.name;if(entry.isDirectory){directories.add(relative);const reader=entry.createReader();while(true){const batch=await new Promise((resolve,reject)=>reader.readEntries(resolve,reject));if(!batch.length)break;await Promise.all(batch.map(child=>visit(child,relative)));}}else if(entry.isFile){const file=await new Promise((resolve,reject)=>entry.file(resolve,reject));files.push({file,relativePath:relative});}}let usedEntries=false;for(const item of items){if(item.kind!=='file')continue;const entry=typeof item.webkitGetAsEntry==='function'?item.webkitGetAsEntry():null;if(entry){usedEntries=true;await visit(entry);}else{const file=item.getAsFile?.();if(file)files.push({file,relativePath:file.name});}}if(!usedEntries&&!files.length){for(const file of Array.from(dataTransfer.files||[]))files.push({file,relativePath:file.webkitRelativePath||file.name});}return {files,directories:[...directories]};}
async function handleVolumeDrop(event){try{const dropped=await collectDroppedVolumeItems(event.dataTransfer);await uploadVolumeFiles(dropped.files,dropped.directories);}catch(e){$('volume-explorer-message').textContent=e.message||'Could not read the dropped files or folders.';}}
function populateVolumeVPS(){const select=$('volume-attach-vps'),old=select.value,volume=volumes.find(v=>v.id===$('volume-attach-select').value);select.length=1;const eligible=volume?instances.filter(i=>i.worker_id===volume.worker_id&&['RUNNING','STOPPED'].includes(i.state)):[];eligible.forEach(i=>select.add(new Option(`${i.id} · ${i.state}`,i.id)));if(!eligible.length)select.add(new Option(volume?'No running or stopped VPS on this volume’s worker':'Select a volume first',''));select.value=eligible.some(i=>i.id===old)?old:(eligible[0]?.id||'');}
function openVolumeEdit(id=''){const select=$('volume-edit-select'),volume=volumes.find(v=>v.id===(id||select.value));if(id)select.value=id;$('volume-edit-name').value=volume?.name||'';$('volume-edit-message').textContent=volume?`Editing ${volume.id}.`:'';$('volume-edit-dialog').showModal();}
function openVolumeAttach(id=''){const select=$('volume-attach-select'),ready=volumes.filter(v=>v.state==='READY'&&!v.instance_id);select.value=id||select.value||ready[0]?.id||'';populateVolumeVPS();const volume=volumes.find(v=>v.id===select.value);$('volume-attach-message').textContent=volume?`Choose a VPS on worker ${volume.worker_id}.`:ready.length?'Select a volume.':'No detached ready volumes are available.';$('volume-attach-dialog').showModal();}
const bind=(id,event,fn)=>{const el=$(id);if(el)el[event]=fn;};
async function ensureWAFWritable(i,actionLabel){if(!i)throw new Error('Select a VPS first.');if(i.state!=='RUNNING')throw new Error('Start the VPS before '+actionLabel+'.');const protectedMode=i.spec?.protection_mode!==false;if(!protectedMode)return i;if(!window.confirm('Protection Mode is enabled. Disable it so CloFix can '+actionLabel+'? The VPS will restart briefly, then the WAF change will continue.'))throw new Error('Cancelled - disable Protection Mode above, then retry.');$('waf-message').textContent='Disabling Protection Mode…';await api('POST',`/api/instances/${i.id}/protection`,{enabled:false});for(let n=0;n<45;n++){await new Promise(r=>setTimeout(r,2000));await refresh();const cur=instances.find(x=>x.id===i.id);if(!cur)throw new Error('VPS disappeared while disabling Protection Mode.');if(cur.state==='RUNNING'&&cur.spec?.protection_mode===false)return cur;if(cur.error)throw new Error(cur.error);$('waf-message').textContent=`Waiting for Protection Mode to disable… (${n+1}/45)`;}throw new Error('Timed out waiting for Protection Mode to disable. Check worker status and retry.');}

const CF_THEMES={dark:{label:'Dark',meta:'#0c111b'},light:{label:'Light',meta:'#f3f5f9'},midnight:{label:'Midnight',meta:'#0d1117'},slate:{label:'Slate',meta:'#0f1419'},contrast:{label:'High contrast',meta:'#000000'},warm:{label:'Warm',meta:'#14110e'}};
function applyTheme(mode,persist=true){const theme=CF_THEMES[mode]?mode:'dark';document.documentElement.dataset.theme=theme;if(persist){try{localStorage.setItem('cf_theme',theme);}catch(e){}}const picker=$('console-theme');if(picker){picker.querySelectorAll('.theme-choice').forEach(btn=>{if(btn.dataset.theme===theme)btn.setAttribute('aria-current','true');else btn.removeAttribute('aria-current');});const summary=picker.querySelector('summary');if(summary){summary.setAttribute('aria-label','Theme: '+CF_THEMES[theme].label);summary.title='Theme: '+CF_THEMES[theme].label;}}const meta=document.querySelector('meta[name="theme-color"]');if(meta)meta.content=CF_THEMES[theme].meta;}
applyTheme(document.documentElement.dataset.theme,false);
$('console-theme')?.addEventListener('click',e=>{const btn=e.target.closest('.theme-choice');if(!btn)return;e.preventDefault();applyTheme(btn.dataset.theme);$('console-theme').removeAttribute('open');});
const colorScheme=window.matchMedia?.('(prefers-color-scheme: light)');colorScheme?.addEventListener?.('change',e=>{try{if(!localStorage.getItem('cf_theme'))applyTheme(e.matches?'light':'dark',false);}catch(err){}});
function backupActions(b,msgEl){const actions=document.createElement('div');actions.className='row-actions';const restore=document.createElement('button');restore.type='button';restore.className='small-btn';restore.textContent='Run as VPS';restore.disabled=b.state!=='READY';restore.onclick=()=>openRestoreDialog(b.id);const del=document.createElement('button');del.type='button';del.className='danger small-btn';del.textContent=b.state==='DELETING'?'Deleting…':'Delete';del.disabled=['CREATING','DELETING'].includes(b.state);del.onclick=async()=>{if(!window.confirm(`Permanently delete backup ${b.id} (${fmtBytes(b.size_bytes)})?`))return;try{await api('DELETE',`/api/backups/${b.id}`);if(msgEl)msgEl.textContent='Backup deletion queued.';await refreshBackups();}catch(e){if(msgEl)msgEl.textContent=e.message;}};actions.append(restore,del);return actions;}
function renderDetailBackups(){const table=$('detail-backups-table'),note=$('detail-backups-note'),msg=$('backup-create-message');if(!table||!selectedID)return;const mine=(backups||[]).filter(b=>b.instance_id===selectedID).sort((a,b)=>new Date(b.created)-new Date(a.created));row(table,['Backup','Created','Archive size','Resources','Status','Actions'],true);if(!mine.length){row(table,['No backups for this VPS yet. Stop the VPS, then create a snapshot.','','','','','']);if(note)note.textContent='Only backups for this VPS are listed here.';return;}mine.forEach(b=>row(table,[b.id,new Date(b.created).toLocaleString(),fmtBytes(b.size_bytes),`${b.ram_mb} MB RAM · ${b.cpu} CPU · ${b.swap_mb} MB swap`,badge(b.state),backupActions(b,msg)]));if(note)note.textContent=`${mine.length} backup${mine.length===1?'':'s'} for ${selectedID}.`;}
async function refreshBackups(){if(!T)return;const msg=$('backups-message');try{backups=await api('GET','/api/backups');const table=$('backups-table');if(table){row(table,['Backup','Source VPS','Worker','Created','Archive size','Resources','Status','Actions'],true);if(!backups.length)row(table,['No backups yet. Stop a VPS and create its first snapshot from VPS Details.','','','','','','']);backups.forEach(b=>{const vps=document.createElement('button');vps.type='button';vps.className='id-link';vps.textContent=b.instance_id;vps.onclick=()=>go('details',b.instance_id);row(table,[b.id,vps,b.worker_id,new Date(b.created).toLocaleString(),fmtBytes(b.size_bytes),`${b.ram_mb} MB RAM · ${b.cpu} CPU · ${b.swap_mb} MB swap`,badge(b.state),backupActions(b,msg)]);});}const select=$('restore-backup');if(select){const old=select.value;select.length=1;backups.filter(b=>b.state==='READY').forEach(b=>select.add(new Option(`${b.id} · ${b.instance_id} · ${fmtBytes(b.size_bytes)}`,b.id)));select.value=backups.some(b=>b.id===old&&b.state==='READY')?old:'';populateRestoreWorkers();}if(msg)msg.textContent=backups.length?`${backups.length} backup${backups.length===1?'':'s'}.`:'';if(page==='details')renderDetailBackups();}catch(e){if(msg)msg.textContent=e.message;if($('backup-create-message'))$('backup-create-message').textContent=e.message;}}
function populateRestoreWorkers(){const backup=backups.find(b=>b.id===$('restore-backup')?.value),select=$('restore-worker');if(!select)return;const old=select.value;select.length=1;if(backup){const worker=workers.find(w=>w.id===backup.worker_id);select.add(new Option(`${backup.worker_id}${worker?.status==='active'?' · active':' · unavailable'}`,backup.worker_id));select.value=backup.worker_id;}else if(old)select.value=old;}
function openRestoreDialog(id=''){const dlg=$('restore-dialog');if(!dlg)return;const select=$('restore-backup');select.value=id||'';const backup=backups.find(b=>b.id===select.value);if(backup){$('restore-ram').value=backup.ram_mb;$('restore-cpu').value=backup.cpu;$('restore-disk').value=backup.disk_mb;$('restore-swap').value=backup.swap_mb;}populateRestoreWorkers();$('restore-message').textContent='';$('restore-run').disabled=!backup;if(!dlg.open)dlg.showModal();}
async function refresh(){
 return preservePageScroll(async()=>{
  if(document.hidden&&lastSuccessfulRefresh)return;if((page==='applications'||page==='docker'||page==='load-balancers'||page==='health-checks'||page==='billing'||page==='logs'||page==='firewall'||page==='waf'||page==='waf-binding'||page==='efs'||page==='users'||page==='profile'||page==='redis-services'||page==='secrets'||page==='audit')&&T)return;if(page==='security'&&lastSuccessfulRefresh)return;try{await ensureSession();if(page==='iam'){await refreshIAM();const banner=$('auth-banner'),stale=$('stale-warning'),stamp=$('refresh-time');if(banner)banner.hidden=true;if(stale)stale.hidden=true;if(stamp)stamp.textContent='Updated '+new Date().toLocaleTimeString();if(typeof updateTopbarMeta==='function')updateTopbarMeta(stamp.textContent);return;}if(page==='audit'&&typeof refreshAuditTrail==='function'){await refreshAuditTrail();const banner=$('auth-banner'),stale=$('stale-warning'),stamp=$('refresh-time');if(banner)banner.hidden=true;if(stale)stale.hidden=true;if(stamp)stamp.textContent='Updated '+new Date().toLocaleTimeString();if(typeof updateTopbarMeta==='function')updateTopbarMeta(stamp.textContent);return;}if(page==='secrets'&&typeof refreshSecrets==='function'){await refreshSecrets();const banner=$('auth-banner'),stale=$('stale-warning'),stamp=$('refresh-time');if(banner)banner.hidden=true;if(stale)stale.hidden=true;if(stamp)stamp.textContent='Updated '+new Date().toLocaleTimeString();if(typeof updateTopbarMeta==='function')updateTopbarMeta(stamp.textContent);return;}if(page==='users'&&typeof refreshUsers==='function'){await refreshUsers();return;}const fetches=[api('GET','/api/instances')];const loadWorkers=cfMe?.kind!=='partner';if(loadWorkers)fetches.push(api('GET','/api/workers'));const results=await Promise.all(fetches);instances=results[0]||[];workers=loadWorkers?(results[1]||[]):[];await loadVPCCatalog();populateWorkerSelect();lastSuccessfulRefresh=Date.now();const banner=$('auth-banner'),stale=$('stale-warning');if(banner)banner.hidden=true;if(stale){stale.hidden=true;stale.textContent='';}const stamp=$('refresh-time');if(stamp){if(page==='security')stamp.textContent='Security Center ready';else stamp.textContent='Updated '+new Date(lastSuccessfulRefresh).toLocaleTimeString();}if(typeof updateTopbarMeta==='function')updateTopbarMeta(stamp?stamp.textContent:'');updateNavBadges();captureUsageSample();loadServerMetricsHistory().catch(()=>{});if(page==='home'){renderHome();if(typeof window.loadHomeActivity==='function')window.loadHomeActivity();}if(page==='instances'){try{loadCKSNodesFrom(await api('GET','/api/v1/kubernetes'));}catch(e){cksNodes=[];}renderInstances();loadPEMKeys();}if(page==='details'){renderDetails();await refreshBackups();if($('waf-log-dialog')?.open)refreshWAFErrorLog(selectedID);if($('service-error-dialog')?.open&&selectedErrorService)refreshServiceErrorLog(selectedID,selectedErrorService);}if(page==='workers')renderWorkers();if(page==='security')renderSecurity();if(page==='services')renderServiceCatalog();if(page==='volumes')await refreshVolumes();if(page==='storage')await refreshS3();if(page==='backups')await refreshBackups();}catch(e){const stale=$('stale-warning');if(stale){stale.hidden=false;stale.textContent='Could not refresh live data: '+e.message;}const banner=$('auth-banner');if(banner&&e.message.startsWith('Unauthorized'))banner.hidden=false;if(page!=='security')showMessage(e.message);if(page==='home'){$('home-loading').hidden=false;$('home-loading').classList.add('load-error');$('home-loading').textContent='Data refresh failed: '+e.message;}}
 });
}
bind('iam-refresh','onclick',async()=>{try{await refreshIAM();$('iam-users-message').textContent='IAM data refreshed.';}catch(e){showMessage(e.message);}});
bind('iam-user-create-open','onclick',()=>{clearIAMUserForm();openIAMUserDialog();});
bind('iam-user-dialog-close','onclick',()=>{clearIAMUserForm();closeIAMUserDialog();});
bind('iam-user-save','onclick',async()=>{const id=$('iam-user-id').value,msg=$('iam-user-form-message'),body={username:$('iam-username').value.trim(),email:($('iam-email')?.value||'').trim(),password:$('iam-password')?.value||'',role:$('iam-role').value,status:$('iam-status').value,group_ids:checkedIAMValues('iam-user-groups'),policy_ids:checkedIAMValues('iam-user-policies')};try{await api(id?'PUT':'POST',id?`/api/iam/users/${id}`:'/api/iam/users',body);msg.textContent=id?'User updated.':'User created.';clearIAMUserForm();closeIAMUserDialog();await refreshIAM();if($('iam-users-message'))$('iam-users-message').textContent=id?'User updated.':'IAM user created.';}catch(e){msg.textContent=e.message;}});
bind('iam-user-cancel','onclick',()=>{clearIAMUserForm();closeIAMUserDialog();});
$('iam-user-dialog')?.addEventListener('click',e=>{if(e.target===$('iam-user-dialog')){clearIAMUserForm();closeIAMUserDialog();}});
bind('iam-key-user','onchange',()=>{ $('iam-new-key').hidden=true; refreshIAMKeys(); });
bind('iam-key-create','onclick',async()=>{const id=$('iam-key-user').value,msg=$('iam-keys-message');if(!id){msg.textContent='Select an IAM user first.';return;}const u=iamUsers.find(x=>x.id===id);if(u&&u.status!=='active'){msg.textContent='Enable the user before generating an access key.';return;}try{const key=await api('POST',`/api/iam/users/${id}/keys`,{});$('iam-new-access-id').textContent=key.access_key_id;$('iam-new-secret').textContent=key.secret_access_key;$('iam-new-bearer').textContent=key.bearer_token;$('iam-new-key').hidden=false;msg.textContent=key.warning;await refreshIAMKeys();}catch(e){msg.textContent=e.message;}});


bind('iam-key-copy','onclick',async()=>{try{await navigator.clipboard.writeText($('iam-new-bearer').textContent);$('iam-keys-message').textContent='Bearer token copied. Store it securely; it is shown once.';}catch(e){$('iam-keys-message').textContent='Clipboard unavailable. Select and copy the bearer token shown above.';}});
bind('iam-key-dismiss','onclick',()=>{$('iam-new-key').hidden=true;$('iam-new-access-id').textContent='';$('iam-new-secret').textContent='';$('iam-new-bearer').textContent='';});
bind('iam-group-create-open','onclick',()=>{clearIAMGroupForm();openIAMGroupDialog();});
bind('iam-group-dialog-close','onclick',()=>{clearIAMGroupForm();closeIAMGroupDialog();});
bind('iam-group-save','onclick',async()=>{const id=$('iam-group-id').value,msg=$('iam-group-form-message')||$('iam-group-message'),body={name:$('iam-group-name').value.trim(),description:$('iam-group-description').value.trim(),policy_ids:checkedIAMValues('iam-group-policies')};try{await api(id?'PUT':'POST',id?`/api/iam/groups/${id}`:'/api/iam/groups',body);clearIAMGroupForm();closeIAMGroupDialog();await refreshIAM();if($('iam-group-message'))$('iam-group-message').textContent=id?'Group updated.':'Group created.';}catch(e){if(msg)msg.textContent=e.message;}});
bind('iam-group-cancel','onclick',()=>{clearIAMGroupForm();closeIAMGroupDialog();});
$('iam-group-dialog')?.addEventListener('click',e=>{if(e.target===$('iam-group-dialog')){clearIAMGroupForm();closeIAMGroupDialog();}});
bind('iam-policy-save','onclick',async()=>{const id=$('iam-policy-id').value,msg=$('iam-policy-message'),body={name:$('iam-policy-name').value.trim(),description:$('iam-policy-description').value.trim(),permissions:checkedIAMValues('iam-policy-permissions')};try{await api(id?'PUT':'POST',id?`/api/iam/policies/${id}`:'/api/iam/policies',body);msg.textContent=id?'Policy updated.':'Policy created.';clearIAMPolicyForm();await refreshIAM();}catch(e){msg.textContent=e.message;}});
bind('iam-policy-cancel','onclick',clearIAMPolicyForm);
bind('s3-add-open','onclick',()=>{setS3ConnectionForm(null);$('s3-connection-dialog').showModal();});bind('s3-all-open','onclick',()=>{$('s3-all-message').textContent='';renderS3Connections();$('s3-all-dialog').showModal();});bind('s3-all-add','onclick',()=>{$('s3-all-dialog').close();setS3ConnectionForm(null);$('s3-connection-dialog').showModal();});bind('s3-connection-close','onclick',()=>$('s3-connection-dialog').close());bind('s3-all-close','onclick',()=>$('s3-all-dialog').close());bind('s3-mount-open','onclick',()=>{$('s3-mount-message').textContent='';$('s3-mount-dialog').showModal();});bind('s3-mount-close','onclick',()=>$('s3-mount-dialog').close());bind('s3-mounts-open','onclick',()=>$('s3-mounts-dialog').showModal());bind('s3-mounts-close','onclick',()=>$('s3-mounts-dialog').close());['s3-connection-dialog','s3-all-dialog','s3-mount-dialog','s3-mounts-dialog'].forEach(id=>$(id)?.addEventListener('click',e=>{if(e.target===$(id))$(id).close();}));
bind('s3-connection-select','onchange',()=>{const c=s3Connections.find(x=>x.id===$('s3-connection-select').value)||null;s3CurrentConnection=c?.id||'';s3BrowserPrefix='';refreshS3Objects();});
bind('s3-provider','onchange',()=>{const provider=$('s3-provider').value,hints={aws:'Amazon S3: use your regional S3 endpoint and signing region.',gcs:'Google Cloud Storage: enable XML API interoperability and use its HMAC access keys.',azure_s3:'Azure: provide an S3-compatible gateway endpoint; native Blob Storage endpoints are not supported.',custom:'Enter the endpoint and credentials supplied by your S3-compatible provider.'};$('s3-provider-hint').textContent=hints[provider]||hints.custom;const placeholders={aws:'https://s3.us-east-1.amazonaws.com',gcs:'https://storage.googleapis.com',azure_s3:'https://your-s3-gateway.example.com',custom:'https://s3.example.com'};$('s3-endpoint').placeholder=placeholders[provider]||placeholders.custom;});
bind('s3-save','onclick',async()=>{const msg=$('s3-message'),id=s3EditingConnectionID,body={name:$('s3-name').value.trim(),provider:$('s3-provider').value,endpoint:$('s3-endpoint').value.trim(),bucket:$('s3-bucket').value.trim(),region:$('s3-region').value.trim(),prefix:$('s3-prefix').value.trim(),access_key:$('s3-access-key').value,secret_key:$('s3-secret-key').value};try{const result=await api(id?'PUT':'POST',id?`/api/storage/connections/${id}`:'/api/storage/connections',body);s3EditingConnectionID=result.connection.id;await refreshS3();$('s3-connection-select').value=result.connection.id;s3CurrentConnection=result.connection.id;s3BrowserPrefix='';setS3ConnectionForm(s3Connections.find(c=>c.id===result.connection.id));msg.textContent=id?'S3 connection updated.':'S3 connection added.';await refreshS3Objects();$('s3-connection-dialog').close();}catch(e){msg.textContent=e.message;}});
bind('s3-mount','onclick',async()=>{const id=$('s3-mount-vps').value,connection_id=$('s3-mount-connection').value,msg=$('s3-mount-message');msg.textContent='';if(!id||!connection_id){msg.textContent='Select an S3 connection and VPS first.';return;}try{const r=await api('POST',`/api/instances/${id}/s3-mount`,{connection_id,guest_path:$('s3-mount-path').value.trim()});msg.textContent=r.restart_required?'Mount queued. The VPS will restart to apply it.':'Mount queued.';await refresh();}catch(e){msg.textContent=e.message;}});
bind('s3-refresh','onclick',()=>refreshS3Objects());bind('s3-download','onclick',()=>{const id=$('s3-connection-select').value;if(!id){$('s3-browser-message').textContent='Select an S3 connection first.';return;}downloadS3Folder(id,s3BrowserPrefix);});bind('s3-move-close','onclick',()=>$('s3-move-dialog').close());bind('s3-move','onclick',async()=>{const id=$('s3-connection-select').value,to=$('s3-move-to').value.trim(),msg=$('s3-move-message');if(!id||!s3Moving||!to){msg.textContent='Enter a destination path.';return;}try{await api('POST',`/api/storage/connections/${id}/move`,{from:s3Moving,to,folder:s3MovingFolder});msg.textContent='Moved.';$('s3-move-dialog').close();await refreshS3Objects();}catch(e){msg.textContent=e.message;}});bind('s3-next','onclick',()=>refreshS3Objects($('s3-next').dataset.cursor||''));bind('s3-search','oninput',()=>refreshS3Objects());bind('s3-delete-selected','onclick',async()=>{const id=$('s3-connection-select').value,keys=[...document.querySelectorAll('.s3-select-object:checked')].map(x=>x.dataset.key);if(!id||!keys.length)return;if(!window.confirm(`Delete ${keys.length} selected object${keys.length===1?'':'s'}? Folder selections also delete their contents.`))return;try{for(const key of keys)await api('DELETE',`/api/storage/connections/${id}/object?key=${encodeURIComponent(key)}`);await refreshS3Objects();}catch(e){$('s3-browser-message').textContent=e.message;}});$('s3-objects')?.addEventListener('change',()=>{$('s3-delete-selected').hidden=!document.querySelector('.s3-select-object:checked');});
bind('s3-up','onclick',()=>{s3BrowserPrefix=s3BrowserPrefix.replace(/\/$/,'').split('/').slice(0,-1).filter(Boolean).join('/');if(s3BrowserPrefix)s3BrowserPrefix+='/';refreshS3Objects();});
bind('s3-folder','onclick',async()=>{const id=$('s3-connection-select').value;if(!id)return;const name=await window.cfPrompt('New folder name','',{title:'Create folder',label:'Folder name',confirm:'Create'});if(!name)return;const key=s3BrowserPrefix+name.replace(/^\/+|\/+$/g,'')+'/';try{await api('POST',`/api/storage/connections/${id}/folders`,{key});await refreshS3Objects();}catch(e){$('s3-browser-message').textContent=e.message;}});
bind('s3-file','onclick',async()=>{const id=$('s3-connection-select').value;if(!id)return;const name=await window.cfPrompt('New text file name','',{title:'Create file',label:'File name',confirm:'Create'});if(!name)return;const key=s3BrowserPrefix+name;try{await apiRaw('PUT',`/api/storage/connections/${id}/object?key=${encodeURIComponent(key)}`,new Blob([''],{type:'text/plain'}),'text/plain');await refreshS3Objects();openS3Editor(id,key);}catch(e){$('s3-browser-message').textContent=e.message;}});
async function uploadS3Items(items,directories=[]){
  const id=$('s3-connection-select').value,msg=$('s3-browser-message');
  if(!id){msg.textContent='Select an S3 connection before uploading.';return;}
  if(!items.length&&!directories.length){msg.textContent='No files or folders were found.';return;}
  try{
    const dirs=new Set(directories);
    for(const item of items){const relative=item.relativePath||item.file.webkitRelativePath||item.file.name,parts=relative.split('/');if(parts.some(part=>!part||part==='.'||part==='..'))throw new Error(`Invalid folder path: ${relative}`);for(let i=1;i<parts.length;i++)dirs.add(parts.slice(0,i).join('/'));}
    for(const dir of dirs)if(dir.split('/').some(part=>!part||part==='.'||part==='..'))throw new Error(`Invalid folder path: ${dir}`);
    const ordered=[...dirs].sort((a,b)=>a.split('/').length-b.split('/').length||a.localeCompare(b));
    for(let i=0;i<ordered.length;i++){msg.textContent=`Preparing folder ${i+1} of ${ordered.length}: ${ordered[i]}`;await api('POST',`/api/storage/connections/${id}/folders`,{key:s3BrowserPrefix+ordered[i]+'/'});}
    for(let i=0;i<items.length;i++){const {file}=items[i],relative=items[i].relativePath||file.webkitRelativePath||file.name;msg.textContent=`Uploading ${i+1} of ${items.length}: ${relative}`;await apiRaw('PUT',`/api/storage/connections/${id}/object?key=${encodeURIComponent(s3BrowserPrefix+relative)}`,file,file.type||'application/octet-stream');}
    msg.textContent=`Uploaded ${items.length} file${items.length===1?'':'s'}${ordered.length?` and prepared ${ordered.length} folder${ordered.length===1?'':'s'}`:''}.`;await refreshS3Objects();
  }catch(e){msg.textContent=e.message;}
}
async function handleS3Drop(event){try{const dropped=await collectDroppedVolumeItems(event.dataTransfer);await uploadS3Items(dropped.files,dropped.directories);}catch(e){$('s3-browser-message').textContent=e.message||'Could not read the dropped files or folders.';}}
bind('s3-upload','onclick',()=>$('s3-upload-input').click());bind('s3-upload-folder','onclick',()=>$('s3-upload-folder-input').click());bind('s3-upload-input','onchange',async()=>{try{await uploadS3Items([...$('s3-upload-input').files].map(file=>({file,relativePath:file.name})));}catch(e){$('s3-browser-message').textContent=e.message;}finally{$('s3-upload-input').value='';}});bind('s3-upload-folder-input','onchange',async()=>{try{await uploadS3Items([...$('s3-upload-folder-input').files].map(file=>({file,relativePath:file.webkitRelativePath||file.name})));}catch(e){$('s3-browser-message').textContent=e.message;}finally{$('s3-upload-folder-input').value='';}});const s3DropZone=$('s3-drop-zone');if(s3DropZone){s3DropZone.addEventListener('dragenter',e=>{e.preventDefault();s3DropZone.classList.add('is-dragover');});s3DropZone.addEventListener('dragover',e=>{e.preventDefault();e.dataTransfer.dropEffect='copy';s3DropZone.classList.add('is-dragover');});s3DropZone.addEventListener('dragleave',e=>{if(!e.relatedTarget||!s3DropZone.contains(e.relatedTarget))s3DropZone.classList.remove('is-dragover');});s3DropZone.addEventListener('drop',e=>{e.preventDefault();s3DropZone.classList.remove('is-dragover');handleS3Drop(e);});}
bind('s3-editor-close','onclick',()=>$('s3-editor-dialog').close());bind('s3-editor-save','onclick',async()=>{const id=$('s3-connection-select').value,msg=$('s3-editor-message');try{await apiRaw('PUT',`/api/storage/connections/${id}/object?key=${encodeURIComponent(s3EditingKey)}`,new Blob([$('s3-editor-content').value],{type:'text/plain; charset=utf-8'}),'text/plain; charset=utf-8');msg.textContent='File saved.';await refreshS3Objects();}catch(e){msg.textContent=e.message;}});
bind('volume-create-open','onclick',()=>{$('volume-create-message').textContent='';$('volume-create-dialog').showModal();});bind('volume-create-close','onclick',()=>$('volume-create-dialog').close());bind('volume-edit-open','onclick',()=>openVolumeEdit());bind('volume-edit-close','onclick',()=>$('volume-edit-dialog').close());bind('volume-attach-open','onclick',()=>openVolumeAttach());bind('volume-attach-close','onclick',()=>$('volume-attach-dialog').close());['volume-create-dialog','volume-edit-dialog','volume-attach-dialog'].forEach(id=>$(id)?.addEventListener('click',e=>{if(e.target===$(id))$(id).close();}));
bind('volume-create','onclick',async()=>{const msg=$('volume-create-message'),name=$('volume-create-name').value.trim(),worker_id=$('volume-create-worker').value,limit_mb=Number($('volume-create-limit').value)||0;if(!name||!worker_id){msg.textContent='Enter a name and select an active worker.';return;}if(limit_mb&&(limit_mb<64||limit_mb>1048576)){msg.textContent='Hard limit must be at least 64 MB and no more than 1,048,576 MB.';return;}try{const v=await api('POST','/api/volumes',{name,worker_id,limit_mb});msg.textContent=`Volume ${v.name} is being created${limit_mb?' with a '+fmtBytes(limit_mb*1048576)+' hard cap':''} on ${v.worker_id}.`;$('volume-create-name').value='';$('volume-create-limit').value='';await refreshVolumes();}catch(e){msg.textContent=e.message;}});
bind('volume-edit-select','onchange',()=>{const v=volumes.find(x=>x.id===$('volume-edit-select').value);$('volume-edit-name').value=v?.name||'';$('volume-edit-message').textContent=v?`Editing ${v.id}.`:'';});
bind('volume-edit','onclick',async()=>{const id=$('volume-edit-select').value,name=$('volume-edit-name').value.trim(),msg=$('volume-edit-message');if(!id||!name){msg.textContent='Select a volume and enter a name.';return;}try{await api('PUT',`/api/volumes/${id}`,{name});msg.textContent='Volume name updated.';await refreshVolumes();}catch(e){msg.textContent=e.message;}});
bind('volume-attach-select','onchange',populateVolumeVPS);
bind('volume-attach','onclick',async()=>{const id=$('volume-attach-select').value,instance_id=$('volume-attach-vps').value,guest_path=$('volume-attach-path').value.trim(),msg=$('volume-attach-message');if(!id||!instance_id||!guest_path){msg.textContent='Select a volume and VPS, then enter a guest path.';return;}try{const result=await api('POST',`/api/volumes/${id}/attach`,{instance_id,guest_path});msg.textContent=result.restart_required?'Volume attachment queued; the VPS will restart.':'Volume attachment queued.';await refreshVolumes();await refresh();}catch(e){msg.textContent=e.message;}});
bind('volume-refresh','onclick',refreshVolumes);
bind('volume-explorer-select','onchange',()=>{volumeExplorerPath='';refreshVolumeFiles();});bind('volume-explorer-refresh','onclick',refreshVolumeFiles);bind('volume-explorer-up','onclick',()=>{volumeExplorerPath=volumeExplorerPath.split('/').slice(0,-1).join('/');refreshVolumeFiles();});bind('volume-explorer-download','onclick',()=>{const id=$('volume-explorer-select').value;if(!id){$('volume-explorer-message').textContent='Select a volume first.';return;}downloadVolumeFolder(id,volumeExplorerPath);});bind('volume-move-close','onclick',()=>$('volume-move-dialog').close());bind('volume-move','onclick',async()=>{const id=$('volume-explorer-select').value,to=$('volume-move-to').value.trim(),msg=$('volume-move-message');if(!id||!volumeMoving||!to){msg.textContent='Enter a destination path.';return;}try{await api('POST',`/api/volumes/${id}/move`,{from:volumeMoving,to,folder:volumeMovingFolder});msg.textContent='Moved.';$('volume-move-dialog').close();await refreshVolumeFiles();await refreshVolumes();}catch(e){msg.textContent=e.message;}});
bind('volume-explorer-folder','onclick',async()=>{const id=$('volume-explorer-select').value;if(!id)return;const name=await window.cfPrompt('New folder name','',{title:'Create folder',label:'Folder name',confirm:'Create'});if(!name)return;const path=volumeChildPath(name.trim());try{await api('POST',`/api/volumes/${id}/folder`,{path});await refreshVolumeFiles();await refreshVolumes();}catch(e){$('volume-explorer-message').textContent=e.message;}});
bind('volume-explorer-file','onclick',async()=>{const id=$('volume-explorer-select').value;if(!id)return;const name=await window.cfPrompt('New text file name','',{title:'Create file',label:'File name',confirm:'Create'});if(!name)return;const path=volumeChildPath(name.trim());try{await apiRaw('PUT',`/api/volumes/${id}/file?path=${encodeURIComponent(path)}`,new Blob([''],{type:'text/plain'}),'text/plain');await refreshVolumeFiles();openVolumeEditor(id,path);}catch(e){$('volume-explorer-message').textContent=e.message;}});
bind('volume-explorer-upload','onclick',()=>$('volume-explorer-input').click());bind('volume-drop-browse','onclick',()=>$('volume-explorer-input').click());bind('volume-explorer-upload-folder','onclick',()=>$('volume-explorer-folder-input').click());bind('volume-explorer-input','onchange',async()=>{try{await uploadVolumeFiles([...$('volume-explorer-input').files].map(file=>({file,relativePath:file.name})));}finally{$('volume-explorer-input').value='';}});bind('volume-explorer-folder-input','onchange',async()=>{try{await uploadVolumeFiles([...$('volume-explorer-folder-input').files].map(file=>({file,relativePath:file.webkitRelativePath||file.name})));}finally{$('volume-explorer-folder-input').value='';}});const volumeDropZone=$('volume-drop-zone');if(volumeDropZone){volumeDropZone.addEventListener('dragenter',e=>{e.preventDefault();volumeDropZone.classList.add('is-dragover');});volumeDropZone.addEventListener('dragover',e=>{e.preventDefault();e.dataTransfer.dropEffect='copy';volumeDropZone.classList.add('is-dragover');});volumeDropZone.addEventListener('dragleave',e=>{if(!e.relatedTarget||!volumeDropZone.contains(e.relatedTarget))volumeDropZone.classList.remove('is-dragover');});volumeDropZone.addEventListener('drop',e=>{e.preventDefault();volumeDropZone.classList.remove('is-dragover');handleVolumeDrop(e);});}
bind('volume-editor-close','onclick',()=>$('volume-editor-dialog').close());bind('volume-editor-save','onclick',async()=>{const id=$('volume-explorer-select').value,msg=$('volume-editor-message');try{await apiRaw('PUT',`/api/volumes/${id}/file?path=${encodeURIComponent(volumeEditingPath)}`,new Blob([$('volume-editor-content').value],{type:'text/plain; charset=utf-8'}),'text/plain; charset=utf-8');msg.textContent='File saved.';await refreshVolumeFiles();await refreshVolumes();}catch(e){msg.textContent=e.message;}});

bind('waf-error-log','onclick',()=>{const d=$('waf-log-dialog');$('waf-log-output').textContent='Loading Nginx error log…';d.showModal();refreshWAFErrorLog(selectedID);});
bind('waf-log-close','onclick',()=>$('waf-log-dialog').close());
bind('waf-error-html','oninput',()=>{wafErrorDirty=true;});
bind('waf-error-preview','onclick',()=>{const html=$('waf-error-html')?.value||defaultWAFErrorHTML(wafErrorStatus);const blob=new Blob([html],{type:'text/html'});const url=URL.createObjectURL(blob);window.open(url,'_blank','noopener');setTimeout(()=>URL.revokeObjectURL(url),60000);});
bind('waf-error-default','onclick',()=>{if($('waf-error-html')){$('waf-error-html').value=defaultWAFErrorHTML(wafErrorStatus);wafErrorDirty=true;if($('waf-error-message'))$('waf-error-message').textContent='Default HTML loaded - click Save to apply on the VPS.';}});
bind('waf-error-save','onclick',async()=>{const i=instances.find(x=>x.id===selectedID);if(!i)return;const html=($('waf-error-html')?.value||'').trim(),msg=$('waf-error-message'),btn=$('waf-error-save');if(msg)msg.textContent='Saving error page…';if(btn)btn.disabled=true;try{await api('PUT',`/api/instances/${i.id}/waf/error-pages`,{status:wafErrorStatus,html});wafErrorDirty=false;wafErrorSourceID=i.id+':'+wafErrorStatus;if(msg)msg.textContent=`${wafErrorStatus} error page queued. Nginx will reload on the worker.`;await refresh();}catch(e){if(msg)msg.textContent=e.message;}finally{if(btn)btn.disabled=false;}});
bind('waf-install','onclick',async()=>{const b=$('waf-install'),msg=$('waf-message');b.disabled=true;msg.textContent='Preparing WAF installation…';try{const i=await ensureWAFWritable(instances.find(x=>x.id===selectedID),'install the WAF');msg.textContent='Queueing WAF installation…';await api('POST',`/api/instances/${i.id}/install-service`,{service_id:'owasp-waf'});msg.textContent='OWASP CRS WAF installation queued.';await refresh();}catch(e){msg.textContent=e.message;await refresh();}finally{b.disabled=false;}});
bind('waf-uninstall','onclick',async()=>{if(!window.confirm('Uninstall the OWASP CRS WAF from this VPS? ModSecurity and CRS packages will be removed; Nginx will remain installed.'))return;const b=$('waf-uninstall'),msg=$('waf-message');b.disabled=true;msg.textContent='Preparing WAF uninstall…';try{const i=await ensureWAFWritable(instances.find(x=>x.id===selectedID),'uninstall the WAF');msg.textContent='Queueing WAF uninstall…';await api('POST',`/api/instances/${i.id}/uninstall-service`,{service_id:'owasp-waf'});msg.textContent='OWASP CRS WAF uninstall queued.';await refresh();}catch(e){msg.textContent=e.message;await refresh();}finally{b.disabled=false;}});
bind('service-log-close','onclick',()=>$('service-log-dialog').close());bind('service-error-close','onclick',()=>$('service-error-dialog').close());$('service-log-dialog')?.addEventListener('click',e=>{if(e.target===$('service-log-dialog'))$('service-log-dialog').close();});$('service-error-dialog')?.addEventListener('click',e=>{if(e.target===$('service-error-dialog'))$('service-error-dialog').close();});$('waf-log-dialog')?.addEventListener('click',e=>{if(e.target===$('waf-log-dialog'))$('waf-log-dialog').close();});
bind('service-install-vps','onchange',()=>{serviceInstallNotice='';serviceInstallError='';renderServiceInstallDialog();});bind('service-install-run','onclick',runServiceInstall);bind('service-install-close','onclick',()=>$('service-install-dialog').close());bind('service-install-done','onclick',()=>$('service-install-dialog').close());$('service-install-dialog')?.addEventListener('click',e=>{if(e.target===$('service-install-dialog'))$('service-install-dialog').close();});
bind('terminal-inline-copy','onclick',async()=>{const cmd=$('terminal-inline-command').textContent;if(cmd==='SSH port unavailable')return;try{await navigator.clipboard.writeText(cmd);$('terminal-inline-message').textContent='Copied. Paste into the Mac Terminal app.';}catch(e){$('terminal-inline-message').textContent='Clipboard unavailable. Select and copy the command above.';}});
bind('terminal-close','onclick',()=>$('terminal-dialog').close());$('terminal-dialog')?.addEventListener('click',e=>{if(e.target===$('terminal-dialog'))$('terminal-dialog').close();});
bind('terminal-copy','onclick',async()=>{const cmd=$('terminal-command').textContent;try{await navigator.clipboard.writeText(cmd);$('terminal-message').textContent='Copied. Paste it into the Mac Terminal app.';}catch(e){const range=document.createRange();range.selectNodeContents($('terminal-command'));const selection=window.getSelection();selection.removeAllRanges();selection.addRange(range);$('terminal-message').textContent='Select and copy the highlighted command, then paste it into the Mac Terminal app.';}});
bind('settings-save','onclick',()=>{const image=$('settings-image').value.trim(),version=$('settings-version').value.trim(),ram=Number($('settings-ram').value),cpu=Number($('settings-cpu').value),disk=Number($('settings-disk').value),swap=Number($('settings-swap').value),msg=$('settings-message');if(!image||!version||!Number.isFinite(ram)||ram<128||!Number.isFinite(cpu)||cpu<0.25||!Number.isFinite(disk)||disk<512||!Number.isFinite(swap)||swap<0){msg.textContent='Enter valid image values, RAM ≥ 128 MB, CPU ≥ 0.25, disk ≥ 512 MB, and swap ≥ 0.';return;}uiSettings={image,version,ram,cpu,disk,swap,protection:$('settings-protection').checked};localStorage.setItem('cf_ui_settings',JSON.stringify(uiSettings));applySettingsToCreate();msg.textContent='Defaults saved in this browser and applied to future Create FileVPS visits.';});
bind('settings-reset','onclick',()=>{uiSettings={...defaultUISettings};localStorage.removeItem('cf_ui_settings');populateSettings();$('settings-message').textContent='Defaults reset.';});
bind('instance-search','oninput',renderInstances);
const resourceForm=$('resource-form');if(resourceForm)resourceForm.onsubmit=async e=>{e.preventDefault();const msg=$('resource-message'),button=$('resource-save');button.disabled=true;msg.textContent='Saving resource settings…';try{const result=await api('POST',`/api/instances/${selectedID}/resources`,{ram_mb:+$('resource-ram').value,cpu:+$('resource-cpu').value,disk_mb:+$('resource-disk').value,swap_mb:+$('resource-swap').value});msg.textContent=result.restart_required?'Settings saved. VPS is restarting to apply RAM, CPU, and swap limits.':'Settings saved. RAM, CPU, and swap limits apply on next VPS start.';await refresh();}catch(err){msg.textContent=err.message;}finally{button.disabled=false;}};
const sidebarShell=document.querySelector('.shell'),sidebarViewport=matchMedia('(max-width:900px)'),sidebarToggle=$('sidebar-toggle'),sidebarElement=document.querySelector('.sidebar');
if(sidebarShell&&sidebarToggle&&sidebarElement){
function syncSidebar(){
  const open=sidebarViewport.matches?sidebarShell.classList.contains('sidebar-open'):!sidebarShell.classList.contains('sidebar-collapsed');
  sidebarToggle.setAttribute('aria-expanded',String(open));
  sidebarToggle.setAttribute('aria-label',open?'Close navigation menu':'Open navigation menu');
  sidebarElement.inert=sidebarViewport.matches&&!open;
  if(!sidebarViewport.matches){
    sidebarToggle.setAttribute('aria-label',open?'Collapse navigation menu':'Expand navigation menu');
    $('sidebar-close')?.setAttribute('aria-label','Collapse navigation menu');
  }else $('sidebar-close')?.setAttribute('aria-label','Close navigation menu');
}
function closeSidebar(){
  sidebarShell.classList.remove('sidebar-open');
  if(!sidebarViewport.matches)sidebarShell.classList.add('sidebar-collapsed');
  syncSidebar();sidebarToggle.focus();
}
sidebarToggle.onclick=()=>{
  sidebarShell.classList.toggle(sidebarViewport.matches?'sidebar-open':'sidebar-collapsed');
  syncSidebar();
  if(sidebarViewport.matches&&sidebarShell.classList.contains('sidebar-open'))$('sidebar-close')?.focus();
};
$('sidebar-close')&&($('sidebar-close').onclick=closeSidebar);
$('sidebar-backdrop')&&($('sidebar-backdrop').onclick=closeSidebar);
document.querySelectorAll('.nav a').forEach(a=>a.addEventListener('click',()=>{if(sidebarViewport.matches)closeSidebar();}));
document.addEventListener('keydown',e=>{if(e.key==='Escape'&&sidebarViewport.matches&&sidebarShell.classList.contains('sidebar-open'))closeSidebar();});
document.querySelectorAll('.nav a').forEach(link=>{const label=link.querySelector('.nav-icon + span')?.textContent.trim();if(label){link.setAttribute('aria-label',label);link.title=label;}});
sidebarViewport.addEventListener('change',()=>{sidebarShell.classList.remove('sidebar-open');syncSidebar();});
syncSidebar();
}
const signOut=async()=>{try{await fetch('/api/auth/logout',{method:'POST',credentials:'include'});}catch(e){}T='';cfMe=null;location.assign('/login');};
bind('logout','onclick',signOut);
bind('logout-account','onclick',signOut);

bind('instance-create-open','onclick',()=>{applySettingsToCreate();syncCreateAuthFields();loadPEMKeys();loadVPCOptions('instance-vpc');$('create-message').textContent='';$('placement').textContent='';$('create-vps-dialog').showModal();});bind('create-vps-close','onclick',()=>$('create-vps-dialog').close());$('create-vps-dialog')?.addEventListener('click',e=>{if(e.target===$('create-vps-dialog'))$('create-vps-dialog').close();});bind('auth-method','onchange',syncCreateAuthFields);bind('img-preset','onchange',syncImagePresetUI);bind('img','oninput',()=>{if($('img-preset')?.value==='custom'&&$('img-name'))$('img-name').value=$('img').value;});bind('ver','oninput',()=>{if($('img-preset')?.value==='custom'&&$('img-version'))$('img-version').value=$('ver').value;});syncCreateAuthFields();syncImagePresetUI();if(page==='instances'&&new URLSearchParams(location.search).get('create')==='1')$('create-vps-dialog')?.showModal();
bind('create','onclick',async()=>{const method=$('auth-method')?.value||'key',password=$('root-password')?.value||'',generate=!!$('generate-pem')?.checked,sshKeyID=$('ssh-key-id')?.value||'',pub=$('sshkey')?.value.trim()||'',vpsName=($('vps-name')?.value||'').trim();if(!vpsName){$('create-message').textContent='Enter a VPS name (shown in the instances list).';return;}if(!/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/.test(vpsName)){$('create-message').textContent='VPS name must be 1–63 characters: letters, digits, hyphens (not starting/ending with hyphen).';return;}if((method==='pam'||method==='both')&&password.length<8){$('create-message').textContent='Enter a root password of at least 8 characters.';return;}if((method==='key'||method==='both')&&!pub&&!generate&&!sshKeyID){$('create-message').textContent='Paste an SSH public key, choose a saved PEM, or generate a new PEM.';return;}const spec={image:createImageSpec(),ram_mb:+$('ram').value,cpu:+$('cpu').value,disk_mb:+$('disk').value,swap_mb:+$('swap').value,protection_mode:uiSettings.protection,vpc_id:$('instance-vpc')?.value||undefined,hostname:vpsName.toLowerCase()};if(pub)spec.ssh_authorized_key=pub;const body={spec,worker_id:$('wsel').value,auth_method:method};if(password)body.root_password=password;if(generate&&!sshKeyID&&!pub)body.generate_pem=true;if(sshKeyID&&!generate)body.ssh_key_id=sshKeyID;if(generate&&!pub)body.generate_pem=true;try{const r=await api('POST','/api/instances',body);$('placement').textContent=(r.placement||[]).join('\n');$('create-vps-dialog').close();if($('root-password'))$('root-password').value='';if($('vps-name'))$('vps-name').value='';showMessage('VPS creation queued.');showCredentialDialog(r,password);await refresh();}catch(e){$('create-message').textContent=e.message+' '+((e.data&&e.data.placement)||[]).join(' ');}});
bind('pem-create','onclick',async()=>{const name=$('pem-name')?.value.trim()||'',pub=$('pem-public')?.value.trim()||'',msg=$('pem-message');if(!name){msg.textContent='Enter a key name.';return;}try{const r=await api('POST','/api/ssh-keys',{name,public_key:pub||undefined});msg.textContent=r.pem?'PEM created. Download the private key now; it is shown once.':'Public key saved.';if(r.pem)showCredentialDialog({pem:r.pem,key:r.key,name:r.key?.name||name},'');$('pem-name').value='';$('pem-public').value='';await loadPEMKeys();}catch(e){msg.textContent=e.message;}});
bind('pem-ready-close','onclick',()=>{pendingCredentials={pem:'',password:'',id:'',name:''};$('pem-ready-dialog')?.close();});$('pem-ready-dialog')?.addEventListener('click',e=>{if(e.target===$('pem-ready-dialog')){pendingCredentials={pem:'',password:'',id:'',name:''};$('pem-ready-dialog').close();}});
bind('pem-download','onclick',()=>{if(!pendingCredentials.pem)return;downloadText(safeDownloadBase(pendingCredentials.name||pendingCredentials.id)+'.pem',pendingCredentials.pem);});
bind('password-download','onclick',()=>{if(!pendingCredentials.password)return;const label=pendingCredentials.name||pendingCredentials.id||'VPS';downloadText(safeDownloadBase(label)+'-root-password.txt','root password for '+label+'\n'+pendingCredentials.password+'\n');});
const hostnameForm=$('hostname-form');if(hostnameForm)hostnameForm.onsubmit=async e=>{e.preventDefault();const msg=$('hostname-message'),button=$('hostname-save'),name=($('detail-hostname')?.value||'').trim();if(!name){msg.textContent='Enter a VPS name.';return;}if(!/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/.test(name)){msg.textContent='Use letters, digits, and hyphens only (1–63 chars).';return;}button.disabled=true;msg.textContent='Saving name…';try{const r=await api('POST',`/api/instances/${selectedID}/hostname`,{hostname:name});msg.textContent=r.unchanged?'Name unchanged.':'Name saved. It appears in the instances list now.';await refresh();}catch(err){msg.textContent=err.message;}finally{button.disabled=false;}};
const passwordForm=$('password-form');if(passwordForm)passwordForm.onsubmit=async e=>{e.preventDefault();const msg=$('password-message'),button=$('password-save'),password=$('detail-root-password').value;if(password.length<8){msg.textContent='Password must be at least 8 characters.';return;}button.disabled=true;msg.textContent='Queueing password update…';try{await api('POST',`/api/instances/${selectedID}/password`,{password});$('detail-root-password').value='';msg.textContent='Password queued. Waiting for the worker to apply it…';await refresh();for(let n=0;n<12;n++){await new Promise(r=>setTimeout(r,1000));await refresh();const cur=instances.find(x=>x.id===selectedID);if(cur?.spec?.password_auth){msg.textContent='Root password applied. Open Terminal → PAM password and connect (wait ~2s if login fails once).';break;}msg.textContent=`Waiting for worker to enable PAM… (${n+1}s)`;}}catch(err){msg.textContent=err.message;}finally{button.disabled=false;}};
const portForm=$('port-form');if(portForm){portForm.onsubmit=async e=>{e.preventDefault();if(!selectedID)return;const guest=+$('guest-port').value,protocol=$('port-protocol').value,editHost=Number($('port-edit-host')?.value)||0,managedEdit=$('port-edit-managed')?.value==='1';try{if(managedEdit){if(![22,80,443].includes(guest)){$('port-message').textContent='Managed ports must use guest 22, 80, or 443.';return;}const r=await api('PUT',`/api/instances/${selectedID}/ports/managed/${guest}`,{enabled:true});$('port-message').textContent=`Published guest ${r.guest_port} → localhost:${r.host_port}`;clearPortForm();}else if(editHost){const r=await api('PUT',`/api/instances/${selectedID}/ports/${editHost}`,{guest_port:guest,protocol});$('port-message').textContent=`Updated ${r.mapping.protocol.toUpperCase()} ${r.mapping.guest_port} → localhost:${r.mapping.host_port}`;clearPortForm();}else{const r=await api('POST',`/api/instances/${selectedID}/ports`,{guest_port:guest,protocol});$('port-message').textContent=`Mapped ${r.mapping.protocol.toUpperCase()} ${r.mapping.guest_port} → localhost:${r.mapping.host_port}`;clearPortForm();}await refresh();}catch(err){$('port-message').textContent=err.message;}};$('port-cancel')?.addEventListener('click',()=>{clearPortForm();$('port-message').textContent='';});}
bind('load-logs','onclick',async()=>{try{const l=await api('GET','/api/instances/'+selectedID+'/logs');$('detail-logs').textContent=l.logs;$('detail-logs').style.display='block';}catch(e){$('detail-message').textContent=e.message;}});
bind('terminal-open-details','onclick',()=>{const i=instances.find(x=>x.id===selectedID);if(i){if(typeof openVPSTerminalNav==='function')openVPSTerminalNav();else openTerminal(i);}});
bind('backup-create','onclick',async()=>{const i=instances.find(x=>x.id===selectedID),msg=$('backup-create-message'),b=$('backup-create');if(!i)return;if(i.state!=='STOPPED'){msg.textContent='Stop the VPS before creating a consistent backup.';return;}b.disabled=true;msg.textContent='Queueing backup…';try{const result=await api('POST',`/api/instances/${i.id}/backups`,{});msg.textContent=`Backup ${result.backup.id} queued on ${result.backup.worker_id}.`;await refreshBackups();}catch(e){msg.textContent=e.message;}finally{b.disabled=false;}});
bind('backups-refresh','onclick',refreshBackups);
bind('restore-backup','onchange',()=>{populateRestoreWorkers();$('restore-run').disabled=!backups.some(b=>b.id===$('restore-backup').value&&b.state==='READY');});bind('restore-open','onclick',()=>openRestoreDialog());bind('restore-close','onclick',()=>$('restore-dialog').close());$('restore-dialog')?.addEventListener('click',e=>{if(e.target===$('restore-dialog'))$('restore-dialog').close();});
bind('restore-run','onclick',async()=>{const backup=backups.find(b=>b.id===$('restore-backup').value),msg=$('restore-message');if(!backup){msg.textContent='Select a ready backup.';return;}if(!$('restore-ssh-key').value.trim()){msg.textContent='Enter an SSH public key for the new VPS.';return;}const spec={image:{name:'ubuntu',version:'24.04'},ram_mb:Number($('restore-ram').value),cpu:Number($('restore-cpu').value),disk_mb:Number($('restore-disk').value),swap_mb:Number($('restore-swap').value),ssh_authorized_key:$('restore-ssh-key').value.trim(),backup_id:backup.id,protection_mode:true};try{const result=await api('POST','/api/instances',{spec,worker_id:$('restore-worker').value,backup_id:backup.id});await refresh();msg.textContent=`New VPS ${result.instance.id} is provisioning from backup ${backup.id}.`;}catch(e){msg.textContent=e.message;}});
bind('mktok','onclick',async()=>{try{const r=await api('POST','/api/enrollment-tokens',{ttl_minutes:60});$('newtok').textContent='Shown once · valid for 60 minutes\n'+r.token;}catch(e){$('newtok').textContent=e.message;}});
window.cfPageRefreshers=window.cfPageRefreshers||{};
function parseStatNumber(text){
 const raw=String(text||'').replace(/,/g,'').replace(/[^\d.\-]/g,' ').trim();
 if(!raw||raw==='-'||raw==='-')return null;
 const m=raw.match(/-?\d+(\.\d+)?/);
 return m?Number(m[0]):null;
}
function waveSeedFrom(card,idx){
 const label=(card.querySelector('.stat-label,.s3-pool-card-top,span')?.textContent||'')+idx;
 let h=2166136261;
 for(let i=0;i<label.length;i++){h^=label.charCodeAt(i);h=Math.imul(h,16777619);}
 return h>>>0;
}
function buildWavePoints(seed,value,utilMode=false,count=28){
 let s=seed||1;
 const rand=()=>{s=(Math.imul(s,1664525)+1013904223)>>>0;return (s&0xffff)/0xffff;};
 const intensity=utilMode&&value!=null
  ? Math.max(0.12,Math.min(0.92,Number(value)/100))
  : (value==null?0.42:Math.max(0.18,Math.min(0.92,0.22+Math.log10(Math.abs(value)+1)/6)));
 const hist=(usageHistory||[]).slice(-count);
 const pts=[];
 let y=1-intensity;
 for(let i=0;i<count;i++){
  const histV=hist[i]?Number(hist[i].cpuUsed)||0:null;
  const base=utilMode
   ? (1-intensity)+Math.sin(i*0.38+seed*0.0002)*0.04*(0.4+intensity)
   : (histV!=null?1-Math.max(0.12,Math.min(0.95,histV/(Math.max(...hist.map(p=>Number(p.cpuUsed)||0),1)*1.15||1))):y);
  const wobble=utilMode
   ? (Math.sin(i*0.7+seed*0.0003)*0.02+(rand()-0.5)*0.025)
   : ((Math.sin(i*0.55+seed*0.0001)+Math.cos(i*0.31))*0.045+(rand()-0.5)*0.07);
  y=Math.max(0.08,Math.min(0.94,base*0.72+y*0.28+wobble));
  pts.push(y);
 }
 if(value!=null){
  const target=1-intensity;
  for(let i=0;i<8;i++){
   const t=(i+1)/8;
   const k=count-8+i;
   if(k>=0)pts[k]=pts[k]*(1-t)+target*t;
  }
 }
 return pts;
}
function wavePath(pts,w,h,fill){
 if(!pts.length)return '';
 const step=w/Math.max(pts.length-1,1);
 let d=`M 0 ${h}`;
 d+=` L 0 ${(pts[0]*h).toFixed(2)}`;
 for(let i=0;i<pts.length-1;i++){
  const x0=i*step,x1=(i+1)*step;
  const y0=pts[i]*h,y1=pts[i+1]*h;
  const cx=(x0+x1)/2;
  d+=` C ${cx.toFixed(2)} ${y0.toFixed(2)}, ${cx.toFixed(2)} ${y1.toFixed(2)}, ${x1.toFixed(2)} ${y1.toFixed(2)}`;
 }
 if(fill)d+=` L ${w} ${h} Z`;
 return d;
}
function ensureStatWaveDefs(svg){
 if(svg.querySelector('defs[data-ai]'))return;
 const old=svg.querySelector('defs'); if(old) old.remove();
 const ns='http://www.w3.org/2000/svg';
 const defs=document.createElementNS(ns,'defs');
 defs.setAttribute('data-ai','1');
 const mk=(id,c1,c2)=>{
  const g=document.createElementNS(ns,'linearGradient');
  g.id=id;g.setAttribute('x1','0');g.setAttribute('y1','0');g.setAttribute('x2','0');g.setAttribute('y2','1');
  const a=document.createElementNS(ns,'stop');a.setAttribute('offset','0%');a.setAttribute('stop-color',c1);a.setAttribute('stop-opacity','0.7');
  const m=document.createElementNS(ns,'stop');m.setAttribute('offset','55%');m.setAttribute('stop-color',c1);m.setAttribute('stop-opacity','0.18');
  const b=document.createElementNS(ns,'stop');b.setAttribute('offset','100%');b.setAttribute('stop-color',c2);b.setAttribute('stop-opacity','0');
  g.append(a,m,b);defs.append(g);
 };
 mk('stat-wave-grad','#5cf2d6','#0a1520');
 mk('stat-wave-grad-warn','#ff9f6b','#0a1520');
 const glow=document.createElementNS(ns,'filter');
 glow.id='stat-wave-glow';glow.setAttribute('x','-40%');glow.setAttribute('y','-40%');glow.setAttribute('width','180%');glow.setAttribute('height','180%');
 const blur=document.createElementNS(ns,'feGaussianBlur');blur.setAttribute('in','SourceGraphic');blur.setAttribute('stdDeviation','1.6');blur.setAttribute('result','b');
 const merge=document.createElementNS(ns,'feMerge');
 const n1=document.createElementNS(ns,'feMergeNode');n1.setAttribute('in','b');
 const n2=document.createElementNS(ns,'feMergeNode');n2.setAttribute('in','SourceGraphic');
 merge.append(n1,n2);glow.append(blur,merge);defs.append(glow);
 svg.append(defs);
}
function enhanceStatWaves(){
 document.querySelectorAll('#billing-overview-stats .stat-wave, #billing-due-stats .stat-wave, #cs-stats .stat-wave, #users-stats .stat-wave, .database-stats .stat-card .stat-wave').forEach(svg=>svg.remove());
 const cards=document.querySelectorAll('.cards .stat, .s3-pool-metrics .s3-pool-card, .worker-resource-grid .stat');
 cards.forEach((card,idx)=>{
  if(card.closest('[hidden]'))return;
  if(card.closest('#billing-overview-stats, #billing-due-stats, #cs-stats, #users-stats, .database-stats'))return;
  let svg=card.querySelector(':scope > .stat-wave');
  if(!svg){
   svg=document.createElementNS('http://www.w3.org/2000/svg','svg');
   svg.classList.add('stat-wave');
   svg.setAttribute('viewBox','0 0 160 46');
   svg.setAttribute('preserveAspectRatio','none');
   svg.setAttribute('aria-hidden','true');
   card.append(svg);
  }
  ensureStatWaveDefs(svg);
  const valueEl=card.querySelector('.stat-value,.s3-pool-value,strong');
  const meter=card.querySelector('.meter[aria-valuenow]');
  const meterPct=meter?Number(meter.getAttribute('aria-valuenow')):NaN;
  const value=Number.isFinite(meterPct)?meterPct:parseStatNumber(valueEl?.textContent);
  const label=(card.querySelector('.stat-label,.s3-pool-card-top,span')?.textContent||'').toLowerCase();
  let tone='muted';
  if(/fail|alert|unprotect|disabled|error|warn/.test(label)||(Number.isFinite(meterPct)&&meterPct>=90))tone='warn';
  else if(value!=null&&value>0)tone='ok';
  card.dataset.waveTone=tone;
  const pts=buildWavePoints(waveSeedFrom(card,idx)^(Math.floor((lastSuccessfulRefresh||Date.now())/8000)),value, Number.isFinite(meterPct));
  const fillD=wavePath(pts,160,46,true);
  let lineOnly=`M 0 ${(pts[0]*46).toFixed(2)}`;
  const step=160/Math.max(pts.length-1,1);
  for(let i=0;i<pts.length-1;i++){
   const x0=i*step,x1=(i+1)*step,y0=pts[i]*46,y1=pts[i+1]*46,cx=(x0+x1)/2;
   lineOnly+=` C ${cx.toFixed(2)} ${y0.toFixed(2)}, ${cx.toFixed(2)} ${y1.toFixed(2)}, ${x1.toFixed(2)} ${y1.toFixed(2)}`;
  }
  let drift=svg.querySelector('.stat-wave-drift');
  if(!drift){
   drift=document.createElementNS('http://www.w3.org/2000/svg','g');
   drift.classList.add('stat-wave-drift');
   svg.append(drift);
  }
  let fill=drift.querySelector('.stat-wave-fill');
  let line=drift.querySelector('.stat-wave-line');
  if(!fill){
   fill=document.createElementNS('http://www.w3.org/2000/svg','path');
   fill.classList.add('stat-wave-fill');
   drift.append(fill);
  }
  if(!line){
   line=document.createElementNS('http://www.w3.org/2000/svg','path');
   line.classList.add('stat-wave-line');
   drift.append(line);
  }
  fill.setAttribute('d',fillD);
  line.setAttribute('d',lineOnly);
  line.setAttribute('filter','url(#stat-wave-glow)');
  const lastX=160, lastY=(pts[pts.length-1]*46);
  let node=drift.querySelector('.stat-wave-node');
  if(!node){
   node=document.createElementNS('http://www.w3.org/2000/svg','circle');
   node.classList.add('stat-wave-node');
   node.setAttribute('r','2.4');
   drift.append(node);
  }
  node.setAttribute('cx',String(lastX));
  node.setAttribute('cy',lastY.toFixed(2));
 });
}
window.enhanceStatWaves=enhanceStatWaves;
window.renderUsageCharts=renderUsageCharts;
window.invalidateUsageCharts=invalidateUsageCharts;
window.fetchMetricsSeries=fetchMetricsSeries;
window.mergeMetricsPoints=mergeMetricsPoints;
window.loadServerMetricsHistory=loadServerMetricsHistory;
window.chartTimeLabel=chartTimeLabel;
const TOPBAR_SECTIONS={
 compute:[['FileVPS','/instances'],['Library','/services'],['Jobs','/applications'],['Docker','/docker'],['CKS','/kubernetes'],['Redis Cluster','/redis-services']],
 storage:[['Volumes','/volumes'],['S3','/storage'],['Backups','/backups'],['Databases','/databases'],['EFS','/efs']],
 networking:[['LBs','/load-balancers'],['VPC','/vpc'],['DNS','/dns'],['Health','/health-checks'],['Firewall','/firewall']],
 security:[['Security','/security'],['WAF','/waf'],['IAM','/iam'],['Audit Trail','/audit'],['Secrets','/secrets'],['Users','/users']],
 platform:[['Workers','/workers'],['Settings','/settings'],['Billing','/billing'],['Logs','/logs'],['Profile','/profile']],
 account:[['Profile','/profile'],['Settings','/settings']]
};
const TOPBAR_PAGE_SECTION={
 home:'compute',instances:'compute',details:'compute',services:'compute',applications:'compute',docker:'compute',kubernetes:'compute','redis-services':'compute',
 volumes:'storage',storage:'storage',backups:'storage',databases:'storage',efs:'storage',
 'load-balancers':'networking',vpc:'networking',dns:'networking','health-checks':'networking',firewall:'networking',
 security:'security',waf:'security','waf-binding':'security',iam:'security',audit:'security',secrets:'security',users:'security',
 workers:'platform',settings:'platform',billing:'platform',logs:'platform',profile:'account'
};
const TOPBAR_FACT_LINKS={failed:'/instances',unprotected:'/security',workers:'/workers',active:'/workers',VPS:'/instances',running:'/instances'};
function topbarRelativeSync(at){
 if(!at)return 'Ready';
 const sec=Math.max(0,Math.floor((Date.now()-at)/1000));
 if(sec<4)return 'just now';
 if(sec<60)return sec+'s ago';
 if(sec<3600)return Math.floor(sec/60)+'m ago';
 return new Date(at).toLocaleTimeString();
}
function topbarFact(label,value,tone,href){
 const el=document.createElement(href?'button':'span');
 el.className='topbar-fact';
 if(tone)el.dataset.tone=tone;
 if(href){el.type='button';el.title='Open '+label;el.onclick=()=>{location.assign(href);};}
 const strong=document.createElement('strong');
 strong.textContent=String(value);
 el.append(strong,document.createTextNode(' '+label));
 return el;
}
function renderTopbarPulse(){
 const svg=$('topbar-pulse');if(!svg)return;
 const pts=(usageHistory||[]).slice(-16).map(p=>Number(p.cpuUsed)||0);
 if(pts.length<3){svg.hidden=true;svg.replaceChildren();return;}
 const max=Math.max(...pts,1),w=64,h=20,pad=2;
 const poly=pts.map((v,i)=>{
  const x=pad+i*((w-pad*2)/Math.max(pts.length-1,1));
  const y=h-pad-((v/max)*(h-pad*2));
  return x.toFixed(1)+','+y.toFixed(1);
 }).join(' ');
 svg.hidden=false;
 svg.replaceChildren();
 const line=document.createElementNS('http://www.w3.org/2000/svg','polyline');
 line.setAttribute('points',poly);
 svg.append(line);
}
function fillTopbarJump(){
 const select=$('topbar-jump');if(!select)return;
 const section=TOPBAR_PAGE_SECTION[page]||'platform';
 const items=TOPBAR_SECTIONS[section]||TOPBAR_SECTIONS.platform;
 const here=location.pathname.replace(/\/$/,'')||'/';
 select.replaceChildren();
 items.forEach(([label,href])=>{
  const opt=new Option(label,href);
  const path=href.split('#')[0].replace(/\/$/,'')||'/';
  if(path===here||(page==='details'&&path==='/instances'))opt.selected=true;
  select.add(opt);
 });
}
function updateTopbarMeta(){
 const facts=$('topbar-aside-facts'),sync=$('topbar-aside-sync'),link=$('topbar-aside-link');
 if(!facts||!sync)return;
 const running=(instances||[]).filter(i=>i.state==='RUNNING').length;
 const total=(instances||[]).length;
 const failed=(instances||[]).filter(i=>i.state==='FAILED').length;
 const activeWorkers=(workers||[]).filter(w=>w.status==='active').length;
 const labWorkers=(workers||[]).filter(w=>w.status==='active'&&w.capacity&&w.capacity.user_ns===false).length;
 const unprotected=(instances||[]).filter(i=>i.spec?.protection_mode===false).length;
 const volCount=(volumes||[]).length;
 const backupCount=(backups||[]).length;
 const map={
  home:{facts:[[ 'VPS', total, total?'ok':'' ],[ 'running', running, running?'ok':'' ],[ 'workers', activeWorkers, activeWorkers?'ok':'warn' ]],href:'/instances',label:'FileVPS'},
  instances:{facts:[[ 'running', running, running?'ok':'' ],[ 'total', total, '' ],[ 'failed', failed, failed?'warn':'' ]],href:'/instances?create=1',label:'Create VPS'},
  details:{facts:[[ 'VPS', selectedID?selectedID.replace(/^vps-/,'').slice(0,10):'-', '' ],[ 'workers', activeWorkers, activeWorkers?'ok':'warn' ]],href:'/instances',label:'All VPS'},
  services:{facts:[[ 'VPS', total, '' ],[ 'workers', activeWorkers, activeWorkers?'ok':'warn' ]],href:'/workers',label:'Workers'},
  applications:{facts:[[ 'VPS', total, '' ],[ 'workers', activeWorkers, activeWorkers?'ok':'' ]],href:'/services',label:'Library'},
  kubernetes:{facts:[[ 'workers', activeWorkers, activeWorkers?'ok':'warn' ],[ 'VPS', total, '' ]],href:'/workers',label:'Workers'},
  volumes:{facts:[[ 'volumes', volCount, volCount?'ok':'' ],[ 'workers', activeWorkers, activeWorkers?'ok':'' ]],href:'/storage',label:'S3'},
  storage:{facts:[[ 'workers', activeWorkers, activeWorkers?'ok':'warn' ],[ 'volumes', volCount, '' ]],href:'/volumes',label:'Volumes'},
  backups:{facts:[[ 'backups', backupCount, backupCount?'ok':'' ],[ 'VPS', total, '' ]],href:'/instances',label:'FileVPS'},
  databases:{facts:[[ 'workers', activeWorkers, activeWorkers?'ok':'warn' ],[ 'VPS', total, '' ]],href:'/services',label:'Library'},
  efs:{facts:[[ 'workers', activeWorkers, activeWorkers?'ok':'' ]],href:'/volumes',label:'Volumes'},
  'load-balancers':{facts:[[ 'VPS', total, '' ],[ 'workers', activeWorkers, '' ]],href:'/dns',label:'DNS'},
  vpc:{facts:[[ 'VPS', total, '' ],[ 'workers', activeWorkers, '' ]],href:'/firewall',label:'Firewall'},
  dns:{facts:[[ 'VPS', total, '' ]],href:'/load-balancers',label:'LBs'},
  'health-checks':{facts:[[ 'VPS', total, '' ],[ 'workers', activeWorkers, '' ]],href:'/logs',label:'Logs'},
  firewall:{facts:[[ 'VPS', total, '' ],[ 'workers', activeWorkers, activeWorkers?'ok':'' ]],href:'/security',label:'Security'},
  security:{facts:[[ 'VPS', total, total?'ok':'' ],[ 'unprotected', unprotected, unprotected?'warn':'' ],[ 'workers', activeWorkers, activeWorkers?'ok':'warn' ]],href:'/firewall',label:'Firewall'},
  iam:{facts:[[ 'account', 'IAM', 'ok' ]],href:'/users',label:'Users'},
  users:{facts:[[ 'platform', 'users', '' ]],href:'/iam',label:'IAM'},
  profile:{facts:[[ 'account', 'you', 'ok' ]],href:'/settings',label:'Settings'},
  workers:{facts:[[ 'active', activeWorkers, activeWorkers?'ok':'warn' ],[ 'VPS', total, '' ]],href:'/services',label:'Library'},
  settings:{facts:[[ 'platform', 'config', '' ]],href:'/billing',label:'Billing'},
  billing:{facts:[[ 'billing', 'live', 'ok' ]],href:'/settings#settings-pricing',label:'Pricing'},
  logs:{facts:[[ 'VPS', total, '' ],[ 'workers', activeWorkers, '' ]],href:'/health-checks',label:'Health'},
  'redis-services':{facts:[[ 'VPS', total, '' ],[ 'workers', activeWorkers, activeWorkers?'ok':'warn' ]],href:'/instances',label:'FileVPS'},
  kubernetes:{facts:[[ 'VPS', total, '' ],[ 'workers', activeWorkers, activeWorkers?'ok':'warn' ]],href:'/workers',label:'Workers'},
  waf:{facts:[[ 'VPS', total, '' ],[ 'workers', activeWorkers, '' ]],href:'/security',label:'Security'},
  'waf-binding':{facts:[[ 'VPS', total, '' ]],href:'/waf',label:'WAF'}
 };
 const cfg=map[page]||{facts:[[ 'workers', activeWorkers, activeWorkers?'ok':'' ],[ 'VPS', total, '' ]],href:'/',label:'Home'};
 facts.replaceChildren();
 if(labWorkers>0)facts.append(topbarFact('lab',labWorkers,'lab','/workers'));
 (cfg.facts||[]).filter(row=>row[1]!==''&&row[1]!=null).forEach(([label,value,tone])=>{
  if(label==='failed'&&!Number(value))return;
  if(label==='unprotected'&&!Number(value))return;
  facts.append(topbarFact(label,value,tone||'',TOPBAR_FACT_LINKS[label]||''));
 });
 sync.textContent=topbarRelativeSync(lastSuccessfulRefresh);
 sync.title=lastSuccessfulRefresh?'Synced '+new Date(lastSuccessfulRefresh).toLocaleString():'Waiting for first sync';
 if(link){
  if(cfg.href){link.hidden=false;link.href=cfg.href;link.textContent=cfg.label;}
  else link.hidden=true;
 }
 fillTopbarJump();
 renderTopbarPulse();
 enhanceStatWaves();
 syncTopbarBack();
}
async function runTopbarRefresh(){
 const btn=$('topbar-refresh'),sync=$('topbar-aside-sync');
 if(btn)btn.disabled=true;
 if(sync)sync.textContent='…';
 try{
  await preservePageScroll(async()=>{
   const custom=window.cfPageRefreshers?.[page];
   if(typeof custom==='function')await custom();
   else await refresh();
   updateTopbarMeta();
  });
 }catch(e){if(sync)sync.textContent='Error';}
 finally{if(btn)btn.disabled=false;}
}
async function copyTopbarLink(){
 const btn=$('topbar-copy');
 try{
  await navigator.clipboard.writeText(location.href);
  if(btn){btn.classList.add('is-copied');btn.title='Copied';setTimeout(()=>{btn.classList.remove('is-copied');btn.title='Copy page link';},1200);}
 }catch(e){showMessage('Could not copy page link.');}
}
$('topbar-refresh')?.addEventListener('click',()=>runTopbarRefresh());
$('topbar-copy')?.addEventListener('click',()=>copyTopbarLink());
$('topbar-jump')?.addEventListener('change',e=>{const href=e.target.value;if(href&&href!==location.pathname+location.search+location.hash)location.assign(href);});
addEventListener('keydown',e=>{
 if(e.defaultPrevented||e.metaKey||e.ctrlKey||e.altKey)return;
 const tag=(e.target&&e.target.tagName||'').toLowerCase();
 if(tag==='input'||tag==='textarea'||tag==='select'||e.target?.isContentEditable)return;
 if(e.key==='r'||e.key==='R'){e.preventDefault();runTopbarRefresh();}
 if(e.key==='c'&&e.shiftKey){e.preventDefault();copyTopbarLink();}
});
window.updateTopbarMeta=updateTopbarMeta;
window.cfRefresh=()=>runTopbarRefresh();
setInterval(refresh,5000);
setInterval(()=>{if($('topbar-aside-sync')&&!document.hidden) $('topbar-aside-sync').textContent=topbarRelativeSync(lastSuccessfulRefresh);},1000);
document.addEventListener('visibilitychange',()=>{if(!document.hidden)refresh();});
updateChartRangeButtons();refresh();updateTopbarMeta();

// Keep form progress visible and preserve pre-existing disabled states.
function beginFormProgress(form,submit,label){
 const controls=Array.from(form.elements).map(el=>[el,el.disabled]);const original=submit.textContent;
 form.setAttribute('aria-busy','true');for(const [el] of controls)el.disabled=true;
 submit.replaceChildren();const spinner=document.createElement('span');spinner.className='progress-icon';spinner.setAttribute('aria-hidden','true');submit.append(spinner,document.createTextNode(label));
 return ()=>{for(const [el,disabled] of controls)el.disabled=disabled;submit.textContent=original;form.removeAttribute('aria-busy');};
}
