/* Local MFA QR helper. Uses vendored qrcode-generator (no network; secret stays in-browser). */
(() => {
  function drawQR(el, text, px = 200) {
    if (!el || !text || typeof qrcode !== 'function') return;
    const qr = qrcode(0, 'M');
    qr.addData(text);
    qr.make();
    const cell = Math.max(2, Math.floor(px / (qr.getModuleCount() + 8)));
    el.innerHTML = qr.createImgTag(cell, cell * 4);
    const img = el.querySelector('img');
    if (img) {
      img.alt = 'Google Authenticator QR code';
      img.width = px;
      img.height = px;
      img.style.width = px + 'px';
      img.style.height = px + 'px';
      img.style.imageRendering = 'pixelated';
      img.style.background = '#fff';
      img.style.borderRadius = '8px';
      img.style.padding = '8px';
      img.style.boxSizing = 'border-box';
    }
  }
  window.cfDrawQR = drawQR;
})();
