(() => {
  if (document.body.dataset.page !== 'users') return;
  async function refreshUsers() {
    const me = cfMe || await ensureSession();
    const create = $('users-create-panel');
    const kind = $('users-kind');
    if (me.kind !== 'admin' && me.kind !== 'partner') {
      if (create) create.hidden = true;
      $('users-lead').textContent = 'Account directory for this login.';
      return;
    }
    if (me.kind === 'partner' && kind) {
      kind.value = 'client';
      kind.querySelector('option[value="partner"]')?.remove();
      $('users-create-help').textContent = 'Create a root user for a client account you manage.';
    }
    const accounts = await api('GET', '/api/accounts');
    $('users-count').textContent = String(accounts.length);
    const table = $('users-table');
    row(table, ['Account', 'Account ID', 'Type', 'Email', 'Parent', 'Created'], true);
    if (!accounts.length) row(table, ['No accounts yet.', '', '', '', '', '']);
    accounts.forEach((acc) => {
      row(table, [acc.name, acc.id, acc.kind + (acc.user_kind ? ' · ' + acc.user_kind : ''), acc.email || '—', acc.parent_id || '—', acc.created ? new Date(acc.created).toLocaleString() : '—']);
    });
  }
  window.refreshUsers = refreshUsers;
  $('users-refresh')?.addEventListener('click', () => refreshUsers().catch((e) => { $('users-message').textContent = e.message; }));
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
  refreshUsers().catch((e) => { if ($('users-message')) $('users-message').textContent = e.message; });
})();
