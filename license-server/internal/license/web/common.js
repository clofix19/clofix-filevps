const $=id=>document.getElementById(id);
let token=sessionStorage.getItem('clofix_license_session')||'';
let plans=[],licenses=[],customers=[];

if(!token){
  location.replace('/login');
}else if($('app')){
  $('app').hidden=false;
}

const PAGE=document.body.dataset.page||'';
for(const link of document.querySelectorAll('[data-license-nav]')){
  if(link.getAttribute('href')==='/'+PAGE)link.setAttribute('aria-current','page');
  else link.removeAttribute('aria-current');
}

function msg(text){
  const el=$('message');
  if(el)el.textContent=text;
  if($('plan-dialog')?.open)$('plan-message').textContent=text;
  if($('license-dialog')?.open)$('license-edit-message').textContent=text;
}

async function api(method,path,body){
  const res=await fetch(path,{
    method,
    headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},
    body:body?JSON.stringify(body):undefined,
    cache:'no-store'
  });
  const data=await res.json().catch(()=>({}));
  if(res.status===401){
    sessionStorage.removeItem('clofix_license_session');
    location.replace('/login');
    throw Error(data.error||'Session expired');
  }
  if(!res.ok)throw Error(data.error||res.status);
  return data;
}

function row(table,values,head=false){
  const tr=document.createElement('tr');
  for(const v of values){
    const cell=document.createElement(head?'th':'td');
    if(v instanceof Node)cell.append(v);else cell.textContent=v;
    tr.append(cell);
  }
  table.append(tr);
}

function button(text,fn,className){
  const b=document.createElement('button');
  b.type='button';
  if(className)b.className=className;
  b.textContent=text;
  b.onclick=async()=>{
    b.disabled=true;
    try{await fn();}catch(e){msg(e.message);}finally{b.disabled=false;}
  };
  return b;
}

(function footerMeta(){
  const host=$('footer-host');
  const clock=$('footer-clock');
  if(host)host.textContent=location.host||'-';
  if(!clock)return;
  const tick=()=>{clock.textContent=new Date().toLocaleString();};
  tick();
  setInterval(tick,1000);
})();

function licenseStatus(l){
  return l.deleted?'Deleted':l.revoked?'Revoked':new Date(l.expires)<=new Date()?'Expired':'Active';
}

function formatLimit(value){
  return value==null?'Not specified':value===-1?'Unlimited':value===0?'Not included':String(value);
}

function pending(form){
  const controls=Array.from(form.elements).map(c=>[c,c.disabled]);
  form.setAttribute('aria-busy','true');
  for(const [c] of controls)c.disabled=true;
  return()=>{
    for(const [c,disabled] of controls)c.disabled=disabled;
    form.removeAttribute('aria-busy');
  };
}

function renderCounts(){
  const counts={Active:0,Expired:0,Revoked:0,Deleted:0};
  for(const l of licenses)counts[licenseStatus(l)]++;
  if($('count-total'))$('count-total').textContent=licenses.length;
  if($('count-active'))$('count-active').textContent=counts.Active;
  if($('count-expired'))$('count-expired').textContent=counts.Expired;
  if($('count-revoked'))$('count-revoked').textContent=counts.Revoked;
  if($('count-deleted'))$('count-deleted').textContent=counts.Deleted;
  if($('nav-plan-count'))$('nav-plan-count').textContent=plans.length;
  if($('nav-customer-count'))$('nav-customer-count').textContent=customers.length;
  if($('nav-license-count'))$('nav-license-count').textContent=licenses.length;
}

async function loadData(){
  const [newPlans,newLicenses,newCustomers]=await Promise.all([
    api('GET','/api/plans'),
    api('GET','/api/licenses'),
    api('GET','/api/customers')
  ]);
  plans=newPlans;
  licenses=newLicenses;
  customers=newCustomers;
  renderCounts();
  if(typeof onDataReady==='function')onDataReady();
}

$('logout')?.addEventListener('click',async()=>{
  try{
    if(token)await fetch('/api/logout',{method:'POST',headers:{Authorization:'Bearer '+token},cache:'no-store'});
  }catch(_){}
  sessionStorage.removeItem('clofix_license_session');
  location.replace('/login');
});

if(token){
  loadData().catch(e=>{
    sessionStorage.removeItem('clofix_license_session');
    location.replace('/login');
  });
}
