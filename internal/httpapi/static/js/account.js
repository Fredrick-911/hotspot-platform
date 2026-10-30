(function () {
  'use strict';
  var h = HP.h, $ = HP.$;
  var tick = null, fetchedAt = 0, acc = null;

  function showLogin() {
    stopTick(); $('#loading').classList.add('hidden'); $('#pass').classList.add('hidden');
    $('#login').classList.remove('hidden'); $('#logout').classList.add('hidden');
  }
  function stopTick() { if (tick) { clearInterval(tick); tick = null; } }

  function load() {
    return HP.get('/api/customer/me').then(function (a) {
      acc = a; fetchedAt = performance.now();
      $('#loading').classList.add('hidden'); $('#login').classList.add('hidden');
      $('#logout').classList.remove('hidden'); $('#pass').classList.remove('hidden');
      render(a);
    }, function (err) {
      if (err.status === 401) showLogin();
      else { $('#loading').classList.add('hidden'); HP.errorToast(err); }
    });
  }

  var STATE = {
    ONLINE: ['Connected', 'ok'], ACTIVE: ['Ready to use', ''], EXPIRED: ['Ended', 'off'],
    REVOKED: ['Cancelled', 'bad'], DATA_FINISHED: ['Data used up', 'warn']
  };

  function secondsLeft() {
    if (!acc) return 0;
    if (acc.state === 'EXPIRED' || acc.state === 'REVOKED') return 0;
    return Math.max(0, acc.seconds_left - (performance.now() - fetchedAt) / 1000);
  }

  function render(a) {
    var box = $('#pass'); HP.clear(box);
    var st = STATE[a.state] || [a.state, 'off'];
    var live = a.state === 'ONLINE' || a.state === 'ACTIVE';

    var countdown = h('div', { class: 'countdown', id: 'cd', text: HP.clock(secondsLeft()) });
    var total = a.duration_minutes * 60;
    var timeMeter = HP.meter(live ? (secondsLeft() / total) * 100 : 0, false);
    timeMeter.id = 'time-meter';

    var stats = h('div', { class: 'pass-grid' });
    if (a.data_limit_bytes) {
      var pct = a.data_used_bytes / a.data_limit_bytes * 100;
      stats.appendChild(h('div', { class: 'pass-stat' }, h('div', { class: 'kpi-label', text: 'Data left' }),
        h('div', { class: 'kpi-value', text: HP.bytes(a.data_left_bytes) }), HP.meter(pct, pct >= 90),
        h('div', { class: 'kpi-sub', text: HP.bytes(a.data_used_bytes) + ' of ' + HP.bytes(a.data_limit_bytes) + ' used' })));
    } else {
      stats.appendChild(h('div', { class: 'pass-stat' }, h('div', { class: 'kpi-label', text: 'Data' }),
        h('div', { class: 'kpi-value', text: 'Unlimited' }), h('div', { class: 'kpi-sub', text: HP.bytes(a.data_used_bytes) + ' used' })));
    }
    stats.appendChild(h('div', { class: 'pass-stat' }, h('div', { class: 'kpi-label', text: 'Speed' }),
      h('div', { class: 'kpi-value', text: a.speed_limit ? a.speed_limit.split('/')[1] + ' down' : 'Full speed' }),
      h('div', { class: 'kpi-sub', text: a.speed_limit ? a.speed_limit.split('/')[0] + ' up' : 'no cap' })));
    stats.appendChild(h('div', { class: 'pass-stat' }, h('div', { class: 'kpi-label', text: a.state === 'EXPIRED' ? 'Ended' : 'Ends' }),
      h('div', { class: 'kpi-value', text: HP.when(a.expires_at) }), h('div', { class: 'kpi-sub', text: 'bought ' + HP.when(a.starts_at) })));

    var main = h('div', { class: 'card' },
      h('div', { class: 'card-head' }, h('div', null, h('h2', { text: a.plan_name }), h('div', { class: 'sub mono', text: a.phone })), HP.chip(st[0], st[1])),
      h('div', { class: 'center' }, h('div', { class: 'muted small', text: live ? 'Time left' : 'This pass has ended' }), countdown),
      timeMeter, stats);
    box.appendChild(main);

    if (live) {
      var pinBox = h('div', { class: 'pin-box', title: 'Tap to copy' }, h('div', { class: 'pin', text: a.pin }), h('div', { class: 'small muted', text: 'Tap to copy your PIN' }));
      pinBox.addEventListener('click', function () { HP.copy(a.pin).then(function (ok) { HP.toast(ok ? 'PIN copied' : 'Long-press the PIN to copy', ok ? 'ok' : ''); }); });
      box.appendChild(h('div', { class: 'card' },
        h('h2', { text: 'Reconnect to the WiFi' }),
        h('p', { class: 'muted small', text: 'If you get logged out or switch device, open the WiFi login page and enter:' }),
        h('div', { class: 'row' }, h('span', { class: 'muted' }, 'Username'), h('b', { class: 'mono', text: a.username })),
        pinBox,
        h('p', { class: 'muted small', text: 'Or go to the main page and use "Already paid?" with your phone number and this PIN.' })));
    } else {
      box.appendChild(h('div', { class: 'notice info' }, 'Need more time? Buy a new pass from the main page.'));
    }

    if (a.receipts && a.receipts.length) {
      box.appendChild(h('div', { class: 'card' }, h('div', { class: 'card-head' }, h('h2', { text: 'Your payments' })),
        HP.table({
          rows: a.receipts, columns: [
            { label: 'Date', render: function (p) { return HP.when(p.created_at); } },
            { label: 'Plan', render: function (p) { return p.plan_name; } },
            { label: 'Amount', num: true, render: function (p) { return HP.kes(p.amount_cents); } },
            { label: 'Status', render: function (p) { return p.status === 'SUCCESS' ? HP.chip('Paid', 'ok') : p.status === 'PENDING' ? HP.chip('Waiting', 'warn') : HP.chip('Failed', 'bad'); } },
            { label: 'Receipt', render: function (p) { return h('span', { class: 'mono small', text: p.receipt || '-' }); } }
          ]
        })));
    }
    renderBanners(a.announcements || []);
    stopTick();
    if (live) tick = setInterval(update, 1000);
  }

  function update() {
    var s = secondsLeft();
    var cd = $('#cd'); if (cd) cd.textContent = HP.clock(s);
    var m = $('#time-meter > i'); if (m && acc) m.style.width = Math.max(0, Math.min(100, s / (acc.duration_minutes * 60) * 100)) + '%';
    if (s <= 0) { stopTick(); load(); }
  }

  function renderBanners(list) {
    var box = $('#banners'); HP.clear(box);
    list.forEach(function (a) { box.appendChild(h('div', { class: 'banner' }, h('span', { 'aria-hidden': 'true', text: '📢' }), h('span', { text: a.message }))); });
  }

  $('#login').addEventListener('submit', function (e) {
    e.preventDefault();
    var msg = $('#l-msg'), btn = $('#l-go');
    msg.className = 'form-msg';
    var phone = $('#l-phone').value.trim(), pin = $('#l-pin').value.trim();
    if (!phone || pin.length !== 4) { msg.className = 'form-msg bad'; msg.textContent = 'Enter your phone number and the 4-digit PIN.'; return; }
    btn.disabled = true; btn.textContent = 'Checking…';
    HP.post('/api/customer/login', { phone: phone, pin: pin }).then(load, function (err) {
      msg.className = 'form-msg bad'; msg.textContent = err.message;
    }).then(function () { btn.disabled = false; btn.textContent = 'Show my pass'; });
  });
  $('#logout').addEventListener('click', function () {
    HP.post('/api/customer/logout').then(function () { acc = null; showLogin(); }, HP.errorToast);
  });

  try {
    HP.live('/api/portal/stream', { announcements: function () { if (acc) load(); } });
  } catch (e) { /* ignore */ }
  setInterval(function () { if (acc && !document.hidden) load(); }, 30000);
  load();
})();