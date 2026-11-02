(() => {
  if (document.body.dataset.page !== 'profile') return;
  const msg = (id, text) => { const n = $(id); if (n) n.textContent = text || ''; };
  const lines = (value) => String(value || '').split(/\r?\n/).map((v) => v.trim()).filter(Boolean);

  async function load() {
    const profile = await api('GET', '/api/profile');
    $('profile-kind').textContent = profile.kind || '—';
    $('profile-role').textContent = profile.role || '—';
    $('profile-mfa-status').textContent = profile.mfa_enabled ? 'On' : 'Off';
    $('profile-client-ip').textContent = profile.client_ip || '—';
    $('profile-display-name').value = profile.display_name || '';
    $('profile-username').value = profile.username || '';
    $('profile-email').value = profile.email || '';
    $('profile-allow-ips').value = (profile.allow_ips || []).join('\n');
    $('profile-deny-ips').value = (profile.deny_ips || []).join('\n');
    $('profile-mfa-setup').hidden = !!profile.mfa_enabled;
    $('profile-mfa-disable').hidden = !profile.mfa_enabled;
    $('profile-mfa-setup-box').hidden = true;
    $('profile-mfa-disable-box').hidden = true;
    const qr = $('profile-mfa-qr');
    if (qr) qr.replaceChildren();
  }

  function showMfaPanel() {
    if (location.hash !== '#profile-mfa') {
      history.pushState(null, '', '#profile-mfa');
      if (typeof applySettingsNav === 'function') applySettingsNav();
    }
  }

  $('profile-save')?.addEventListener('click', async () => {
    msg('profile-message', 'Saving…');
    try {
      const body = {
        display_name: $('profile-display-name').value.trim(),
        username: $('profile-username').value.trim(),
        email: $('profile-email').value.trim(),
      };
      const next = $('profile-new-password').value;
      if (next) {
        body.current_password = $('profile-current-password').value;
        body.new_password = next;
      }
      await api('PUT', '/api/profile', body);
      $('profile-current-password').value = '';
      $('profile-new-password').value = '';
      msg('profile-message', 'Profile saved.');
      cfMe = null;
      await ensureSession();
      await load();
    } catch (e) { msg('profile-message', e.message); }
  });

  $('profile-save-ips')?.addEventListener('click', async () => {
    msg('profile-ip-message', 'Saving…');
    try {
      await api('PUT', '/api/profile', { allow_ips: lines($('profile-allow-ips').value), deny_ips: lines($('profile-deny-ips').value) });
      msg('profile-ip-message', 'IP rules saved.');
      await load();
    } catch (e) { msg('profile-ip-message', e.message); }
  });

  $('profile-mfa-setup')?.addEventListener('click', async () => {
    showMfaPanel();
    msg('profile-mfa-message', 'Preparing authenticator…');
    try {
      const setup = await api('POST', '/api/profile/mfa/setup', {});
      const otpauth = setup.otpauth_url || setup.otpauth_uri || '';
      $('profile-mfa-secret').textContent = setup.secret || '';
      $('profile-mfa-otpauth').textContent = otpauth;
      $('profile-mfa-setup-box').hidden = false;
      if (typeof cfDrawQR === 'function' && otpauth) {
        cfDrawQR($('profile-mfa-qr'), otpauth, 220);
      } else {
        $('profile-mfa-qr').textContent = 'QR unavailable — enter the secret manually.';
      }
      msg('profile-mfa-message', 'Scan the QR code (or enter the secret), then confirm with a code.');
      $('profile-mfa-code')?.focus();
    } catch (e) { msg('profile-mfa-message', e.message); }
  });

  $('profile-mfa-enable')?.addEventListener('click', async () => {
    msg('profile-mfa-message', 'Enabling MFA…');
    try {
      await api('POST', '/api/profile/mfa/enable', { code: $('profile-mfa-code').value.trim() });
      msg('profile-mfa-message', 'MFA enabled. Sign-in will ask for a code.');
      await load();
    } catch (e) { msg('profile-mfa-message', e.message); }
  });

  $('profile-mfa-disable')?.addEventListener('click', () => {
    showMfaPanel();
    $('profile-mfa-disable-box').hidden = false;
    msg('profile-mfa-message', 'Enter password and current authenticator code to disable.');
  });

  $('profile-mfa-disable-confirm')?.addEventListener('click', async () => {
    msg('profile-mfa-message', 'Disabling MFA…');
    try {
      await api('POST', '/api/profile/mfa/disable', {
        password: $('profile-mfa-disable-password').value,
        code: $('profile-mfa-disable-code').value.trim(),
      });
      msg('profile-mfa-message', 'MFA disabled.');
      await load();
    } catch (e) { msg('profile-mfa-message', e.message); }
  });

  ensureSession().then(load).catch((e) => msg('profile-message', e.message));
})();
