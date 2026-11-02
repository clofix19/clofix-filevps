(() => {
  const host = document.getElementById('footer-host');
  const clock = document.getElementById('footer-clock');
  if (host) host.textContent = location.host || 'CloFix Cloud';
  function tick() {
    if (!clock) return;
    const now = new Date();
    clock.dateTime = now.toISOString();
    clock.textContent = now.toLocaleString(undefined, {
      weekday: 'short', year: 'numeric', month: 'short', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    });
  }
  tick();
  if (clock) setInterval(tick, 1000);
})();
