(function () {
  'use strict';
  var h = HP.h, $ = HP.$;
  var staff = { staff: true };
  var tenantsCache = [];

  $('#logout').addEventListener('click', function () { HP.logout(); });

  var loaders = { overview: loadOverview, tenants: loadTenants, payments: loadPayments, routers: loadRouters, settings: function () {} };
  var current = 'overview';
  HP.views(function (name) { current = name; loaders[name](); });

  function row(label, value) {
    return h('div', { class: 'row' }, h('span', { class: 'muted grow', text: label }), h('b', { text: value }));
  }

  // ---------------- overview ----------------
  function loadOverview() {
    return HP.get('/api/admin/overview', staff).then(function (o) {
      var kp = $('#ov-kpis'); HP.clear(kp);
      kp.append(
        HP.kpi('Your fees today', HP.kes(o.today.fee_cents), HP.kes(o.today.gross_cents) + ' paid · ' + o.today.sales + ' sales'),
        HP.kpi('Fees this week', HP.kes(o.week.fee_cents), HP.kes(o.week.gross_cents) + ' paid', 'green'),
        HP.kpi('Fees this month', HP.kes(o.month.fee_cents), HP.kes(o.month.gross_cents) + ' paid', 'orange'),
        HP.kpi('Online now', HP.num(o.online_now), HP.num(o.active_passes) + ' active passes', 'purple')
      );
      $('#ov-sub').textContent = 'All time: ' + HP.kes(o.all.gross_cents) + ' paid by customers, ' + HP.kes(o.all.fee_cents) + ' your fee, ' + HP.num(o.all.sales) + ' sales.';
      var ch = $('#ov-chart'); HP.clear(ch); ch.appendChild(HP.chart(o.daily, true));

      var top = $('#ov-top'); HP.clear(top);
      top.appendChild(HP.table({
        rows: o.top_tenants, emptyTitle: 'No sales yet', emptyText: 'Businesses with sales show here.',
        columns: [
          { label: 'Business', render: function (t) { return t.name; } },
          { label: 'Paid', num: true, render: function (t) { return HP.kes(t.gross_30d_cents); } },
          { label: 'Fee', num: true, render: function (t) { return HP.kes(t.fee_30d_cents); } }
        ]
      }));

      var side = $('#side-summary'); HP.clear(side);
      side.append(
        row('Businesses', o.tenants.active + ' active / ' + o.tenants.total),
        row('Suspended', String(o.tenants.suspended)),
        row('Routers online', o.routers_online + ' of ' + o.routers),
        row('People online', HP.num(o.online_now))
      );
      setCount('#n-tenants', o.tenants.total, o.tenants.total > 0);
      setCount('#n-routers', o.routers_online + '/' + o.routers, o.routers > 0);
      return HP.get('/api/admin/payments?status=SUCCESS&limit=8', staff);
    }, HP.errorToast).then(function (r) { if (r) renderFeed(r.payments); }, function () {});
  }
  function setCount(sel, text, show) { var el = $(sel); el.textContent = text; el.classList.toggle('hidden', !show); }
  function renderFeed(payments) {
    var feed = $('#ov-feed'); HP.clear(feed);
    if (!payments.length) { feed.appendChild(HP.empty('No sales yet', 'Sales from every business appear here as they happen.')); return; }
    payments.forEach(function (p) {
      feed.appendChild(h('div', { class: 'feed-item' },
        h('div', { class: 'feed-icon', text: '✓' }),
        h('div', { class: 'grow' }, h('b', { text: p.tenant_name + ' · ' + p.plan_name }), h('div', { class: 'muted small', text: p.phone.slice(0, 4) + '***' + p.phone.slice(-3) + ' · ' + HP.ago(p.created_at) })),
        h('div', { class: 'right' }, h('b', { text: HP.kes(p.amount_cents) }), h('div', { class: 'muted small', text: 'fee ' + HP.kes(p.fee_cents) }))));
    });
  }

  // ---------------- businesses ----------------
  function loadTenants() {
    var box = $('#tenant-table'); HP.clear(box); box.appendChild(HP.skeleton());
    return HP.get('/api/admin/tenants', staff).then(function (r) {
      tenantsCache = r.tenants; fillTenantSelect();
      HP.clear(box);
      box.appendChild(HP.table({
        rows: r.tenants, emptyTitle: 'No businesses yet', emptyText: 'Add the first one with the form on the left.',
        columns: [
          { label: 'Business', render: function (t) { return h('span', null, h('b', { text: t.name }), h('span', { class: 'sub', text: t.subdomain + ' · ' + (t.owner_email || 'no owner') })); } },
          { label: 'Status', render: function (t) { return t.status === 'ACTIVE' ? HP.chip('Active', 'ok') : HP.chip('Suspended', 'bad'); } },
          { label: 'Fee', num: true, render: function (t) { return (t.commission_bps / 100) + '%'; } },
          { label: 'Routers', num: true, render: function (t) { return t.routers_online + '/' + t.routers; } },
          { label: 'Online', num: true, render: function (t) { return HP.num(t.online_now); } },
          { label: 'Paid (30d)', num: true, render: function (t) { return HP.kes(t.gross_30d_cents); } },
          { label: 'Your fee (30d)', num: true, render: function (t) { return HP.kes(t.fee_30d_cents); } },
          { label: '', render: function (t) { return tenantActions(t); } }
        ]
      }));
    }, HP.errorToast);
  }
  function tenantActions(t) {
    var suspended = t.status !== 'ACTIVE';
    return h('div', { class: 'cell-actions' },
      h('button', { class: 'btn btn-ghost btn-sm', type: 'button', text: 'Fee %', onclick: function () { editCommission(t); } }),
      h('button', { class: 'btn btn-ghost btn-sm', type: 'button', text: 'Reset password', onclick: function () { resetPassword(t); } }),
      h('button', {
        class: 'btn btn-sm ' + (suspended ? 'btn-green' : 'btn-warn'), type: 'button', text: suspended ? 'Activate' : 'Suspend',
        onclick: function (e) {
          var go = function () {
            HP.busy(e.target, function () {
              return HP.api('PATCH', '/api/admin/tenants/' + t.id, { status: suspended ? 'ACTIVE' : 'SUSPENDED' }, staff).then(function () {
                HP.toast(t.name + (suspended ? ' is active again' : ' is suspended'), 'ok'); loadTenants();
              });
            });
          };
          if (suspended) go();
          else HP.confirm('Suspend ' + t.name + '?', 'Their customer page stops working until you activate them again. Passes already sold keep working on the router.', 'Suspend', 'btn-warn').then(function (y) { if (y) go(); });
        }
      }));
  }
  function editCommission(t) {
    var input = h('input', { type: 'number', min: '0', max: '100', step: '0.5', value: String(t.commission_bps / 100) });
    HP.modal({
      title: 'Commission for ' + t.name, sub: 'Applies to new sales only. Sales already made keep the fee recorded at the time.',
      body: h('label', null, 'Percent of each sale', input),
      actions: [{ label: 'Cancel' }, { label: 'Save', kind: 'btn', onclick: function () {
        var pct = parseFloat(input.value);
        if (isNaN(pct) || pct < 0 || pct > 100) { HP.toast('Enter a number from 0 to 100', 'bad'); return false; }
        HP.api('PATCH', '/api/admin/tenants/' + t.id, { commission_bps: Math.round(pct * 100) }, staff).then(function () { HP.toast('Commission updated', 'ok'); loadTenants(); }, HP.errorToast);
      } }]
    });
  }
  function resetPassword(t) {
    HP.confirm('Reset the owner password?', 'A new password is generated for ' + (t.owner_email || t.name) + '. You will see it once - share it with them securely.', 'Reset password', 'btn-warn').then(function (y) {
      if (!y) return;
      HP.post('/api/admin/tenants/' + t.id + '/reset-password', {}, staff).then(function (r) { showCredentials('New password', r.owner_email, r.temp_password); }, HP.errorToast);
    });
  }
  function showCredentials(title, email, password) {
    HP.modal({
      title: title, sub: 'This is shown only once. Copy it now and send it to the owner privately.',
      body: h('div', null, h('p', { class: 'muted small', text: 'Email' }), h('div', { class: 'secret', text: email }), h('p', { class: 'muted small', text: 'Temporary password' }),
        h('div', { class: 'secret', text: password }), h('div', { class: 'notice small', text: 'Ask the owner to change it in Settings after their first sign-in.' })),
      actions: [{ label: 'Copy both', kind: 'btn', onclick: function () { HP.copy('Email: ' + email + '\nPassword: ' + password).then(function (ok) { HP.toast(ok ? 'Copied' : 'Select and copy manually', ok ? 'ok' : ''); }); return false; } }, { label: 'I have saved it' }]
    });
  }

  $('#tenant-form').addEventListener('submit', function (e) {
    e.preventDefault();
    var msg = $('#t-msg'); msg.className = 'form-msg'; msg.textContent = '';
    var pct = parseFloat($('#t-comm').value);
    if (isNaN(pct) || pct < 0 || pct > 100) { msg.className = 'form-msg bad'; msg.textContent = 'Commission must be from 0 to 100.'; return; }
    HP.busy($('#t-save'), function () {
      return HP.post('/api/admin/tenants', {
        name: $('#t-name').value, subdomain: $('#t-sub').value, owner_name: $('#t-owner').value, owner_email: $('#t-email').value,
        commission_bps: Math.round(pct * 100), seed_plans: $('#t-seed').checked
      }, staff).then(function (r) {
        $('#tenant-form').reset(); $('#t-comm').value = '10'; $('#t-seed').checked = true;
        showCredentials('Business created: ' + r.tenant.name, r.owner_email, r.temp_password);
        loadTenants();
      }, function (err) { msg.className = 'form-msg bad'; msg.textContent = err.message; });
    });
  });

  // ---------------- payments ----------------
  function fillTenantSelect() {
    var sel = $('#pay-tenant'), keep = sel.value;
    HP.clear(sel); sel.appendChild(h('option', { value: '', text: 'All businesses' }));
    tenantsCache.forEach(function (t) { sel.appendChild(h('option', { value: t.id, text: t.name })); });
    sel.value = keep;
  }
  function loadPayments() {
    var box = $('#pay-table'); HP.clear(box); box.appendChild(HP.skeleton());
    var pre = tenantsCache.length ? Promise.resolve() : HP.get('/api/admin/tenants', staff).then(function (r) { tenantsCache = r.tenants; fillTenantSelect(); });
    return pre.then(function () {
      var q = '?limit=200';
      if ($('#pay-tenant').value) q += '&tenant_id=' + encodeURIComponent($('#pay-tenant').value);
      if ($('#pay-status').value) q += '&status=' + $('#pay-status').value;
      return HP.get('/api/admin/payments' + q, staff);
    }).then(function (r) {
      HP.clear(box);
      box.appendChild(HP.table({
        rows: r.payments, emptyTitle: 'No payments found',
        columns: [
          { label: 'When', render: function (p) { return h('span', null, HP.when(p.created_at), h('span', { class: 'sub', text: HP.ago(p.created_at) })); } },
          { label: 'Business', render: function (p) { return p.tenant_name; } },
          { label: 'Customer', render: function (p) { return h('span', { class: 'mono', text: p.phone }); } },
          { label: 'Plan', render: function (p) { return p.plan_name; } },
          { label: 'Paid', num: true, render: function (p) { return HP.kes(p.amount_cents); } },
          { label: 'Your fee', num: true, render: function (p) { return p.status === 'SUCCESS' ? HP.kes(p.fee_cents) : '-'; } },
          { label: 'Status', render: function (p) { return p.status === 'SUCCESS' ? HP.chip('Paid', 'ok') : p.status === 'PENDING' ? HP.chip('Waiting', 'warn') : h('span', { title: p.note || '' }, HP.chip('Failed', 'bad')); } },
          { label: 'M-Pesa receipt', render: function (p) { return h('span', { class: 'mono small', text: p.receipt || '-' }); } }
        ]
      }));
    }, HP.errorToast);
  }
  $('#pay-tenant').addEventListener('change', loadPayments);
  $('#pay-status').addEventListener('change', loadPayments);

  // ---------------- routers ----------------
  function loadRouters() {
    var box = $('#router-table'); HP.clear(box); box.appendChild(HP.skeleton());
    return HP.get('/api/admin/devices', staff).then(function (r) {
      HP.clear(box);
      box.appendChild(HP.table({
        rows: r.devices, emptyTitle: 'No routers registered', emptyText: 'Owners register their routers from their own dashboard.',
        columns: [
          { label: 'Router', render: function (d) { return h('span', null, h('b', { text: d.name || 'Unnamed' }), h('span', { class: 'sub mono', text: d.mac_address })); } },
          { label: 'Business', render: function (d) { return d.tenant_name; } },
          { label: 'Status', render: function (d) { return d.online ? HP.chip('Online', 'ok') : HP.chip('Offline', 'off'); } },
          { label: 'Last check-in', render: function (d) { return HP.ago(d.last_seen_at); } },
          { label: 'Tunnel IP', render: function (d) { return h('span', { class: 'mono', text: d.nas_ip_address || 'not set' }); } },
          { label: 'People on it', num: true, render: function (d) { return HP.num(d.active_users); } }
        ]
      }));
    }, HP.errorToast);
  }

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

  // ---------------- live ----------------
  var refreshSoon = HP.debounce(function () { loaders[current](); }, 400);
  HP.live('/api/admin/stream', {
    sale: function (d) { HP.toast('New sale: ' + (d && d.plan_name ? d.plan_name + ' · ' : '') + HP.kes(d ? d.amount_cents : 0), 'ok'); refreshSoon(); },
    tenant: refreshSoon, device: refreshSoon
  }, $('#live'));
  setInterval(function () { if (!document.hidden) refreshSoon(); }, 60000);
})();