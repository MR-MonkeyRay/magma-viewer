(() => {
  'use strict';

  const $ = (sel) => document.querySelector(sel);
  const store = {
    get(k, d) { try { const v = localStorage.getItem('mv.' + k); return v === null ? d : v; } catch (e) { return d; } },
    set(k, v) { try { localStorage.setItem('mv.' + k, v); } catch (e) { /* private mode */ } },
  };

  const state = {
    page: 1,
    size: Number(store.get('size', 48)),
    sort: store.get('sort', 'desc'),
    view: store.get('view', 'grid'),
    group: store.get('group', '0') === '1',
    items: [],
    total: 0,
    pages: 1,
    admin: false,
    guestLimit: 10,
    selected: new Set(),
    selectMode: false,
    knownTotal: null,
    lbIndex: -1,
  };

  const el = {
    gallery: $('#gallery'), now: $('#now'), summary: $('#summary'), empty: $('#empty'), pager: $('#pager'),
    sort: $('#sort'), size: $('#size'), group: $('#group'), selectMode: $('#select-mode'),
    theme: $('#theme'), account: $('#account'), accountLabel: $('#account-label'),
    selbar: $('#selbar'), selCount: $('#sel-count'), toast: $('#toast'), fresh: $('#fresh'),
    lb: $('#lightbox'), lbImg: $('#lb-img'), lbTime: $('#lb-time'), lbSub: $('#lb-sub'),
    lbPrev: $('#lb-prev'), lbNext: $('#lb-next'), lbDownload: $('#lb-download'), lbSelect: $('#lb-select'),
    loginDialog: $('#login-dialog'), loginForm: $('#login-form'), loginError: $('#login-error'),
    adminDialog: $('#admin-dialog'), adminStats: $('#admin-stats'), fetchNow: $('#fetch-now'),
  };

  // ---------- formatting ----------
  const pad = (n) => String(n).padStart(2, '0');
  const fmtDate = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  const fmtClock = (d) => `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  const fmtFull = (d) => `${fmtDate(d)} ${fmtClock(d)}`;
  const fmtShort = (d) => `${fmtDate(d)} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
  const WEEK = ['星期日', '星期一', '星期二', '星期三', '星期四', '星期五', '星期六'];
  const fmtDay = (d) => `${d.getFullYear()}年${d.getMonth() + 1}月${d.getDate()}日 ${WEEK[d.getDay()]}`;
  function fmtSize(b) {
    if (b < 1024) return b + ' B';
    if (b < 1024 * 1024) return (b / 1024).toFixed(0) + ' KB';
    return (b / 1024 / 1024).toFixed(b < 10 * 1024 * 1024 ? 2 : 1) + ' MB';
  }
  function fmtAgo(d) {
    const s = Math.max(0, (Date.now() - d.getTime()) / 1000);
    if (s < 60) return '刚刚保存';
    if (s < 3600) return `${Math.floor(s / 60)} 分钟前保存`;
    if (s < 86400) return `${Math.floor(s / 3600)} 小时前保存`;
    return `${Math.floor(s / 86400)} 天前保存`;
  }
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const enc = encodeURIComponent;

  let toastTimer;
  function toast(msg) {
    el.toast.textContent = msg;
    el.toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { el.toast.hidden = true; }, 2600);
  }

  async function api(path, opts) {
    const res = await fetch(path, Object.assign({ credentials: 'same-origin' }, opts));
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw Object.assign(new Error(body.error || res.statusText), { status: res.status });
    return body;
  }

  function download(href) {
    const a = document.createElement('a');
    a.href = href;
    a.download = '';
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  // ---------- data ----------
  function readHash() {
    const m = /p=(\d+)/.exec(location.hash);
    state.page = m ? Math.max(1, Number(m[1])) : 1;
  }
  function writeHash() {
    const h = state.page > 1 ? `#p=${state.page}` : '';
    if (location.hash !== h) history.replaceState(null, '', h || location.pathname);
  }

  let loadSeq = 0;
  async function load({ scroll = false } = {}) {
    const seq = ++loadSeq;
    if (!state.items.length) renderSkeleton();
    try {
      const d = await api(`/api/images?page=${state.page}&size=${state.size}&sort=${state.sort}`);
      if (seq !== loadSeq) return;
      Object.assign(state, { items: d.items, total: d.total, page: d.page, pages: d.pages, admin: d.admin, guestLimit: d.guest_limit });
      if (state.sort === 'desc' && state.page === 1) { state.knownTotal = d.total; el.fresh.hidden = true; }
      else if (state.knownTotal === null) state.knownTotal = d.total;
      writeHash();
      render();
      if (scroll) window.scrollTo({ top: 0, behavior: 'instant' in window ? 'instant' : 'auto' });
    } catch (e) {
      el.gallery.innerHTML = '';
      el.summary.textContent = '加载失败：' + e.message + '，请刷新页面重试。';
    }
  }

  function go(page) {
    state.page = page;
    return load({ scroll: true });
  }

  // ---------- rendering ----------
  function renderSkeleton() {
    el.gallery.className = `gallery view-${state.view} skeleton`;
    const n = Math.min(state.size, 12);
    el.gallery.innerHTML = `<div class="tiles">${'<figure class="tile"><div class="tile-open"></div><figcaption class="tile-cap">&nbsp;</figcaption></figure>'.repeat(n)}</div>`;
  }

  function tileHTML(it, i) {
    const d = new Date(it.time);
    const sel = state.selected.has(it.name);
    const name = esc(it.name);
    return `<figure class="tile${sel ? ' selected' : ''}" data-i="${i}">
      <button type="button" class="tile-check" aria-label="选择" aria-pressed="${sel}"><svg><use href="#i-check"/></svg></button>
      <button type="button" class="tile-open" aria-label="查看 ${esc(fmtFull(d))}"><img loading="lazy" decoding="async" src="/thumb/${enc(it.name)}"${it.w ? ` width="${it.w}" height="${it.h}"` : ''} alt=""></button>
      <figcaption class="tile-cap">
        <time datetime="${esc(it.time)}" title="${esc(fmtFull(d))}">${state.group ? fmtClock(d) : fmtShort(d)}</time>
        <span class="name" title="${name}">${name}</span>
        <span class="size">${fmtSize(it.size)}</span>
        <a class="tile-dl" href="/download/${enc(it.name)}" download title="下载" aria-label="下载"><svg><use href="#i-download"/></svg></a>
      </figcaption>
    </figure>`;
  }

  // Masonry fills the shortest column, keeping left-to-right, newest-first order.
  const MASONRY_COL = 260;
  function masonryCols() {
    const gap = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--gap')) || 14;
    const w = el.gallery.clientWidth || window.innerWidth;
    return Math.max(2, Math.floor((w + gap) / (MASONRY_COL + gap)));
  }

  function tilesHTML(entries) {
    if (state.view !== 'masonry') return `<div class="tiles">${entries.map(([it, i]) => tileHTML(it, i)).join('')}</div>`;
    const n = masonryCols();
    const cols = Array.from({ length: n }, () => ({ h: 0, html: [] }));
    for (const [it, i] of entries) {
      const col = cols.reduce((a, b) => (b.h < a.h ? b : a));
      col.h += (it.w ? it.h / it.w : 1) + 0.2; // 0.2 ≈ caption height relative to width
      col.html.push(tileHTML(it, i));
    }
    return `<div class="tiles" data-cols="${n}">${cols.map((c) => `<div class="mcol">${c.html.join('')}</div>`).join('')}</div>`;
  }

  function renderNow() {
    const latest = state.sort === 'desc' && state.page === 1 ? state.items[0] : null;
    el.now.hidden = !latest;
    if (!latest) return;
    const d = new Date(latest.time);
    el.now.innerHTML = `
      <button type="button" class="now-frame" data-i="0" aria-label="查看最新画面"><img src="/preview/${enc(latest.name)}" alt="最新画面"></button>
      <div class="now-side">
        <p class="now-label">画板最新状态</p>
        <p class="now-date">${fmtDay(d)}</p>
        <p class="now-time">${fmtClock(d)}</p>
        <p class="now-ago">${fmtAgo(d)}</p>
        <p class="now-meta">${fmtSize(latest.size)}，共存档 ${state.total} 个画面</p>
        <a class="btn primary" href="/download/${enc(latest.name)}" download><svg><use href="#i-download"/></svg>下载这张</a>
      </div>`;
  }

  function render() {
    renderNow();
    el.empty.hidden = state.total > 0;
    el.summary.hidden = state.total === 0;
    const from = (state.page - 1) * state.size + 1;
    const to = from + state.items.length - 1;
    el.summary.innerHTML = state.total
      ? `<strong>共 ${state.total} 张</strong><span>第 ${from}–${to} 张，${state.sort === 'desc' ? '最新在前' : '最早在前'}</span>`
      : '';

    el.gallery.className = `gallery view-${state.view}${state.selectMode ? ' selecting' : ''}`;
    // Grid tiles follow the page's typical aspect ratio (magma canvases are 16:9).
    const ratios = state.items.filter((it) => it.w).map((it) => it.w / it.h).sort((x, y) => x - y);
    const ratio = ratios.length ? ratios[ratios.length >> 1] : 1;
    el.gallery.style.setProperty('--tile-ratio', Math.min(Math.max(ratio, 0.6), 2).toFixed(3));
    if (!state.group) {
      el.gallery.innerHTML = tilesHTML(state.items.map((it, i) => [it, i]));
    } else {
      const groups = [];
      state.items.forEach((it, i) => {
        const key = fmtDate(new Date(it.time));
        let g = groups[groups.length - 1];
        if (!g || g.key !== key) groups.push(g = { key, d: new Date(it.time), entries: [] });
        g.entries.push([it, i]);
      });
      el.gallery.innerHTML = groups.map((g) =>
        `<h2 class="group-title">${fmtDay(g.d)}<span>本页 ${g.entries.length} 张</span></h2>${tilesHTML(g.entries)}`).join('');
    }
    renderPager();
    renderControls();
    renderSelection();
  }

  function pageList(cur, total) {
    const set = new Set([1, total, cur - 2, cur - 1, cur, cur + 1, cur + 2]);
    const list = [...set].filter((p) => p >= 1 && p <= total).sort((a, b) => a - b);
    const out = [];
    list.forEach((p, i) => {
      if (i && p - list[i - 1] > 1) out.push(null);
      out.push(p);
    });
    return out;
  }

  function renderPager() {
    if (state.pages <= 1) { el.pager.innerHTML = ''; return; }
    const btn = (p, label, extra = '') => `<button type="button" data-page="${p}" ${extra}>${label}</button>`;
    el.pager.innerHTML =
      btn(state.page - 1, '上一页', state.page <= 1 ? 'disabled' : '') +
      pageList(state.page, state.pages).map((p) => p === null
        ? '<span class="gap">…</span>'
        : btn(p, p, p === state.page ? 'aria-current="page"' : '')).join('') +
      btn(state.page + 1, '下一页', state.page >= state.pages ? 'disabled' : '') +
      `<label class="jump">跳至<input type="number" min="1" max="${state.pages}" placeholder="${state.page}" aria-label="跳转页码">/ ${state.pages} 页</label>`;
  }

  function renderControls() {
    document.querySelectorAll('.seg [data-view]').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.view === state.view)));
    el.sort.value = state.sort;
    el.size.value = String(state.size);
    el.group.checked = state.group;
    el.selectMode.setAttribute('aria-pressed', String(state.selectMode));
    el.accountLabel.textContent = state.admin ? '管理' : '管理员登录';
    el.account.classList.toggle('is-admin', state.admin);
    const dark = getComputedStyle(document.documentElement).colorScheme === 'dark';
    el.theme.querySelector('use').setAttribute('href', dark ? '#i-sun' : '#i-moon');
  }

  // ---------- selection ----------
  const limit = () => (state.admin ? Infinity : state.guestLimit);

  function toggleSelect(name) {
    if (state.selected.has(name)) {
      state.selected.delete(name);
    } else {
      if (state.selected.size >= limit()) {
        toast(`访客一次最多打包下载 ${state.guestLimit} 张`);
        return false;
      }
      state.selected.add(name);
    }
    return true;
  }

  function renderSelection() {
    const n = state.selected.size;
    el.selbar.hidden = n === 0 && !state.selectMode;
    el.selCount.textContent = state.admin ? `已选 ${n} 张` : `已选 ${n} / ${state.guestLimit}`;
    $('#sel-zip').disabled = n === 0;
    el.gallery.querySelectorAll('.tile[data-i]').forEach((t) => {
      const it = state.items[t.dataset.i];
      const on = state.selected.has(it.name);
      t.classList.toggle('selected', on);
      t.querySelector('.tile-check').setAttribute('aria-pressed', String(on));
    });
    if (!el.lb.hidden && state.lbIndex >= 0) {
      el.lbSelect.setAttribute('aria-pressed', String(state.selected.has(state.items[state.lbIndex].name)));
    }
  }

  function setSelectMode(on) {
    state.selectMode = on;
    el.gallery.classList.toggle('selecting', on);
    renderControls();
    renderSelection();
  }

  // ---------- lightbox ----------
  function openLightbox(i) {
    state.lbIndex = i;
    const it = state.items[i];
    if (!it) return;
    const d = new Date(it.time);
    // 1600px preview for most screens; the browser picks the original on large/HiDPI displays.
    el.lbImg.srcset = it.w > 1600 ? `/preview/${enc(it.name)} 1600w, /img/${enc(it.name)} ${it.w}w` : '';
    el.lbImg.sizes = '100vw';
    el.lbImg.src = `/preview/${enc(it.name)}`;
    el.lbImg.alt = fmtFull(d);
    el.lbTime.textContent = fmtFull(d);
    el.lbSub.textContent = `${fmtSize(it.size)}，第 ${(state.page - 1) * state.size + i + 1} / ${state.total} 张`;
    el.lbDownload.href = `/download/${enc(it.name)}`;
    el.lbPrev.disabled = i === 0 && state.page === 1;
    el.lbNext.disabled = i === state.items.length - 1 && state.page === state.pages;
    const next = state.items[i + 1];
    if (next) new Image().src = `/preview/${enc(next.name)}`;
    if (el.lb.hidden) {
      el.lb.hidden = false;
      document.body.style.overflow = 'hidden';
      $('#lb-close').focus();
    }
    renderSelection();
  }

  function closeLightbox() {
    el.lb.hidden = true;
    state.lbIndex = -1;
    document.body.style.overflow = '';
  }

  async function step(delta) {
    const i = state.lbIndex + delta;
    if (i >= 0 && i < state.items.length) return openLightbox(i);
    if (i < 0 && state.page > 1) { await go(state.page - 1); return openLightbox(state.items.length - 1); }
    if (i >= state.items.length && state.page < state.pages) { await go(state.page + 1); return openLightbox(0); }
  }

  // ---------- admin ----------
  async function openAdmin() {
    el.adminStats.innerHTML = '<dt>状态</dt><dd>加载中…</dd>';
    $('#download-all').textContent = '';
    el.adminDialog.showModal();
    try { renderStats(await api('/api/status')); } catch (e) { handleAuthError(e); }
  }

  function renderStats(s) {
    const f = s.fetch;
    const t = (v) => (v && !v.startsWith('0001') ? fmtFull(new Date(v)) : '暂无');
    const result = { saved: '<span class="ok">已保存新画面</span>', unchanged: '画面没有变化，已跳过', error: '<span class="bad">失败</span>' }[f.result] || '尚未运行';
    el.adminStats.innerHTML = `
      <dt>存档总数</dt><dd>${s.count} 张</dd>
      <dt>抓取计划</dt><dd><code>${esc(s.schedule)}</code></dd>
      <dt>上次抓取</dt><dd>${t(f.last_run)}</dd>
      <dt>结果</dt><dd>${result}${f.message ? `<br><span class="bad">${esc(f.message)}</span>` : ''}</dd>
      <dt>最近保存</dt><dd>${t(f.last_saved)}</dd>
      <dt>本次启动</dt><dd>抓取 ${f.runs} 次，保存 ${f.saved} 张</dd>${tokenRow(s.token)}`;
    $('#download-all').innerHTML = `<svg><use href="#i-download"/></svg>下载全部 ${s.count} 张`;
  }

  function tokenRow(tok) {
    if (!tok) return '';
    const exp = new Date(tok.expires);
    const days = Math.floor((exp - Date.now()) / 86400000);
    const left = days < 0 ? '<span class="bad">已过期，请更新 magma.cookie</span>'
      : days < 7 ? `<span class="bad">剩 ${days} 天</span>` : `<span class="ok">剩 ${days} 天</span>`;
    return `<dt>登录凭证</dt><dd>${esc(tok.cookie)} 有效至 ${fmtShort(exp)}，${left}</dd>`;
  }

  function handleAuthError(e) {
    if (e.status === 401) {
      state.admin = false;
      el.adminDialog.close();
      renderControls();
      toast('登录已过期，请重新登录');
    } else {
      toast(e.message);
    }
  }

  // ---------- events ----------
  document.querySelectorAll('.seg [data-view]').forEach((b) => b.addEventListener('click', () => {
    state.view = b.dataset.view;
    store.set('view', state.view);
    render();
  }));
  el.sort.addEventListener('change', () => { state.sort = el.sort.value; store.set('sort', state.sort); go(1); });
  el.size.addEventListener('change', () => { state.size = Number(el.size.value); store.set('size', state.size); go(1); });
  el.group.addEventListener('change', () => { state.group = el.group.checked; store.set('group', state.group ? '1' : '0'); render(); });
  el.selectMode.addEventListener('click', () => setSelectMode(!state.selectMode));
  $('#brand').addEventListener('click', (e) => { e.preventDefault(); go(1); });

  el.theme.addEventListener('click', () => {
    const dark = getComputedStyle(document.documentElement).colorScheme === 'dark';
    document.documentElement.dataset.theme = dark ? 'light' : 'dark';
    store.set('theme', document.documentElement.dataset.theme);
    renderControls();
  });

  el.gallery.addEventListener('click', (e) => {
    const tile = e.target.closest('.tile[data-i]');
    if (!tile) return;
    const i = Number(tile.dataset.i);
    const it = state.items[i];
    if (e.target.closest('.tile-check')) {
      toggleSelect(it.name);
      renderSelection();
    } else if (e.target.closest('.tile-open')) {
      if (state.selectMode || state.selected.size) { toggleSelect(it.name); renderSelection(); } else openLightbox(i);
    }
  });
  el.now.addEventListener('click', (e) => { if (e.target.closest('.now-frame')) openLightbox(0); });

  el.pager.addEventListener('click', (e) => {
    const b = e.target.closest('button[data-page]');
    if (b && !b.disabled) go(Number(b.dataset.page));
  });
  el.pager.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' || !e.target.matches('.jump input')) return;
    const p = Math.min(Math.max(1, Number(e.target.value) || 1), state.pages);
    go(p);
  });

  $('#sel-clear').addEventListener('click', () => { state.selected.clear(); setSelectMode(false); });
  $('#sel-page').addEventListener('click', () => {
    for (const it of state.items) {
      if (!state.selected.has(it.name) && !toggleSelect(it.name)) break;
    }
    renderSelection();
  });
  $('#sel-zip').addEventListener('click', () => {
    const names = [...state.selected];
    if (!names.length) return;
    if (names.length === 1) download(`/download/${enc(names[0])}`);
    else download('/download/zip?' + names.map((n) => 'f=' + enc(n)).join('&'));
    toast(`开始下载 ${names.length} 张`);
  });

  $('#lb-close').addEventListener('click', closeLightbox);
  el.lbPrev.addEventListener('click', () => step(-1));
  el.lbNext.addEventListener('click', () => step(1));
  el.lbSelect.addEventListener('click', () => {
    toggleSelect(state.items[state.lbIndex].name);
    renderSelection();
  });
  el.lb.addEventListener('click', (e) => { if (e.target === el.lb || e.target.classList.contains('lb-stage')) closeLightbox(); });
  let touchX = null;
  el.lb.addEventListener('touchstart', (e) => { touchX = e.touches[0].clientX; }, { passive: true });
  el.lb.addEventListener('touchend', (e) => {
    if (touchX === null) return;
    const dx = e.changedTouches[0].clientX - touchX;
    touchX = null;
    if (Math.abs(dx) > 50) step(dx < 0 ? 1 : -1);
  });
  document.addEventListener('keydown', (e) => {
    if (el.lb.hidden) return;
    if (e.key === 'Escape') closeLightbox();
    else if (e.key === 'ArrowLeft') step(-1);
    else if (e.key === 'ArrowRight') step(1);
  });

  el.account.addEventListener('click', () => {
    if (state.admin) return openAdmin();
    el.loginError.hidden = true;
    el.loginForm.reset();
    el.loginDialog.showModal();
  });
  $('#login-cancel').addEventListener('click', () => el.loginDialog.close());
  el.loginForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const fd = new FormData(el.loginForm);
    const btn = $('#login-submit');
    btn.disabled = true;
    try {
      await api('/api/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: fd.get('username'), password: fd.get('password') }),
      });
      el.loginDialog.close();
      toast('已登录为管理员');
      await load();
    } catch (err) {
      el.loginError.textContent = err.message;
      el.loginError.hidden = false;
    } finally {
      btn.disabled = false;
    }
  });

  $('#admin-close').addEventListener('click', () => el.adminDialog.close());
  $('#logout').addEventListener('click', async () => {
    await api('/api/logout', { method: 'POST' }).catch(() => {});
    el.adminDialog.close();
    // Guests are capped, so trim an oversized selection.
    if (state.selected.size > state.guestLimit) state.selected = new Set([...state.selected].slice(0, state.guestLimit));
    toast('已退出登录');
    await load();
  });
  el.fetchNow.addEventListener('click', async () => {
    el.fetchNow.disabled = true;
    el.fetchNow.textContent = '抓取中…';
    try {
      const s = await api('/api/fetch', { method: 'POST' });
      renderStats(s);
      if (s.fetch.result === 'saved') { toast('已保存新画面'); state.page = 1; await load(); }
      else if (s.fetch.result === 'unchanged') toast('画面没有变化');
      else toast('抓取失败：' + s.fetch.message);
    } catch (e) {
      handleAuthError(e);
    } finally {
      el.fetchNow.disabled = false;
      el.fetchNow.textContent = '立即抓取';
    }
  });

  // Poll for newly saved images; reload silently only when it cannot disrupt the viewer.
  $('#fresh-btn').addEventListener('click', () => { el.fresh.hidden = true; state.sort = 'desc'; store.set('sort', 'desc'); go(1); });
  setInterval(async () => {
    if (document.hidden) return;
    try {
      const d = await api('/api/images?page=1&size=12&sort=desc');
      if (state.knownTotal === null || d.total <= state.knownTotal) return;
      const idle = state.page === 1 && state.sort === 'desc' && el.lb.hidden && !state.selected.size && window.scrollY < 200;
      if (idle) { await load(); } else { el.fresh.hidden = false; }
    } catch (e) { /* offline: try again next tick */ }
  }, 60000);

  let resizeTimer;
  window.addEventListener('resize', () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      const t = el.gallery.querySelector('.tiles[data-cols]');
      if (t && Number(t.dataset.cols) !== masonryCols()) render();
    }, 150);
  });
  window.addEventListener('hashchange', () => { readHash(); load({ scroll: true }); });
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', renderControls);

  readHash();
  renderControls();
  load();
})();
