(() => {
  if (document.body.dataset.page !== 'users') return;

  let accountsCache = [];
  let meCache = null;

  function setCount(id, value, noteId, note) {
    if ($(id)) $(id).textContent = String(value);
    if (noteId && $(noteId) && note != null) $(noteId).textContent = note;
  }

  function renderStats(accounts) {
    const list = Array.isArray(accounts) ? accounts : [];
    const partners = list.filter((a) => a.kind === 'partner').length;
    const clients = list.filter((a) => a.kind === 'client').length;
    const admins = list.filter((a) => a.kind === 'admin').length;
    const suspended = list.filter((a) => a.status === 'suspended').length;
    setCount('users-partner-count', partners, 'users-partner-note', partners === 1 ? '1 partner account' : partners + ' partner accounts');
    setCount('users-client-count', clients, 'users-client-note', clients === 1 ? '1 root client' : clients + ' root clients');
    setCount('users-admin-count', admins, 'users-admin-note', admins === 1 ? '1 platform admin' : admins + ' platform admins');
    setCount('users-count', list.length, 'users-count-note', suspended ? `${suspended} suspended` : 'Visible to this login');
  }

  function canManage(acc) {
    if (!meCache || !acc) return false;
    if (acc.kind === 'admin') return false;
    if (meCache.kind === 'admin') return true;
    if (meCache.kind === 'partner') return acc.kind === 'client' && acc.parent_id === meCache.account_id;
    return false;
  }

  function canAdminSuspend(acc) {
    return meCache?.kind === 'admin' && acc && acc.kind !== 'admin' && acc.id !== meCache.account_id;
  }

  function openEdit(acc) {
    if (!canManage(acc)) return;
    $('users-edit-id').value = acc.id;
    $('users-edit-name').value = acc.name || '';
    $('users-edit-email').value = acc.email || '';
    $('users-edit-password').value = '';
    $('users-edit-meta').textContent = `${acc.kind}${acc.user_kind ? ' · ' + acc.user_kind : ''} · ${acc.status || 'active'} · ${acc.id}`;
    $('users-edit-message').textContent = '';
    $('users-edit-dialog')?.showModal();
  }

  async function deleteAccount(acc) {
    if (!canManage(acc)) return;
    if (acc.id === meCache?.account_id) {
      $('users-message').textContent = 'Cannot delete your own account.';
      return;
    }
    if (!window.confirm(`Delete account “${acc.name}” (${acc.id})? This removes its login users. Type-confirm next.`)) return;
    if (await window.cfPrompt('Type DELETE to remove this account', '', { title: 'Delete account', label: 'Confirmation', confirm: 'Delete', hint: 'Type DELETE exactly.' }) !== 'DELETE') return;
    $('users-message').textContent = 'Deleting…';
    try {
      await api('DELETE', `/api/accounts/${encodeURIComponent(acc.id)}`);
      $('users-message').textContent = `Deleted ${acc.name}.`;
      await refreshUsers();
    } catch (e) {
      $('users-message').textContent = e.message;
    }
  }

  async function suspendAccount(acc) {
    if (!canAdminSuspend(acc)) return;
    if (!window.confirm(`Suspend “${acc.name}”? Root and all IAM users under this account will be logged out and cannot sign in.`)) return;
    $('users-message').textContent = 'Suspending…';
    try {
      await api('POST', `/api/accounts/${encodeURIComponent(acc.id)}/suspend`);
      $('users-message').textContent = `Suspended ${acc.name}. IAM users under this account are inactive.`;
      await refreshUsers();
    } catch (e) {
      $('users-message').textContent = e.message;
    }
  }

  async function resumeAccount(acc) {
    if (!canAdminSuspend(acc)) return;
    if (!window.confirm(`Resume “${acc.name}”? The root login will be re-enabled; IAM users stay inactive until you enable them.`)) return;
    $('users-message').textContent = 'Resuming…';
    try {
      await api('POST', `/api/accounts/${encodeURIComponent(acc.id)}/resume`);
      $('users-message').textContent = `Resumed ${acc.name}. Re-enable IAM users from the IAM page if needed.`;
      await refreshUsers();
    } catch (e) {
      $('users-message').textContent = e.message;
    }
  }

  async function stopAccountServices(acc) {
    if (!canAdminSuspend(acc) || acc.kind !== 'client') return;
    if (!window.confirm(`Stop all running services for “${acc.name}” (unpaid / billing hold)? VPS, managed apps, and Docker for this account will be stopped.`)) return;
    $('users-message').textContent = 'Stopping services…';
    try {
      const res = await api('POST', `/api/accounts/${encodeURIComponent(acc.id)}/stop-services`);
      const stopped = res?.stopped || {};
      $('users-message').textContent = `Stopped services for ${acc.name}: VPS ${stopped.vps || 0}, managed apps ${stopped.managed_apps || 0}, Docker ${stopped.docker || 0}.`;
      await refreshUsers();
    } catch (e) {
      $('users-message').textContent = e.message;
    }
  }

  function renderTable(accounts) {
    const table = $('users-table');
    row(table, ['Account', 'Account ID', 'Type', 'Status', 'Email', 'Parent', 'Created', 'Actions'], true);
    if (!accounts.length) {
      row(table, ['No accounts yet.', '', '', '', '', '', '', '']);
      return;
    }
    accounts.forEach((acc) => {
      const actions = document.createElement('div');
      actions.className = 'row-actions';
      if (canManage(acc)) {
        const edit = document.createElement('button');
        edit.type = 'button';
        edit.className = 'small-btn';
        edit.textContent = 'Edit';
        edit.onclick = () => openEdit(acc);
        actions.append(edit);
        if (canAdminSuspend(acc)) {
          if (acc.status === 'suspended') {
            const resume = document.createElement('button');
            resume.type = 'button';
            resume.className = 'small-btn';
            resume.textContent = 'Resume';
            resume.onclick = () => resumeAccount(acc);
            actions.append(resume);
          } else {
            const suspend = document.createElement('button');
            suspend.type = 'button';
            suspend.className = 'danger small-btn';
            suspend.textContent = 'Suspend';
            suspend.onclick = () => suspendAccount(acc);
            actions.append(suspend);
          }
          if (acc.kind === 'client') {
            const stop = document.createElement('button');
            stop.type = 'button';
            stop.className = 'danger small-btn';
            stop.textContent = 'Stop services';
            stop.title = 'Stop all running workloads (e.g. unpaid bill)';
            stop.onclick = () => stopAccountServices(acc);
            actions.append(stop);
          }
        }
        const del = document.createElement('button');
        del.type = 'button';
        del.className = 'danger small-btn';
        del.textContent = 'Delete';
        del.onclick = () => deleteAccount(acc);
        actions.append(del);
      } else {
        const locked = document.createElement('span');
        locked.className = 'muted';
        locked.textContent = acc.kind === 'admin' ? 'Protected' : '-';
        actions.append(locked);
      }
      const statusLabel = acc.status === 'suspended' ? 'suspended' : 'active';
      row(table, [acc.name, acc.id, acc.kind + (acc.user_kind ? ' · ' + acc.user_kind : ''), badge(statusLabel), acc.email || '-', acc.parent_id || '-', acc.created ? new Date(acc.created).toLocaleString() : '-', actions]);
    });
  }

  async function refreshUsers() {
    const me = cfMe || await ensureSession();
    meCache = me;
    const create = $('users-create-panel');
    const kind = $('users-kind');
    if (me.kind !== 'admin' && me.kind !== 'partner') {
      if (create) create.hidden = true;
      if ($('users-lead')) $('users-lead').textContent = 'Account directory for this login.';
      renderStats([]);
      renderTable([]);
      return;
    }
    if (me.kind === 'partner' && kind) {
      kind.value = 'client';
      kind.querySelector('option[value="partner"]')?.remove();
      if ($('users-create-help')) $('users-create-help').textContent = 'Create a root user for a client account you manage.';
    }
    if (me.kind === 'admin' && $('users-lead')) {
      $('users-lead').textContent = 'Suspend a root account to lock out all IAM users, or stop services when a bill is unpaid.';
    }
    const accounts = await api('GET', '/api/accounts');
    accountsCache = Array.isArray(accounts) ? accounts : [];
    renderStats(accountsCache);
    renderTable(accountsCache);
  }

  window.refreshUsers = refreshUsers;
  $('users-refresh')?.addEventListener('click', () => refreshUsers().catch((e) => { if ($('users-message')) $('users-message').textContent = e.message; }));
  $('users-create')?.addEventListener('click', async () => {
    const msg = $('users-create-message');
    msg.textContent = 'Creating…';
    try {
      await api('POST', '/api/accounts', {kind: $('users-kind').value, name: $('users-name').value.trim(), email: $('users-email').value.trim(), password: $('users-password').value});
      msg.textContent = 'Account created.';
      $('users-name').value = '';
      $('users-email').value = '';
      $('users-password').value = '';
      await refreshUsers();
    } catch (e) {
      msg.textContent = e.message;
    }
  });
  $('users-edit-form')?.addEventListener('submit', async (event) => {
    event.preventDefault();
    const id = $('users-edit-id').value;
    const msg = $('users-edit-message');
    const body = {
      name: $('users-edit-name').value.trim(),
      email: $('users-edit-email').value.trim(),
    };
    const password = $('users-edit-password').value;
    if (password) body.password = password;
    msg.textContent = 'Saving…';
    try {
      await api('PUT', `/api/accounts/${encodeURIComponent(id)}`, body);
      msg.textContent = 'Saved.';
      $('users-edit-dialog')?.close();
      if ($('users-message')) $('users-message').textContent = 'Account updated.';
      await refreshUsers();
    } catch (e) {
      msg.textContent = e.message;
    }
  });
  document.querySelectorAll('#users-edit-dialog .dialog-close').forEach((button) => {
    button.onclick = () => button.closest('dialog')?.close();
  });
  refreshUsers().catch((e) => { if ($('users-message')) $('users-message').textContent = e.message; });
})();
