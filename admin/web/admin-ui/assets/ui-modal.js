/* CloFix shared prompt modal - replaces native window.prompt everywhere. */
(function () {
  const DIALOG_ID = 'cf-prompt-dialog';

  function ensureDialog() {
    let dlg = document.getElementById(DIALOG_ID);
    if (dlg) return dlg;
    dlg = document.createElement('dialog');
    dlg.id = DIALOG_ID;
    dlg.className = 'terminal-dialog form-dialog cf-prompt-dialog';
    dlg.setAttribute('aria-labelledby', 'cf-prompt-title');
    dlg.innerHTML = `
      <div class="section-head">
        <div>
          <p class="eyebrow" id="cf-prompt-eyebrow">CloFix</p>
          <h2 id="cf-prompt-title">Input required</h2>
          <p class="muted" id="cf-prompt-message"></p>
        </div>
        <button type="button" class="secondary" id="cf-prompt-close" aria-label="Close">Close</button>
      </div>
      <form id="cf-prompt-form" class="form-grid" method="dialog">
        <div class="form-field form-span">
          <label for="cf-prompt-input" id="cf-prompt-label">Value</label>
          <input id="cf-prompt-input" class="field" autocomplete="off" spellcheck="false">
          <small class="muted" id="cf-prompt-hint" hidden></small>
        </div>
        <div class="inline-fields form-span" style="margin-top:4px">
          <button type="submit" class="primary" id="cf-prompt-ok">OK</button>
          <button type="button" class="secondary" id="cf-prompt-cancel">Cancel</button>
          <span class="message" id="cf-prompt-error" role="status" aria-live="polite"></span>
        </div>
      </form>`;
    document.body.appendChild(dlg);
    return dlg;
  }

  let activeResolver = null;

  function finish(value) {
    const dlg = document.getElementById(DIALOG_ID);
    const resolve = activeResolver;
    activeResolver = null;
    if (dlg?.open) dlg.close();
    if (resolve) resolve(value);
  }

  /**
   * @param {string} message
   * @param {string} [defaultValue]
   * @param {{title?:string,eyebrow?:string,label?:string,confirm?:string,cancel?:string,hint?:string,required?:boolean,pattern?:RegExp,validate?:(v:string)=>string|null}} [opts]
   * @returns {Promise<string|null>}
   */
  function cfPrompt(message, defaultValue, opts) {
    const options = opts && typeof opts === 'object' ? opts : {};
    const dlg = ensureDialog();
    const title = dlg.querySelector('#cf-prompt-title');
    const eyebrow = dlg.querySelector('#cf-prompt-eyebrow');
    const msg = dlg.querySelector('#cf-prompt-message');
    const label = dlg.querySelector('#cf-prompt-label');
    const input = dlg.querySelector('#cf-prompt-input');
    const hint = dlg.querySelector('#cf-prompt-hint');
    const err = dlg.querySelector('#cf-prompt-error');
    const ok = dlg.querySelector('#cf-prompt-ok');
    const cancel = dlg.querySelector('#cf-prompt-cancel');
    const close = dlg.querySelector('#cf-prompt-close');
    const form = dlg.querySelector('#cf-prompt-form');

    if (activeResolver) finish(null);

    title.textContent = options.title || 'Input required';
    eyebrow.textContent = options.eyebrow || 'CloFix';
    msg.textContent = message || '';
    label.textContent = options.label || 'Value';
    ok.textContent = options.confirm || 'OK';
    cancel.textContent = options.cancel || 'Cancel';
    err.textContent = '';
    if (options.hint) {
      hint.hidden = false;
      hint.textContent = options.hint;
    } else {
      hint.hidden = true;
      hint.textContent = '';
    }
    input.value = defaultValue == null ? '' : String(defaultValue);
    input.required = options.required !== false;

    return new Promise((resolve) => {
      activeResolver = resolve;

      const onCancel = () => finish(null);
      const onSubmit = (e) => {
        e.preventDefault();
        const value = String(input.value || '');
        const trimmed = value.trim();
        if (options.required !== false && !trimmed) {
          err.textContent = 'Enter a value to continue.';
          input.focus();
          return;
        }
        if (options.pattern instanceof RegExp && !options.pattern.test(trimmed)) {
          err.textContent = options.hint || 'Value format is invalid.';
          input.focus();
          return;
        }
        if (typeof options.validate === 'function') {
          const problem = options.validate(trimmed);
          if (problem) {
            err.textContent = problem;
            input.focus();
            return;
          }
        }
        finish(trimmed);
      };

      form.onsubmit = onSubmit;
      cancel.onclick = onCancel;
      close.onclick = onCancel;
      dlg.oncancel = (ev) => {
        ev.preventDefault();
        onCancel();
      };
      dlg.onclick = (ev) => {
        if (ev.target === dlg) onCancel();
      };

      if (!dlg.open) dlg.showModal();
      requestAnimationFrame(() => {
        input.focus();
        input.select();
      });
    });
  }

  window.cfPrompt = cfPrompt;
})();
