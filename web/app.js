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
    knownTotal: null,
    lbIndex: -1,
  };

  const el = {
    bar: $('#bar'), main: $('#main'), toTop: $('#to-top'),
    gallery: $('#gallery'), now: $('#now'), summary: $('#summary'), empty: $('#empty'), pager: $('#pager'),
    seg: $('.seg'), sort: $('#sort'), size: $('#size'), group: $('#group'), selectMode: $('#select-mode'),
    theme: $('#theme'), account: $('#account'), accountLabel: $('#account-label'),
    selbar: $('#selbar'), selCount: $('#sel-count'), fresh: $('#fresh'),
    toast: $('#toast'), toastText: $('#toast span'), toastIcon: $('#toast use'),
    lb: $('#lightbox'), lbStage: $('#lb-stage'), lbImg: $('#lb-img'), lbTime: $('#lb-time'), lbSub: $('#lb-sub'),
    lbCount: $('#lb-count'), lbPrev: $('#lb-prev'), lbNext: $('#lb-next'), lbDownload: $('#lb-download'),
    lbSelect: $('#lb-select'), lbZoom: $('#lb-zoom'), lbLink: $('#lb-link'),
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
  const WEEK = ['星期日', '星期一', '星期二', '星期三', '星期四', '星期五', '星期六'];
  const fmtDay = (d) => `${d.getFullYear()}年${d.getMonth() + 1}月${d.getDate()}日 ${WEEK[d.getDay()]}`;
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

  let loadSeq = 0;
  // silent reloads come from polling: no skeleton, no progress bar, new tiles are highlighted.
  async function load({ scroll = false, silent = false } = {}) {
    const seq = ++loadSeq;
    if (!silent) {
      if (!state.items.length) renderSkeleton();
      else el.gallery.classList.add('stale');
      busy(true);
    }
    const before = silent ? new Set(state.items.map((it) => it.name)) : null;
    try {
      const d = await api(`/api/images?page=${state.page}&size=${state.size}&sort=${state.sort}`);
      if (seq !== loadSeq) return;
      Object.assign(state, { items: d.items, total: d.total, page: d.page, pages: d.pages, admin: d.admin, guestLimit: d.guest_limit, task: d.task });
      if (state.sort === 'desc' && state.page === 1) { state.knownTotal = d.total; hide(el.fresh); }
      else if (state.knownTotal === null) state.knownTotal = d.total;
      writeHash();
      render({ animate: !silent });
      if (before && before.size) markNew(before);
      if (scroll) scrollToTop();
    } catch (e) {
      if (seq === loadSeq && !silent) renderError(e);
    } finally {
      if (!silent) busy(false);
      if (seq === loadSeq) el.gallery.classList.remove('stale');
      schedulePoll();
    }
  }

  function go(page) {
    state.page = page;
    state.lastIndex = null;
    return load({ scroll: true });
  }

  // ---------- rendering ----------
  function renderSkeleton() {
    el.gallery.className = `gallery view-${state.view} skeleton`;
    const n = Math.min(state.size, 12);
    el.gallery.innerHTML = `<div class="tiles">${'<figure class="tile"><div class="tile-open"></div><figcaption class="tile-cap">&nbsp;</figcaption></figure>'.repeat(n)}</div>`;
  }

  function renderError(e) {
    el.gallery.className = 'gallery';
    el.gallery.innerHTML = `<div class="load-error"><svg><use href="#i-alert"/></svg>
      <p>加载失败：${esc(e.message)}</p>
      <button type="button" class="btn primary" data-retry><svg><use href="#i-refresh"/></svg>重试</button></div>`;
    el.summary.textContent = '';
    el.pager.innerHTML = '';
  }

  function tileHTML(it, i) {
    const d = new Date(it.time);
    const sel = state.selected.has(it.name);
    const name = esc(it.name);
    return `<figure class="tile${sel ? ' selected' : ''}" data-i="${i}" style="--d:${i}">
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
    const meta = `${fmtSize(latest.size)}，共存档 ${state.total} 个画面`;
    // Keep the loaded preview when only the count changed.
    if (el.now.dataset.name === latest.name) {
      el.now.querySelector('.now-meta').textContent = meta;
      return;
    }
    el.now.dataset.name = latest.name;
    const d = new Date(latest.time);
    el.now.innerHTML = `
      <button type="button" class="now-frame" data-i="0" aria-label="查看最新画面">
        <img src="/preview/${enc(latest.name)}" alt="最新画面">
        <span class="now-zoom"><svg><use href="#i-zoom"/></svg>查看大图</span>
      </button>
      <div class="now-side">
        <p class="now-label">画板最新状态</p>
        <p class="now-date">${fmtDay(d)}</p>
        <p class="now-time">${fmtClock(d)}</p>
        <p class="now-ago" data-ago="${esc(latest.time)}" data-verb="保存">${fmtAgo(d)}</p>
        <p class="now-meta">${meta}</p>
        <a class="btn primary" href="/download/${enc(latest.name)}" download><svg><use href="#i-download"/></svg>下载这张</a>
      </div>`;
    markCached(el.now);
  }

  function renderTask() {
    const t = state.task;
    el.task.hidden = !t;
    if (!t) return;
    const at = validTime(t.last_fetched) ? new Date(t.last_fetched) : null;
    const next = !t.paused && validTime(t.next_run) ? t.next_run : '';
    el.task.classList.toggle('paused', t.paused);
    el.task.innerHTML = `<span class="task-dot" aria-hidden="true"></span>
      <span>定时抓取<strong>${t.paused ? '已暂停' : '运行中'}</strong></span>
      <span class="task-time">最近获取图片：${at
        ? `<time datetime="${esc(t.last_fetched)}">${fmtFull(at)}</time>（<span data-ago="${esc(t.last_fetched)}">${fmtAgo(at, '')}</span>）`
        : '暂无'}</span>
      ${next ? `<span class="task-next">下次抓取：<span data-until="${esc(next)}">${fmtUntil(new Date(next))}</span></span>` : ''}`;
  }

  function render({ animate = false } = {}) {
    renderTask();
    renderNow();
    el.empty.hidden = state.total > 0;
    el.summary.hidden = state.total === 0;
    const from = (state.page - 1) * state.size + 1;
    const to = from + state.items.length - 1;
    el.summary.innerHTML = state.total
      ? `<strong>共 ${state.total} 张</strong><span>第 ${from}–${to} 张，${state.sort === 'desc' ? '最新在前' : '最早在前'}</span>`
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
      el.gallery.innerHTML = groups.map((g) =>
        `<h2 class="group-title">${fmtDay(g.d)}<span>本页 ${g.entries.length} 张</span></h2>${tilesHTML(g.entries)}`).join('');
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
      btn(state.page - 1, '上一页', state.page <= 1 ? 'disabled' : 'title="上一页（←）"') +
      pageList(state.page, state.pages).map((p) => p === null
        ? '<span class="gap">…</span>'
        : btn(p, p, p === state.page ? 'aria-current="page"' : '')).join('') +
      btn(state.page + 1, '下一页', state.page >= state.pages ? 'disabled' : 'title="下一页（→）"') +
      `<label class="jump">跳至<input type="number" min="1" max="${state.pages}" placeholder="${state.page}" aria-label="跳转页码">/ ${state.pages} 页</label>`;
  }

  function renderControls() {
    document.querySelectorAll('.seg [data-view]').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.view === state.view)));
    el.seg.style.setProperty('--seg-i', Math.max(0, VIEWS.indexOf(state.view)));
    el.sort.value = state.sort;
    el.size.value = String(state.size);
    el.group.checked = state.group;
    el.selectMode.setAttribute('aria-pressed', String(state.selectMode));
    el.accountLabel.textContent = state.admin ? '管理' : '管理员登录';
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
    el.selCount.textContent = state.admin ? `已选 ${n} 张` : `已选 ${n} / ${state.guestLimit}`;
    if (Number(el.selCount.dataset.n || 0) !== n) replay(el.selCount, 'bump');
    el.selCount.dataset.n = n;
    $('#sel-zip').disabled = n === 0;
    el.gallery.querySelectorAll('.tile[data-i]').forEach((t) => {
      const it = state.items[t.dataset.i];
      const on = state.selected.has(it.name);
      t.classList.toggle('selected', on);
      t.querySelector('.tile-check').setAttribute('aria-pressed', String(on));
    });
    if (state.lbIndex >= 0) {
      const on = state.selected.has(state.items[state.lbIndex].name);
      el.lbSelect.setAttribute('aria-pressed', String(on));
      el.lbSelect.querySelector('span').textContent = on ? '已选择' : '选择';
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
  const zoom = { on: false, s: 1, x: 0, y: 0 };

  function setInert(on) {
    [el.bar, el.main, el.selbar, el.fresh, el.toTop].forEach((n) => { n.inert = on; });
  }

  function openLightbox(i, dir = 0) {
    const it = state.items[i];
    if (!it) return;
    const opening = state.lbIndex < 0;
    state.lbIndex = i;
    resetZoom();
    const d = new Date(it.time);
    el.lbTime.textContent = fmtFull(d);
    el.lbSub.textContent = `${fmtSize(it.size)}${it.w ? `，${it.w} × ${it.h}` : ''}`;
    el.lbCount.textContent = `${(state.page - 1) * state.size + i + 1} / ${state.total}`;
    el.lbDownload.href = `/download/${enc(it.name)}`;
    el.lbPrev.disabled = i === 0 && state.page === 1;
    el.lbNext.disabled = i === state.items.length - 1 && state.page === state.pages;

    // 1600px preview for most screens; the browser picks the original on large/HiDPI displays.
    const src = `/preview/${enc(it.name)}`;
    const srcset = it.w > 1600 ? `${src} 1600w, /img/${enc(it.name)} ${it.w}w` : '';
    const seq = ++lbSeq;
    const swap = () => {
      if (seq !== lbSeq) return;
      el.lbImg.srcset = srcset;
      el.lbImg.sizes = '100vw';
      el.lbImg.src = src;
      el.lbImg.alt = fmtFull(d);
    };
    if (opening) {
      el.lbStage.classList.add('loading');
      swap();
      lbReturn = document.activeElement;
      show(el.lb);
      setInert(true);
      document.body.style.overflow = 'hidden';
      $('#lb-close').focus({ preventScroll: true });
    } else {
      // Decode the next image before swapping so it slides in whole instead of painting progressively.
      const spin = setTimeout(() => { if (seq === lbSeq) el.lbStage.classList.add('loading'); }, 150);
      const pre = new Image();
      pre.sizes = '100vw';
      pre.srcset = srcset;
      pre.src = src;
      const done = () => {
        clearTimeout(spin);
        if (seq !== lbSeq) return;
        swap();
        if (dir && !reduced()) {
          el.lbImg.animate([{ opacity: 0, transform: `translateX(${dir * 56}px)` }, { opacity: 1, transform: 'none' }],
            { duration: 320, easing: 'cubic-bezier(.2, .8, .2, 1)' });
        }
      };
      pre.decode().then(done, done);
    }
    for (const n of [state.items[i + 1], state.items[i - 1]]) if (n) new Image().src = `/preview/${enc(n.name)}`;
    renderSelection();
  }

  function closeLightbox() {
    if (state.lbIndex < 0) return;
    ++lbSeq;
    state.lbIndex = -1;
    resetZoom();
    setInert(false);
    document.body.style.overflow = '';
    hide(el.lb, () => { el.lbImg.removeAttribute('srcset'); el.lbImg.removeAttribute('src'); });
    if (lbReturn && lbReturn.isConnected) lbReturn.focus({ preventScroll: true });
    lbReturn = null;
  }

  let stepping = false;
  async function step(delta) {
    if (state.lbIndex < 0 || stepping) return;
    const i = state.lbIndex + delta;
    if (i >= 0 && i < state.items.length) return openLightbox(i, delta);
    const page = state.page + delta;
    if (page < 1 || page > state.pages) return;
    stepping = true;
    try {
      await go(page);
      if (state.lbIndex >= 0) openLightbox(delta < 0 ? state.items.length - 1 : 0, delta);
    } finally {
      stepping = false;
    }
  }

  function applyZoom() {
    el.lbImg.style.transform = zoom.on ? `translate(${zoom.x}px, ${zoom.y}px) scale(${zoom.s})` : '';
    el.lbStage.classList.toggle('zoomed', zoom.on);
    el.lbZoom.setAttribute('aria-pressed', String(zoom.on));
    el.lbZoom.querySelector('use').setAttribute('href', zoom.on ? '#i-zoom-out' : '#i-zoom');
    el.lbZoom.querySelector('span').textContent = zoom.on ? '还原' : '放大';
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

  // Zooms towards (cx, cy) so the clicked spot stays under the pointer.
  function toggleZoom(cx, cy) {
    if (state.lbIndex < 0) return;
    if (zoom.on) return resetZoom();
    const it = state.items[state.lbIndex];
    const img = el.lbImg;
    if (!img.offsetWidth) return;
    const r = img.getBoundingClientRect();
    const mx = r.left + r.width / 2;
    const my = r.top + r.height / 2;
    zoom.s = Math.min(4, Math.max(2, (it.w || img.naturalWidth) / img.offsetWidth));
    zoom.x = ((cx ?? mx) - mx) * (1 - zoom.s);
    zoom.y = ((cy ?? my) - my) * (1 - zoom.s);
    zoom.on = true;
    clampPan();
    if (it.w > 1600 && !img.currentSrc.includes('/img/')) {
      img.srcset = '';
      img.src = `/img/${enc(it.name)}`;
    }
    applyZoom();
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
  el.sort.addEventListener('change', () => { state.sort = el.sort.value; store.set('sort', state.sort); go(1); });
  el.size.addEventListener('change', () => { state.size = Number(el.size.value); store.set('size', state.size); go(1); });
  el.group.addEventListener('change', () => { state.group = el.group.checked; store.set('group', state.group ? '1' : '0'); render({ animate: true }); });
  el.selectMode.addEventListener('click', () => setSelectMode(!state.selectMode));
  el.theme.addEventListener('click', toggleTheme);
  $('#brand').addEventListener('click', (e) => { e.preventDefault(); go(1); });

  el.gallery.addEventListener('click', (e) => {
    if (e.target.closest('[data-retry]')) { load(); return; }
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
      openLightbox(i);
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
  $('#lb-close').addEventListener('click', closeLightbox);
  el.lbPrev.addEventListener('click', () => step(-1));
  el.lbNext.addEventListener('click', () => step(1));
  el.lbSelect.addEventListener('click', lbToggleSelect);
  el.lbZoom.addEventListener('click', () => toggleZoom());
  el.lbLink.addEventListener('click', async () => {
    const it = state.items[state.lbIndex];
    if (!it) return;
    const ok = await copyText(new URL(`/img/${enc(it.name)}`, location.href).href);
    toast(ok ? '已复制原图链接' : '复制失败，请长按“下载原图”复制链接', ok ? 'ok' : 'error');
  });
  el.lb.addEventListener('click', (e) => {
    if (!zoom.on && (e.target === el.lb || e.target === el.lbStage || e.target.classList.contains('lb-top'))) closeLightbox();
  });
  const lbLoaded = () => el.lbStage.classList.remove('loading');
  el.lbImg.addEventListener('load', lbLoaded);
  el.lbImg.addEventListener('error', lbLoaded);

  // Mouse: double-click zooms, drag pans while zoomed.
  let drag = null;
  let tapZoomAt = 0;
  el.lbImg.addEventListener('dblclick', (e) => {
    if (Date.now() - tapZoomAt < 600) return; // already handled as a double tap
    toggleZoom(e.clientX, e.clientY);
  });
  el.lbImg.addEventListener('pointerdown', (e) => {
    if (!zoom.on || e.button !== 0) return;
    e.preventDefault();
    drag = { id: e.pointerId, x: e.clientX - zoom.x, y: e.clientY - zoom.y };
    el.lbImg.setPointerCapture(e.pointerId);
    el.lbStage.classList.add('dragging');
  });
  el.lbImg.addEventListener('pointermove', (e) => {
    if (!drag || e.pointerId !== drag.id) return;
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

  // Touch: the image follows a horizontal swipe to change pictures, swipe down closes, double tap zooms.
  let touch = null;
  let lastTap = 0;
  el.lbStage.addEventListener('touchstart', (e) => {
    if (e.touches.length !== 1) { touch = null; return; }
    const t = e.touches[0];
    touch = { x: t.clientX, y: t.clientY, dx: 0, dy: 0, axis: null, swipe: !zoom.on };
  }, { passive: true });
  el.lbStage.addEventListener('touchmove', (e) => {
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
      el.lb.style.opacity = String(1 - Math.min(touch.dy / 600, 0.5));
    }
  }, { passive: true });
  const endTouch = (x, y) => {
    if (!touch) return;
    const { dx, dy, axis, swipe } = touch;
    touch = null;
    el.lbStage.classList.remove('dragging');
    el.lb.style.opacity = '';
    if (swipe) el.lbImg.style.transform = '';
    if (!axis) {
      const now = Date.now();
      if (x !== undefined && now - lastTap < 300) {
        lastTap = 0;
        tapZoomAt = now;
        toggleZoom(x, y);
      } else {
        lastTap = now;
      }
      return;
    }
    if (!swipe) return;
    if (axis === 'x' && Math.abs(dx) > 60) step(dx < 0 ? 1 : -1);
    else if (axis === 'y' && dy > 100) closeLightbox();
  };
  el.lbStage.addEventListener('touchend', (e) => {
    const t = e.changedTouches[0];
    endTouch(t && t.clientX, t && t.clientY);
  });
  el.lbStage.addEventListener('touchcancel', () => { lastTap = 0; endTouch(); });

  // ---------- keyboard ----------
  function lightboxKey(e) {
    const pan = { ArrowLeft: [80, 0], ArrowRight: [-80, 0], ArrowUp: [0, 80], ArrowDown: [0, -80] }[e.key];
    if (pan && zoom.on) panBy(...pan);
    else if (e.key === 'Escape') { if (zoom.on) resetZoom(); else closeLightbox(); }
    else if (e.key === 'ArrowLeft') step(-1);
    else if (e.key === 'ArrowRight') step(1);
    else if (e.key === 'z' || e.key === 'Z') toggleZoom();
    else if (e.key === 'x' || e.key === 'X') lbToggleSelect();
    else if (e.key === 'd' || e.key === 'D') el.lbDownload.click();
    else return;
    e.preventDefault();
  }

  document.addEventListener('keydown', (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return;
    if (state.lbIndex >= 0) { lightboxKey(e); return; }
    if (document.querySelector('dialog[open]') || (e.target instanceof Element && e.target.closest('input, select, textarea, [contenteditable]'))) return;
    const k = e.key;
    if (k === 'ArrowLeft' && state.page > 1) go(state.page - 1);
    else if (k === 'ArrowRight' && state.page < state.pages) go(state.page + 1);
    else if (k === '1' || k === '2' || k === '3') setView(VIEWS[k - 1]);
    else if (k === 's' || k === 'S') setSelectMode(!state.selectMode);
    else if (k === 't' || k === 'T') toggleTheme();
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
  // Checks shortly after each scheduled fetch should have finished, at least once a minute.
  // New images reload silently only when that cannot disrupt the viewer.
  let pollTimer = 0;
  let lastPoll = 0;
  function schedulePoll() {
    clearTimeout(pollTimer);
    let delay = 60000;
    const t = state.task;
    if (t && !t.paused && validTime(t.next_run)) {
      delay = Math.min(Math.max(new Date(t.next_run) - Date.now() + 8000, 10000), 60000);
    }
    pollTimer = setTimeout(poll, delay);
  }

  async function poll() {
    clearTimeout(pollTimer);
    if (document.hidden) return; // resumed by visibilitychange
    lastPoll = Date.now();
    try {
      const d = await api('/api/images?page=1&size=12&sort=desc');
      state.task = d.task;
      renderTask();
      if (state.knownTotal !== null && d.total > state.knownTotal) {
        const idle = state.page === 1 && state.sort === 'desc' && state.lbIndex < 0 && !state.selected.size
          && window.scrollY < 200 && !document.querySelector('dialog[open]');
        if (idle) { await load({ silent: true }); return; } // load reschedules
        show(el.fresh);
      }
    } catch (e) { /* offline: try again next tick */ }
    schedulePoll();
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
      if (zoom.on) { clampPan(); applyZoom(); }
    }, 150);
  });
  window.addEventListener('hashchange', () => { readHash(); load({ scroll: true }); });
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', renderControls);

  readHash();
  renderControls();
  onScroll();
  load();
})();
