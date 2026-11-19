/* CloFix FileVPS - in-browser SSH terminal (PEM or PAM). */
(function () {
  const $ = (id) => document.getElementById(id);
  const RESUME_KEY = 'cf_vps_term_resume';
  let term = null;
  let fitAddon = null;
  let socket = null;
  let connected = false;
  let sessionReady = false;
  let resizeObserver = null;
  let methodChosen = false; // do not override user selection on refresh
  let boundInstance = '';
  let intentionalClose = false;
  let resumeAttempted = false;
  let lastAuth = null; // { method, password?, private_key? } for tab refresh resume

  function msg(text) {
    const el = $('vps-term-message');
    if (el) el.textContent = text || '';
  }

  function saveResume(id, auth) {
    if (!id || !auth) return;
    try {
      sessionStorage.setItem(RESUME_KEY, JSON.stringify({
        id,
        method: auth.method,
        password: auth.password || '',
        private_key: auth.private_key || '',
        at: Date.now(),
      }));
    } catch (_) {}
  }

  function clearResume() {
    lastAuth = null;
    try { sessionStorage.removeItem(RESUME_KEY); } catch (_) {}
  }

  function loadResume() {
    try {
      const raw = sessionStorage.getItem(RESUME_KEY);
      if (!raw) return null;
      const data = JSON.parse(raw);
      if (!data?.id || !data?.method) return null;
      if (Date.now() - (data.at || 0) > 8 * 60 * 60 * 1000) {
        clearResume();
        return null;
      }
      return data;
    } catch (_) {
      return null;
    }
  }

  function currentMethod() {
    return $('vps-term-method')?.value === 'password' ? 'password' : 'pem';
  }

  function instanceAuth(inst) {
    return {
      pam: !!inst?.spec?.password_auth,
      key: !!(inst?.spec?.ssh_authorized_key || '').trim(),
    };
  }

  function setMethod(method, fromUser, inst) {
    const auth = instanceAuth(inst || (typeof instances !== 'undefined' ? instances : []).find((x) => x.id === selectedID));
    let next = method === 'password' ? 'password' : 'pem';
    // Only force PAM when there is no key at all.
    if (next === 'pem' && !auth.key && auth.pam) next = 'password';
    if ($('vps-term-method')) $('vps-term-method').value = next;
    const pemBtn = $('vps-term-method-pem');
    const pwBtn = $('vps-term-method-password');
    if (pemBtn) {
      pemBtn.disabled = false;
      pemBtn.classList.toggle('is-active', next === 'pem');
      pemBtn.setAttribute('aria-pressed', next === 'pem' ? 'true' : 'false');
      pemBtn.title = !auth.key ? 'No authorized SSH key on this VPS yet' : '';
    }
    if (pwBtn) {
      pwBtn.disabled = false;
      pwBtn.classList.toggle('is-active', next === 'password');
      pwBtn.setAttribute('aria-pressed', next === 'password' ? 'true' : 'false');
      pwBtn.title = auth.pam
        ? 'Root password login is enabled'
        : 'Set a root password under Access to enable PAM on this VPS';
    }
    if ($('vps-term-pem-wrap')) $('vps-term-pem-wrap').hidden = next !== 'pem';
    if ($('vps-term-password-wrap')) $('vps-term-password-wrap').hidden = next !== 'password';
    if ($('vps-term-hint')) {
      if (next === 'pem') {
        $('vps-term-hint').textContent = 'Use the private PEM downloaded when this VPS was created (not the public ssh-ed25519 line).';
      } else if (auth.pam) {
        $('vps-term-hint').textContent = 'Enter the root password set at create time, or updated under Access → Root password.';
      } else {
        $('vps-term-hint').textContent = 'PAM is off for this VPS. Open Access → Root password, set a password (enables PAM), then connect here.';
      }
    }
    if (fromUser) {
      methodChosen = true;
      // Keep credentials isolated when switching tabs.
      if (next === 'pem' && $('vps-term-password')) $('vps-term-password').value = '';
      if (next === 'password') {
        if ($('vps-term-pem')) $('vps-term-pem').value = '';
        if ($('vps-term-pem-file')) $('vps-term-pem-file').value = '';
      }
    }
  }

  function fitTerm() {
    if (!term) return;
    try {
      const host = term.element?.parentElement;
      const cell = term._core?._renderService?.dimensions?.css?.cell;
      if (host && cell?.width > 0 && cell?.height > 0) {
        // Measure with clientHeight + xterm padding (FitAddon uses CSS height and can over-count).
        const cs = window.getComputedStyle(term.element);
        const padY = (parseFloat(cs.paddingTop) || 0) + (parseFloat(cs.paddingBottom) || 0);
        const padX = (parseFloat(cs.paddingLeft) || 0) + (parseFloat(cs.paddingRight) || 0);
        const sb = term.options.scrollback === 0 ? 0 : (term._core?.viewport?.scrollBarWidth || 0);
        const cols = Math.max(2, Math.floor((host.clientWidth - padX - sb - 2) / cell.width));
        const rows = Math.max(2, Math.floor((host.clientHeight - padY - 4) / cell.height));
        if (term.cols !== cols || term.rows !== rows) term.resize(cols, rows);
      } else if (fitAddon) {
        fitAddon.fit();
      }
      try { term.scrollToBottom(); } catch (_) {}
      if (sessionReady && socket && socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
      }
    } catch (_) {}
  }

  function setConnected(on) {
    connected = !!on;
    if ($('vps-term-login')) $('vps-term-login').hidden = connected;
    if ($('vps-term-screen')) $('vps-term-screen').hidden = !connected;
    if ($('vps-term-exit')) $('vps-term-exit').hidden = !connected;
    if ($('vps-term-live-badge')) $('vps-term-live-badge').hidden = !connected;
    if ($('vps-term-connect')) $('vps-term-connect').disabled = connected;
    if ($('vps-term-sub')) {
      $('vps-term-sub').textContent = connected
        ? 'Live root shell - click Exit to close the session.'
        : 'Interactive root shell in the browser (PEM or PAM).';
    }
    if (connected) {
      // Fit only after the screen is visible - opening into display:none yields bad cell metrics.
      requestAnimationFrame(() => {
        fitTerm();
        requestAnimationFrame(() => {
          fitTerm();
          try { term?.refresh(0, Math.max(0, (term?.rows || 1) - 1)); } catch (_) {}
          try { term?.focus(); } catch (_) {}
        });
      });
      if (document.fonts?.ready) document.fonts.ready.then(() => fitTerm()).catch(() => {});
    }
  }

  function updateAuthBadge(inst) {
    const badge = $('vps-term-auth-badge');
    if (!badge) return;
    const pam = !!inst?.spec?.password_auth;
    const key = !!(inst?.spec?.ssh_authorized_key || '').trim();
    if (pam && key) {
      badge.textContent = 'PEM + PAM';
      badge.className = 'badge RUNNING';
    } else if (pam) {
      badge.textContent = 'PAM';
      badge.className = 'badge STARTING';
    } else if (key) {
      badge.textContent = 'PEM';
      badge.className = 'badge RUNNING';
    } else {
      badge.textContent = 'No login';
      badge.className = 'badge STOPPED';
    }
  }

  function ensureTerm() {
    if (term) return term;
    const Term = window.Terminal;
    const FitCtor = window.FitAddon?.FitAddon || window.FitAddon;
    if (!Term || !FitCtor) {
      msg('Terminal library failed to load. Refresh the page.');
      return null;
    }
    term = new Term({
      cursorBlink: true,
      cursorStyle: 'block',
      fontSize: 14,
      lineHeight: 1.2,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
      // Full ANSI palette so guest PS1 / ls --color / grep match a normal Linux terminal.
      theme: {
        background: '#0a1119',
        foreground: '#e6edf3',
        cursor: '#5eead4',
        cursorAccent: '#0a1119',
        selectionBackground: '#264f78',
        selectionForeground: '#ffffff',
        black: '#0d1117',
        red: '#ff7b72',
        green: '#3fb950',
        yellow: '#d29922',
        blue: '#58a6ff',
        magenta: '#bc8cff',
        cyan: '#39c5cf',
        white: '#b1bac4',
        brightBlack: '#6e7681',
        brightRed: '#ffa198',
        brightGreen: '#56d364',
        brightYellow: '#e3b341',
        brightBlue: '#79c0ff',
        brightMagenta: '#d2a8ff',
        brightCyan: '#56d4dd',
        brightWhite: '#f0f6fc',
      },
      scrollback: 5000,
      convertEol: true,
      allowTransparency: false,
    });
    fitAddon = new FitCtor();
    term.loadAddon(fitAddon);
    const host = $('vps-term-xterm');
    host.replaceChildren();
    term.open(host);
    term.onData((data) => {
      if (sessionReady && socket && socket.readyState === WebSocket.OPEN) socket.send(data);
    });
    if (resizeObserver) {
      try { resizeObserver.disconnect(); } catch (_) {}
    }
    resizeObserver = new ResizeObserver(() => {
      requestAnimationFrame(fitTerm);
    });
    resizeObserver.observe(host);
    return term;
  }

  function disconnect(reason, opts) {
    const options = opts || {};
    sessionReady = false;
    const sock = socket;
    socket = null;
    intentionalClose = true;
    if (sock) {
      // Detach handlers first so a late onclose cannot wipe a new reconnect socket.
      try {
        sock.onclose = null;
        sock.onmessage = null;
        sock.onerror = null;
        sock.onopen = null;
        sock.close();
      } catch (_) {}
    }
    intentionalClose = false;
    if (options.clearResume) clearResume();
    if (reason && term) term.writeln('\r\n\x1b[90m' + reason + '\x1b[0m');
    setConnected(false);
  }

  /** User Exit only - send shell exit, then close. Browser refresh must not call this. */
  function exitTerminal() {
    if (!connected && !sessionReady && !socket) {
      setConnected(false);
      clearResume();
      return;
    }
    msg('Exiting…');
    if (sessionReady && socket && socket.readyState === WebSocket.OPEN) {
      try { socket.send('exit\r'); } catch (_) {}
    }
    window.setTimeout(() => {
      disconnect('Session ended (Exit)', { clearResume: true });
      msg('Terminal closed. Connect again to open a new shell.');
    }, 180);
  }

  function wsURL(id) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    return proto + '//' + location.host + '/api/instances/' + encodeURIComponent(id) + '/terminal';
  }

  function normalizeClientPEM(raw) {
    let key = String(raw || '').replace(/^\uFEFF/, '').replace(/\r\n/g, '\n').replace(/\r/g, '\n').trim();
    // Some editors wrap the base64 body; keep header/footer lines intact.
    return key;
  }

  async function connect() {
    const id = typeof selectedID === 'string' ? selectedID : '';
    if (!id) { msg('Select a VPS first.'); return; }
    // Snapshot credentials first - refresh()/preferAuthMethod can clear the PEM
    // textarea when rebinding the panel, which made "PEM file" login look broken
    // while password still worked.
    const methodAtClick = currentMethod();
    const passwordAtClick = $('vps-term-password')?.value || lastAuth?.password || '';
    const pemAtClick = normalizeClientPEM($('vps-term-pem')?.value || lastAuth?.private_key || '');
    // Refresh instance flags so a password just set under Access is visible.
    if (typeof refresh === 'function') {
      try { await refresh(); } catch (_) {}
    }
    const inst = (typeof instances !== 'undefined' ? instances : []).find((x) => x.id === id);
    if (!inst) { msg('VPS not found.'); return; }
    if (inst.state !== 'RUNNING') { msg('VPS must be running.'); return; }
    if (inst.spec?.protection_mode !== false) { msg('Disable Protection Mode before opening a shell.'); return; }
    if (inst.spec?.ssh_login_blocked) { msg('SSH login is blocked for this VPS.'); return; }

    const auth = instanceAuth(inst);
    let method = methodAtClick === 'password' ? 'password' : 'pem';
    if (method === 'password' && !auth.pam) {
      if (auth.key) {
        msg('Password login is off on this VPS. Switch to PEM key, or set a root password under Access → Root password and wait a few seconds.');
      } else {
        msg('PAM is off. Set a root password under Access → Root password, wait a few seconds, then retry.');
      }
      return;
    }
    if (method === 'pem' && !auth.key) {
      if (auth.pam) {
        msg('No authorized SSH key on this VPS. Switch to PAM password, or add a key under Access.');
      } else {
        msg('No authorized SSH key on this VPS. Use PAM password, or add a key under Access.');
      }
      return;
    }
    // Restore fields if refresh wiped them so the user still sees what will be sent.
    if (method === 'pem' && pemAtClick && $('vps-term-pem') && !$('vps-term-pem').value.trim()) {
      $('vps-term-pem').value = pemAtClick;
    }
    if (method === 'password' && passwordAtClick && $('vps-term-password') && !$('vps-term-password').value) {
      $('vps-term-password').value = passwordAtClick;
    }
    // fromUser=false: keep snapshotted credentials; only sync method UI state.
    setMethod(method, false, inst);
    methodChosen = true;
    const payload = { method, cols: 80, rows: 24 };
    if (method === 'password') {
      payload.password = passwordAtClick;
      if (payload.password.length < 8) { msg('Enter the root PAM password (min 8 characters).'); return; }
    } else {
      payload.private_key = pemAtClick;
      const key = payload.private_key;
      if (!key) { msg('Paste or choose a private PEM key.'); return; }
      if (/^ssh-(ed25519|rsa)|ecdsa-sha2-/.test(key.split('\n')[0])) {
        msg('Paste the private PEM (BEGIN PRIVATE KEY), not the public ssh-… line.');
        return;
      }
      if (!/BEGIN [^\n]*PRIVATE KEY/.test(key)) {
        msg('Private key must include a BEGIN … PRIVATE KEY PEM block.');
        return;
      }
    }
    lastAuth = {
      method,
      password: payload.password || '',
      private_key: payload.private_key || '',
    };

    msg('Connecting…');
    disconnect(); // close prior socket only; keep resume until Exit
    // Show the host before open/fit - xterm metrics are wrong under display:none.
    if ($('vps-term-login')) $('vps-term-login').hidden = true;
    if ($('vps-term-screen')) $('vps-term-screen').hidden = false;
    const t = ensureTerm();
    if (!t) {
      if ($('vps-term-login')) $('vps-term-login').hidden = false;
      if ($('vps-term-screen')) $('vps-term-screen').hidden = true;
      return;
    }
    t.reset();
    fitTerm();
    t.focus();

    let sock;
    try {
      sock = new WebSocket(wsURL(id));
    } catch (e) {
      msg(e.message || 'WebSocket failed');
      setConnected(false);
      return;
    }
    socket = sock;
    sessionReady = false;
    sock.binaryType = 'arraybuffer';
    sock.onopen = () => {
      fitTerm();
      payload.cols = t.cols || 80;
      payload.rows = t.rows || 24;
      sock.send(JSON.stringify(payload));
    };
    sock.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        try {
          const ctrl = JSON.parse(ev.data);
          if (ctrl.type === 'ready') {
            sessionReady = true;
            setConnected(true);
            saveResume(id, lastAuth);
            msg('');
            if ($('vps-term-sub')) {
              $('vps-term-sub').textContent = 'Live as root@' + id + ' - click Exit to close.';
            }
            fitTerm();
            t.focus();
            return;
          }
          if (ctrl.type === 'error') {
            msg(ctrl.message || 'Connection failed');
            disconnect(ctrl.message || 'Connection failed');
            return;
          }
        } catch (_) {
          t.write(ev.data);
        }
        return;
      }
      t.write(new Uint8Array(ev.data));
    };
    sock.onerror = () => {
      if (!intentionalClose) msg('WebSocket error');
    };
    sock.onclose = () => {
      socket = null;
      if (intentionalClose) {
        setConnected(false);
        return;
      }
      // Unexpected drop (network / page hide) - keep resume so refresh can reconnect.
      if (connected || sessionReady) {
        sessionReady = false;
        setConnected(false);
        msg('Connection lost. Reconnect, or click Exit after reconnecting to end the session.');
        if (term) term.writeln('\r\n\x1b[90mConnection lost\x1b[0m');
      } else if (!$('vps-term-message')?.textContent || $('vps-term-message').textContent === 'Connecting…') {
        msg('Disconnected');
        setConnected(false);
      } else {
        setConnected(false);
      }
    };
  }

  function tryResumeSession(inst) {
    if (resumeAttempted || connected || sessionReady) return;
    const id = inst?.id || (typeof selectedID === 'string' ? selectedID : '');
    if (!id) return;
    const data = loadResume();
    if (!data || data.id !== id) return;
    resumeAttempted = true;
    lastAuth = {
      method: data.method === 'password' ? 'password' : 'pem',
      password: data.password || '',
      private_key: data.private_key || '',
    };
    setMethod(lastAuth.method, true, inst);
    if (lastAuth.method === 'password' && $('vps-term-password') && lastAuth.password) {
      $('vps-term-password').value = lastAuth.password;
    }
    if (lastAuth.method === 'pem' && $('vps-term-pem') && lastAuth.private_key) {
      $('vps-term-pem').value = lastAuth.private_key;
    }
    if (location.hash !== '#detail-terminal') location.hash = 'detail-terminal';
    msg('Restoring terminal session…');
    connect();
  }

  function preferAuthMethod(inst, force) {
    updateAuthBadge(inst);
    const id = inst?.id || '';
    if (id && id !== boundInstance) {
      boundInstance = id;
      methodChosen = false;
      // Only clear credentials when switching to a different VPS - never on
      // the periodic refresh that connect() triggers before dialing.
      if ($('vps-term-password')) $('vps-term-password').value = '';
      if ($('vps-term-pem')) $('vps-term-pem').value = '';
      if ($('vps-term-pem-file')) $('vps-term-pem-file').value = '';
      msg('');
    }
    const auth = instanceAuth(inst);
    if (methodChosen && !force) {
      // Keep selection but re-apply disabled state for this VPS.
      setMethod(currentMethod(), false, inst);
      return;
    }
    // Prefer PEM whenever a key is installed; PAM only when that is the sole option.
    setMethod(auth.key ? 'pem' : (auth.pam ? 'password' : 'pem'), false, inst);
  }

  window.refreshVPSTerminalPanel = function refreshVPSTerminalPanel(inst) {
    if (!$('detail-terminal')) return;
    preferAuthMethod(inst, false);
    if (inst?.state !== 'RUNNING' && (connected || sessionReady)) {
      disconnect('VPS is no longer running', { clearResume: true });
      return;
    }
    // After a browser refresh, restore the live session automatically.
    tryResumeSession(inst);
  };

  window.openVPSTerminalNav = function openVPSTerminalNav() {
    location.hash = 'detail-terminal';
    preferAuthMethod((typeof instances !== 'undefined' ? instances : []).find((x) => x.id === selectedID), false);
  };

  document.addEventListener('DOMContentLoaded', () => {
    $('vps-term-method-pem')?.addEventListener('click', () => {
      const inst = (typeof instances !== 'undefined' ? instances : []).find((x) => x.id === selectedID);
      setMethod('pem', true, inst);
      if (inst && !instanceAuth(inst).key) {
        msg('No authorized SSH key on this VPS yet. Paste a private PEM only if you install the matching public key.');
      }
    });
    $('vps-term-method-password')?.addEventListener('click', () => {
      const inst = (typeof instances !== 'undefined' ? instances : []).find((x) => x.id === selectedID);
      setMethod('password', true, inst);
      if (inst && !instanceAuth(inst).pam) {
        msg('PAM is off until you set a root password under Access → Root password.');
      }
    });
    $('vps-term-pem-file')?.addEventListener('change', async (e) => {
      const file = e.target.files?.[0];
      if (!file) return;
      try {
        const inst = (typeof instances !== 'undefined' ? instances : []).find((x) => x.id === selectedID);
        setMethod('pem', true, inst);
        const text = normalizeClientPEM(await file.text());
        if (!/BEGIN [^\n]*PRIVATE KEY/.test(text)) {
          msg('File does not look like a private PEM (need BEGIN … PRIVATE KEY).');
          return;
        }
        if (/^ssh-(ed25519|rsa)|ecdsa-sha2-/.test(text.split('\n')[0])) {
          msg('That file is a public key. Choose the private .pem downloaded at create time.');
          return;
        }
        $('vps-term-pem').value = text;
        msg('Loaded ' + file.name + ' (' + text.split('\n').length + ' lines)');
      } catch (err) {
        msg(err.message || 'Could not read key file');
      }
    });
    $('vps-term-connect')?.addEventListener('click', () => connect());
    $('vps-term-exit')?.addEventListener('click', () => exitTerminal());
    $('vps-term-password')?.addEventListener('keydown', (e) => { if (e.key === 'Enter') connect(); });
    setMethod('pem', false);

    // Warn on browser refresh / tab close while a shell is live.
    window.addEventListener('beforeunload', (e) => {
      if (!connected && !sessionReady) return;
      e.preventDefault();
      e.returnValue = '';
    });
  });
})();
