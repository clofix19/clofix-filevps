(() => {
  const PAGE_SIZE = 100;
  let auditEvents = [];
  let retentionDays = 90;
  let auditPage = 1;
  let auditPages = 0;
  let auditTotal = 0;

  function fmtAt(v) {
    try { return new Date(v).toLocaleString(); } catch (_) { return String(v || ''); }
  }

  function filterQuery() {
    const params = new URLSearchParams();
    params.set('limit', String(PAGE_SIZE));
    params.set('page', String(auditPage));
    const q = ($('audit-search')?.value || '').trim();
    const action = ($('audit-filter-action')?.value || '').trim();
    const actor = ($('audit-filter-actor')?.value || '').trim();
    const account = ($('audit-filter-account')?.value || '').trim();
    const from = ($('audit-filter-from')?.value || '').trim();
    const to = ($('audit-filter-to')?.value || '').trim();
    if (q) params.set('q', q);
    if (action) params.set('action', action);
    if (actor) params.set('actor', actor);
    if (account) params.set('account_id', account);
    if (from) params.set('from', from);
    if (to) params.set('to', to);
    return params.toString();
  }

  function openDetail(ev) {
    const dlg = $('audit-detail-dialog');
    if (!dlg || !ev) return;
    const summary = ev.summary || {};
    $('audit-detail-title').textContent = summary.did || ev.action || 'Audit event';
    $('audit-detail-summary').textContent = [
      fmtAt(ev.at || summary.when),
      summary.who || ev.actor || '—',
      summary.on || ev.target || '',
      summary.outcome || ev.result || ''
    ].filter(Boolean).join(' · ');
    let payload = ev;
    if (typeof ev.detail === 'string' && ev.detail) {
      try { payload = { ...ev, detail: JSON.parse(ev.detail) }; } catch (_) {}
    }
    $('audit-detail-json').textContent = JSON.stringify(payload, null, 2);
    if (!dlg.open) dlg.showModal();
  }

  async function openDetailByID(id) {
    const msg = $('audit-message');
    try {
      const ev = await api('GET', `/api/audit/${id}`);
      openDetail(ev);
    } catch (e) {
      if (msg) msg.textContent = e.message;
    }
  }

  function renderPager() {
    const pager = $('audit-pager');
    const label = $('audit-pager-label');
    const prev = $('audit-page-prev');
    const next = $('audit-page-next');
    if (!pager) return;
    const show = auditTotal > 0;
    pager.hidden = !show;
    if (!show) return;
    const from = auditTotal ? ((auditPage - 1) * PAGE_SIZE) + 1 : 0;
    const to = Math.min(auditPage * PAGE_SIZE, auditTotal);
    if (label) label.textContent = `Showing ${from}–${to} of ${auditTotal} · Page ${auditPage} of ${Math.max(auditPages, 1)} · ${PAGE_SIZE}/page`;
    if (prev) prev.disabled = auditPage <= 1;
    if (next) next.disabled = auditPage >= auditPages || auditPages <= 1;
  }

  function renderAuditTable() {
    const table = $('audit-table');
    if (!table) return;
    const showAccount = !!(cfMe && cfMe.kind === 'admin');
    const headers = showAccount
      ? ['When', 'Account', 'Who', 'Action', 'Target', 'Result', '']
      : ['When', 'Who', 'Action', 'Target', 'Result', ''];
    row(table, headers, true);
    if (!auditEvents.length) {
      row(table, ['No events match these filters.', ...headers.slice(1).map(() => '')]);
      renderPager();
      return;
    }
    auditEvents.forEach(e => {
      const who = document.createElement('div');
      const name = document.createElement('strong');
      name.textContent = e.actor || '-';
      who.append(name);
      if (e.actor_id) {
        const sub = document.createElement('div');
        sub.className = 'muted';
        sub.style.fontSize = '11px';
        sub.textContent = e.actor_id;
        who.append(sub);
      }
      const view = document.createElement('button');
      view.type = 'button';
      view.className = 'small-btn';
      view.textContent = 'JSON';
      view.title = 'Show full event JSON';
      view.onclick = () => openDetailByID(e.id);
      const actions = document.createElement('div');
      actions.className = 'row-actions';
      actions.append(view);
      const when = document.createElement('button');
      when.type = 'button';
      when.className = 'id-link';
      when.textContent = fmtAt(e.at);
      when.onclick = () => openDetailByID(e.id);
      const cells = showAccount
        ? [when, e.account_id || '-', who, e.action, e.target || '-', e.result || '-', actions]
        : [when, who, e.action, e.target || '-', e.result || '-', actions];
      row(table, cells);
    });
    renderPager();
  }

  async function fillAccountFilter() {
    const wrap = $('audit-filter-account-wrap');
    const sel = $('audit-filter-account');
    if (!wrap || !sel) return;
    const isAdmin = cfMe?.kind === 'admin';
    wrap.hidden = !isAdmin;
    if (!isAdmin) return;
    const current = sel.value;
    try {
      const accounts = await api('GET', '/api/accounts');
      sel.replaceChildren(new Option('All accounts', ''));
      (Array.isArray(accounts) ? accounts : []).forEach(a => {
        sel.add(new Option(`${a.name || a.id} · ${a.kind || 'account'}`, a.id));
      });
      if ([...sel.options].some(o => o.value === current)) sel.value = current;
    } catch (_) {
      /* keep existing options */
    }
  }

  function setScopeNote(meta) {
    const note = $('audit-scope-note');
    const lead = $('audit-lead');
    if (meta?.retention_days) retentionDays = Number(meta.retention_days) || retentionDays;
    if (lead) {
      if (cfMe?.kind === 'admin') {
        lead.textContent = `Platform admin · last ${retentionDays} days across all accounts. ${PAGE_SIZE} events per page. Rows older than 180 days are deleted.`;
      } else {
        lead.textContent = `Last ${retentionDays} days for this account (root + IAM). ${PAGE_SIZE} events per page. Older activity is removed automatically.`;
      }
    }
    if (!note) return;
    const since = meta?.since ? ` · since ${fmtAt(meta.since)}` : '';
    if (cfMe?.kind === 'admin') note.textContent = `Platform admin · ${retentionDays}-day retention · ${PAGE_SIZE}/page${since}`;
    else if (cfMe?.kind === 'root') note.textContent = `Account ${cfMe.account_id || ''} · ${retentionDays}-day retention · ${PAGE_SIZE}/page${since}`;
    else if (cfMe?.kind === 'iam') note.textContent = `Account ${cfMe.account_id || ''} · ${retentionDays}-day retention · ${PAGE_SIZE}/page${since}`;
    else if (cfMe) note.textContent = `Account ${cfMe.account_id || '-'} · ${retentionDays}-day retention`;
    else note.textContent = 'Sign in to load account activity.';
  }

  window.refreshAuditTrail = async function refreshAuditTrail() {
    const msg = $('audit-message');
    if (msg) msg.textContent = '';
    try {
      await ensureSession();
      await fillAccountFilter();
      const data = await api('GET', `/api/audit?${filterQuery()}`);
      auditEvents = data.events || [];
      auditPage = Number(data.page) || auditPage || 1;
      auditPages = Number(data.pages) || 0;
      auditTotal = Number(data.total) || 0;
      setScopeNote(data);
      renderAuditTable();
      if (msg) {
        msg.textContent = auditTotal
          ? `${auditTotal} event${auditTotal === 1 ? '' : 's'} · page ${auditPage}/${Math.max(auditPages, 1)} · ${PAGE_SIZE}/page · ${retentionDays}-day window.`
          : `No activity in the last ${retentionDays} days for these filters.`;
      }
    } catch (e) {
      if (msg) msg.textContent = e.message;
      if ($('audit-scope-note') && $('audit-scope-note').textContent === 'Loading account scope…') {
        $('audit-scope-note').textContent = 'Could not load account scope.';
      }
      auditEvents = [];
      auditTotal = 0;
      auditPages = 0;
      renderAuditTable();
    }
  };

  function clearFilters() {
    ['audit-search', 'audit-filter-action', 'audit-filter-actor', 'audit-filter-from', 'audit-filter-to'].forEach(id => {
      const el = $(id);
      if (el) el.value = '';
    });
    if ($('audit-filter-account')) $('audit-filter-account').value = '';
    auditPage = 1;
  }

  bind('audit-refresh', 'onclick', () => window.refreshAuditTrail());
  $('audit-filter-form')?.addEventListener('submit', e => {
    e.preventDefault();
    auditPage = 1;
    window.refreshAuditTrail();
  });
  bind('audit-filter-clear', 'onclick', () => {
    clearFilters();
    window.refreshAuditTrail();
  });
  bind('audit-page-prev', 'onclick', () => {
    if (auditPage <= 1) return;
    auditPage -= 1;
    window.refreshAuditTrail();
  });
  bind('audit-page-next', 'onclick', () => {
    if (auditPage >= auditPages) return;
    auditPage += 1;
    window.refreshAuditTrail();
  });
  bind('audit-detail-close', 'onclick', () => $('audit-detail-dialog')?.close());
  $('audit-detail-dialog')?.addEventListener('click', e => {
    if (e.target === $('audit-detail-dialog')) $('audit-detail-dialog').close();
  });
  bind('audit-detail-copy', 'onclick', async () => {
    const text = $('audit-detail-json')?.textContent || '';
    const msg = $('audit-message');
    try {
      await navigator.clipboard.writeText(text);
      if (msg) msg.textContent = 'JSON copied.';
    } catch (_) {
      if (msg) msg.textContent = 'Clipboard unavailable. Select the JSON and copy manually.';
    }
  });

  window.cfPageRefreshers = window.cfPageRefreshers || {};
  window.cfPageRefreshers.audit = () => window.refreshAuditTrail();

  if (typeof page !== 'undefined' && page === 'audit') {
    window.refreshAuditTrail();
  }
})();
