(() => {
  if (document.body.dataset.page !== 'home') return;
  const recentKey = 'cf_recent_services';
  const hideKey = 'cf_home_hidden_widgets';
  const names = {
    recent: 'Recently visited',
    security: 'Security',
    welcome: 'Get started',
    usage: 'Resource usage',
    health: 'Fleet health',
    compute: 'Compute',
    network: 'Network',
    host: 'Host capacity',
    fleet: 'VPS fleet',
    workers: 'Worker capacity',
    activity: 'Recent activity'
  };
  const fallback = [
    { name: 'FileVPS', href: '/instances' },
    { name: 'Library', href: '/services' },
    { name: 'Databases', href: '/databases' },
    { name: 'Load Balancers', href: '/load-balancers' },
    { name: 'Workers', href: '/workers' },
    { name: 'Security', href: '/security' },
    { name: 'Billing & Payments', href: '/billing' },
    { name: 'Settings', href: '/settings' }
  ];
  function hidden() {
    try { return JSON.parse(localStorage.getItem(hideKey) || '[]'); } catch (e) { return []; }
  }
  function save(list) {
    localStorage.setItem(hideKey, JSON.stringify(list));
    apply();
  }
  function apply() {
    const hid = new Set(hidden());
    document.querySelectorAll('[data-home-widget]').forEach((widget) => {
      widget.hidden = hid.has(widget.dataset.homeWidget);
    });
    const add = $('home-add-list');
    if (!add) return;
    add.replaceChildren();
    hid.forEach((id) => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'small-btn';
      button.textContent = 'Show ' + (names[id] || id);
      button.onclick = () => save(hidden().filter((item) => item !== id));
      add.append(button);
    });
    if (!hid.length) {
      const empty = document.createElement('p');
      empty.className = 'muted';
      empty.textContent = 'All widgets are visible.';
      add.append(empty);
    }
  }
  function renderRecent() {
    const box = $('home-recent-list');
    if (!box) return;
    let list = [];
    try { list = JSON.parse(localStorage.getItem(recentKey) || '[]'); } catch (e) { list = []; }
    if (!list.length) list = fallback;
    box.replaceChildren();
    list.slice(0, 8).forEach((item) => {
      const link = document.createElement('a');
      link.href = item.href;
      const mark = document.createElement('span');
      mark.className = 'home-recent-mark';
      mark.textContent = (item.name || '?').slice(0, 2).toUpperCase();
      link.append(mark, document.createTextNode(item.name));
      box.append(link);
    });
  }
  document.querySelectorAll('[data-hide-widget]').forEach((button) => {
    button.onclick = () => {
      const id = button.dataset.hideWidget;
      if (!hidden().includes(id)) save([...hidden(), id]);
    };
  });
  $('home-reset-layout')?.addEventListener('click', () => save([]));
  const banner = $('auth-banner'), loading = $('home-loading');
  if (banner && loading && !banner.hidden) loading.hidden = true;
  renderRecent();
  apply();
})();
