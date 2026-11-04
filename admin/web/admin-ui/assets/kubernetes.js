(()=>{
 if(page!=='kubernetes')return;
 const get=id=>document.getElementById(id),message=text=>{const el=get('cks-message');if(el)el.textContent=text;};
 let state={clusters:[],jobs:[],ready_workers:[],supported_versions:['1.31','1.32'],filevps_report:[]},catalog={load_balancers:[],volumes:[],efs:[],s3:[],vps:[]},selected=document.body.dataset.instanceId||'',watchedJob='',busy=false,editingResource=false;
 const cluster=()=>state.clusters.find(item=>item.id===selected);
 const action=(label,handler,kind='small-btn')=>{const button=document.createElement('button');button.type='button';button.className=kind;button.textContent=label;button.onclick=async()=>{button.disabled=true;try{await handler();}catch(error){message(error.message);}finally{button.disabled=false;}};return button;};
 const nsValue=()=>get('cks-ns-filter')?.value||'';
 const inNs=item=>!nsValue()||item===nsValue()||item?.namespace===nsValue();
 function activateCksResourceTab(id){
  const key=String(id||'ns');
  document.querySelectorAll('.cks-resource-tabs [role="tab"]').forEach(tab=>{
   const on=tab.getAttribute('data-cks-tab')===key;
   tab.setAttribute('aria-selected',on?'true':'false');
   tab.tabIndex=on?0:-1;
  });
  document.querySelectorAll('.cks-resource-panel').forEach(panel=>{panel.hidden=panel.getAttribute('data-cks-panel')!==key;});
 }
 function parseData(text){const data={};String(text||'').split('\n').forEach(line=>{const i=line.indexOf('=');if(i<=0)return;data[line.slice(0,i).trim()]=line.slice(i+1).trim();});return data;}
 async function removeResource(kind,namespace,name){const current=cluster();if(!current)return;watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/workloads`,{action:'delete',kind,namespace,name}));message(`Delete ${kind} queued.`);await refresh();}
 function kindValue(kind){
  const key=String(kind||'').toLowerCase().replace(/[^a-z]/g,'');
  return {persistentvolumeclaim:'pvc',persistentvolume:'pv',horizontalpodautoscaler:'hpa',configmap:'configmap',serviceaccount:'serviceaccount',networkpolicy:'networkpolicy',statefulset:'statefulset',daemonset:'daemonset',cronjob:'cronjob'}[key]||key;
 }
 function resourceActions(kind,item){
  const actions=document.createElement('div');actions.className='row-actions';
  actions.append(action('Edit',()=>openResourceForm(kind,item)));
  actions.append(action('Delete',()=>removeResource(kind,item.namespace,item.name||item)));
  return actions;
 }
 function fillNsFilter(current){
  const select=get('cks-ns-filter');if(!select)return;
  const keep=select.value,names=current.inventory?.namespaces||[];
  select.replaceChildren(new Option('All namespaces',''));
  names.forEach(name=>select.append(new Option(name,name)));
  if([...select.options].some(option=>option.value===keep))select.value=keep;
 }
 function workloadRows(table,headers,items,empty,extra){
  row(table,headers,true);
  const list=(items||[]).filter(inNs);
  if(!list.length)row(table,[empty,...headers.slice(1).map(()=>'')]);
  list.forEach(item=>row(table,extra(item)));
 }
 function watch(result){if(result?.job_id)watchedJob=result.job_id;return result;}
 function estimate(){
  const nodes=Number(get('cks-nodes').value||1),cpu=Number(get('cks-cpu').value||1),ram=Number(get('cks-ram').value||0),disk=Number(get('cks-disk').value||0);
  get('cks-estimate').textContent=`Estimated allocation: ${nodes} node${nodes===1?'':'s'} · ${cpu*nodes} vCPU · ${ram*nodes} MB RAM · ${disk*nodes} MB disk. Placement uses live worker free capacity and does not RAM-overcommit.`;
 }
 function fillWorkers(){
  const select=get('cks-worker'),version=get('cks-version'),poolWorker=get('cks-pool-worker');
  if(!select||!version)return;
  const currentWorker=select.value,currentVersion=version.value,currentPoolWorker=poolWorker?poolWorker.value:'';
  select.replaceChildren(new Option('Automatic placement',''));
  if(poolWorker)poolWorker.replaceChildren(new Option('Automatic placement',''));
  (state.ready_workers||[]).forEach(worker=>{
   const label=`${worker.hostname||worker.id} · ${Math.max(0,Number(worker.cpu_free||0)).toFixed(1)} CPU free`;
   select.append(new Option(label,worker.id));
   if(poolWorker)poolWorker.append(new Option(label,worker.id));
  });
  if([...select.options].some(option=>option.value===currentWorker))select.value=currentWorker;
  if(poolWorker&&[...poolWorker.options].some(option=>option.value===currentPoolWorker))poolWorker.value=currentPoolWorker;
  const versions=new Set(state.supported_versions||[]);
  (state.ready_workers||[]).forEach(worker=>(worker.versions||[]).forEach(item=>versions.add(item)));
  version.replaceChildren();
  [...versions].forEach(item=>version.append(new Option(item,item)));
  if([...version.options].some(option=>option.value===currentVersion))version.value=currentVersion;
  estimate();
 }
 function selectCluster(id){
  selected=id||'';
  if(selected){
   history.replaceState({},'',`/kubernetes/${selected}`);
   document.body.dataset.instanceId=selected;
   if(typeof window.syncTopbarBack==='function')window.syncTopbarBack();
   else if(typeof window.updateTopbarMeta==='function')window.updateTopbarMeta();
  }
 }
 function openClusterResources(id){
  selectCluster(id);
  location.hash='#cks-workloads';
  if(typeof window.applySettingsNav==='function')window.applySettingsNav();
  render();
 }
 async function downloadKubeconfig(){
  const current=cluster();if(!current){message('Select a cluster first.');return;}
  const response=await fetch(`/api/v1/kubernetes/clusters/${current.id}/kubeconfig`,{credentials:'include',cache:'no-store'});
  if(!response.ok){let problem={};try{problem=await response.json();}catch(e){}message(problem.error||'Kubeconfig is not available');return;}
  const blob=await response.blob();const url=URL.createObjectURL(blob);const link=document.createElement('a');link.href=url;link.download=current.id+'.kubeconfig';link.click();URL.revokeObjectURL(url);
  message('Kubeconfig download started.');
 }
 function render(){
  if(!selected&&(state.clusters||[]).length)selected=state.clusters[0].id;
  const running=state.clusters.filter(item=>item.state==='RUNNING').length,activeJobs=(state.jobs||[]).filter(job=>!['SUCCEEDED','FAILED','CANCELLED'].includes(job.state));
  if(get('cks-cluster-count'))get('cks-cluster-count').textContent=state.clusters.length;
  get('cks-ready-count').textContent=running;
  get('cks-worker-count').textContent=(state.ready_workers||[]).length;
  get('cks-job-count').textContent=activeJobs.length;
  get('cks-cluster-note').textContent=state.clusters.length?'Tenant-scoped clusters':'No clusters yet';
  get('cks-ready-note').textContent=running?'Control plane reporting READY':'Waiting for kubeadm init';
  const lab=(state.ready_workers||[]).some(worker=>worker.lab_mode);
  get('cks-worker-note').textContent=(state.ready_workers||[]).length?(lab?'Ready · local lab executor':'Ready · kubeadm + containerd'):'Waiting for a CKS worker';
  get('cks-job-note').textContent=activeJobs.length?`${activeJobs[0].action} · ${activeJobs[0].state}`:'No recent jobs';
  const readiness=get('cks-readiness'),create=get('cks-create');
  if(!(state.ready_workers||[]).length){
    readiness.hidden=false;
    readiness.textContent='No live CKS worker heartbeat. Ensure the worker can resolve admin (Docker DNS), then restart worker. Lab mode: CLOFIX_KUBERNETES_FAKE=1.';
  }else{
    readiness.hidden=true;
    readiness.textContent='';
  }
  const compat=get('cks-compat-note');
  if(compat){
   const gate=state.production_gate||{};
   const workers=(state.ready_workers||[]).length;
   compat.textContent=workers?(gate.reason||(state.production_ready?'Production gate: ready.':'Lab CKS ready — manage clusters below.')):'Clusters stay listed from inventory; create/scale need a live CKS worker.';
  }
  create.disabled=!(state.ready_workers||[]).length;create.title=create.disabled?'A CKS-ready worker is required.':'';
  const table=get('cks-clusters-table');row(table,['Name','Version','Mode','Nodes','Status','Actions'],true);
  if(!state.clusters.length)row(table,['No clusters yet. Use Create cluster.','','','','','']);
  state.clusters.forEach(item=>{
   const actions=document.createElement('div');actions.className='row-actions';
   actions.append(action('Open',()=>{selectCluster(item.id);render();}));
   actions.append(action('Resources',()=>openClusterResources(item.id)));
   actions.append(action('Kubeconfig',async()=>{selectCluster(item.id);await downloadKubeconfig();}));
   actions.append(action('Edit',()=>openEditCluster(item)));
   row(table,[item.name,item.version,item.infrastructure_mode,(item.nodes||[]).length||item.node_pool?.count||0,badge(item.state),actions]);
  });
  const current=cluster();
  get('cks-cluster-panel').hidden=!current;
  if(current){
   get('cks-cluster-title').textContent=current.name;
   get('cks-cluster-meta').textContent=`${current.id} · control plane ${current.control_plane_worker_id||'—'}`;
   get('cks-metric-version').textContent=current.version||'—';
   get('cks-metric-mode').textContent=current.infrastructure_mode||'—';
   get('cks-metric-api').textContent=current.api_endpoint||'Private worker API (not published)';
   const liveNodes=(current.nodes||[]).filter(node=>node.state!=='DELETED');
   get('cks-metric-nodes').textContent=String(liveNodes.length)+(current.ha?' · HA':'');
   get('cks-toggle-protection').textContent=current.delete_protection?'Disable delete protection':'Enable delete protection';
   const nodes=get('cks-nodes-table');row(nodes,['Role','Worker','CPU','RAM','Disk','State','Actions'],true);
   liveNodes.forEach(node=>{
    const actions=document.createElement('div');actions.className='row-actions';
    actions.append(action('Update',()=>openNodeForm(node)));
    row(nodes,[node.role,node.hostname||node.worker_id,node.cpu||current.node_pool?.cpu||'—',node.ram_mb||current.node_pool?.ram_mb||'—',node.disk_mb||current.node_pool?.disk_mb||'—',badge(node.state),actions]);
   });
   fillNsFilter(current);
   const nsTable=get('cks-ns-table');row(nsTable,['Name','Actions'],true);
   const nsList=current.inventory?.namespaces||[];
   if(!nsList.length)row(nsTable,['No namespaces.','']);
   nsList.forEach(name=>{
    const actions=document.createElement('div');actions.className='row-actions';
    if(name!=='default'&&name!=='kube-system')actions.append(action('Delete',()=>removeResource('Namespace','',name)));
    row(nsTable,[name,actions]);
   });
   const pods=get('cks-pods-table');row(pods,['Namespace','Name','Phase','Restarts','Actions'],true);
   const podList=(current.inventory?.pods||[]).filter(inNs);
   if(!podList.length)row(pods,['No pods reported yet.','','','','']);
   podList.forEach(pod=>{
    const actions=document.createElement('div');actions.className='row-actions';
    actions.append(action('Logs',async()=>{activateCksResourceTab('logs');watch(await api('GET',`/api/v1/kubernetes/clusters/${current.id}/pods/${encodeURIComponent(pod.namespace)}/${encodeURIComponent(pod.name)}/logs`));message('Log request queued.');}));
    actions.append(action('Delete',()=>removeResource('Pod',pod.namespace,pod.name)));
    row(pods,[pod.namespace,pod.name,pod.phase,pod.restarts||0,actions]);
   });
   const deploys=get('cks-deploy-table');row(deploys,['Namespace','Name','Ready','Actions'],true);
   const deployList=(current.inventory?.deployments||[]).filter(inNs);
   if(!deployList.length)row(deploys,['No deployments.','','','']);
   deployList.forEach(item=>{
    const actions=resourceActions('Deployment',item);
    actions.prepend(action('Restart',async()=>{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/workloads`,{action:'restart',kind:item.kind||'Deployment',namespace:item.namespace,name:item.name}));message('Restart queued.');}));
    actions.prepend(action('Scale',async()=>{const replicas=Number(prompt('Replicas',item.replicas||1));if(!Number.isFinite(replicas))return;watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/workloads`,{action:'scale',kind:'Deployment',namespace:item.namespace,name:item.name,replicas}));message('Scale queued.');}));
    row(deploys,[item.namespace,item.name,`${item.ready||0}/${item.replicas||0}`,actions]);
   });
   const sts=get('cks-sts-table');row(sts,['Namespace','Name','Ready','Actions'],true);
   const stsList=(current.inventory?.stateful_sets||[]).filter(inNs);
   if(!stsList.length)row(sts,['No StatefulSets.','','','']);
   stsList.forEach(item=>{
    const actions=resourceActions('StatefulSet',item);
    actions.prepend(action('Restart',async()=>{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/workloads`,{action:'restart',kind:'StatefulSet',namespace:item.namespace,name:item.name}));message('StatefulSet restart queued.');}));
    actions.prepend(action('Scale',async()=>{const replicas=Number(prompt('Replicas',item.replicas||1));if(!Number.isFinite(replicas))return;watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/workloads`,{action:'scale',kind:'StatefulSet',namespace:item.namespace,name:item.name,replicas}));message('StatefulSet scale queued.');}));
    row(sts,[item.namespace,item.name,`${item.ready||0}/${item.replicas||0}`,actions]);
   });
   if(get('cks-rs-table'))workloadRows(get('cks-rs-table'),['Namespace','Name','Ready','Actions'],current.inventory?.replica_sets,'No ReplicaSets.',item=>{
    const actions=document.createElement('div');actions.className='row-actions';
    actions.append(action('Delete',()=>removeResource('ReplicaSet',item.namespace,item.name)));
    return [item.namespace,item.name,`${item.ready||0}/${item.replicas||0}`,actions];
   });
   workloadRows(get('cks-ds-table'),['Namespace','Name','Actions'],current.inventory?.daemon_sets,'No DaemonSets.',item=>[item.namespace,item.name,resourceActions('DaemonSet',item)]);
   const svcs=get('cks-svc-table');row(svcs,['Namespace','Name','URL','Actions'],true);
   const svcList=(current.inventory?.services||[]).filter(inNs);
   if(!svcList.length)row(svcs,['No services.','','','']);
   svcList.forEach(item=>row(svcs,[item.namespace,item.name,item.url||'',resourceActions(item.kind||'Service',item)]));
   workloadRows(get('cks-ing-table'),['Namespace','Name','URL','Actions'],current.inventory?.ingresses,'No ingress.',item=>[item.namespace,item.name,item.url||'',resourceActions(item.kind||'Ingress',item)]);
   workloadRows(get('cks-secret-table'),['Namespace','Name','Actions'],current.inventory?.secrets,'No secrets.',item=>[item.namespace,item.name,resourceActions('Secret',item)]);
   workloadRows(get('cks-cm-table'),['Namespace','Name','Actions'],current.inventory?.config_maps,'No config maps.',item=>[item.namespace,item.name,resourceActions('ConfigMap',item)]);
   workloadRows(get('cks-pv-table'),['Name','Storage','Actions'],current.inventory?.persistent_volumes,'No PersistentVolumes.',item=>[item.name,item.storage||'—',resourceActions('PersistentVolume',item)]);
   workloadRows(get('cks-pvc-table'),['Namespace','Name','Storage','Actions'],current.inventory?.persistent_volume_claims,'No PersistentVolumeClaims.',item=>[item.namespace,item.name,item.storage||'—',resourceActions('PersistentVolumeClaim',item)]);
   const jobItems=[...(current.inventory?.jobs||[]),...(current.inventory?.cron_jobs||[])];
   workloadRows(get('cks-k8sjob-table'),['Kind','Namespace','Name','Actions'],jobItems,'No jobs.',item=>[item.kind||'Job',item.namespace,item.name,resourceActions(item.kind||'Job',item)]);
   workloadRows(get('cks-hpa-table'),['Namespace','Name','Actions'],current.inventory?.hpa,'No HPA.',item=>[item.namespace,item.name,resourceActions(item.kind||'HorizontalPodAutoscaler',item)]);
   workloadRows(get('cks-netpol-table'),['Namespace','Name','Actions'],current.inventory?.network_policies,'No network policies.',item=>[item.namespace,item.name,resourceActions('NetworkPolicy',item)]);
   workloadRows(get('cks-sa-table'),['Namespace','Name','Actions'],current.inventory?.service_accounts,'No service accounts.',item=>[item.namespace,item.name,resourceActions('ServiceAccount',item)]);
   const attached=get('cks-integrate-table');row(attached,['Kind','CloFix resource','Kubernetes','Namespace','Status','URL'],true);
   const integrations=current.integrations||[];
   if(!integrations.length)row(attached,['No CloFix LB, VPS, volume, EFS or S3 attachments yet.','','','','','']);
   integrations.forEach(item=>row(attached,[item.kind,item.name||item.resource_id,item.k8s_kind+'/'+item.k8s_name,item.namespace||'default',item.status||'—',item.url||'']));
   get('cks-logs').textContent=current.inventory?.logs||'Select a pod to request logs.';
   const pools=get('cks-pools-table');row(pools,['Name','Kind','Count','CPU','RAM','Disk','Actions'],true);
   const poolList=current.node_pools&&current.node_pools.length?current.node_pools:[current.node_pool].filter(Boolean);
   if(!poolList.length)row(pools,['No VPS groups or node pools.','','','','','','']);
   poolList.forEach(pool=>{
    const actions=document.createElement('div');actions.className='row-actions';
    actions.append(action('Scale',async()=>{const count=Number(prompt('Desired VPS count',pool.count||1));if(!Number.isFinite(count))return;watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/node-pools/${encodeURIComponent(pool.id)}/scale`,{count}));message('VPS group scale queued.');await refresh();}));
    if(pool.name!=='default')actions.append(action('Delete',async()=>{if(prompt('Type DELETE to remove this VPS group')!=='DELETE')return;watch(await api('DELETE',`/api/v1/kubernetes/clusters/${current.id}/node-pools/${encodeURIComponent(pool.id)}`));message('VPS group deletion queued.');await refresh();}));
    const countLabel=pool.autoscaling?`${pool.count} (auto ${pool.min||0}-${pool.max||pool.count})`:pool.count;
   row(pools,[pool.name,pool.kind==='vps-group'?'VPS group':'Node pool',countLabel,pool.cpu,pool.ram_mb,pool.disk_mb,actions]);
   });
   const helm=get('cks-helm-table');row(helm,['Release','Namespace','Chart','Status'],true);
   const helmList=current.helm||current.inventory?.helm||[];
   if(!helmList.length)row(helm,['No Helm releases.','','','']);
   helmList.forEach(item=>row(helm,[item.name,item.namespace||'default',item.chart||'—',item.status||'—']));
   const addons=get('cks-addon-table');row(addons,['Kind','Name','Namespace'],true);
   const addonItems=[...(current.inventory?.volumes||[]),...(current.inventory?.storage_classes||[]).map(item=>({...item,kind:item.kind||'StorageClass'})),...(current.inventory?.ingresses||[]),...(current.inventory?.rbac||[]),...(current.inventory?.hpa||[])];
   if(!addonItems.length)row(addons,['No storage, ingress, RBAC or HPA objects yet.','','']);
   addonItems.forEach(item=>row(addons,[item.kind||'—',item.name,item.namespace||'default']));
   const backups=get('cks-backup-table');row(backups,['Backup','State','Size','Digest'],true);
   const backupList=current.backups||[];
   if(!backupList.length)row(backups,['No etcd backups.','','','']);
   backupList.forEach(item=>row(backups,[item.id,badge(item.state),item.size_bytes||0,item.digest||'—']));
   const events=get('cks-events-table');row(events,['When','Level','Message'],true);
   const eventList=current.events||[];
   if(!eventList.length)row(events,['No cluster events.','','']);
   eventList.slice().reverse().forEach(event=>row(events,[event.at?new Date(event.at).toLocaleString():'—',event.level||'info',event.message||'']));
  }
  const jobs=get('cks-jobs-table');row(jobs,['Action','Cluster','State','Error'],true);
  const list=(state.jobs||[]).filter(job=>!selected||job.cluster_id===selected);
  if(!list.length)row(jobs,['No jobs.','','','']);
  list.slice(0,50).forEach(job=>row(jobs,[job.action,job.cluster_id,badge(job.state),job.error||'']));
  if(watchedJob){
   const job=(state.jobs||[]).find(item=>item.id===watchedJob);
   if(job){
    if(job.state==='SUCCEEDED'){message(`Completed · ${job.action}`);watchedJob='';}
    else if(['FAILED','CANCELLED'].includes(job.state)){message(`${job.action} ${job.state.toLowerCase()} · ${job.error||''}`);watchedJob='';}
    else message(`${job.action} · ${job.state.toLowerCase()}`);
   }
  }
  fillWorkers();
 }
 async function refresh(){
  const run=async()=>{
   if(busy)return;busy=true;
   try{
    state=await api('GET','/api/v1/kubernetes');
    if((!selected||!cluster())&&state.clusters?.length)selected=state.clusters[0].id;
    if(selected){try{catalog=await api('GET',`/api/v1/kubernetes/clusters/${selected}/catalog`);}catch(e){catalog={load_balancers:[],volumes:[],efs:[],s3:[],vps:[]};}}
    const gate=get('cks-gate-note');
    if(gate&&state.production_gate?.reason)gate.textContent=state.production_gate.reason;
    render();
   }catch(error){message(error.message);}
   finally{busy=false;}
  };
  return typeof preservePageScroll==='function'?preservePageScroll(run):run();
 }
 try{
 get('cks-create').onclick=()=>{fillWorkers();get('cks-create-dialog').showModal();};
 get('cks-refresh').onclick=refresh;
 ['cks-nodes','cks-cpu','cks-ram','cks-disk'].forEach(id=>get(id)?.addEventListener('input',estimate));
 get('cks-create-form').onsubmit=async event=>{
  event.preventDefault();
  try{
   const created=await api('POST','/api/v1/kubernetes/clusters',{
    name:get('cks-name').value.trim(),version:get('cks-version').value,infrastructure_mode:get('cks-mode').value,
    worker_id:get('cks-worker').value,node_count:Number(get('cks-nodes').value),control_plane_count:Number(get('cks-ha').value),
    ha:Number(get('cks-ha').value)>1,cpu:Number(get('cks-cpu').value),
    ram_mb:Number(get('cks-ram').value),disk_mb:Number(get('cks-disk').value)
   });
   watch(created);selected=created.cluster?.id||selected;get('cks-create-dialog').close();message('Cluster provisioning queued.');await refresh();
  }catch(error){message(error.data?.error||error.message);}
 };
 get('cks-apply-form').onsubmit=async event=>{
  event.preventDefault();
  const current=cluster();if(!current){message('Select a cluster first.');return;}
  try{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/workloads`,{yaml:get('cks-yaml').value}));message('Apply queued.');await refresh();}
  catch(error){message(error.message);}
 };
 get('cks-delete').onclick=async()=>{
  const current=cluster();if(!current)return;
  if(prompt('Type DELETE to remove this cluster')!=='DELETE')return;
  try{watch(await api('DELETE',`/api/v1/kubernetes/clusters/${current.id}`));message('Deletion queued.');await refresh();}catch(error){message(error.message);}
 };
 get('cks-toggle-protection').onclick=async()=>{
  const current=cluster();if(!current)return;
  try{await api('POST',`/api/v1/kubernetes/clusters/${current.id}/protection`,{delete_protection:!current.delete_protection});await refresh();}catch(error){message(error.message);}
 };
 get('cks-download-kubeconfig').onclick=()=>downloadKubeconfig();
 function openPoolForm(kind){
  const current=cluster();if(!current){message('Select a cluster first.');return;}
  fillWorkers();
  const vps=kind==='vps-group';
  get('cks-pool-kind').value=vps?'vps-group':'';
  get('cks-pool-title').textContent=vps?'Add VPS group':'Add node pool';
  get('cks-pool-save').textContent=vps?'Create VPS group':'Create pool';
  get('cks-pool-hint').textContent=vps?'Each VPS in the group is a CKS node. Pods and Services schedule onto these nodes. FileVPS guests cannot run kubelet.':'Adds worker nodes on dedicated CKS workers. FileVPS guests cannot run kubelet.';
  get('cks-pool-dialog').showModal();
 }
 function openEditCluster(item){
  const current=item||cluster();if(!current){message('Select a cluster first.');return;}
  selected=current.id;
  get('cks-edit-name').value=current.name||'';
  get('cks-edit-protection').checked=!!current.delete_protection;
  get('cks-edit-dialog').showModal();
 }
 function openNodeForm(node){
  const current=cluster();if(!current||!node){message('Select a cluster first.');return;}
  get('cks-node-id').value=node.id;
  get('cks-node-meta').textContent=`${node.role} · ${node.hostname||node.worker_id}`;
  get('cks-node-cpu').value=node.cpu||current.node_pool?.cpu||1;
  get('cks-node-ram').value=node.ram_mb||current.node_pool?.ram_mb||2048;
  get('cks-node-disk').value=node.disk_mb||current.node_pool?.disk_mb||10240;
  get('cks-node-dialog').showModal();
 }
 get('cks-edit').onclick=()=>openEditCluster();
 get('cks-edit-form').onsubmit=async event=>{
  event.preventDefault();
  const current=cluster();if(!current)return;
  try{await api('PUT',`/api/v1/kubernetes/clusters/${current.id}`,{name:get('cks-edit-name').value.trim(),delete_protection:get('cks-edit-protection').checked});get('cks-edit-dialog').close();message('Cluster updated.');await refresh();}
  catch(error){message(error.message);}
 };
 get('cks-node-form').onsubmit=async event=>{
  event.preventDefault();
  const current=cluster();if(!current)return;
  try{watch(await api('PUT',`/api/v1/kubernetes/clusters/${current.id}/nodes/${encodeURIComponent(get('cks-node-id').value)}`,{cpu:Number(get('cks-node-cpu').value),ram_mb:Number(get('cks-node-ram').value),disk_mb:Number(get('cks-node-disk').value)}));get('cks-node-dialog').close();message('Node update queued.');await refresh();}
  catch(error){message(error.message);}
 };
 get('cks-vps-group-add').onclick=()=>openPoolForm('vps-group');
 get('cks-pool-add').onclick=()=>openPoolForm('');
 get('cks-pool-form').onsubmit=async event=>{
  event.preventDefault();
  const current=cluster();if(!current)return;
  const kind=get('cks-pool-kind').value;
  try{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/node-pools`,{name:get('cks-pool-name').value.trim(),kind,count:Number(get('cks-pool-count').value),cpu:Number(get('cks-pool-cpu').value),ram_mb:Number(get('cks-pool-ram').value),disk_mb:Number(get('cks-pool-disk').value),worker_id:get('cks-pool-worker').value,autoscaling:!!get('cks-pool-autoscaling')?.checked,min:Number(get('cks-pool-min')?.value||1),max:Number(get('cks-pool-max')?.value||3)}));get('cks-pool-dialog').close();message(kind==='vps-group'?'VPS group queued.':'Node pool queued.');await refresh();}
  catch(error){message(error.message);}
 };
 get('cks-helm-form').onsubmit=async event=>{
  event.preventDefault();
  const current=cluster();if(!current){message('Select a cluster first.');return;}
  try{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/helm`,{release:get('cks-helm-release').value.trim(),chart:get('cks-helm-chart').value.trim(),namespace:get('cks-helm-ns').value.trim(),values:get('cks-helm-values').value,action:get('cks-helm-action').value}));message('Helm job queued.');await refresh();}
  catch(error){message(error.message);}
 };
 get('cks-storage-install').onclick=async()=>{const current=cluster();if(!current)return;try{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/storage`,{install:true}));message('Storage class install queued.');await refresh();}catch(error){message(error.message);}};
 get('cks-ingress-install').onclick=async()=>{const current=cluster();if(!current)return;try{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/ingress`,{install:true}));message('Ingress class install queued.');await refresh();}catch(error){message(error.message);}};
 get('cks-addon-form').onsubmit=async event=>{
  event.preventDefault();
  const current=cluster();if(!current){message('Select a cluster first.');return;}
  const kind=get('cks-addon-kind').value,yaml=get('cks-addon-yaml').value,path={storage:'storage',ingress:'ingress',rbac:'rbac',hpa:'autoscaling'}[kind];
  try{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/${path}`,{yaml}));message(kind+' apply queued.');await refresh();}
  catch(error){message(error.message);}
 };
 get('cks-backup').onclick=async()=>{const current=cluster();if(!current)return;try{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/backups`,{}));message('Backup queued.');await refresh();}catch(error){message(error.message);}};
 get('cks-restore-form').onsubmit=async event=>{
  event.preventDefault();
  const current=cluster();if(!current)return;
  try{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/restore`,{backup_id:get('cks-restore-id').value.trim()}));message('Restore queued.');await refresh();}
  catch(error){message(error.message);}
 };
 get('cks-upgrade').onclick=async()=>{
  const current=cluster();if(!current)return;
  const version=prompt('Target Kubernetes version',current.version||'1.32');if(!version)return;
  try{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/upgrade`,{version}));message('Upgrade queued.');await refresh();}
  catch(error){message(error.message);}
 };
 function fillAttachOptions(){
  const kind=get('cks-attach-kind').value,select=get('cks-attach-id');
  const key={lb:'load_balancers',vps:'vps',volume:'volumes',efs:'efs',s3:'s3'}[kind]||kind;
  const items=catalog[key]||[];
  select.replaceChildren();
  if(!items.length)select.append(new Option('No '+kind+' in this account',''));
  items.forEach(item=>select.append(new Option(item.name||item.bucket||item.id,item.id)));
 }
 get('cks-ns-filter')?.addEventListener('change',render);
 function syncResourceForm(){
  const kind=get('cks-res-kind').value;
  get('cks-resource-dialog').querySelectorAll('[data-cks-kind]').forEach(group=>{
   const visible=group.dataset.cksKind.split(',').includes(kind);
   group.hidden=!visible;
   group.querySelectorAll('input,select,textarea').forEach(control=>{if(control.id!=='cks-res-kind'&&control.id!=='cks-res-name')control.disabled=!visible;});
  });
  const selector=get('cks-res-selector-label');
  if(selector)selector.firstChild.textContent=kind==='ingress'?'Backend service':kind==='hpa'?'Target deployment':kind==='service'?'Selector (app label)':'App label';
  const data=get('cks-res-data-label');
  if(data)data.firstChild.textContent=kind==='secret'?'Secret data (KEY=value per line)':'Config data (KEY=value per line)';
  const hints={
   namespace:'Creates a Kubernetes namespace.',
   deployment:'Runs a container replica set. Image, replicas and port apply to the pod template.',
   statefulset:'Ordered pods with stable names. Replicas, image and port apply.',
   daemonset:'One pod per node.',
   service:'Exposes pods that match the app label.',
   secret:'Stores KEY=value pairs. Inventory shows the name only. On edit, leave data blank to keep keys or enter KEY=value to set them.',
   configmap:'Stores KEY=value configuration for pods. On edit, leave blank to keep keys.',
   pv:'Cluster-scoped volume. Bind a matching PVC in a namespace.',
   pvc:'Namespace claim against a PersistentVolume or StorageClass.',
   ingress:'HTTP route to a backend Service name.',
   job:'Runs a one-shot container and exits.',
   cronjob:'Runs the container on a cron schedule.',
   hpa:'Scales a Deployment. Replicas is the max count.',
   networkpolicy:'Allows Ingress to pods with the app label.',
   serviceaccount:'Creates a ServiceAccount in the selected namespace.'
  };
  get('cks-res-hint').textContent=hints[kind]||'Choose a kind to see the matching fields.';
 }
 function resetResourceForm(){
  editingResource=false;
  get('cks-res-title').textContent='Create Kubernetes resource';
  get('cks-res-save').textContent='Create';
  get('cks-res-kind').disabled=false;
  get('cks-res-name').readOnly=false;
  get('cks-res-ns').readOnly=false;
  get('cks-res-kind').value='deployment';
  get('cks-res-name').value='';
  get('cks-res-ns').value='default';
  get('cks-res-replicas').value='1';
  get('cks-res-image').value='nginx:1.27-alpine';
  get('cks-res-port').value='80';
  get('cks-res-type').value='ClusterIP';
  get('cks-res-selector').value='';
  get('cks-res-storage').value='1Gi';
  get('cks-res-schedule').value='0 * * * *';
  get('cks-res-data').value='';
  syncResourceForm();
 }
 function openResourceForm(kind,item){
  if(!cluster()){message('Select a cluster first.');return;}
  resetResourceForm();
  if(item){
   editingResource=true;
   const value=kindValue(kind||item.kind);
   get('cks-res-kind').value=value;
   get('cks-res-kind').disabled=true;
   get('cks-res-name').value=item.name||'';
   get('cks-res-name').readOnly=true;
   get('cks-res-ns').value=item.namespace||'default';
   get('cks-res-ns').readOnly=true;
   get('cks-res-replicas').value=item.replicas||1;
   get('cks-res-image').value=item.image||'nginx:1.27-alpine';
   get('cks-res-port').value=item.port||80;
   get('cks-res-type').value=item.type||'ClusterIP';
   get('cks-res-selector').value=item.selector||item.name||'';
   get('cks-res-storage').value=item.storage||'1Gi';
   get('cks-res-schedule').value=item.schedule||'0 * * * *';
   get('cks-res-title').textContent='Edit '+(item.kind||kind);
   get('cks-res-save').textContent='Save';
   syncResourceForm();
  }
  get('cks-resource-dialog').showModal();
 }
 get('cks-res-kind').onchange=syncResourceForm;
 get('cks-resource-add').onclick=()=>openResourceForm();
 get('cks-resource-dialog')?.addEventListener('close',resetResourceForm);
 get('cks-resource-form').onsubmit=async event=>{
  event.preventDefault();
  const current=cluster();if(!current)return;
  const kind=get('cks-res-kind').value,data=parseData(get('cks-res-data').value);
  if(!editingResource&&(kind==='secret'||kind==='configmap')&&!Object.keys(data).length){message('Enter KEY=value data.');return;}
  const payload={kind,name:get('cks-res-name').value.trim(),namespace:get('cks-res-ns').value.trim()||'default',replicas:Number(get('cks-res-replicas').value||1),image:get('cks-res-image').value.trim(),port:Number(get('cks-res-port').value||80),type:get('cks-res-type').value,selector:get('cks-res-selector').value.trim(),storage:get('cks-res-storage').value.trim(),schedule:get('cks-res-schedule').value.trim(),data};
  const saved=editingResource;
  try{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/workloads`,payload));get('cks-resource-dialog').close();message((saved?'Saved ':'Created ')+kind+'.');await refresh();}
  catch(error){message(error.message);}
 };
 get('cks-attach').onclick=()=>{if(!cluster()){message('Select a cluster first.');return;}fillAttachOptions();get('cks-attach-dialog').showModal();};
 get('cks-attach-kind').onchange=fillAttachOptions;
 get('cks-attach-form').onsubmit=async event=>{
  event.preventDefault();
  const current=cluster();if(!current)return;
  try{watch(await api('POST',`/api/v1/kubernetes/clusters/${current.id}/integrations`,{kind:get('cks-attach-kind').value,resource_id:get('cks-attach-id').value,name:get('cks-attach-name').value.trim(),namespace:get('cks-attach-ns').value.trim()||'default',selector:get('cks-attach-selector').value.trim()}));get('cks-attach-dialog').close();message('CloFix attach queued.');await refresh();}
  catch(error){message(error.message);}
 };
 document.querySelectorAll('.dialog-close').forEach(button=>button.onclick=()=>button.closest('dialog').close());
 document.querySelector('.cks-resource-tabs')?.addEventListener('click',event=>{
  const tab=event.target.closest('[data-cks-tab]');
  if(!tab||!event.currentTarget.contains(tab))return;
  activateCksResourceTab(tab.getAttribute('data-cks-tab'));
 });
 activateCksResourceTab(document.querySelector('.cks-resource-tabs [aria-selected="true"]')?.getAttribute('data-cks-tab')||'ns');
 }catch(e){message(e.message||String(e));}
 refresh();setInterval(()=>{if(!document.hidden)refresh();},5000);
})();
