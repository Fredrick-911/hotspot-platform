(function () {
  'use strict';
  var h = HP.h, $ = HP.$;
  var staff = { staff: true };
  var layout = $('.layout');
  var portalURL = layout.dataset.portal;
  var state = { overview: null, plans: [] };

  $('#logout').addEventListener('click', function () { HP.logout(); });
  $('#portal-url').textContent = portalURL;
  $('#open-portal').href = portalURL;
  $('#copy-portal').addEventListener('click', function () {
    HP.copy(portalURL).then(function (ok) { HP.toast(ok ? 'Link copied' : 'Press Ctrl+C to copy', ok ? 'ok' : ''); });
  });

  var loaders = {
    overview: loadOverview, payments: loadPayments, customers: loadCustomers,
    plans: loadPlans, routers: loadRouters, announcements: loadNotes, settings: function () {}
  };
  var current = 'overview';
  var show = HP.views(function (name) { current = name; loaders[name](); });

  // ---------------- overview ----------------
  function loadOverview() {
    return HP.get('/api/tenant/overview', staff).then(function (o) {
      state.overview = o;
      renderOverview(o);
    }, HP.errorToast);
  }

  function renderOverview(o) {
    var kp = $('#ov-kpis'); HP.clear(kp);
    kp.append(
      HP.kpi('Today - you keep', HP.kes(o.today.net_cents), o.today.sales + (o.today.sales === 1 ? ' sale' : ' sales') + ' · ' + HP.kes(o.today.gross_cents) + ' paid'),
      HP.kpi('This week', HP.kes(o.week.net_cents), o.week.sales + ' sales · ' + HP.kes(o.week.gross_cents) + ' paid', 'green'),
      HP.kpi('This month', HP.kes(o.month.net_cents), o.month.sales + ' sales · ' + HP.kes(o.month.gross_cents) + ' paid', 'orange'),
      HP.kpi('Online now', HP.num(o.online_now), HP.num(o.active_passes) + ' active passes', 'purple')
    );
    $('#ov-sub').textContent = 'All time you have kept ' + HP.kes(o.all.net_cents) + ' from ' + HP.num(o.all.sales) + ' sales.';
    $('#commission-note').textContent = 'Platform commission: ' + (o.commission_bps / 100) + '% of each sale. "You keep" is the sale minus this.';

    var ch = $('#ov-chart'); HP.clear(ch); ch.appendChild(HP.chart(o.daily, false));

    var top = $('#ov-top'); HP.clear(top);
    top.appendChild(HP.table({
      rows: o.top_plans, emptyTitle: 'No sales yet', emptyText: 'Your best-selling plans will show here.',
      columns: [
        { label: 'Plan', render: function (p) { return p.name; } },
        { label: 'Sales', num: true, render: function (p) { return HP.num(p.sales); } },
        { label: 'Paid', num: true, render: function (p) { return HP.kes(p.gross_cents); } }
      ]
    }));

    var lv = $('#ov-live'); HP.clear(lv);
    lv.append(
      row('Routers online', o.routers_online + ' of ' + o.routers, o.routers === 0 ? 'off' : (o.routers_online === o.routers ? 'ok' : 'warn')),
      row('People online', HP.num(o.online_now)),
      row('Active passes', HP.num(o.active_passes)),
      row('Data used (30 days)', HP.bytes(o.data_used_30d_bytes))
    );
    setCount('#n-routers', o.routers_online + '/' + o.routers, o.routers > 0);
    setCount('#n-online', o.online_now, o.online_now > 0);

    HP.get('/api/tenant/payments?status=SUCCESS&limit=8', staff).then(function (r) { renderFeed(r.payments); }, function () {});
  }

  function row(label, value, chipKind) {
    return h('div', { class: 'row' }, h('span', { class: 'muted grow', text: label }),
      chipKind ? HP.chip(value, chipKind) : h('b', { text: value }));
  }
  function setCount(sel, text, show) {
    var el = $(sel); el.textContent = text; el.classList.toggle('hidden', !show);
  }

  function renderFeed(payments, flashFirst) {
    var feed = $('#ov-feed'); HP.clear(feed);
    if (!payments.length) { feed.appendChild(HP.empty('No sales yet', 'When a customer pays, it appears here instantly.')); return; }
    payments.forEach(function (p, i) {
      feed.appendChild(h('div', { class: 'feed-item' + (flashFirst && i === 0 ? ' new' : '') },
        h('div', { class: 'feed-icon', text: '✓' }),
        h('div', { class: 'grow' }, h('b', { text: p.plan_name }), h('div', { class: 'muted small', text: maskPhone(p.phone) + ' · ' + HP.ago(p.created_at) })),
        h('b', { text: HP.kes(p.amount_cents) })));
    });
  }
  function maskPhone(p) { return p && p.length >= 8 ? p.slice(0, 4) + '***' + p.slice(-3) : p; }

  // ---------------- payments ----------------
  function loadPayments() {
    var status = $('#pay-status').value;
    var box = $('#pay-table'); HP.clear(box); box.appendChild(HP.skeleton());
    return HP.get('/api/tenant/payments?limit=200' + (status ? '&status=' + status : ''), staff).then(function (r) {
      HP.clear(box);
      box.appendChild(HP.table({
        rows: r.payments, emptyTitle: 'No payments found', emptyText: 'Payments appear here as customers buy passes.',
        columns: [
          { label: 'When', render: function (p) { return h('span', null, HP.when(p.created_at), h('span', { class: 'sub', text: HP.ago(p.created_at) })); } },
          { label: 'Customer', render: function (p) { return h('span', { class: 'mono', text: p.phone }); } },
          { label: 'Plan', render: function (p) { return p.plan_name; } },
          { label: 'Paid', num: true, render: function (p) { return HP.kes(p.amount_cents); } },
          { label: 'Platform fee', num: true, render: function (p) { return p.status === 'SUCCESS' ? HP.kes(p.fee_cents) : '-'; } },
          { label: 'You keep', num: true, render: function (p) { return p.status === 'SUCCESS' ? h('b', { text: HP.kes(p.amount_cents - p.fee_cents) }) : '-'; } },
          { label: 'Status', render: function (p) { return payChip(p); } },
          { label: 'M-Pesa receipt', render: function (p) { return h('span', { class: 'mono small', text: p.receipt || '-' }); } }
        ]
      }));
    }, HP.errorToast);
  }
  function payChip(p) {
    if (p.status === 'SUCCESS') return HP.chip('Paid', 'ok');
    if (p.status === 'PENDING') return HP.chip('Waiting', 'warn');
    return h('span', { title: p.note || '' }, HP.chip('Failed', 'bad'));
  }
  $('#pay-status').addEventListener('change', loadPayments);

  // ---------------- customers ----------------
  function loadCustomers() {
    var q = $('#cust-q').value.trim();
    var box = $('#cust-table'); HP.clear(box); box.appendChild(HP.skeleton());
    return HP.get('/api/tenant/customers?limit=100' + (q ? '&q=' + encodeURIComponent(q) : ''), staff).then(function (r) {
      HP.clear(box);
      box.appendChild(HP.table({
        rows: r.customers, emptyTitle: q ? 'No customer matches' : 'No customers yet', emptyText: q ? 'Try fewer digits.' : 'Customers appear here after their first purchase.',
        columns: [
          { label: 'Customer', render: function (c) { return h('span', null, h('span', { class: 'mono', text: c.phone }), c.mac_address ? h('span', { class: 'sub mono', text: c.mac_address }) : null); } },
          { label: 'Plan', render: function (c) { return c.plan_name; } },
          { label: 'Status', render: function (c) { return stateChip(c.state); } },
          { label: 'Time', render: function (c) { return h('span', null, HP.left(c.expires_at), h('span', { class: 'sub', text: 'bought ' + HP.ago(c.starts_at) })); } },
          { label: 'Data', render: function (c) { return dataCell(c); } },
          { label: 'Last seen', render: function (c) { return HP.ago(c.last_seen_at); } },
          { label: '', render: function (c) { return revokeCell(c); } }
        ]
      }));
    }, HP.errorToast);
  }
  function stateChip(s) {
    return { ONLINE: HP.chip('Online', 'ok'), ACTIVE: HP.chip('Ready', ''), EXPIRED: HP.chip('Ended', 'off'),
      REVOKED: HP.chip('Revoked', 'bad'), DATA_FINISHED: HP.chip('Data used up', 'warn') }[s] || HP.chip(s, 'off');
  }
  function dataCell(c) {
    if (!c.data_limit_bytes) return h('span', null, HP.bytes(c.data_used_bytes), h('span', { class: 'sub', text: 'unlimited' }));
    var pct = c.data_used_bytes / c.data_limit_bytes * 100;
    return h('div', null, HP.meter(pct, pct >= 90), h('span', { class: 'sub', text: HP.bytes(c.data_used_bytes) + ' of ' + HP.bytes(c.data_limit_bytes) }));
  }
  function revokeCell(c) {
    if (c.state !== 'ONLINE' && c.state !== 'ACTIVE' && c.state !== 'DATA_FINISHED') return '';
    return h('div', { class: 'cell-actions' }, h('button', {
      class: 'btn btn-danger btn-sm', type: 'button', text: 'Revoke',
      onclick: function (e) {
        HP.confirm('Revoke this pass?', 'The customer ' + c.phone + ' loses access immediately and is disconnected. This cannot be undone and no refund is made automatically.', 'Revoke pass').then(function (yes) {
          if (!yes) return;
          HP.busy(e.target, function () {
            return HP.post('/api/tenant/customers/' + c.session_id + '/revoke', {}, staff).then(function (r) {
              HP.toast(r.warning || 'Pass revoked', r.warning ? '' : 'ok'); loadCustomers();
            });
          });
        });
      }
    }));
  }
  $('#cust-q').addEventListener('input', HP.debounce(loadCustomers, 300));

  // ---------------- plans ----------------
  function loadPlans() {
    var box = $('#plan-table'); HP.clear(box); box.appendChild(HP.skeleton());
    return HP.get('/api/tenant/plans', staff).then(function (r) {
      state.plans = r.plans; HP.clear(box);
      box.appendChild(HP.table({
        rows: r.plans, emptyTitle: 'No plans yet', emptyText: 'Add your first plan with the form on the left.',
        columns: [
          { label: 'Plan', render: function (p) { return h('b', { text: p.name }); } },
          { label: 'Price', num: true, render: function (p) { return HP.kes(p.price_cents); } },
          { label: 'Lasts', render: function (p) { return p.duration_text; } },
          { label: 'Data', render: function (p) { return p.data_text; } },
          { label: 'Speed', render: function (p) { return p.speed_limit ? '↑' + p.speed_limit.split('/')[0] + ' ↓' + p.speed_limit.split('/')[1] : 'Full speed'; } },
          { label: 'Sold', num: true, render: function (p) { return HP.num(p.sold); } },
          { label: 'Earned', num: true, render: function (p) { return HP.kes(p.gross_cents); } },
          { label: 'Status', render: function (p) { return p.active ? HP.chip('On sale', 'ok') : HP.chip('Archived', 'off'); } },
          { label: '', render: function (p) { return planActions(p); } }
        ]
      }));
    }, HP.errorToast);
  }
  function planActions(p) {
    return h('div', { class: 'cell-actions' },
      h('button', { class: 'btn btn-ghost btn-sm', type: 'button', text: 'Edit', onclick: function () { editPlan(p); } }),
      h('button', {
        class: 'btn btn-ghost btn-sm', type: 'button', text: p.active ? 'Archive' : 'Restore',
        onclick: function (e) {
          HP.busy(e.target, function () {
            return HP.post('/api/tenant/plans/' + p.id + (p.active ? '/archive' : '/restore'), {}, staff).then(function () {
              HP.toast(p.active ? 'Plan archived - hidden from customers' : 'Plan is on sale again', 'ok'); loadPlans();
            });
          });
        }
      }));
  }

  var planForm = $('#plan-form');
  function resetPlanForm() {
    planForm.reset(); $('#plan-id').value = ''; $('#plan-form-title').textContent = 'Add a plan';
    $('#plan-cancel').classList.add('hidden'); $('#plan-msg').textContent = '';
    $('#plan-dur-unit').value = '60'; $('#plan-data-unit').value = '1024';
  }
  function editPlan(p) {
    show('plans');
    $('#plan-id').value = p.id; $('#plan-form-title').textContent = 'Edit plan';
    $('#plan-name').value = p.name; $('#plan-price').value = p.price_cents / 100;
    var m = p.duration_minutes;
    if (m % 1440 === 0) { $('#plan-dur').value = m / 1440; $('#plan-dur-unit').value = '1440'; }
    else if (m % 60 === 0) { $('#plan-dur').value = m / 60; $('#plan-dur-unit').value = '60'; }
    else { $('#plan-dur').value = m; $('#plan-dur-unit').value = '1'; }
    if (p.data_limit_bytes) {
      var mb = Math.round(p.data_limit_bytes / 1048576);
      if (mb % 1024 === 0) { $('#plan-data').value = mb / 1024; $('#plan-data-unit').value = '1024'; }
      else { $('#plan-data').value = mb; $('#plan-data-unit').value = '1'; }
    } else { $('#plan-data').value = ''; }
    var sp = (p.speed_limit || '').split('/');
    $('#plan-up').value = sp[0] || ''; $('#plan-down').value = sp[1] || '';
    $('#plan-cancel').classList.remove('hidden');
    $('#plan-name').focus();
  }
  $('#plan-cancel').addEventListener('click', resetPlanForm);
  planForm.addEventListener('submit', function (e) {
    e.preventDefault();
    var msg = $('#plan-msg'); msg.className = 'form-msg'; msg.textContent = '';
    var up = $('#plan-up').value.trim(), down = $('#plan-down').value.trim();
    if ((up && !down) || (!up && down)) { msg.className = 'form-msg bad'; msg.textContent = 'Fill both speed boxes (upload and download) or leave both empty.'; return; }
    var dataVal = $('#plan-data').value.trim();
    var body = {
      name: $('#plan-name').value,
      price_kes: parseInt($('#plan-price').value, 10) || 0,
      duration_minutes: Math.round((parseFloat($('#plan-dur').value) || 0) * parseInt($('#plan-dur-unit').value, 10)),
      data_limit_mb: dataVal ? Math.round(parseFloat(dataVal) * parseInt($('#plan-data-unit').value, 10)) : null,
      speed_limit: up ? up + '/' + down : null
    };
    var id = $('#plan-id').value;
    HP.busy($('#plan-save'), function () {
      return (id ? HP.api('PUT', '/api/tenant/plans/' + id, body, staff) : HP.post('/api/tenant/plans', body, staff)).then(function () {
        HP.toast(id ? 'Plan updated' : 'Plan added', 'ok'); resetPlanForm(); loadPlans();
      }, function (err) { msg.className = 'form-msg bad'; msg.textContent = err.message; });
    });
  });

  // ---------------- routers ----------------
  function loadRouters() {
    var box = $('#router-table'); HP.clear(box); box.appendChild(HP.skeleton());
    return HP.get('/api/tenant/devices', staff).then(function (r) {
      HP.clear(box);
      box.appendChild(HP.table({
        rows: r.devices, emptyTitle: 'No routers yet', emptyText: 'Register your MikroTik with the form on the left. You get a script to paste into WinBox.',
        columns: [
          { label: 'Router', render: function (d) { return h('span', null, h('b', { text: d.name || 'Unnamed' }), h('span', { class: 'sub mono', text: d.mac_address })); } },
          { label: 'Status', render: function (d) { return d.online ? HP.chip('Online', 'ok') : HP.chip('Offline', 'off'); } },
          { label: 'Last check-in', render: function (d) { return HP.ago(d.last_seen_at); } },
          { label: 'Tunnel IP', render: function (d) { return h('span', { class: 'mono', text: d.nas_ip_address || 'not set' }); } },
          { label: 'People on it', num: true, render: function (d) { return HP.num(d.active_users); } },
          { label: '', render: function (d) { return routerActions(d); } }
        ]
      }));
    }, HP.errorToast);
  }
  function routerActions(d) {
    return h('div', { class: 'cell-actions' },
      h('button', { class: 'btn btn-ghost btn-sm', type: 'button', text: 'Setup script', onclick: function (e) {
        HP.busy(e.target, function () { return HP.get('/api/tenant/devices/' + d.id + '/script', staff).then(function (r) { showScript(r.script, d.name || 'router'); }); });
      } }),
      h('button', { class: 'btn btn-ghost btn-sm', type: 'button', text: 'Edit', onclick: function () { editRouter(d); } }),
      h('button', { class: 'btn btn-danger btn-sm', type: 'button', text: 'Remove', onclick: function (e) {
        HP.confirm('Remove ' + (d.name || 'this router') + '?', 'Customers on this router will no longer be able to log in. You can register it again later.', 'Remove router').then(function (yes) {
          if (!yes) return;
          HP.busy(e.target, function () { return HP.api('DELETE', '/api/tenant/devices/' + d.id, undefined, staff).then(function () { HP.toast('Router removed', 'ok'); loadRouters(); }); });
        });
      } }));
  }
  function showScript(script, name) {
    var pre = h('pre', { class: 'code', text: script });
    HP.modal({
      wide: true, title: 'Setup script for ' + name,
      sub: 'Open WinBox → New Terminal, paste this whole script and press Enter. Keep it private: it contains this router\'s RADIUS secret.',
      body: h('div', null, pre,
        h('div', { class: 'notice info small', text: 'Before pasting: the router needs its WireGuard tunnel to the server, and your hotspot must already be set up on its LAN interface.' })),
      actions: [{ label: 'Copy script', kind: 'btn', onclick: function () { HP.copy(script).then(function (ok) { HP.toast(ok ? 'Script copied' : 'Select the text and press Ctrl+C', ok ? 'ok' : ''); }); return false; } }, { label: 'Done' }]
    });
  }
  var routerForm = $('#router-form');
  function resetRouterForm() {
    routerForm.reset(); $('#router-id').value = ''; $('#router-form-title').textContent = 'Register a router';
    $('#router-cancel').classList.add('hidden'); $('#router-msg').textContent = '';
    $('#router-mac').disabled = false; $('#router-save').textContent = 'Register & get script';
  }
  function editRouter(d) {
    show('routers');
    $('#router-id').value = d.id; $('#router-form-title').textContent = 'Edit router';
    $('#router-name').value = d.name; $('#router-mac').value = d.mac_address; $('#router-mac').disabled = true;
    $('#router-ip').value = d.nas_ip_address || '';
    $('#router-cancel').classList.remove('hidden'); $('#router-save').textContent = 'Save changes';
  }
  $('#router-cancel').addEventListener('click', resetRouterForm);
  routerForm.addEventListener('submit', function (e) {
    e.preventDefault();
    var msg = $('#router-msg'); msg.className = 'form-msg'; msg.textContent = '';
    var id = $('#router-id').value, ip = $('#router-ip').value.trim();
    var body = { name: $('#router-name').value, mac_address: $('#router-mac').value, nas_ip_address: ip || null };
    HP.busy($('#router-save'), function () {
      return (id ? HP.api('PUT', '/api/tenant/devices/' + id, body, staff) : HP.post('/api/tenant/devices', body, staff)).then(function (r) {
        var name = body.name || 'router';
        resetRouterForm(); loadRouters(); loadOverviewQuiet();
        showScript(r.script, name);
      }, function (err) { msg.className = 'form-msg bad'; msg.textContent = err.message; });
    });
  });
  function loadOverviewQuiet() { HP.get('/api/tenant/overview', staff).then(function (o) { state.overview = o; setCount('#n-routers', o.routers_online + '/' + o.routers, o.routers > 0); }, function () {}); }

  // ---------------- announcements ----------------
  function loadNotes() {
    var box = $('#note-table'); HP.clear(box); box.appendChild(HP.skeleton());
    return HP.get('/api/tenant/announcements', staff).then(function (r) {
      HP.clear(box);
      box.appendChild(HP.table({
        rows: r.announcements, emptyTitle: 'No announcements', emptyText: 'Publish one with the form on the left. Customers see it on their page straight away.',
        columns: [
          { label: 'Message', render: function (a) { return a.message; } },
          { label: 'Published', render: function (a) { return HP.ago(a.created_at); } },
          { label: 'Status', render: function (a) { return new Date(a.expires_at) > new Date() ? HP.chip('Showing · ' + HP.left(a.expires_at), 'ok') : HP.chip('Expired', 'off'); } },
          { label: '', render: function (a) { return h('div', { class: 'cell-actions' }, h('button', { class: 'btn btn-ghost btn-sm', type: 'button', text: 'Remove', onclick: function (e) {
            HP.busy(e.target, function () { return HP.api('DELETE', '/api/tenant/announcements/' + a.id, undefined, staff).then(function () { HP.toast('Removed', 'ok'); loadNotes(); }); });
          } })); } }
        ]
      }));
    }, HP.errorToast);
  }
  $('#note-text').addEventListener('input', function () { $('#note-count').textContent = this.value.length + ' / 280'; });
  $('#note-form').addEventListener('submit', function (e) {
    e.preventDefault();
    var msg = $('#note-msg'); msg.className = 'form-msg'; msg.textContent = '';
    HP.busy(e.target.querySelector('button[type=submit]'), function () {
      return HP.post('/api/tenant/announcements', { message: $('#note-text').value, hours: parseInt($('#note-hours').value, 10) }, staff).then(function () {
        $('#note-text').value = ''; $('#note-count').textContent = '0 / 280'; HP.toast('Published to your customers', 'ok'); loadNotes();
      }, function (err) { msg.className = 'form-msg bad'; msg.textContent = err.message; });
    });
  });

  // ---------------- settings ----------------
  $('#pw-form').addEventListener('submit', function (e) {
    e.preventDefault();
    var msg = $('#pw-msg'); msg.className = 'form-msg'; msg.textContent = '';
    HP.busy(e.target.querySelector('button[type=submit]'), function () {
      return HP.post('/api/auth/password', { current: $('#pw-cur').value, 'new': $('#pw-new').value }, staff).then(function () {
        $('#pw-form').reset(); msg.className = 'form-msg ok'; msg.textContent = 'Password updated.';
      }, function (err) { msg.className = 'form-msg bad'; msg.textContent = err.message; });
    });
  });

  // ---------------- live updates ----------------
  var refreshSoon = HP.debounce(function () { if (current === 'overview') loadOverview(); else loaders[current](); }, 400);
  HP.live('/api/tenant/stream', {
    sale: function (d) {
      HP.toast('New sale: ' + (d && d.plan_name ? d.plan_name + ' · ' : '') + HP.kes(d ? d.amount_cents : 0), 'ok');
      refreshSoon();
    },
    session: refreshSoon,
    device: refreshSoon
  }, $('#live'));
  // Router "online" is time-based, so refresh the numbers now and then even without events.
  setInterval(function () { if (!document.hidden) refreshSoon(); }, 60000);
})();