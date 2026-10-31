/* Per-table column preferences, independent of live data refreshes. */
(() => {
  const defaults = {
    instances:['VPS','State','Resources','IP address','Actions'],
    'home-vps-table':['VPS','State','RAM','CPU','Actions'],
    'home-worker-table':['Worker','Status','Free RAM','CPU'],
    workers:['Worker','Host','Status','Free RAM','CPU utilization','Actions'],
    'security-instances':['VPS','State','Protection Mode','OWASP CRS WAF','Action'],
    'iam-users-table':['Username','Role','Status','Actions'],
    'iam-groups-table':['Group','Users','Policies','Actions'],
    'iam-policies-table':['Policy','Permissions','Actions'],
    's3-connections-table':['Storage','Provider','Bucket','Action'],
    'service-table':['Service','Status','CPU','RAM (PSS estimate)','Actions'],
    'volume-table':['Name','State','Usage / hard limit','Attached VPS','Actions'],
    'backups-table':['Backup','Source VPS','Created','Archive size','Status','Actions']
  };
  document.querySelectorAll('.table-wrap table[id]').forEach(table => {
    const key = 'cf_table_columns_v1:' + table.id;
    let saved = null, signature = '', controls;
    try { const value = JSON.parse(localStorage.getItem(key)); if (Array.isArray(value)) saved = value; } catch (_) {}
    const wrap = table.closest('.table-wrap');
    const bar = document.createElement('div');
    bar.className = 'table-settings';
    bar.hidden = true;
    wrap.before(bar);
    function update() {
      const header = table.rows[0];
      if (!header || !header.querySelector('th')) { bar.hidden = true; return; }
      const labels = Array.from(header.cells, (cell, i) => cell.textContent.trim() || (cell.querySelector('input') ? 'Select' : 'Actions'));
      // Placeholder headers are replaced once the API returns actual columns.
      if (labels.filter(label => label !== 'Actions').length < 2) { bar.hidden = true; return; }
      const visible = labels.map((label, i) => label === 'Select' || (saved ? saved.includes(label) : defaults[table.id] ? defaults[table.id].includes(label) : true));
      if (!visible.some(Boolean)) visible[0] = true;
      const nextSignature = JSON.stringify(labels);
      if (signature !== nextSignature) {
        signature = nextSignature;
        bar.replaceChildren();
        controls = [];
        const details = document.createElement('details');
        details.className = 'column-picker';
        const summary = document.createElement('summary');
        summary.textContent = 'Columns';
        summary.setAttribute('aria-label', 'Choose visible table columns');
        const menu = document.createElement('div');
        menu.className = 'column-picker-menu';
        const title = document.createElement('strong');
        title.textContent = 'Visible columns';
        menu.append(title);
        labels.forEach((label, i) => {
          const item = document.createElement('label');
          const input = document.createElement('input');
          input.type = 'checkbox';
          input.disabled = label === 'Select';
          const text = document.createElement('span');
          text.textContent = label;
          item.append(input, text);
          menu.append(item);
          controls.push(input);
          input.addEventListener('change', () => {
            saved = labels.filter((_, index) => controls[index].checked);
            if (!saved.length) { input.checked = true; saved = [label]; }
            try { localStorage.setItem(key, JSON.stringify(saved)); } catch (_) {}
            update();
          });
        });
        const reset = document.createElement('button');
        reset.type = 'button';
        reset.className = 'small-btn';
        reset.textContent = 'Reset to default';
        reset.addEventListener('click', () => {
          saved = null;
          try { localStorage.removeItem(key); } catch (_) {}
          update();
        });
        menu.append(reset);
        details.append(summary, menu);
        bar.append(details);
      }
      bar.hidden = false;
      controls.forEach((input, i) => { input.checked = visible[i]; });
      Array.from(table.rows).forEach(row => {
        Array.from(row.cells).forEach((cell, i) => { cell.hidden = !visible[i]; });
      });
    }
    new MutationObserver(update).observe(table, {childList:true, subtree:true});
    document.addEventListener('click', event => {
      const details = bar.querySelector('details');
      if (details && !details.contains(event.target)) details.open = false;
    });
    bar.addEventListener('keydown', event => {
      if (event.key === 'Escape') {
        const details = bar.querySelector('details');
        details.open = false;
        details.querySelector('summary').focus();
      }
    });
    update();
  });
})();
