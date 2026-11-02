(() => {
  const page = document.body.dataset.page;
  async function send(path, body) {
    const r = await fetch(path, {
      method: 'POST',
      credentials: 'include',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body),
    });
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(j.error || (r.status === 429 ? 'Too many attempts; try again later' : r.status));
    return j;
  }
  const login = document.getElementById('login-form');
  const mfaForm = document.getElementById('login-mfa-form');
  function showMFA(token) {
    if (!mfaForm || !token) return;
    login.hidden = true;
    mfaForm.hidden = false;
    mfaForm.setAttribute('aria-hidden', 'false');
    document.getElementById('login-mfa-token').value = token;
    document.getElementById('login-mfa-code').value = '';
    document.getElementById('login-mfa-code').focus();
  }
  function showPassword() {
    if (!mfaForm) return;
    mfaForm.hidden = true;
    mfaForm.setAttribute('aria-hidden', 'true');
    document.getElementById('login-mfa-token').value = '';
    document.getElementById('login-mfa-code').value = '';
    login.hidden = false;
  }
  // MFA step stays hidden until password login succeeds and server returns mfa_required.
  if (mfaForm) mfaForm.hidden = true;
  if (login) {
    login.addEventListener('submit', async (e) => {
      e.preventDefault();
      const msg = document.getElementById('login-message');
      const btn = login.querySelector('button[type="submit"]');
      msg.textContent = 'Signing in…';
      if (btn) btn.disabled = true;
      try {
        const result = await send('/api/auth/login', {
          email: document.getElementById('login-email').value.trim(),
          password: document.getElementById('login-password').value,
          website: document.getElementById('login-website')?.value || '',
        });
        // Only after email+password OK: show authenticator if MFA is enabled for that user.
        if (result && result.mfa_required === true && result.mfa_token) {
          msg.textContent = '';
          showMFA(result.mfa_token);
          return;
        }
        location.assign('/');
      } catch (err) {
        showPassword();
        msg.textContent = err.message;
      } finally {
        if (btn) btn.disabled = false;
      }
    });
  }
  if (mfaForm) {
    mfaForm.addEventListener('submit', async (e) => {
      e.preventDefault();
      const msg = document.getElementById('login-mfa-message');
      const btn = mfaForm.querySelector('button[type="submit"]');
      msg.textContent = 'Verifying…';
      if (btn) btn.disabled = true;
      try {
        await send('/api/auth/login/mfa', {
          mfa_token: document.getElementById('login-mfa-token').value,
          code: document.getElementById('login-mfa-code').value.trim(),
          website: document.getElementById('login-mfa-website')?.value || '',
        });
        location.assign('/');
      } catch (err) {
        msg.textContent = err.message;
      } finally {
        if (btn) btn.disabled = false;
      }
    });
    document.getElementById('login-mfa-back')?.addEventListener('click', () => {
      showPassword();
      document.getElementById('login-message').textContent = '';
    });
  }
  const register = document.getElementById('register-form');
  if (register) {
    register.addEventListener('submit', async (e) => {
      e.preventDefault();
      const msg = document.getElementById('register-message');
      const btn = register.querySelector('button[type="submit"]');
      msg.textContent = 'Creating account…';
      if (btn) btn.disabled = true;
      try {
        await send('/api/auth/register', {
          account_name: document.getElementById('register-account').value.trim(),
          email: document.getElementById('register-email').value.trim(),
          password: document.getElementById('register-password').value,
          website: document.getElementById('register-website')?.value || '',
        });
        location.assign('/');
      } catch (err) {
        msg.textContent = err.message;
      } finally {
        if (btn) btn.disabled = false;
      }
    });
  }
  if (page === 'login' || page === 'register') {
    fetch('/api/auth/me', {credentials: 'include', cache: 'no-store'}).then((r) => {
      if (r.ok) location.replace('/');
    }).catch(() => {});
  }
})();
