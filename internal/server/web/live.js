(function () {
  'use strict';

  if (typeof window === 'undefined' || typeof window.EventSource === 'undefined') return;
  if (window.location.protocol === 'file:') return;

  const endpoint = '/api/live';
  let source = null;
  let stopped = false;
  let reconnectTimer = null;
  let backoff = 1000;
  let refreshing = false;
  let refreshAgain = false;
  let indicator = null;

  function ready(fn) {
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', fn, { once: true });
    else fn();
  }

  function ensureIndicator() {
    if (indicator || !document.body) return indicator;
    const anchor = document.querySelector('#reanalyze') || document.querySelector('.header-actions');
    if (!anchor || !anchor.parentNode) return null;
    indicator = document.createElement('span');
    indicator.textContent = 'live';
    indicator.title = 'Waiting for live updates';
    indicator.setAttribute('aria-label', 'Live updates disconnected');
    indicator.style.cssText = 'font-size:12px;opacity:.65;border:1px solid currentColor;border-radius:999px;padding:3px 8px;margin-left:8px;white-space:nowrap';
    anchor.parentNode.insertBefore(indicator, anchor.nextSibling);
    return indicator;
  }

  function setStatus(status) {
    const node = ensureIndicator();
    if (!node) return;
    node.dataset.status = status;
    if (status === 'connected') {
      node.textContent = 'live';
      node.title = 'Live updates connected';
      node.setAttribute('aria-label', 'Live updates connected');
      node.style.opacity = '1';
    } else if (status === 'updating') {
      node.textContent = 'updating…';
      node.title = 'Live update received';
      node.style.opacity = '1';
    } else {
      node.textContent = 'live';
      node.title = 'Live updates disconnected';
      node.setAttribute('aria-label', 'Live updates disconnected');
      node.style.opacity = '.45';
    }
  }

  function showToast(message, kind) {
    if (typeof window.toast === 'function') {
      window.toast(message, kind || 'ok');
      return;
    }
    const node = document.querySelector('#toast');
    if (!node) return;
    node.textContent = message;
    node.className = `toast show ${kind || 'ok'}`;
    setTimeout(() => { node.className = 'toast'; }, 3000);
  }

  async function refreshReport() {
    if (refreshing) {
      refreshAgain = true;
      return;
    }
    refreshing = true;
    setStatus('updating');
    try {
      if (typeof window.apply === 'function') {
        const res = await fetch('/api/report', { cache: 'no-store' });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        window.apply(await res.json());
      } else {
        const btn = document.querySelector('#reanalyze');
        if (btn) btn.click();
      }
      showToast('Live update applied.', 'ok');
      backoff = 1000;
      setStatus('connected');
    } catch (_) {
      setStatus('disconnected');
    } finally {
      refreshing = false;
      if (refreshAgain) {
        refreshAgain = false;
        refreshReport();
      }
    }
  }

  function scheduleReconnect() {
    if (stopped || reconnectTimer) return;
    setStatus('disconnected');
    const wait = backoff;
    backoff = Math.min(backoff * 2, 30000);
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      connect();
    }, wait);
  }

  function connect() {
    if (stopped) return;
    try {
      source = new EventSource(endpoint);
    } catch (_) {
      scheduleReconnect();
      return;
    }
    source.addEventListener('open', () => {
      backoff = 1000;
      setStatus('connected');
    });
    source.addEventListener('update', refreshReport);
    source.addEventListener('error', () => {
      if (source) source.close();
      source = null;
      scheduleReconnect();
    });
  }

  function stop() {
    stopped = true;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    if (source) source.close();
    source = null;
  }

  ready(() => {
    ensureIndicator();
    connect();
  });
  window.addEventListener('pagehide', stop);
  window.addEventListener('beforeunload', stop);
})();
