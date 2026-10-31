(()=>{
 const banner=$('license-warning');if(!banner)return;let busy=false;
 async function update(){if(busy||!T)return;busy=true;try{const d=await api('GET','/api/license/usage');const over=Object.entries(d.quotas).filter(([,q])=>q.limit>=0&&q.used>q.limit).map(([name])=>name);banner.hidden=d.valid&&!over.length;$('license-warning-text').textContent=!d.valid?d.status+' · allocation and resource changes are blocked.':over.length?'License quota exceeded: '+over.join(', ')+'. Delete excess resources or upgrade the license.':'';}catch(e){banner.hidden=true;}finally{busy=false;}}
 $('save').addEventListener('click',update);document.addEventListener('license-updated',update);setInterval(()=>{if(!document.hidden)update();},30000);update();
})();
