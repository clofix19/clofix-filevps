(()=>{
 const banner=$('license-warning');if(!banner)return;let busy=false;
 function paintHomeLicense(usage,lic){
  const tile=$('home-license'),note=$('home-license-note'),card=tile&&tile.closest('.bit-tile');
  if(!tile)return;
  if(cfMe&&cfMe.kind!=='admin'){if(card)card.hidden=true;return;}
  if(card)card.hidden=false;
  const plan=lic?.grant?.plan?.name||lic?.grant?.plan?.id||'';
  if(usage?.valid){
   tile.textContent=plan||'Active';
   tile.dataset.state='ok';
   if(note)note.textContent=plan?'Plan entitlement · valid':'Controller entitlement · valid';
  }else{
   const status=usage?.status||lic?.status||'Not configured';
   tile.textContent=status.length>28?status.slice(0,28)+'…':status;
   tile.dataset.state='warn';
   if(note)note.innerHTML=lic?.configured===false||!lic?.configured
    ?'<a href="/settings#settings-license">Configure license</a>'
    :'<a href="/settings#settings-license">Validate in Settings</a>';
  }
 }
 async function update(){if(busy||!T)return;if(cfMe&&cfMe.kind!=='admin'){banner.hidden=true;paintHomeLicense(null,null);return;}busy=true;try{const [d,lic]=await Promise.all([api('GET','/api/license/usage'),api('GET','/api/license').catch(()=>null)]);const over=Object.entries(d.quotas||{}).filter(([,q])=>q.limit>=0&&q.used>q.limit).map(([name])=>name);const phase=lic?.phase||'';const offline=!!lic?.offline;banner.hidden=d.valid&&!over.length&&phase!=='expired'&&phase!=='stopped';$('license-warning-text').textContent=phase==='stopped'?'License expired past grace · services auto-stopped until duration is increased and validated.':phase==='expired'?'License expired · creating resources is blocked. Services stop automatically 7 days after expiry.':!d.valid?(d.status||'License invalid')+(offline?' (offline cached)':'')+' · allocation and resource changes are blocked.':over.length?'License quota exceeded: '+over.join(', ')+'. Delete excess resources or upgrade the license.':'';paintHomeLicense(d,lic);}catch(e){banner.hidden=true;paintHomeLicense({valid:false,status:'Unavailable'},null);}finally{busy=false;}}
 $('save')?.addEventListener('click',update);document.addEventListener('license-updated',update);setInterval(()=>{if(!document.hidden)update();},30000);update();
})();
