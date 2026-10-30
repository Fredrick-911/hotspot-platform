/* Shared helpers for every page. No framework and no innerHTML with data:
   all text goes in through textContent, so a hostile plan name or phone number cannot inject markup. */
(function () {
  'use strict';

  var tenantParam = new URLSearchParams(location.search).get('tenant');

  // ---------- DOM builder ----------
  // h('div', {class:'x', onclick: fn, dataset:{a:1}}, 'text', childNode, [more])
  function h(tag, props) {
    var el = document.createElement(tag);
    if (props) {
      Object.keys(props).forEach(function (k) {
        var v = props[k];
        if (v == null || v === false) return;
        if (k === 'class') el.className = v;
        else if (k === 'text') el.textContent = v;
        else if (k === 'dataset') Object.keys(v).forEach(function (d) { el.dataset[d] = v[d]; });
        else if (k.slice(0, 2) === 'on' && typeof v === 'function') el.addEventListener(k.slice(2), v);
        else if (v === true) el.setAttribute(k, '');
        else el.setAttribute(k, v);
      });
    }
    for (var i = 2; i < arguments.length; i++) append(el, arguments[i]);
    return el;
  }
  function append(el, kid) {
    if (kid == null || kid === false) return;
    if (Array.isArray(kid)) { kid.forEach(function (k) { append(el, k); }); return; }
    el.appendChild(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); return el; }
  function $(sel, root) { return (root || document).querySelector(sel); }
  function $all(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }

  // ---------- formatting ----------
  var nf = new Intl.NumberFormat('en-KE');
  function kes(cents) {
    var v = (cents || 0) / 100;
    var whole = Math.abs(v - Math.round(v)) < 0.005;
    return 'KES ' + v.toLocaleString('en-KE', { minimumFractionDigits: whole ? 0 : 2, maximumFractionDigits: 2 });
  }
  function num(n) { return nf.format(n || 0); }
  function bytes(n) {
    n = n || 0;
    if (n < 1024) return n + ' B';
    var u = ['KB', 'MB', 'GB', 'TB'], i = -1;
    do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
    return (n >= 100 ? n.toFixed(0) : n.toFixed(1)).replace(/\.0$/, '') + ' ' + u[i];
  }
  function dur(sec) {
    sec = Math.max(0, Math.floor(sec || 0));
    var d = Math.floor(sec / 86400), hh = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
    if (d > 0) return d + 'd ' + hh + 'h';
    if (hh > 0) return hh + 'h ' + m + 'm';
    if (m > 0) return m + 'm ' + (s < 10 ? '0' : '') + s + 's';
    return s + 's';
  }
  function clock(sec) {
    sec = Math.max(0, Math.floor(sec || 0));
    var hh = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
    var p = function (x) { return (x < 10 ? '0' : '') + x; };
    return (hh > 99 ? Math.floor(hh / 24) + 'd ' + p(hh % 24) : p(hh)) + ':' + p(m) + ':' + p(s);
  }
  function ago(iso) {
    if (!iso) return 'never';
    var s = (Date.now() - new Date(iso).getTime()) / 1000;
    if (s < -60) return 'in ' + dur(-s);
    if (s < 45) return 'just now';
    if (s < 3600) return Math.round(s / 60) + ' min ago';
    if (s < 86400) return Math.round(s / 3600) + ' h ago';
    return Math.round(s / 86400) + ' d ago';
  }
  function left(iso) {
    var s = (new Date(iso).getTime() - Date.now()) / 1000;
    return s <= 0 ? 'ended' : dur(s) + ' left';
  }
  function when(iso) {
    return new Date(iso).toLocaleString('en-KE', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
  }
  function day(iso) {
    return new Date(iso).toLocaleDateString('en-KE', { day: 'numeric', month: 'short', year: 'numeric' });
  }

  // ---------- network ----------
  function withTenant(url) {
    if (!tenantParam || url.indexOf('tenant=') !== -1) return url;
    return url + (url.indexOf('?') === -1 ? '?' : '&') + 'tenant=' + encodeURIComponent(tenantParam);
  }
  function ApiError(status, message) { this.status = status; this.message = message; }

  // api('POST', '/api/x', {..}) -> parsed JSON. Rejects with ApiError.
  function api(method, url, body, opts) {
    opts = opts || {};
    var init = { method: method, credentials: 'same-origin', headers: { 'X-Requested-With': 'hotspot', 'Accept': 'application/json' } };
    if (body !== undefined) { init.headers['Content-Type'] = 'application/json'; init.body = JSON.stringify(body); }
    return fetch(withTenant(url), init).then(function (res) {
      return res.text().then(function (text) {
        var data = null;
        try { data = text ? JSON.parse(text) : null; } catch (e) { /* not JSON */ }
        if (res.ok) return data;
        if (res.status === 401 && opts.staff) { location.href = '/login'; }
        throw new ApiError(res.status, (data && data.error) || 'Something went wrong (' + res.status + ')');
      });
    }, function () { throw new ApiError(0, 'You appear to be offline. Check your connection.'); });
  }
  var get = function (u, o) { return api('GET', u, undefined, o); };
  var post = function (u, b, o) { return api('POST', u, b === undefined ? {} : b, o); };

  // ---------- toast, modal, confirm ----------
  var toastBox;
  function toast(msg, kind) {
    if (!toastBox) { toastBox = h('div', { class: 'toasts', role: 'status', 'aria-live': 'polite' }); document.body.appendChild(toastBox); }
    var t = h('div', { class: 'toast ' + (kind || '') }, msg);
    toastBox.appendChild(t);
    setTimeout(function () { t.remove(); }, kind === 'bad' ? 6500 : 3800);
  }
  function errorToast(e) { toast(e && e.message ? e.message : 'Something went wrong', 'bad'); }

  // modal({title, body: Node, actions: [{label, kind, onclick}], wide}) -> {close}
  function modal(o) {
    var prevFocus = document.activeElement;
    var overlay = h('div', { class: 'overlay', role: 'dialog', 'aria-modal': 'true' });
    var card = h('div', { class: 'modal' + (o.wide ? ' wide' : '') });
    function close() { overlay.remove(); document.removeEventListener('keydown', onKey); if (prevFocus && prevFocus.focus) prevFocus.focus(); }
    function onKey(e) { if (e.key === 'Escape') close(); }
    card.appendChild(h('h2', { text: o.title }));
    if (o.sub) card.appendChild(h('p', { class: 'muted small', text: o.sub }));
    if (o.body) card.appendChild(o.body);
    var acts = h('div', { class: 'modal-actions' });
    (o.actions || [{ label: 'Close' }]).forEach(function (a) {
      acts.appendChild(h('button', {
        class: 'btn ' + (a.kind || 'btn-ghost'), type: 'button', text: a.label,
        onclick: function () { if (!a.onclick || a.onclick(close) !== false) close(); }
      }));
    });
    card.appendChild(acts);
    overlay.appendChild(card);
    overlay.addEventListener('mousedown', function (e) { if (e.target === overlay) close(); });
    document.addEventListener('keydown', onKey);
    document.body.appendChild(overlay);
    var first = card.querySelector('input,select,textarea,button');
    if (first) first.focus();
    return { close: close, card: card };
  }
  function confirmBox(title, text, okLabel, kind) {
    return new Promise(function (resolve) {
      modal({
        title: title, body: h('p', { class: 'muted', text: text }),
        actions: [
          { label: 'Cancel', onclick: function () { resolve(false); } },
          { label: okLabel || 'Confirm', kind: kind || 'btn-danger', onclick: function () { resolve(true); } }
        ]
      });
    });
  }

  function copy(text) {
    function fallback() {
      var t = h('textarea');
      t.style.position = 'fixed'; t.style.opacity = '0';
      t.value = text; document.body.appendChild(t); t.select();
      var ok = false; try { ok = document.execCommand('copy'); } catch (e) { /* ignore */ }
      t.remove(); return ok;
    }
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text).then(function () { return true; }, function () { return fallback(); });
    }
    return Promise.resolve(fallback());
  }
  function copyButton(getText, label) {
    var b = h('button', { class: 'btn btn-ghost btn-sm', type: 'button', text: label || 'Copy' });
    b.addEventListener('click', function () {
      copy(typeof getText === 'function' ? getText() : getText).then(function (ok) {
        toast(ok ? 'Copied' : 'Press Ctrl+C to copy', ok ? 'ok' : '');
      });
    });
    return b;
  }

  // ---------- small components ----------
  function chip(text, kind) { return h('span', { class: 'chip ' + (kind || ''), text: text }); }
  function empty(title, text) { return h('div', { class: 'empty' }, h('strong', { text: title }), text || ''); }
  function skeleton(n) {
    var box = h('div', { class: 'stack' });
    for (var i = 0; i < (n || 3); i++) box.appendChild(h('div', { class: 'skeleton' }));
    return box;
  }
  function meter(pct, hot) {
    var bar = h('i');
    bar.style.width = Math.max(0, Math.min(100, pct)) + '%';
    return h('div', { class: 'meter' + (hot ? ' hot' : '') }, bar);
  }
  function kpi(label, value, sub, color) {
    return h('div', { class: 'kpi ' + (color || '') },
      h('div', { class: 'kpi-label', text: label }),
      h('div', { class: 'kpi-value', text: value }),
      sub ? h('div', { class: 'kpi-sub', text: sub }) : null);
  }

  // table({columns:[{label, num, render(row)}], rows, emptyTitle, emptyText}) -> Node
  function table(o) {
    if (!o.rows || !o.rows.length) return empty(o.emptyTitle || 'Nothing here yet', o.emptyText);
    var thead = h('tr');
    o.columns.forEach(function (c) { thead.appendChild(h('th', { class: c.num ? 'num' : '', text: c.label })); });
    var tbody = h('tbody');
    o.rows.forEach(function (row) {
      var tr = h('tr');
      o.columns.forEach(function (c) {
        var v = c.render(row);
        tr.appendChild(h('td', { class: (c.num ? 'num ' : '') + (c.cls || '') }, v));
      });
      tbody.appendChild(tr);
    });
    return h('div', { class: 'table-wrap' }, h('table', null, h('thead', null, thead), tbody));
  }

  // Bars for the last N days. series: [{date, gross_cents, fee_cents}] ; fee overlay is optional.
  var SVGNS = 'http://www.w3.org/2000/svg';
  function svg(tag, attrs) {
    var e = document.createElementNS(SVGNS, tag);
    Object.keys(attrs || {}).forEach(function (k) { e.setAttribute(k, attrs[k]); });
    return e;
  }
  function chart(daily, showFee) {
    var W = 640, H = 220, padL = 44, padB = 26, padT = 10;
    var s = svg('svg', { class: 'chart', viewBox: '0 0 ' + W + ' ' + H, preserveAspectRatio: 'none', role: 'img', 'aria-label': 'Daily sales for the last 14 days' });
    var max = 0;
    daily.forEach(function (d) { if (d.gross_cents > max) max = d.gross_cents; });
    var top = niceMax(max / 100) * 100;
    [0, 0.5, 1].forEach(function (f) {
      var y = padT + (H - padT - padB) * (1 - f);
      s.appendChild(svg('line', { class: 'grid', x1: padL, x2: W, y1: y, y2: y }));
      var t = svg('text', { x: padL - 6, y: y + 3, 'text-anchor': 'end' });
      t.textContent = shortMoney(top * f);
      s.appendChild(t);
    });
    var slot = (W - padL) / Math.max(1, daily.length), bw = slot * 0.62;
    daily.forEach(function (d, i) {
      var x = padL + slot * i + (slot - bw) / 2;
      var bh = top > 0 ? (H - padT - padB) * (d.gross_cents / top) : 0;
      var g = svg('g');
      var title = svg('title'); title.textContent = d.date + ': ' + kes(d.gross_cents) + ' from ' + d.sales + (d.sales === 1 ? ' sale' : ' sales');
      g.appendChild(title);
      g.appendChild(svg('rect', { class: 'bar' + (i === daily.length - 1 ? ' today' : ''), x: x, y: H - padB - bh, width: bw, height: Math.max(bh, d.gross_cents > 0 ? 2 : 0), rx: 4 }));
      if (showFee && d.fee_cents > 0 && top > 0) {
        var fh = (H - padT - padB) * (d.fee_cents / top);
        g.appendChild(svg('rect', { class: 'fee', x: x, y: H - padB - fh, width: bw, height: Math.max(fh, 2), rx: 4 }));
      }
      if (i % 2 === (daily.length - 1) % 2) {
        var lt = svg('text', { x: x + bw / 2, y: H - 8, 'text-anchor': 'middle' });
        lt.textContent = d.date.slice(8) + '/' + d.date.slice(5, 7);
        g.appendChild(lt);
      }
      s.appendChild(g);
    });
    return s;
  }
  function niceMax(v) {
    if (v <= 0) return 100;
    var p = Math.pow(10, Math.floor(Math.log10(v))), n = v / p;
    return (n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10) * p;
  }
  function shortMoney(cents) {
    var v = cents / 100;
    return v >= 1000 ? (v / 1000).toFixed(v % 1000 ? 1 : 0).replace(/\.0$/, '') + 'k' : String(Math.round(v));
  }

  // ---------- navigation between views (sidebar buttons <-> main panels <-> aside panels) ----------
  function views(onShow) {
    var buttons = $all('.nav button[data-view]');
    function show(name, push) {
      if (!$('[data-panel="' + name + '"]')) name = buttons[0].dataset.view;
      buttons.forEach(function (b) { b.classList.toggle('active', b.dataset.view === name); b.setAttribute('aria-current', b.dataset.view === name ? 'page' : 'false'); });
      $all('[data-panel]').forEach(function (p) { p.classList.toggle('hidden', p.dataset.panel !== name); });
      $all('[data-aside]').forEach(function (p) { p.classList.toggle('hidden', p.dataset.aside !== name); });
      if (push !== false) history.replaceState(null, '', '#' + name);
      onShow(name);
    }
    buttons.forEach(function (b) { b.addEventListener('click', function () { show(b.dataset.view); }); });
    window.addEventListener('hashchange', function () { show(location.hash.slice(1), false); });
    show(location.hash.slice(1) || buttons[0].dataset.view, false);
    return show;
  }

  // ---------- live updates ----------
  // live('/api/tenant/stream', {sale: fn}, dotElement)
  function live(url, handlers, dot) {
    if (!window.EventSource) return null;
    var es = new EventSource(withTenant(url));
    es.onopen = function () { if (dot) dot.classList.add('on'); };
    es.onerror = function () { if (dot) dot.classList.remove('on'); };
    Object.keys(handlers).forEach(function (type) {
      es.addEventListener(type, function (e) {
        var data = null; try { data = JSON.parse(e.data); } catch (x) { /* ignore */ }
        handlers[type](data);
      });
    });
    return es;
  }
  function debounce(fn, ms) {
    var t; return function () { var a = arguments, c = this; clearTimeout(t); t = setTimeout(function () { fn.apply(c, a); }, ms); };
  }

  // Run a button's async action with a busy state and error toast.
  function busy(btn, fn) {
    var label = btn.textContent;
    btn.disabled = true;
    return Promise.resolve().then(fn).catch(errorToast).then(function (r) { btn.disabled = false; btn.textContent = label; return r; });
  }

  function logout(url) {
    return post(url || '/api/auth/logout').then(function () { location.href = '/login'; }, function () { location.href = '/login'; });
  }

  window.HP = {
    h: h, $: $, $all: $all, clear: clear, kes: kes, num: num, bytes: bytes, dur: dur, clock: clock, ago: ago, left: left,
    when: when, day: day, api: api, get: get, post: post, ApiError: ApiError, toast: toast, errorToast: errorToast,
    modal: modal, confirm: confirmBox, copy: copy, copyButton: copyButton, chip: chip, empty: empty, skeleton: skeleton,
    meter: meter, kpi: kpi, table: table, chart: chart, views: views, live: live, debounce: debounce, busy: busy,
    logout: logout, tenantParam: tenantParam, withTenant: withTenant
  };
})();