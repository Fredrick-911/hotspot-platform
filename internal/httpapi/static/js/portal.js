(function () {
  'use strict';
  var h = HP.h, $ = HP.$;
  var q = new URLSearchParams(location.search);
  var linkLogin = q.get('link-login') || '';
  var linkOrig = q.get('link-orig') || '';
  var mac = q.get('mac') || '';
  var isDev = document.body.dataset.dev === 'true';
  var planId = null, pollTimer = null, creds = null;

  var steps = ['phone', 'wait', 'done', 'fail'];
  function step(name) { steps.forEach(function (s) { $('#step-' + s).classList.toggle('hidden', s !== name); }); }
  function openDialog() { $('#dlg').classList.remove('hidden'); }
  function closeDialog() { stopPolling(); $('#dlg').classList.add('hidden'); }
  function stopPolling() { if (pollTimer) { clearInterval(pollTimer); pollTimer = null; } }
  function remember(id) { try { if (id) sessionStorage.setItem('hp_pending', id); else sessionStorage.removeItem('hp_pending'); } catch (e) { /* storage blocked */ } }

  // ---------- buy ----------
  HP.$all('.buy').forEach(function (b) {
    b.addEventListener('click', function () {
      planId = b.dataset.plan;
      $('#dlg-title').textContent = b.dataset.name;
      $('#dlg-sub').textContent = 'KES ' + b.dataset.price + ' · you will get an M-Pesa prompt on your phone.';
      $('#p-msg').textContent = ''; $('#p-pay').disabled = false; $('#p-pay').textContent = 'Pay with M-Pesa';
      step('phone'); openDialog(); $('#p-phone').focus();
    });
  });
  $('#p-cancel').addEventListener('click', closeDialog);
  $('#w-cancel').addEventListener('click', closeDialog);
  $('#f-retry').addEventListener('click', function () { step('phone'); });
  $('#p-phone').addEventListener('keydown', function (e) { if (e.key === 'Enter') $('#p-pay').click(); });

  $('#p-pay').addEventListener('click', function () {
    var phone = $('#p-phone').value.trim(), msg = $('#p-msg'), btn = $('#p-pay');
    msg.textContent = '';
    if (!phone) { msg.textContent = 'Enter your M-Pesa phone number.'; return; }
    btn.disabled = true; btn.textContent = 'Sending…';
    HP.post('/api/payments/initiate', { plan_id: planId, phone_number: phone, mac_address: mac }).then(function (r) {
      remember(r.payment_id);
      $('#w-dev').classList.toggle('hidden', !(isDev && r.dev_mode));
      $('#w-dev').onclick = function () {
        HP.post('/api/dev/confirm', { payment_id: r.payment_id }).catch(HP.errorToast);
      };
      $('#w-note').textContent = r.dev_mode ? r.message : '';
      step('wait'); poll(r.payment_id);
    }, function (err) {
      msg.textContent = err.message; btn.disabled = false; btn.textContent = 'Pay with M-Pesa';
    });
  });

  function poll(id) {
    stopPolling();
    var tries = 0;
    pollTimer = setInterval(function () {
      tries++;
      if (tries > 45) { // ~90 seconds
        stopPolling(); remember(null);
        $('#f-msg').textContent = 'We did not get a confirmation. If M-Pesa took your money, use "Already paid?" below with your phone number and PIN, or contact the owner.';
        step('fail'); return;
      }
      HP.get('/api/payments/status?id=' + encodeURIComponent(id)).then(function (r) {
        if (r.status === 'SUCCESS') { stopPolling(); remember(null); paid(r); }
        else if (r.status === 'FAILED') {
          stopPolling(); remember(null);
          $('#f-msg').textContent = r.message ? 'M-Pesa said: ' + r.message : 'The payment was cancelled or failed. You have not been charged.';
          step('fail');
        }
      }, function () { /* a blip: keep polling */ });
    }, 2000);
  }

  function paid(r) {
    creds = { username: r.username, pin: r.pin };
    $('#d-pin').textContent = r.pin;
    $('#d-account').href = '/account' + (HP.tenantParam ? '?tenant=' + encodeURIComponent(HP.tenantParam) : '');
    step('done'); openDialog();
    login(creds);
  }

  $('#d-pin-box').addEventListener('click', function () {
    if (!creds) return;
    HP.copy(creds.pin).then(function (ok) { $('#d-copy').textContent = ok ? 'Copied ✓' : 'Long-press the PIN to copy'; });
  });
  $('#d-go').addEventListener('click', function () { if (creds) login(creds, true); });

  // ---------- log in on the router ----------
  function login(c, now) {
    if (!linkLogin) {
      $('#d-note').textContent = 'You are paid up. Open the WiFi login page and sign in with this PIN to start browsing.';
      return;
    }
    $('#d-note').textContent = 'Connecting you to the internet…';
    $('#hs-form').action = linkLogin;
    $('#hs-user').value = c.username; $('#hs-pass').value = c.pin; $('#hs-dst').value = linkOrig || location.origin;
    setTimeout(function () { $('#hs-form').submit(); }, now ? 0 : 1800);
  }

  // ---------- reconnect ----------
  $('#restore').addEventListener('submit', function (e) {
    e.preventDefault();
    var msg = $('#r-msg'), btn = $('#r-go');
    msg.className = 'form-msg';
    var phone = $('#r-phone').value.trim(), pin = $('#r-pin').value.trim();
    if (!phone || pin.length !== 4) { msg.className = 'form-msg bad'; msg.textContent = 'Enter your phone number and the 4-digit PIN.'; return; }
    btn.disabled = true; btn.textContent = 'Checking…';
    HP.post('/api/sessions/reconnect', { phone: phone, pin: pin, mac_address: mac }).then(function (r) {
      creds = { username: r.username, pin: r.pin };
      msg.className = 'form-msg ok'; msg.textContent = 'Pass found - connecting you…';
      login(creds);
      btn.disabled = false; btn.textContent = 'Reconnect me';
    }, function (err) {
      msg.className = 'form-msg bad'; msg.textContent = err.message;
      btn.disabled = false; btn.textContent = 'Reconnect me';
    });
  });

  // ---------- live announcements / plan changes ----------
  function renderBanners(list) {
    var box = $('#banners'); HP.clear(box);
    list.forEach(function (a) { box.appendChild(h('div', { class: 'banner' }, h('span', { 'aria-hidden': 'true', text: '📢' }), h('span', { text: a.message }))); });
  }
  try {
    HP.live('/api/portal/stream', {
      announcements: function () { HP.get('/api/portal/announcements').then(function (r) { renderBanners(r.announcements); }, function () {}); },
      plans: function () { if ($('#dlg').classList.contains('hidden')) location.reload(); }
    });
  } catch (e) { /* captive-portal browsers may not support EventSource */ }

  // ---------- resume after a refresh ----------
  try {
    var pending = sessionStorage.getItem('hp_pending');
    if (pending) { step('wait'); openDialog(); poll(pending); }
  } catch (e) { /* ignore */ }
})();