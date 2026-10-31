(()=>{
 if(page!=='settings')return;
 const panels=Array.from(document.querySelectorAll('[data-settings-panel]'));
 const links=Array.from(document.querySelectorAll('[data-settings-link]'));
 function activate(focus=false){
  if(location.hash==='#settings-access')history.replaceState(null,'','#settings-license');
  const id=location.hash.slice(1),selected=panels.find(panel=>panel.id===id)||panels[0];
  for(const panel of panels)panel.hidden=panel!==selected;
  for(const link of links){if(link.getAttribute('aria-controls')===selected.id)link.setAttribute('aria-current','location');else link.removeAttribute('aria-current');}
  if(focus)selected.querySelector('h2')?.focus({preventScroll:true});
 }
 for(const link of links)link.addEventListener('click',event=>{if(event.button!==0||event.ctrlKey||event.metaKey||event.shiftKey||event.altKey)return;event.preventDefault();if(location.hash!==link.hash)history.pushState(null,'',link.hash);activate(true);});
 addEventListener('hashchange',()=>activate());addEventListener('popstate',()=>activate());activate();
})();
