(() => {
  'use strict';

  const $ = (sel) => document.querySelector(sel);
  const store = {
    get(k, d) { try { const v = localStorage.getItem('mv.' + k); return v === null ? d : v; } catch (e) { return d; } },
    set(k, v) { try { localStorage.setItem('mv.' + k, v); } catch (e) { /* private mode */ } },
  };
  const motion = window.matchMedia('(prefers-reduced-motion: reduce)');
  const reduced = () => motion.matches;
  const VIEWS = ['grid', 'masonry', 'list'];

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
    task: null,
    selected: new Set(),
    selectMode: false,
    lastIndex: null, // anchor for shift-click range selection
    latest: undefined, // newest saved image; undefined until known, null when the archive is empty
    lbIndex: -1,
  };

  const el = {
    bar: $('#bar'), main: $('#main'), toTop: $('#to-top'),
    gallery: $('#gallery'), summary: $('#summary'), empty: $('#empty'), pager: $('#pager'),
    now: $('#now'), nowImg: $('#now-img'), nowTime: $('#now-time'), nowAgo: $('#now-ago'), nowMeta: $('#now-meta'), nowDl: $('#now-dl'),
    seg: $('.seg'), sort: $('#sort'), size: $('#size'), group: $('#group'), selectMode: $('#select-mode'),
    theme: $('#theme'), account: $('#account'), foot: $('.foot'),
    selbar: $('#selbar'), selCount: $('#sel-count'), fresh: $('#fresh'),
    toast: $('#toast'), toastText: $('#toast span'), toastIcon: $('#toast use'),
    lb: $('#lightbox'), lbStage: $('#lb-stage'), lbImg: $('#lb-img'), lbTime: $('#lb-time'), lbSub: $('#lb-sub'),
    lbCount: $('#lb-count'), lbPrev: $('#lb-prev'), lbNext: $('#lb-next'), lbDownload: $('#lb-download'),
    lbSelect: $('#lb-select'), lbZoom: $('#lb-zoom'), lbLink: $('#lb-link'), lbMenu: $('#lb-menu'), lbInfo: $('#lb-info'),
    lbpDate: $('#lbp-date'), lbpTime: $('#lbp-time'), lbpAgo: $('#lbp-ago'), lbpDownload: $('#lbp-download'),
    lbpSize: $('#lbp-size'), lbpSizeMenu: $('#lbp-size-menu'), lbpGrid: $('#lbp-grid'), lbpName: $('#lbp-name'),
    loginDialog: $('#login-dialog'), loginForm: $('#login-form'), loginError: $('#login-error'), loginSubmit: $('#login-submit'),
    adminDialog: $('#admin-dialog'), adminStats: $('#admin-stats'), fetchNow: $('#fetch-now'),
    toggleSchedule: $('#toggle-schedule'), scheduleHint: $('#schedule-hint'),
    keysDialog: $('#keys-dialog'), task: $('#task'),
  };

  // ---------- formatting ----------
  const pad = (n) => String(n).padStart(2, '0');
  const fmtDate = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  const fmtClock = (d) => `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  const fmtFull = (d) => `${fmtDate(d)} ${fmtClock(d)}`;
  const fmtShort = (d) => `${fmtDate(d)} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
  const fmtHM = (d) => `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  const WEEK = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];
  const sameDay = (a, b) => fmtDate(a) === fmtDate(b);
  // Tile labels drop the date when it is today; the date header or tooltip has the full time.
  const fmtTile = (d) => (state.group || sameDay(d, new Date()) ? fmtHM(d) : `${d.getMonth() + 1}/${d.getDate()} ${fmtHM(d)}`);
  function fmtDay(d) {
    const today = new Date();
    const yest = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1);
    const label = sameDay(d, today) ? '今天' : sameDay(d, yest) ? '昨天' : WEEK[d.getDay()];
    const year = d.getFullYear() === today.getFullYear() ? '' : `${d.getFullYear()}年`;
    return `${year}${d.getMonth() + 1}月${d.getDate()}日 ${label}`;
  }
  function fmtSize(b) {
    if (b < 1024) return b + ' B';
    if (b < 1024 * 1024) return (b / 1024).toFixed(0) + ' KB';
    return (b / 1024 / 1024).toFixed(b < 10 * 1024 * 1024 ? 2 : 1) + ' MB';
  }
  function fmtAgo(d, verb = '保存') {
    const s = Math.max(0, (Date.now() - d.getTime()) / 1000);
    if (s < 60) return '刚刚' + verb;
    if (s < 3600) return `${Math.floor(s / 60)} 分钟前${verb}`;
    if (s < 86400) return `${Math.floor(s / 3600)} 小时前${verb}`;
    return `${Math.floor(s / 86400)} 天前${verb}`;
  }
  function fmtUntil(d) {
    const s = Math.ceil((d.getTime() - Date.now()) / 1000);
    if (s <= 0) return '即将开始';
    if (s < 60) return `${s} 秒后`;
    if (s < 3600) return `${Math.floor(s / 60)} 分 ${pad(s % 60)} 秒后`;
    if (s < 86400) return `${Math.floor(s / 3600)} 小时 ${Math.floor((s % 3600) / 60)} 分后`;
    return fmtShort(d);
  }
  // Rounds before picking the unit, so 59.6 s reads "1 分钟" rather than "60 秒".
  function fmtGap(s) {
    s = Math.round(s);
    if (s < 60) return `${s} 秒`;
    const m = Math.round(s / 60);
    if (m < 60) return `${m} 分钟`;
    if (m < 1440) return `${Math.floor(m / 60)} 小时 ${m % 60} 分`;
    return `${Math.floor(m / 1440)} 天`;
  }
  const extOf = (name) => ((/\.([a-z0-9]+)$/i.exec(name) || [])[1] || '').toUpperCase().replace('JPEG', 'JPG');
  function ratioOf(w, h) {
    const gcd = (a, b) => (b ? gcd(b, a % b) : a);
    const d = gcd(w, h);
    return w / d <= 32 && h / d <= 32 ? `${w / d}:${h / d}` : `${(w / h).toFixed(2)}:1`;
  }
  // Mirrors store.fit (integer division included): the long edge shrinks to edge, small images keep their size.
  function fitEdge(w, h, edge) {
    if (w <= edge && h <= edge) return [w, h];
    return w >= h ? [edge, Math.max(1, Math.floor(h * edge / w))] : [Math.max(1, Math.floor(w * edge / h)), edge];
  }
  // Go encodes an unset time.Time as 0001-01-01.
  const validTime = (v) => !!v && !v.startsWith('0001');
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const enc = encodeURIComponent;

  // Elements with data-ago / data-until keep their relative time current.
  function tickTimes() {
    const set = (n, v) => { if (n.textContent !== v) n.textContent = v; };
    document.querySelectorAll('[data-ago]').forEach((n) => set(n, fmtAgo(new Date(n.dataset.ago), n.dataset.verb || '')));
    document.querySelectorAll('[data-until]').forEach((n) => set(n, fmtUntil(new Date(n.dataset.until))));
  }

  // ---------- motion helpers ----------
  function replay(node, cls) {
    node.classList.remove(cls);
    void node.offsetWidth;
    node.classList.add(cls);
  }

  // Runs fn once node's own animation ends, with a timeout in case it never starts.
  function afterAnimation(node, fn, ms = 400) {
    let done = false;
    const onEnd = (e) => { if (e.target === node) finish(); };
    const timer = setTimeout(() => finish(), reduced() ? 0 : ms);
    function finish() {
      if (done) return;
      done = true;
      clearTimeout(timer);
      node.removeEventListener('animationend', onEnd);
      fn();
    }
    node.addEventListener('animationend', onEnd);
  }

  function show(node) {
    node.classList.remove('leaving');
    node.hidden = false;
  }

  // Plays the node's .leaving animation, then hides it unless it was shown again meanwhile.
  function hide(node, then) {
    if (node.hidden || node.classList.contains('leaving')) return;
    node.classList.add('leaving');
    afterAnimation(node, () => {
      if (!node.classList.contains('leaving')) return;
      node.classList.remove('leaving');
      node.hidden = true;
      if (then) then();
    });
  }

  function openDialog(d) {
    d.classList.remove('closing');
    if (!d.open) d.showModal();
  }

  function closeDialog(d) {
    if (!d.open || d.classList.contains('closing')) return;
    d.classList.add('closing');
    afterAnimation(d, () => {
      if (!d.classList.contains('closing')) return;
      d.classList.remove('closing');
      d.close();
    }, 250);
  }

  let busyCount = 0;
  function busy(on) {
    busyCount = Math.max(0, busyCount + (on ? 1 : -1));
    document.body.classList.toggle('busy', busyCount > 0);
  }

  function scrollToTop() {
    window.scrollTo({ top: 0, behavior: reduced() ? 'auto' : 'smooth' });
  }

  const TOAST_ICON = { info: '#i-info', ok: '#i-ok', error: '#i-alert' };
  let toastTimer;
  function toast(msg, kind = 'info') {
    const visible = !el.toast.hidden && !el.toast.classList.contains('leaving');
    el.toast.className = 'toast ' + kind;
    el.toastText.textContent = msg;
    el.toastIcon.setAttribute('href', TOAST_ICON[kind] || TOAST_ICON.info);
    el.toast.hidden = false;
    if (visible) { el.toast.style.animation = 'none'; void el.toast.offsetWidth; el.toast.style.animation = ''; }
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => hide(el.toast), kind === 'error' ? 4200 : 2600);
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

  // navigator.clipboard needs a secure context; plain-http deployments fall back to execCommand.
  async function copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (e) {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.style.cssText = 'position:fixed;opacity:0;pointer-events:none';
      el.lb.appendChild(ta);
      ta.select();
      let ok = false;
      try { ok = document.execCommand('copy'); } catch (err) { /* unsupported */ }
      ta.remove();
      return ok;
    }
  }

  // ---------- images ----------
  function imageDone(img, failed) {
    img.classList.add('loaded');
    const box = img.closest('.tile-open');
    if (box) box.classList.add('ready', ...(failed ? ['failed'] : []));
  }
  document.addEventListener('load', (e) => { if (e.target.tagName === 'IMG') imageDone(e.target, false); }, true);
  document.addEventListener('error', (e) => { if (e.target.tagName === 'IMG') imageDone(e.target, true); }, true);
  // Cached images show at once instead of fading in again on every re-render.
  function markCached(root) {
    root.querySelectorAll('img:not(.loaded)').forEach((img) => { if (img.complete && img.naturalWidth) imageDone(img, false); });
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

  const onFirstPage = () => state.sort === 'desc' && state.page === 1;

  let loadSeq = 0;
  // silent reloads come from polling: no skeleton, no progress bar, new tiles are highlighted.
  // Resolves true once the page is shown, false when the request failed or was superseded.
  async function load({ scroll = false, silent = false } = {}) {
    const seq = ++loadSeq;
    if (!silent) {
      if (!state.items.length) renderSkeleton();
      else el.gallery.classList.add('stale');
      busy(true);
    }
    const before = silent ? new Set(state.items.map((it) => it.name)) : null;
    const viewing = state.lbIndex >= 0 && state.items[state.lbIndex] ? state.items[state.lbIndex].name : null;
    const anchor = state.lastIndex !== null && state.items[state.lastIndex] ? state.items[state.lastIndex].name : null;
    try {
      const d = await api(`/api/images?page=${state.page}&size=${state.size}&sort=${state.sort}`);
      if (seq !== loadSeq) return false;
      Object.assign(state, { items: d.items, total: d.total, page: d.page, size: d.size, pages: d.pages, admin: d.admin, guestLimit: d.guest_limit, task: d.task });
      // New pictures shift indexes; keep the shift-click anchor on the same picture.
      if (anchor) { const j = state.items.findIndex((it) => it.name === anchor); state.lastIndex = j >= 0 ? j : null; }
      if (onFirstPage()) { state.latest = d.items[0] || null; hide(el.fresh); }
      writeHash();
      // Keep an open lightbox on the same picture even though indexes shifted.
      let lost = false;
      if (viewing && state.lbIndex >= 0) {
        const i = state.items.findIndex((it) => it.name === viewing);
        if (i >= 0) { state.lbIndex = i; updateLightboxMeta(); }
        else { state.lbIndex = Math.max(0, Math.min(state.lbIndex, state.items.length - 1)); lost = true; }
      }
      olderTimes.clear();
      render({ animate: !silent, slide: silent ? 0 : slideDir });
      slideDir = 0;
      if (before && before.size) markNew(before);
      if (scroll) scrollToTop();
      // The picture left this page: step() opens the right one itself; otherwise show the nearest one.
      if (lost && !stepping) { if (state.items.length) openLightbox(state.lbIndex); else closeLightbox(); }
      return true;
    } catch (e) {
      if (seq === loadSeq && !silent) renderError(e);
      return false;
    } finally {
      if (!silent) busy(false);
      if (seq === loadSeq) el.gallery.classList.remove('stale');
      schedulePoll();
    }
  }

  let slideDir = 0; // pages slide in from the side they were reached from
  function go(page) {
    slideDir = Math.sign(page - state.page);
    state.page = page;
    state.lastIndex = null;
    return load({ scroll: true });
  }

  // ---------- rendering ----------
  function renderSkeleton() {
    el.gallery.className = `gallery view-${state.view} skeleton`;
    const n = Math.min(state.size, 12);
    el.gallery.innerHTML = `<div class="tiles">${'<figure class="tile"><div class="tile-open"></div></figure>'.repeat(n)}</div>`;
    if (onFirstPage() && !el.now.dataset.name) { el.now.hidden = false; el.now.classList.add('pending'); }
  }

  function renderError(e) {
    el.gallery.className = 'gallery';
    el.gallery.innerHTML = `<div class="load-error"><svg><use href="#i-alert"/></svg>
      <p>加载失败：${esc(e.message)}</p>
      <button type="button" class="btn primary" data-retry><svg><use href="#i-refresh"/></svg>重试</button></div>`;
    el.summary.textContent = '';
    el.pager.innerHTML = '';
    // A hero that never got a picture must not keep shimmering above the error.
    if (!el.now.dataset.name) { el.now.classList.remove('pending'); el.now.hidden = true; }
  }

  function tileHTML(it, i) {
    const d = new Date(it.time);
    const sel = state.selected.has(it.name);
    const name = esc(it.name);
    const list = state.view === 'list';
    return `<figure class="tile${sel ? ' selected' : ''}" data-i="${i}" style="--d:${i}">
      <button type="button" class="tile-check" aria-label="选择" aria-pressed="${sel}"><svg><use href="#i-check"/></svg></button>
      <button type="button" class="tile-open" aria-label="查看 ${esc(fmtFull(d))}" title="${esc(fmtFull(d))}"><img loading="lazy" decoding="async" src="/thumb/${enc(it.name)}"${it.w ? ` width="${it.w}" height="${it.h}"` : ''} alt=""></button>
      <figcaption class="tile-cap">
        <time datetime="${esc(it.time)}">${list ? fmtFull(d) : fmtTile(d)}</time>
        ${list ? `<span class="name" title="${name}">${name}</span><span class="size">${it.w ? `<span class="dims">${it.w}×${it.h} · </span>` : ''}${fmtSize(it.size)}</span>` : ''}
        <a class="tile-dl" href="/download/${enc(it.name)}" download title="下载原图" aria-label="下载原图"><svg><use href="#i-download"/></svg></a>
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
      col.h += (it.w ? it.h / it.w : 1) + 0.05; // gap relative to column width
      col.html.push(tileHTML(it, i));
    }
    return `<div class="tiles" data-cols="${n}">${cols.map((c) => `<div class="mcol">${c.html.join('')}</div>`).join('')}</div>`;
  }

  // The hero follows state.latest, which polling keeps current even when the gallery below is not reloaded.
  function renderNow() {
    const latest = onFirstPage() ? state.latest : null;
    el.now.classList.remove('pending');
    el.now.hidden = !latest;
    if (!latest) return;
    const d = new Date(latest.time);
    el.nowTime.dateTime = latest.time;
    el.nowTime.title = fmtFull(d);
    el.nowTime.textContent = `${sameDay(d, new Date()) ? '今天' : fmtDate(d)} ${fmtClock(d)}`;
    el.nowAgo.dataset.ago = latest.time;
    el.nowAgo.textContent = fmtAgo(d, '');
    el.nowMeta.textContent = `${latest.w ? `${latest.w}×${latest.h} · ` : ''}${fmtSize(latest.size)}`;
    el.nowDl.href = `/download/${enc(latest.name)}`;
    if (el.now.dataset.name === latest.name) return;
    const first = !el.now.dataset.name;
    el.now.dataset.name = latest.name;
    const src = `/preview/${enc(latest.name)}`;
    const swap = () => {
      if (el.now.dataset.name !== latest.name) return;
      if (latest.w) { el.nowImg.width = latest.w; el.nowImg.height = latest.h; }
      el.nowImg.src = src;
      if (first) { markCached(el.now); return; }
      replay(el.now, 'updated');
      if (!reduced()) {
        el.nowImg.animate([{ opacity: 0.3, filter: 'blur(8px)' }, { opacity: 1, filter: 'blur(0)' }], { duration: 700, easing: 'ease-out' });
      }
    };
    if (first) return swap();
    // Decode the new picture before swapping so the old one stays until the new one can fade in whole.
    const pre = new Image();
    pre.src = src;
    pre.decode().then(swap, swap);
  }

  function renderTask() {
    const t = state.task;
    el.task.hidden = !t;
    if (!t) return;
    const at = validTime(t.last_fetched) ? new Date(t.last_fetched) : null;
    const next = !t.paused && validTime(t.next_run) ? t.next_run : '';
    el.task.classList.toggle('paused', t.paused);
    el.task.innerHTML = `<span class="task-state" title="定时抓取${t.paused ? '已暂停' : '运行中'}"><span class="task-dot" aria-hidden="true"></span>${t.paused ? '已暂停' : '运行中'}</span>
      <span class="task-item" title="最近获取图片${at ? '：' + fmtFull(at) : '：暂无'}"><svg aria-hidden="true"><use href="#i-refresh"/></svg><span class="sr-only">最近获取</span>${at
        ? `<span data-ago="${esc(t.last_fetched)}">${fmtAgo(at, '')}</span>` : '暂无'}</span>
      ${next ? `<span class="task-item" title="下次抓取：${esc(fmtFull(new Date(next)))}"><svg aria-hidden="true"><use href="#i-clock"/></svg><span class="sr-only">下次抓取</span><span data-until="${esc(next)}">${fmtUntil(new Date(next))}</span></span>` : ''}`;
  }

  // slide is the direction a new page arrives from; view and grouping changes animate in place.
  function render({ animate = false, slide = 0 } = {}) {
    el.gallery.style.setProperty('--sx', `${slide * 28}px`);
    renderTask();
    renderNow();
    el.empty.hidden = state.total > 0;
    el.summary.hidden = state.total === 0;
    const from = (state.page - 1) * state.size + 1;
    const to = from + state.items.length - 1;
    el.summary.innerHTML = state.total
      ? `<strong>${state.total}</strong> 张${state.pages > 1 ? `<span>${from}–${to}</span>` : ''}`
      : '';

    el.gallery.className = `gallery view-${state.view}${state.selectMode ? ' selecting' : ''}${animate ? ' animate' : ''}`;
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
      el.gallery.innerHTML = groups.map((g) => {
        const first = g.entries[0][1];
        const last = g.entries[g.entries.length - 1][1];
        return `<h2 class="group-title" data-from="${first}" data-to="${last}">
          <button type="button" class="group-check" aria-label="选择这一天" title="选择这一天"><svg><use href="#i-check"/></svg></button>
          ${fmtDay(g.d)}<span>${g.entries.length}</span></h2>${tilesHTML(g.entries)}`;
      }).join('');
    }
    markCached(el.gallery);
    renderPager();
    renderControls();
    renderSelection();
  }

  function markNew(before) {
    el.gallery.querySelectorAll('.tile[data-i]').forEach((t) => {
      if (!before.has(state.items[t.dataset.i].name)) t.classList.add('is-new');
    });
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
      btn(state.page - 1, '<svg><use href="#i-left"/></svg>', `class="step" aria-label="上一页" title="上一页（←）"${state.page <= 1 ? ' disabled' : ''}`) +
      pageList(state.page, state.pages).map((p) => p === null
        ? '<span class="gap">…</span>'
        : btn(p, p, p === state.page ? 'aria-current="page"' : '')).join('') +
      btn(state.page + 1, '<svg><use href="#i-right"/></svg>', `class="step" aria-label="下一页" title="下一页（→）"${state.page >= state.pages ? ' disabled' : ''}`) +
      `<label class="jump" title="跳转页码（回车）"><input type="number" min="1" max="${state.pages}" placeholder="${state.page}" aria-label="跳转页码"><span>/ ${state.pages}</span></label>`;
  }

  function renderControls() {
    document.querySelectorAll('.seg [data-view]').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.view === state.view)));
    el.seg.style.setProperty('--seg-i', Math.max(0, VIEWS.indexOf(state.view)));
    const sortLabel = state.sort === 'desc' ? '最新在前' : '最早在前';
    el.sort.querySelector('use').setAttribute('href', state.sort === 'desc' ? '#i-sort-desc' : '#i-sort-asc');
    el.sort.title = `${sortLabel}（O）`;
    el.sort.setAttribute('aria-label', `排序：${sortLabel}`);
    el.size.value = String(state.size);
    el.group.setAttribute('aria-pressed', String(state.group));
    el.selectMode.setAttribute('aria-pressed', String(state.selectMode));
    const account = state.admin ? '管理' : '管理员登录';
    el.account.title = account;
    el.account.setAttribute('aria-label', account);
    el.account.querySelector('use').setAttribute('href', state.admin ? '#i-shield' : '#i-user');
    el.account.classList.toggle('is-admin', state.admin);
    const root = getComputedStyle(document.documentElement);
    el.theme.querySelector('use').setAttribute('href', root.colorScheme === 'dark' ? '#i-sun' : '#i-moon');
    const board = root.getPropertyValue('--board').trim();
    document.querySelectorAll('meta[name="theme-color"]').forEach((m) => { m.content = board; });
  }

  function setView(v) {
    if (state.view === v) return;
    state.view = v;
    store.set('view', v);
    render({ animate: true });
  }

  function toggleSort() {
    state.sort = state.sort === 'desc' ? 'asc' : 'desc';
    store.set('sort', state.sort);
    go(1);
    toast(state.sort === 'desc' ? '最新在前' : '最早在前');
  }

  function toggleGroup() {
    state.group = !state.group;
    store.set('group', state.group ? '1' : '0');
    render({ animate: true });
  }

  function toggleTheme() {
    const dark = getComputedStyle(document.documentElement).colorScheme === 'dark';
    const apply = () => {
      document.documentElement.dataset.theme = dark ? 'light' : 'dark';
      store.set('theme', document.documentElement.dataset.theme);
      renderControls();
    };
    if (!document.startViewTransition || reduced()) return apply();
    // Reveal the new theme as a circle growing from the toggle button.
    const r = el.theme.getBoundingClientRect();
    const x = r.left + r.width / 2;
    const y = r.top + r.height / 2;
    const radius = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y));
    document.startViewTransition(apply).ready.then(() => {
      document.documentElement.animate(
        { clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${radius}px at ${x}px ${y}px)`] },
        { duration: 480, easing: 'cubic-bezier(.4, 0, .2, 1)', pseudoElement: '::view-transition-new(root)' });
    }).catch(() => {});
  }

  // ---------- selection ----------
  const limit = () => (state.admin ? Infinity : state.guestLimit);

  function toggleSelect(name) {
    if (state.selected.has(name)) {
      state.selected.delete(name);
    } else {
      if (state.selected.size >= limit()) {
        toast(`访客一次最多打包下载 ${state.guestLimit} 张`, 'error');
        return false;
      }
      state.selected.add(name);
    }
    return true;
  }

  function selectRange(from, to) {
    const [a, b] = from < to ? [from, to] : [to, from];
    for (let i = a; i <= b; i++) {
      const name = state.items[i].name;
      if (!state.selected.has(name) && !toggleSelect(name)) break;
    }
  }

  function renderSelection() {
    const n = state.selected.size;
    const bar = n > 0 || state.selectMode;
    if (bar) show(el.selbar); else hide(el.selbar);
    document.body.classList.toggle('has-selbar', bar);
    el.selCount.textContent = state.admin ? `已选 ${n}` : `已选 ${n} / ${state.guestLimit}`;
    if (Number(el.selCount.dataset.n || 0) !== n) replay(el.selCount, 'bump');
    el.selCount.dataset.n = n;
    $('#sel-zip').disabled = n === 0;
    el.gallery.querySelectorAll('.tile[data-i]').forEach((t) => {
      const it = state.items[t.dataset.i];
      const on = state.selected.has(it.name);
      t.classList.toggle('selected', on);
      t.querySelector('.tile-check').setAttribute('aria-pressed', String(on));
    });
    el.gallery.querySelectorAll('.group-title').forEach((h) => {
      let all = true;
      for (let i = Number(h.dataset.from); i <= Number(h.dataset.to) && all; i++) all = state.selected.has(state.items[i].name);
      h.classList.toggle('selected', all);
      h.querySelector('.group-check').setAttribute('aria-pressed', String(all));
    });
    if (state.items[state.lbIndex]) {
      const on = state.selected.has(state.items[state.lbIndex].name);
      el.lbSelect.setAttribute('aria-pressed', String(on));
      el.lbSelect.setAttribute('aria-label', on ? '取消选择' : '选择');
    }
  }

  function setSelectMode(on) {
    state.selectMode = on;
    el.gallery.classList.toggle('selecting', on);
    renderControls();
    renderSelection();
  }

  function clearSelection() {
    state.selected.clear();
    state.lastIndex = null;
    setSelectMode(false);
  }

  // ---------- lightbox ----------
  let lbSeq = 0;
  let lbReturn = null;
  let lbFrom = null; // page image the lightbox opened from, for the zoom-out on close
  let lbHistory = false; // an entry was pushed so the Back button closes the lightbox
  let expectPop = false;
  const zoom = { on: false, s: 1, x: 0, y: 0 };
  const MAX_ZOOM = 5;

  function setInert(on) {
    [el.bar, el.main, el.foot, el.selbar, el.fresh, el.toTop].forEach((n) => { n.inert = on; });
  }

  // Sizing the box from the stored dimensions lets a cached thumbnail stand in at full size while the sharp image loads.
  function fitImage() {
    const it = state.items[state.lbIndex];
    const img = el.lbImg;
    if (!it || !it.w) { img.style.width = img.style.height = ''; return; }
    const cs = getComputedStyle(el.lbStage);
    const aw = el.lbStage.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
    const ah = el.lbStage.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom);
    const s = Math.min(aw / it.w, ah / it.h, 1);
    img.style.width = Math.round(it.w * s) + 'px';
    img.style.height = Math.round(it.h * s) + 'px';
  }

  // The 1600px preview covers most screens; HiDPI displays and deep zoom get the original.
  function fullSrc(it, scale = 1) {
    const need = el.lbImg.offsetWidth * scale * (window.devicePixelRatio || 1);
    return it.w > 1600 && need > 1600 ? `/img/${enc(it.name)}` : `/preview/${enc(it.name)}`;
  }

  function updateLightboxMeta() {
    const i = state.lbIndex;
    const it = state.items[i];
    el.lbTime.textContent = fmtFull(new Date(it.time));
    el.lbSub.textContent = `${it.w ? `${it.w}×${it.h} · ` : ''}${fmtSize(it.size)}`;
    el.lbCount.textContent = `${(state.page - 1) * state.size + i + 1} / ${state.total}`;
    el.lbDownload.href = `/download/${enc(it.name)}`;
    el.lbPrev.disabled = i === 0 && state.page === 1;
    el.lbNext.disabled = i === state.items.length - 1 && state.page === state.pages;

    const d = new Date(it.time);
    el.lbpDate.textContent = fmtDay(d);
    el.lbpTime.textContent = fmtClock(d);
    el.lbpAgo.dataset.ago = it.time;
    el.lbpAgo.dataset.verb = '保存';
    el.lbpAgo.textContent = fmtAgo(d);
    el.lbpDownload.href = `/download/${enc(it.name)}`;
    el.lbpName.textContent = it.name;
    renderGrid(i);
    // Sizes the server already keeps, both JPEG: the 1600px preview, and the 480px thumbnail scaled down from it.
    // GIFs are served as-is for the preview, so only the thumbnail is offered.
    const dims = it.w ? `${it.w} × ${it.h}` : '未知';
    const base = it.name.replace(/\.[^.]+$/, '');
    const sizes = [[`/download/${enc(it.name)}`, '原图', `${extOf(it.name)} · ${dims} · ${fmtSize(it.size)}`, '']];
    const preview = it.w ? fitEdge(it.w, it.h, 1600) : null;
    if (preview && Math.max(it.w, it.h) > 1600 && extOf(it.name) !== 'GIF') {
      const [w, h] = preview;
      sizes.push([`/preview/${enc(it.name)}`, '大图', `JPG · ${w} × ${h}`, `${base}-${w}.jpg`]);
    }
    if (preview && Math.max(it.w, it.h) > 480) {
      const [w, h] = fitEdge(...preview, 480);
      sizes.push([`/thumb/${enc(it.name)}`, '缩略图', `JPG · ${w} × ${h}`, `${base}-${w}.jpg`]);
    }
    el.lbpSizeMenu.innerHTML = sizes.map(([href, label, sub, file]) =>
      `<a role="menuitem" href="${href}" download="${esc(file)}"><strong>${label}</strong><span>${esc(sub)}</span></a>`).join('');
  }

  function renderGrid(i) {
    const it = state.items[i];
    const older = olderTime(i);
    const gap = older ? (new Date(it.time) - new Date(older)) / 1000 : 0;
    el.lbpGrid.innerHTML = [
      ['尺寸', it.w ? `${it.w} × ${it.h}` : '未知'], ['大小', fmtSize(it.size)], ['格式', extOf(it.name) || '—'],
      ['比例', it.w ? ratioOf(it.w, it.h) : '—'], ['距上一张', older === undefined ? '…' : gap > 0 ? fmtGap(gap) : '—'],
      ['位置', el.lbCount.textContent],
    ].map(([k, v]) => `<div><dt>${k}</dt><dd>${esc(v)}</dd></div>`).join('');
  }

  // The picture before the oldest one on a page sits on the neighbouring page: fetch just that one, once.
  // Returns its time, null when there is none, or undefined while it is being fetched.
  const olderTimes = new Map();
  function olderTime(i) {
    const older = state.items[state.sort === 'desc' ? i + 1 : i - 1];
    if (older) return older.time;
    const it = state.items[i];
    if (olderTimes.has(it.name)) return olderTimes.get(it.name);
    const g = (state.page - 1) * state.size + i + (state.sort === 'desc' ? 1 : -1);
    if (g < 0 || g >= state.total) return null;
    const n = 12; // the server's smallest page size
    const page = Math.floor(g / n) + 1;
    olderTimes.set(it.name, undefined);
    api(`/api/images?page=${page}&size=${n}&sort=${state.sort}`).then((d) => {
      const o = d.page === page ? d.items[g % n] : null;
      olderTimes.set(it.name, o ? o.time : null);
    }, () => olderTimes.delete(it.name)).then(() => {
      if (state.items[state.lbIndex] === it) renderGrid(state.lbIndex);
    });
    return undefined;
  }

  const wideScreen = window.matchMedia('(min-width: 1100px)');
  function setInfo(on, save) {
    closeMenu();
    el.lb.classList.toggle('info-open', on);
    el.lbInfo.setAttribute('aria-pressed', String(on));
    if (save) store.set('info', on ? '1' : '0');
    if (state.lbIndex < 0) return;
    fitImage();
    if (zoom.on) { clampPan(); applyZoom(); }
  }

  function imgFor(i) {
    const it = state.items[i];
    if (it && lbFrom === el.nowImg && !el.now.hidden && el.now.dataset.name === it.name) return el.nowImg;
    return el.gallery.querySelector(`.tile[data-i="${i}"] .tile-open img`);
  }

  function visibleRect(node) {
    if (!node || !node.isConnected) return null;
    const r = node.getBoundingClientRect();
    return r.width && r.bottom > 0 && r.top < innerHeight ? r : null;
  }

  // Moves the lightbox image between its place on the page and the stage.
  function flip(rect, back) {
    const to = el.lbImg.getBoundingClientRect();
    if (!rect || !to.width || reduced()) return null;
    const s = rect.width / to.width;
    const dx = rect.left + rect.width / 2 - (to.left + to.width / 2);
    const dy = rect.top + rect.height / 2 - (to.top + to.height / 2);
    const frames = [{ transform: `translate(${dx}px, ${dy}px) scale(${s})` }, { transform: 'none' }];
    if (back) frames.reverse();
    return el.lbImg.animate(frames, { duration: back ? 260 : 340, easing: 'cubic-bezier(.4, 0, .22, 1)', fill: back ? 'forwards' : 'none' });
  }

  function openLightbox(i, dir = 0, from = null) {
    const it = state.items[i];
    if (!it) return;
    const opening = state.lbIndex < 0;
    state.lbIndex = i;
    resetZoom();
    closeMenu();
    updateLightboxMeta();
    const img = el.lbImg;
    const seq = ++lbSeq;
    if (opening) {
      // Reopened before the closing flip ended: its forwards fill would pin the image to the old tile.
      img.getAnimations().forEach((a) => a.cancel());
      el.lb.classList.remove('flipping');
      lbFrom = from;
      lbReturn = document.activeElement;
      el.lb.classList.remove('chrome-off');
      show(el.lb);
      // The detail panel starts open on wide screens unless the viewer closed it last time.
      setInfo(wideScreen.matches && store.get('info', '1') === '1');
      setInert(true);
      document.body.style.overflow = 'hidden';
      document.documentElement.classList.add('lb-open');
      if (!lbHistory) { history.pushState({ lightbox: true }, ''); lbHistory = true; }
      $('#lb-close').focus({ preventScroll: true });
    }
    // Show the image already on the page at once, then swap in the sharp one when it is decoded.
    img.src = from && from.currentSrc ? from.currentSrc : `/thumb/${enc(it.name)}`;
    img.alt = fmtFull(new Date(it.time));
    fitImage();
    if (opening) {
      if (!flip(visibleRect(from)) && !reduced()) {
        img.animate([{ opacity: 0, transform: 'scale(0.94)' }, { opacity: 1, transform: 'none' }], { duration: 300, easing: 'cubic-bezier(.2, .8, .2, 1)' });
      }
    } else if (dir && !reduced()) {
      img.animate([{ opacity: 0, transform: `translateX(${dir * 56}px)` }, { opacity: 1, transform: 'none' }],
        { duration: 300, easing: 'cubic-bezier(.2, .8, .2, 1)' });
    }
    const src = fullSrc(it);
    const spin = setTimeout(() => { if (seq === lbSeq) el.lbStage.classList.add('loading'); }, 250);
    const pre = new Image();
    pre.src = src;
    const done = () => {
      clearTimeout(spin);
      if (seq !== lbSeq) return;
      el.lbStage.classList.remove('loading');
      if (!(zoom.on && img.getAttribute('src').startsWith('/img/'))) img.src = src;
    };
    pre.decode().then(done, done);
    for (const n of [state.items[i + 1], state.items[i - 1]]) if (n) new Image().src = `/preview/${enc(n.name)}`;
    renderSelection();
  }

  function closeLightbox(fromHistory = false) {
    if (state.lbIndex < 0) return;
    const shown = imgFor(state.lbIndex);
    const rect = zoom.on ? null : visibleRect(shown);
    // A reload re-renders the tiles, so the button that opened the lightbox may be gone.
    const back = lbReturn && lbReturn.isConnected ? lbReturn : shown && shown.closest('button');
    ++lbSeq;
    state.lbIndex = -1;
    closeMenu();
    resetZoom();
    setInert(false);
    document.body.style.overflow = '';
    el.lbStage.classList.remove('loading');
    const anim = flip(rect, true);
    el.lb.classList.toggle('flipping', !!anim);
    hide(el.lb, () => {
      if (anim) anim.cancel();
      el.lb.classList.remove('flipping');
      if (state.lbIndex < 0) document.documentElement.classList.remove('lb-open');
      el.lbImg.removeAttribute('src');
      el.lbImg.style.width = el.lbImg.style.height = '';
    });
    if (back) back.focus({ preventScroll: true });
    lbReturn = null;
    lbFrom = null;
    if (lbHistory) {
      lbHistory = false;
      if (!fromHistory) { expectPop = true; history.back(); }
    }
  }

  let stepping = false;
  async function step(delta) {
    if (state.lbIndex < 0 || stepping) return;
    const i = state.lbIndex + delta;
    if (i >= 0 && i < state.items.length) return openLightbox(i, delta);
    const page = state.page + delta;
    if (page < 1 || page > state.pages) return;
    stepping = true;
    const from = state.page;
    try {
      if (await go(page)) {
        if (state.lbIndex >= 0) openLightbox(delta < 0 ? state.items.length - 1 : 0, delta);
      } else if (state.page === page) {
        // Still showing the old page's pictures: put the gallery back under them.
        state.page = from;
        writeHash();
        render();
        toast('加载失败，请稍后再试', 'error');
      }
    } finally {
      stepping = false;
    }
  }

  function applyZoom() {
    el.lbImg.style.transform = zoom.on ? `translate(${zoom.x}px, ${zoom.y}px) scale(${zoom.s})` : '';
    el.lbStage.classList.toggle('zoomed', zoom.on);
    el.lbZoom.setAttribute('aria-pressed', String(zoom.on));
    el.lbZoom.setAttribute('aria-label', zoom.on ? '还原' : '放大');
    el.lbZoom.querySelector('use').setAttribute('href', zoom.on ? '#i-zoom-out' : '#i-zoom');
  }

  function clampPan() {
    const mx = Math.max(0, (el.lbImg.offsetWidth * zoom.s - el.lbStage.clientWidth) / 2);
    const my = Math.max(0, (el.lbImg.offsetHeight * zoom.s - el.lbStage.clientHeight) / 2);
    zoom.x = Math.min(mx, Math.max(-mx, zoom.x));
    zoom.y = Math.min(my, Math.max(-my, zoom.y));
  }

  function resetZoom() {
    Object.assign(zoom, { on: false, s: 1, x: 0, y: 0 });
    applyZoom();
  }

  // Scales to s while keeping the point (cx, cy) under the pointer.
  function zoomTo(s, cx, cy) {
    const img = el.lbImg;
    if (state.lbIndex < 0 || !img.offsetWidth) return;
    s = Math.min(MAX_ZOOM, Math.max(1, s));
    if (s < 1.02) return resetZoom();
    const r = img.getBoundingClientRect();
    const mx = r.left + r.width / 2 - zoom.x;
    const my = r.top + r.height / 2 - zoom.y;
    const px = (cx ?? mx) - mx;
    const py = (cy ?? my) - my;
    zoom.x = px - (px - zoom.x) * (s / zoom.s);
    zoom.y = py - (py - zoom.y) * (s / zoom.s);
    zoom.s = s;
    zoom.on = true;
    clampPan();
    const want = fullSrc(state.items[state.lbIndex], s);
    if (want.startsWith('/img/') && !img.getAttribute('src').startsWith('/img/')) img.src = want;
    applyZoom();
  }

  function toggleZoom(cx, cy) {
    if (state.lbIndex < 0) return;
    if (zoom.on) return resetZoom();
    const it = state.items[state.lbIndex];
    const img = el.lbImg;
    if (!img.offsetWidth) return;
    zoomTo(Math.min(4, Math.max(2, (it.w || img.naturalWidth) / img.offsetWidth)), cx, cy);
  }

  function panBy(dx, dy) {
    zoom.x += dx;
    zoom.y += dy;
    clampPan();
    applyZoom();
  }

  function lbToggleSelect() {
    if (state.lbIndex < 0) return;
    toggleSelect(state.items[state.lbIndex].name);
    state.lastIndex = state.lbIndex;
    renderSelection();
  }

  // ---------- link menu ----------
  function linkText(fmt, it) {
    const url = new URL(`/img/${enc(it.name)}`, location.href).href;
    const alt = fmtFull(new Date(it.time));
    return { url, md: `![${alt}](${url})`, html: `<img src="${url}" alt="${alt}">`, bb: `[img]${url}[/img]` }[fmt];
  }

  let menuBtn = null; // the trigger of the open menu; its menu is the next sibling
  function openMenu(btn) {
    closeMenu();
    const menu = btn.nextElementSibling;
    menu.hidden = false;
    btn.setAttribute('aria-expanded', 'true');
    menuBtn = btn;
    menu.querySelector('button, a').focus();
  }

  function closeMenu(refocus) {
    if (!menuBtn) return;
    const btn = menuBtn;
    menuBtn = null;
    btn.nextElementSibling.hidden = true;
    btn.setAttribute('aria-expanded', 'false');
    if (refocus) btn.focus();
  }

  function menuKey(e) {
    const items = [...menuBtn.nextElementSibling.querySelectorAll('button, a')];
    const at = items.indexOf(document.activeElement);
    if (e.key === 'Escape' || e.key === 'Tab') closeMenu(e.key === 'Escape');
    else if (e.key === 'ArrowDown') items[(at + 1) % items.length].focus();
    else if (e.key === 'ArrowUp') items[(at - 1 + items.length) % items.length].focus();
    // Links ignore Space natively; click both kinds so Enter and Space behave alike.
    else if ((e.key === 'Enter' || e.key === ' ') && at >= 0) items[at].click();
    else return false;
    if (e.key !== 'Tab') e.preventDefault();
    return true;
  }

  async function copyLink(fmt, label) {
    const it = state.items[state.lbIndex];
    if (!it) return false;
    const ok = await copyText(linkText(fmt, it));
    toast(ok ? `已复制${fmt === 'url' ? '原图链接' : ' ' + label}` : '复制失败，请长按下载按钮复制链接', ok ? 'ok' : 'error');
    return ok;
  }

  // ---------- admin ----------
  async function openAdmin() {
    el.adminStats.innerHTML = '<dt class="loading-row">状态</dt><dd class="loading-row">加载中…</dd>';
    el.toggleSchedule.disabled = true;
    el.scheduleHint.textContent = '加载中…';
    $('#download-all').innerHTML = '<svg><use href="#i-download"/></svg>下载全部';
    openDialog(el.adminDialog);
    try {
      renderStats(await api('/api/status'));
    } catch (e) { handleAuthError(e); }
  }

  function renderStats(s) {
    const f = s.fetch;
    const t = (v) => (validTime(v) ? fmtFull(new Date(v)) : '暂无');
    const result = { saved: '<span class="ok">已保存新画面</span>', unchanged: '画面没有变化，已跳过', error: '<span class="bad">失败</span>' }[f.result] || '尚未运行';
    el.adminStats.innerHTML = `
      <dt>存档总数</dt><dd>${s.count} 张</dd>
      <dt>上次抓取</dt><dd>${t(f.last_run)}</dd>
      <dt>最近获取</dt><dd>${t(f.last_fetched)}</dd>
      <dt>结果</dt><dd>${result}${f.message ? `<br><span class="bad">${esc(f.message)}</span>` : ''}</dd>
      <dt>最近保存</dt><dd>${t(f.last_saved)}</dd>
      <dt>本次启动</dt><dd>抓取 ${f.runs} 次，保存 ${f.saved} 张</dd>${tokenRow(s.token)}`;
    $('#download-all').innerHTML = `<svg><use href="#i-download"/></svg>下载全部 ${s.count} 张`;
    el.toggleSchedule.disabled = false;
    el.toggleSchedule.setAttribute('aria-checked', String(!f.paused));
    el.scheduleHint.innerHTML = f.paused
      ? '已暂停，仍可手动立即抓取'
      : `计划 <code>${esc(s.schedule)}</code>${validTime(s.next_run) ? `，下次 <span data-until="${esc(s.next_run)}">${fmtUntil(new Date(s.next_run))}</span>` : ''}`;
    state.task = { paused: f.paused, last_fetched: f.last_fetched, next_run: s.next_run };
    renderTask();
    schedulePoll();
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
      closeDialog(el.adminDialog);
      renderControls();
      toast('登录已过期，请重新登录', 'error');
    } else {
      toast(e.message, 'error');
    }
  }

  // ---------- events: header & gallery ----------
  document.querySelectorAll('.seg [data-view]').forEach((b) => b.addEventListener('click', () => setView(b.dataset.view)));
  el.sort.addEventListener('click', toggleSort);
  el.size.addEventListener('change', () => { state.size = Number(el.size.value); store.set('size', state.size); go(1); });
  el.group.addEventListener('click', toggleGroup);
  el.selectMode.addEventListener('click', () => setSelectMode(!state.selectMode));
  el.theme.addEventListener('click', toggleTheme);
  $('#brand').addEventListener('click', (e) => {
    e.preventDefault();
    if (state.sort !== 'desc') { state.sort = 'desc'; store.set('sort', 'desc'); }
    go(1);
  });

  el.gallery.addEventListener('click', (e) => {
    if (e.target.closest('[data-retry]')) { load(); return; }
    const head = e.target.closest('.group-check');
    if (head) {
      const h = head.closest('.group-title');
      const from = Number(h.dataset.from);
      const to = Number(h.dataset.to);
      if (h.classList.contains('selected')) {
        for (let i = from; i <= to; i++) state.selected.delete(state.items[i].name);
      } else {
        selectRange(from, to);
      }
      state.lastIndex = to;
      renderSelection();
      return;
    }
    const tile = e.target.closest('.tile[data-i]');
    if (!tile) return;
    const i = Number(tile.dataset.i);
    const it = state.items[i];
    const selecting = e.target.closest('.tile-check') || (e.target.closest('.tile-open') && (state.selectMode || state.selected.size));
    if (selecting) {
      if (e.shiftKey && state.lastIndex !== null && state.lastIndex !== i && state.items[state.lastIndex]) selectRange(state.lastIndex, i);
      else toggleSelect(it.name);
      state.lastIndex = i;
      renderSelection();
    } else if (e.target.closest('.tile-open')) {
      openLightbox(i, 0, tile.querySelector('.tile-open img'));
    }
  });

  // The hero may already show a newer picture than the gallery holds; catch the gallery up first.
  async function openLatest() {
    const name = el.now.dataset.name;
    let i = state.items.findIndex((it) => it.name === name);
    if (i < 0) {
      await load({ silent: true });
      i = state.items.findIndex((it) => it.name === name);
    }
    if (i >= 0) openLightbox(i, 0, el.nowImg);
  }
  $('#now-open').addEventListener('click', openLatest);
  $('#now-zoom').addEventListener('click', openLatest);

  el.pager.addEventListener('click', (e) => {
    const b = e.target.closest('button[data-page]');
    if (b && !b.disabled) go(Number(b.dataset.page));
  });
  el.pager.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' || !e.target.matches('.jump input')) return;
    const p = Math.min(Math.max(1, Number(e.target.value) || 1), state.pages);
    go(p);
  });

  $('#sel-clear').addEventListener('click', clearSelection);
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
    toast(`开始下载 ${names.length} 张`, 'ok');
  });

  // ---------- events: lightbox ----------
  $('#lb-close').addEventListener('click', () => closeLightbox());
  el.lbPrev.addEventListener('click', () => step(-1));
  el.lbNext.addEventListener('click', () => step(1));
  el.lbSelect.addEventListener('click', lbToggleSelect);
  el.lbZoom.addEventListener('click', () => toggleZoom());
  for (const btn of [el.lbLink, el.lbpSize]) {
    btn.addEventListener('click', () => (menuBtn === btn ? closeMenu(true) : openMenu(btn)));
  }
  el.lbMenu.addEventListener('click', (e) => {
    const b = e.target.closest('[data-fmt]');
    if (!b) return;
    closeMenu(true);
    copyLink(b.dataset.fmt, b.textContent);
  });
  el.lbpSizeMenu.addEventListener('click', (e) => { if (e.target.closest('a')) closeMenu(true); });
  $('#lbp-share').addEventListener('click', async (e) => {
    const chip = e.target.closest('.chip');
    if (chip && await copyLink(chip.dataset.fmt, chip.textContent)) replay(chip, 'done');
  });
  el.lbInfo.addEventListener('click', () => setInfo(!el.lb.classList.contains('info-open'), true));
  document.addEventListener('pointerdown', (e) => {
    if (menuBtn && !menuBtn.parentElement.contains(e.target)) closeMenu();
  });
  el.lb.addEventListener('click', (e) => {
    if (!zoom.on && (e.target === el.lb || e.target === el.lbStage || e.target.classList.contains('lb-foot'))) closeLightbox();
  });

  // Mouse: click zooms towards the pointer, the wheel zooms smoothly, drag pans while zoomed.
  let drag = null;
  let dragMoved = false;
  let lastTouch = 0; // clicks synthesized from taps are handled by the touch code
  el.lbImg.addEventListener('click', (e) => {
    if (Date.now() - lastTouch < 700) return;
    if (dragMoved) { dragMoved = false; return; }
    toggleZoom(e.clientX, e.clientY);
  });
  let wheelTimer;
  el.lbStage.addEventListener('wheel', (e) => {
    if (state.lbIndex < 0) return;
    e.preventDefault();
    const dy = e.deltaMode === 1 ? e.deltaY * 16 : e.deltaY;
    // Trackpad pinches arrive as ctrl+wheel with small deltas.
    const k = e.ctrlKey ? 0.01 : 0.002;
    el.lbStage.classList.add('wheeling');
    clearTimeout(wheelTimer);
    wheelTimer = setTimeout(() => el.lbStage.classList.remove('wheeling'), 160);
    zoomTo(zoom.s * Math.exp(-dy * k), e.clientX, e.clientY);
  }, { passive: false });
  el.lbImg.addEventListener('pointerdown', (e) => {
    dragMoved = false;
    if (!zoom.on || e.button !== 0) return;
    e.preventDefault();
    drag = { id: e.pointerId, x: e.clientX - zoom.x, y: e.clientY - zoom.y, sx: e.clientX, sy: e.clientY };
    el.lbImg.setPointerCapture(e.pointerId);
    el.lbStage.classList.add('dragging');
  });
  el.lbImg.addEventListener('pointermove', (e) => {
    if (!drag || e.pointerId !== drag.id) return;
    if (Math.hypot(e.clientX - drag.sx, e.clientY - drag.sy) > 4) dragMoved = true;
    zoom.x = e.clientX - drag.x;
    zoom.y = e.clientY - drag.y;
    clampPan();
    applyZoom();
  });
  const endDrag = (e) => {
    if (!drag || e.pointerId !== drag.id) return;
    drag = null;
    el.lbStage.classList.remove('dragging');
  };
  el.lbImg.addEventListener('pointerup', endDrag);
  el.lbImg.addEventListener('pointercancel', endDrag);

  // Touch: swipe sideways to change pictures, swipe down to close, pinch or double tap to zoom,
  // a single tap on the picture hides or shows the controls.
  let touch = null;
  let pinch = null;
  let lastTap = 0;
  let tapTimer = 0;
  const span = (ts) => Math.hypot(ts[0].clientX - ts[1].clientX, ts[0].clientY - ts[1].clientY);
  el.lbStage.addEventListener('touchstart', (e) => {
    if (e.touches.length === 2) {
      touch = null;
      drag = null;
      pinch = { d: span(e.touches), s: zoom.s };
      el.lbStage.classList.add('dragging');
      return;
    }
    if (e.touches.length !== 1) { touch = null; return; }
    const t = e.touches[0];
    touch = { x: t.clientX, y: t.clientY, dx: 0, dy: 0, axis: null, swipe: !zoom.on, onImg: e.target === el.lbImg };
  }, { passive: true });
  el.lbStage.addEventListener('touchmove', (e) => {
    if (pinch && e.touches.length === 2) {
      const [a, b] = e.touches;
      zoomTo(pinch.s * span(e.touches) / pinch.d, (a.clientX + b.clientX) / 2, (a.clientY + b.clientY) / 2);
      return;
    }
    if (!touch) return;
    const t = e.touches[0];
    touch.dx = t.clientX - touch.x;
    touch.dy = t.clientY - touch.y;
    if (!touch.axis && Math.hypot(touch.dx, touch.dy) > 10) touch.axis = Math.abs(touch.dx) > Math.abs(touch.dy) ? 'x' : 'y';
    if (!touch.swipe || !touch.axis) return;
    el.lbStage.classList.add('dragging');
    if (touch.axis === 'x') {
      el.lbImg.style.transform = `translateX(${touch.dx}px)`;
    } else if (touch.dy > 0) {
      el.lbImg.style.transform = `translateY(${touch.dy}px) scale(${1 - Math.min(touch.dy / 1500, 0.15)})`;
      el.lb.style.setProperty('--lb-fade', String(1 - Math.min(touch.dy / 600, 0.5)));
    }
  }, { passive: true });
  const endTouch = (x, y) => {
    lastTouch = Date.now();
    if (!touch) return;
    const { dx, dy, axis, swipe, onImg } = touch;
    touch = null;
    el.lbStage.classList.remove('dragging');
    el.lb.style.removeProperty('--lb-fade');
    if (swipe) el.lbImg.style.transform = '';
    if (!axis) {
      const now = Date.now();
      clearTimeout(tapTimer);
      if (x !== undefined && now - lastTap < 300) {
        lastTap = 0;
        toggleZoom(x, y);
      } else {
        lastTap = now;
        if (onImg) tapTimer = setTimeout(() => el.lb.classList.toggle('chrome-off'), 300);
      }
      return;
    }
    if (!swipe) return;
    if (axis === 'x' && Math.abs(dx) > 60) step(dx < 0 ? 1 : -1);
    else if (axis === 'y' && dy > 100) closeLightbox();
  };
  el.lbStage.addEventListener('touchend', (e) => {
    if (pinch) {
      if (e.touches.length < 2) { pinch = null; el.lbStage.classList.remove('dragging'); }
      lastTouch = Date.now();
      return;
    }
    const t = e.changedTouches[0];
    endTouch(t && t.clientX, t && t.clientY);
  });
  el.lbStage.addEventListener('touchcancel', () => {
    pinch = null;
    lastTap = 0;
    el.lbStage.classList.remove('dragging');
    endTouch();
  });

  window.addEventListener('popstate', () => {
    if (expectPop) {
      expectPop = false;
    } else if (lbHistory) {
      lbHistory = false;
      closeLightbox(true);
    } else {
      return;
    }
    // Stepping across pages in the lightbox moved the page; keep it rather than the older entry's hash.
    writeHash();
  });

  // ---------- keyboard ----------
  function lightboxKey(e) {
    if (menuBtn && menuKey(e)) return;
    const pan = { ArrowLeft: [80, 0], ArrowRight: [-80, 0], ArrowUp: [0, 80], ArrowDown: [0, -80] }[e.key];
    const k = e.key.toLowerCase();
    if (pan && zoom.on) panBy(...pan);
    else if (e.key === 'Escape') { if (zoom.on) resetZoom(); else closeLightbox(); }
    else if (e.key === 'ArrowLeft') step(-1);
    else if (e.key === 'ArrowRight') step(1);
    else if (k === 'z') toggleZoom();
    else if (k === 'x') lbToggleSelect();
    else if (k === 'i') setInfo(!el.lb.classList.contains('info-open'), true);
    else if (k === 'c') { if (el.lb.classList.contains('info-open')) copyLink('url'); else openMenu(el.lbLink); }
    else if (k === 'd') el.lbDownload.click();
    else return;
    e.preventDefault();
  }

  document.addEventListener('keydown', (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return;
    if (state.lbIndex >= 0) { lightboxKey(e); return; }
    if (document.querySelector('dialog[open]') || (e.target instanceof Element && e.target.closest('input, select, textarea, [contenteditable]'))) return;
    const k = e.key.length === 1 ? e.key.toLowerCase() : e.key;
    if (k === 'ArrowLeft' && state.page > 1) go(state.page - 1);
    else if (k === 'ArrowRight' && state.page < state.pages) go(state.page + 1);
    else if (k === '1' || k === '2' || k === '3') setView(VIEWS[k - 1]);
    else if (k === 's') setSelectMode(!state.selectMode);
    else if (k === 't') toggleTheme();
    else if (k === 'o') toggleSort();
    else if (k === 'g') toggleGroup();
    else if (k === '?') openDialog(el.keysDialog);
    else if (k === 'Escape' && (state.selectMode || state.selected.size)) clearSelection();
    else return;
    e.preventDefault();
  });

  // ---------- dialogs ----------
  document.querySelectorAll('dialog.modal').forEach((d) => {
    d.addEventListener('cancel', (e) => { e.preventDefault(); closeDialog(d); });
    // A click on the backdrop lands on the dialog itself; ignore drags that started inside it.
    let downOnBackdrop = false;
    d.addEventListener('pointerdown', (e) => { downOnBackdrop = e.target === d; });
    d.addEventListener('click', (e) => { if (e.target === d && downOnBackdrop) closeDialog(d); });
  });
  $('#show-keys').addEventListener('click', () => openDialog(el.keysDialog));
  $('#keys-close').addEventListener('click', () => closeDialog(el.keysDialog));

  el.account.addEventListener('click', () => {
    if (state.admin) return openAdmin();
    el.loginError.hidden = true;
    el.loginForm.reset();
    setPasswordVisible(false);
    openDialog(el.loginDialog);
  });
  $('#login-cancel').addEventListener('click', () => closeDialog(el.loginDialog));

  const pwToggle = $('#pw-toggle');
  function setPasswordVisible(on) {
    el.loginForm.elements.password.type = on ? 'text' : 'password';
    pwToggle.setAttribute('aria-pressed', String(on));
    pwToggle.setAttribute('aria-label', on ? '隐藏密码' : '显示密码');
    pwToggle.querySelector('use').setAttribute('href', on ? '#i-eye-off' : '#i-eye');
  }
  pwToggle.addEventListener('click', () => setPasswordVisible(el.loginForm.elements.password.type === 'password'));

  el.loginForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const fd = new FormData(el.loginForm);
    const btn = el.loginSubmit;
    btn.disabled = true;
    btn.classList.add('loading');
    try {
      await api('/api/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: fd.get('username'), password: fd.get('password') }),
      });
      closeDialog(el.loginDialog);
      toast('已登录为管理员', 'ok');
      await load();
    } catch (err) {
      el.loginError.innerHTML = `<svg><use href="#i-alert"/></svg>${esc(err.message)}`;
      el.loginError.hidden = false;
      replay(el.loginForm, 'shake');
      el.loginForm.elements.password.select();
    } finally {
      btn.disabled = false;
      btn.classList.remove('loading');
    }
  });

  $('#admin-close').addEventListener('click', () => closeDialog(el.adminDialog));
  $('#logout').addEventListener('click', async () => {
    await api('/api/logout', { method: 'POST' }).catch(() => {});
    closeDialog(el.adminDialog);
    // Guests are capped, so trim an oversized selection.
    if (state.selected.size > state.guestLimit) state.selected = new Set([...state.selected].slice(0, state.guestLimit));
    toast('已退出登录', 'ok');
    await load();
  });
  el.fetchNow.addEventListener('click', async () => {
    const label = el.fetchNow.querySelector('span');
    el.fetchNow.disabled = true;
    el.fetchNow.classList.add('loading');
    label.textContent = '抓取中…';
    try {
      const s = await api('/api/fetch', { method: 'POST' });
      renderStats(s);
      if (s.fetch.result === 'saved') { toast('已保存新画面', 'ok'); state.page = 1; await load(); }
      else if (s.fetch.result === 'unchanged') toast('画面没有变化');
      else toast('抓取失败：' + s.fetch.message, 'error');
    } catch (e) {
      handleAuthError(e);
    } finally {
      el.fetchNow.disabled = false;
      el.fetchNow.classList.remove('loading');
      label.textContent = '立即抓取';
    }
  });

  el.toggleSchedule.addEventListener('click', async () => {
    const sw = el.toggleSchedule;
    if (sw.dataset.busy) return;
    const paused = sw.getAttribute('aria-checked') === 'true';
    sw.dataset.busy = '1';
    sw.setAttribute('aria-checked', String(!paused)); // flip now; the response confirms or reverts
    try {
      renderStats(await api('/api/schedule', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ paused }),
      }));
      toast(paused ? '已暂停定时抓取' : '已恢复定时抓取', 'ok');
    } catch (e) {
      sw.setAttribute('aria-checked', String(paused));
      handleAuthError(e);
    } finally {
      delete sw.dataset.busy;
    }
  });

  // ---------- polling ----------
  // Checks shortly after each scheduled fetch, and again every few seconds while that fetch is still
  // running, at least once a minute. A new picture always refreshes the hero; the gallery reloads
  // silently when that cannot disrupt the viewer, otherwise a pill offers it.
  let pollTimer = 0;
  let lastPoll = 0;
  let awaitRun = ''; // the scheduled run the next poll looks for
  let awaitUntil = 0;
  function schedulePoll(delay) {
    clearTimeout(pollTimer);
    if (delay === undefined && awaitUntil > Date.now()) delay = 4000; // a page load mid-run keeps watching it
    if (delay === undefined) {
      delay = 60000;
      awaitRun = '';
      awaitUntil = 0;
      const t = state.task;
      if (t && !t.paused && validTime(t.next_run)) {
        const wait = new Date(t.next_run) - Date.now() + 5000;
        if (wait <= 65000) { delay = Math.max(wait, 5000); awaitRun = t.next_run; }
      }
    }
    pollTimer = setTimeout(poll, delay);
  }

  async function poll() {
    clearTimeout(pollTimer);
    if (document.hidden) return; // resumed by visibilitychange
    lastPoll = Date.now();
    let again = false;
    try {
      const d = await api('/api/images?page=1&size=12&sort=desc');
      const t = d.task;
      state.task = t;
      renderTask();
      // last_fetched is the start time of the last successful download, so an older value means
      // the expected run has not finished (or failed); give it up to 45 s.
      const finished = validTime(t.last_fetched) && new Date(t.last_fetched) >= new Date(awaitRun) - 1000;
      if (awaitRun && !t.paused && !finished) {
        if (!awaitUntil) awaitUntil = Date.now() + 45000;
        again = Date.now() < awaitUntil;
      }
      if (!again) awaitUntil = 0;
      const top = d.items[0] || null;
      if (state.latest === undefined) {
        state.latest = top;
      } else if ((top && top.name) !== (state.latest && state.latest.name)) {
        state.latest = top;
        if (onFirstPage()) {
          renderNow();
          const idle = state.lbIndex < 0 && window.scrollY < 400 && !document.querySelector('dialog[open]') &&
            !el.gallery.contains(document.activeElement); // re-rendering would drop keyboard focus
          if (idle) { await load({ silent: true }); return; } // load reschedules
        }
        show(el.fresh);
      }
    } catch (e) { /* offline: try again next tick */ }
    schedulePoll(again ? 4000 : undefined);
  }

  document.addEventListener('visibilitychange', () => {
    if (document.hidden) return;
    tickTimes();
    if (Date.now() - lastPoll > 15000) poll(); else schedulePoll();
  });
  window.addEventListener('offline', () => toast('网络已断开，恢复后会自动刷新', 'error'));
  window.addEventListener('online', () => { toast('网络已恢复', 'ok'); poll(); });
  setInterval(() => { if (!document.hidden) tickTimes(); }, 1000);

  $('#fresh-btn').addEventListener('click', () => { hide(el.fresh); state.sort = 'desc'; store.set('sort', 'desc'); go(1); });

  // ---------- layout ----------
  let scrollQueued = false;
  function onScroll() {
    scrollQueued = false;
    const y = window.scrollY;
    el.bar.classList.toggle('scrolled', y > 4);
    el.toTop.classList.toggle('show', y > 800);
  }
  window.addEventListener('scroll', () => {
    if (scrollQueued) return;
    scrollQueued = true;
    requestAnimationFrame(onScroll);
  }, { passive: true });
  el.toTop.addEventListener('click', scrollToTop);

  // Sticky day headers and the "new images" pill sit just below the header, whose height wraps.
  if ('ResizeObserver' in window) {
    new ResizeObserver(() => document.documentElement.style.setProperty('--bar-h', el.bar.offsetHeight + 'px')).observe(el.bar);
  }

  let resizeTimer;
  window.addEventListener('resize', () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      const t = el.gallery.querySelector('.tiles[data-cols]');
      if (t && Number(t.dataset.cols) !== masonryCols()) render();
      if (state.lbIndex >= 0) fitImage();
      if (zoom.on) { clampPan(); applyZoom(); }
    }, 150);
  });
  window.addEventListener('hashchange', () => {
    const before = state.page;
    readHash();
    if (state.page !== before) load({ scroll: true });
  });
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', renderControls);

  readHash();
  renderControls();
  onScroll();
  load();
})();
