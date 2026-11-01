/* Per-table column preferences, search, and top actions. */
(() => {
  const defaults = {
    instances:['Select','VPS','State','Worker','CPU','RAM','IN','OUT','Bandwidth','Disk','IP address','Created'],
    'home-vps-table':['Select','VPS','State','RAM','CPU'],
    'home-worker-table':['Worker','Status','Free RAM','CPU'],
    workers:['Select','Worker','Host','Status','CPU','RAM','Disk','Host swap free / total','ZRAM','GPU inventory','Memory pressure','Last heartbeat'],
    'security-instances':['Select','VPS','State','Protection Mode','OWASP CRS WAF'],
    'iam-users-table':['Select','Username','Role','Status','Groups','Policies','Created'],
    'iam-groups-table':['Select','Group','Users','Policies'],
    'iam-policies-table':['Select','Policy','Permissions'],
    's3-connections-table':['Select','Storage','Provider','Bucket','Endpoint'],
    'service-table':['Select','Service','Status','Ports','CPU','RAM (PSS estimate)','Swap','ZRAM'],
    'volume-table':['Select','Name','State','Usage / hard limit','Attached VPS'],
    'backups-table':['Select','Backup','Source VPS','Worker','Created','Archive size','Resources','Status']
  };
  const actionName = label => /^actions?$/i.test((label || '').trim());
  document.querySelectorAll('.table-wrap table[id]').forEach(table => {
    const key = 'cf_table_columns_v2:' + table.id;
    let saved = null, signature = '', controls, enhancing = false, selectedKeys = new Set(), timer = 0;
    try { const value = JSON.parse(localStorage.getItem(key)); if (Array.isArray(value)) saved = value; } catch (_) {}
    const wrap = table.closest('.table-wrap');
    const bar = document.createElement('div');
    bar.className = 'table-settings';
    bar.hidden = true;
    const tools = document.createElement('div');
    tools.className = 'table-tools';
    const search = document.createElement('input');
    search.type = 'search';
    search.className = 'table-search';
    search.placeholder = 'Search this table…';
    search.setAttribute('aria-label', 'Search this table');
    const actions = document.createElement('div');
    actions.className = 'table-actions';
    const hint = document.createElement('span');
    hint.className = 'table-actions-hint muted';
    tools.append(search, actions, hint);
    bar.append(tools);
    wrap.before(bar);

    function headerRow(){ return table.rows[0]; }
    function isHeader(row){ return row === headerRow() || !!row.querySelector('th'); }
    function rowButtons(row){
      const found = new Set();
      row.querySelectorAll('.row-actions button, td.table-actions-col button').forEach(button => found.add(button));
      const last = row.cells[row.cells.length - 1];
      last?.querySelectorAll('button').forEach(button => found.add(button));
      return Array.from(found).filter(button => !button.disabled && !button.classList.contains('id-link') && (button.textContent || '').trim());
    }
    function buttonLabel(button){ return (button.textContent || '').replace(/\s+/g, ' ').trim(); }
    function rowKey(row){
      const id = row.querySelector('.id-link')?.textContent.trim();
      if (id) return id;
      return Array.from(row.cells)
        .filter(cell => !cell.classList.contains('table-select') && !cell.classList.contains('table-actions-col'))
        .slice(0, 2).map(cell => cell.innerText.trim()).join('|');
    }
    function selectedRows(){
      return Array.from(table.rows).filter(row => !row.hidden && row.querySelector('td.table-select input:checked'));
    }
    function rememberSelection(){
      selectedKeys = new Set(selectedRows().map(rowKey).filter(Boolean));
    }
    function restoreSelection(){
      table.querySelectorAll('td.table-select input').forEach(box => {
        box.checked = selectedKeys.has(rowKey(box.closest('tr')));
      });
    }
    function ensureSelect(header){
      const actionable = Array.from(table.rows).some(row => !isHeader(row) && rowButtons(row).length);
      if (!actionable) return;
      Array.from(table.rows).forEach(row => {
        if (row.cells[0]?.classList.contains('table-select')) return;
        const cell = document.createElement(isHeader(row) ? 'th' : 'td');
        cell.className = 'table-select';
        if (isHeader(row)) {
          const all = document.createElement('input');
          all.type = 'checkbox';
          all.setAttribute('aria-label', 'Select all rows');
          cell.append(all);
        } else if (rowButtons(row).length) {
          const box = document.createElement('input');
          box.type = 'checkbox';
          box.setAttribute('aria-label', 'Select row');
          cell.append(box);
        }
        row.insertBefore(cell, row.firstChild);
      });
    }
    function hideActionCols(header){
      Array.from(header.cells).forEach((cell, index) => {
        if (!actionName(cell.textContent)) return;
        Array.from(table.rows).forEach(row => row.cells[index]?.classList.add('table-actions-col'));
      });
    }
    function filterRows(){
      const query = search.value.trim().toLowerCase();
      Array.from(table.rows).forEach(row => {
        if (isHeader(row)) return;
        row.hidden = !!(query && !row.innerText.toLowerCase().includes(query));
      });
    }
    function refreshActions(){
      const rows = selectedRows();
      const hasRowActions = Array.from(table.rows).some(row => !isHeader(row) && row.querySelector('.row-actions, td.table-actions-col button'));
      if (!hasRowActions) { if (actions.dataset.sig !== '') { actions.replaceChildren(); actions.dataset.sig = ''; } hint.textContent = ''; return; }
      if (!rows.length) { if (actions.dataset.sig !== 'none') { actions.replaceChildren(); actions.dataset.sig = 'none'; } hint.textContent = 'Select a row to apply an action'; return; }
      const seen = new Set();
      const labels = [];
      rows.forEach(row => rowButtons(row).forEach(button => {
        const label = buttonLabel(button);
        if (seen.has(label)) return;
        seen.add(label);
        labels.push(label);
      }));
      const sig = rows.length + ':' + labels.join('|');
      hint.textContent = rows.length === 1 ? '1 selected' : rows.length + ' selected';
      if (actions.dataset.sig === sig) return;
      actions.dataset.sig = sig;
      actions.replaceChildren();
      labels.forEach(label => {
        const button = rows.flatMap(row => rowButtons(row)).find(item => buttonLabel(item) === label);
        const top = document.createElement('button');
        top.type = 'button';
        top.className = button.className || 'small-btn';
        top.textContent = label;
        top.addEventListener('click', () => {
          rememberSelection();
          selectedRows().forEach(selected => {
            const match = rowButtons(selected).find(item => buttonLabel(item) === label);
            if (match) match.click();
          });
        });
        actions.append(top);
      });
    }
    function enhance(){
      const header = headerRow();
      if (!header || !header.querySelector('th') || enhancing) return;
      enhancing = true;
      try {
        ensureSelect(header);
        hideActionCols(headerRow());
        restoreSelection();
        filterRows();
        refreshActions();
      } finally { enhancing = false; }
    }

    function update() {
      const header = headerRow();
      if (!header || !header.querySelector('th')) { if (!signature) bar.hidden = true; return; }
      const labels = Array.from(header.cells, cell => cell.textContent.trim() || (cell.querySelector('input') ? 'Select' : 'Actions'));
      if (labels.filter(label => label !== 'Actions' && label !== 'Select').length < 2) { bar.hidden = true; return; }
      const visible = labels.map((label, i) => {
        if (label === 'Select') return true;
        if (actionName(label)) return false;
        return saved ? saved.includes(label) : defaults[table.id] ? defaults[table.id].includes(label) : true;
      });
      if (!visible.some(Boolean)) visible[0] = true;
      const nextSignature = JSON.stringify(labels);
      if (signature !== nextSignature) {
        signature = nextSignature;
        bar.querySelector('.column-picker')?.remove();
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
          if (actionName(label)) return;
          const item = document.createElement('label');
          const input = document.createElement('input');
          input.type = 'checkbox';
          input.disabled = label === 'Select';
          const text = document.createElement('span');
          text.textContent = label;
          item.append(input, text);
          menu.append(item);
          controls[i] = input;
          input.addEventListener('change', () => {
            saved = labels.filter((name, index) => !actionName(name) && name !== 'Select' && controls[index]?.checked);
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
      labels.forEach((_, i) => { if (controls[i]) controls[i].checked = visible[i]; });
      Array.from(table.rows).forEach(row => {
        Array.from(row.cells).forEach((cell, i) => {
          if (cell.classList.contains('table-select')) cell.hidden = false;
          else if (cell.classList.contains('table-actions-col') || actionName(labels[i])) cell.hidden = true;
          else cell.hidden = !visible[i];
        });
      });
      enhance();
    }
    search.addEventListener('input', () => { filterRows(); refreshActions(); });
    table.addEventListener('change', event => {
      if (!event.target.matches('.table-select input')) return;
      if (event.target.closest('th')) {
        const on = event.target.checked;
        table.querySelectorAll('td.table-select input').forEach(box => {
          if (!box.closest('tr').hidden) box.checked = on;
        });
      }
      rememberSelection();
      refreshActions();
    });
    new MutationObserver(() => {
      if (enhancing) return;
      clearTimeout(timer);
      timer = setTimeout(update, 50);
    }).observe(table, {childList:true});
    table.addEventListener('table-painted', () => {
      restoreSelection();
      filterRows();
      refreshActions();
    });
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
