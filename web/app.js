(() => {
  'use strict';

  const $ = (sel) => document.querySelector(sel);
  const { i18n } = window;
  const { t } = i18n;
  const store = {
    get(k, d) { try { const v = localStorage.getItem('mv.' + k); return v === null ? d : v; } catch (e) { return d; } },
    set(k, v) { try { localStorage.setItem('mv.' + k, v); } catch (e) { /* private mode */ } },
  };
  const motion = window.matchMedia('(prefers-reduced-motion: reduce)');
  const reduced = () => motion.matches;
  // The mascot's blink is SMIL inside the sprite, which the reduced-motion CSS cannot reach. Every <use> copy
  // follows the sprite's timeline, so pausing the sprite stills them all.
  function stillMascots() {
    const sprite = document.getElementById('sprite');
    if (reduced()) { sprite.pauseAnimations(); sprite.setCurrentTime(0); } else sprite.unpauseAnimations();
  }
  const VIEWS = ['grid', 'list'];

  const state = {
    page: 1,
    size: Number(store.get('size', 48)),
    sort: store.get('sort', 'desc'),
    view: VIEWS.includes(store.get('view')) ? store.get('view') : 'grid', // masonry was dropped
    group: store.get('group', '1') === '1',
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
    history: $('#history'), historyBar: $('#history-bar'), gallery: $('#gallery'), summary: $('#summary'), empty: $('#empty'), pager: $('#pager'),
    now: $('#now'), nowImg: $('#now-img'), nowTime: $('#now-time'), nowAgo: $('#now-ago'), nowDate: $('#now-date'), nowFacts: $('#now-facts'), nowDl: $('#now-dl'),
    nowBadge: $('#now-badge'), nowView: $('#now-view'), nowPrev: $('#now-prev'), nowNext: $('#now-next'), nowBack: $('#now-back'), reel: $('#reel'), reelTrack: $('#reel-track'),
    reelHeat: $('#reel-heat'), reelFill: $('#reel-fill'), reelHead: $('#reel-head'), reelTip: $('#reel-tip'), reelPlay: $('#reel-play'),
    reelPos: $('#reel-pos'), reelSpeed: $('#reel-speed'), reelQuality: $('#reel-quality'), reelLoop: $('#reel-loop'), reelTimed: $('#reel-timed'),
    seg: $('.seg'), sort: $('#sort'), size: $('#size'), group: $('#group'), selectMode: $('#select-mode'),
    theme: $('#theme'), lang: $('#lang'), account: $('#account'), acctMenu: $('#acct-menu'), noticeBtn: $('#notice-btn'), foot: $('.foot'), version: $('#version'),
    selbar: $('#selbar'), selCount: $('#sel-count'), fresh: $('#fresh'),
    toast: $('#toast'), toastText: $('#toast span'), toastIcon: $('#toast use'), announce: $('#announce'), alert: $('#alert'),
    lb: $('#lightbox'), lbStage: $('#lb-stage'), lbImg: $('#lb-img'), lbTime: $('#lb-time'), lbSub: $('#lb-sub'),
    lbCount: $('#lb-count'), lbPrev: $('#lb-prev'), lbNext: $('#lb-next'), lbDownload: $('#lb-download'),
    lbSelect: $('#lb-select'), lbZoom: $('#lb-zoom'), lbLink: $('#lb-link'), lbMenu: $('#lb-menu'), lbInfo: $('#lb-info'),
    lbpDate: $('#lbp-date'), lbpTime: $('#lbp-time'), lbpAgo: $('#lbp-ago'), lbpDownload: $('#lbp-download'),
    lbpSize: $('#lbp-size'), lbpSizeMenu: $('#lbp-size-menu'), lbpGrid: $('#lbp-grid'), lbpName: $('#lbp-name'), lbpZone: $('#lbp-zone'),
    loginDialog: $('#login-dialog'), loginForm: $('#login-form'), loginError: $('#login-error'), loginSubmit: $('#login-submit'),
    statusDialog: $('#status-dialog'), adminStats: $('#admin-stats'), fetchNow: $('#fetch-now'),
    adminRuns: $('#admin-runs'), runsList: $('#runs-list'), notifyTest: $('#notify-test'),
    lbCompare: $('#lb-compare'), cmp: $('#lb-cmp'), cmpOld: $('#cmp-old'), cmpDiff: $('#cmp-diff'), cmpLine: $('#cmp-line'),
    cmpBar: $('#lb-cmpbar'), cmpNote: $('#cmp-note'), selCompare: $('#sel-compare'), selVideo: $('#sel-video'), reelVideo: $('#reel-video'),
    videoDialog: $('#video-dialog'), videoForm: $('#video-form'), videoFrame: $('#video-frame'), videoEst: $('#video-est'),
    videoProgress: $('#video-progress'), videoBar: $('#video-bar'), videoStep: $('#video-step'), videoError: $('#video-error'), videoStart: $('#video-start'),
    cookieDialog: $('#cookie-dialog'), cookieForm: $('#cookie-form'), cookieState: $('#cookie-state'), cookieError: $('#cookie-error'), cookieSave: $('#cookie-save'),
    scheduleDialog: $('#schedule-dialog'), scheduleStats: $('#schedule-stats'), toggleSchedule: $('#toggle-schedule'), scheduleHint: $('#schedule-hint'),
    noticeDialog: $('#notice-dialog'), noticeList: $('#notice-list'), noticeActions: $('#notice-actions'),
    noticeBanner: $('#notice-banner'), noticeBannerTitle: $('#notice-banner-title'),
    naDialog: $('#notice-admin-dialog'), naList: $('#na-list'), naForm: $('#na-form'), naState: $('#na-state'), naCount: $('#na-count'),
    naError: $('#na-error'), naDelete: $('#na-delete'), naDraft: $('#na-draft'), naPublish: $('#na-publish'),
    keysDialog: $('#keys-dialog'), task: $('#task'), nowWarn: $('#now-warn'),
  };

  // ---------- formatting ----------
  const pad = (n) => String(n).padStart(2, '0');
  const fmtDate = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  const fmtClock = (d) => `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  const fmtFull = (d) => `${fmtDate(d)} ${fmtClock(d)}`;
  const fmtShort = (d) => `${fmtDate(d)} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
  const fmtHM = (d) => `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  const sameDay = (a, b) => fmtDate(a) === fmtDate(b);
  // Compact: the clock today, month/day before it.
  const fmtStamp = (d) => (sameDay(d, new Date()) ? fmtHM(d) : `${d.getMonth() + 1}/${d.getDate()} ${fmtHM(d)}`);
  // Tile labels drop the date when it is today; the date header or tooltip has the full time.
  const fmtTile = (d) => (state.group ? fmtHM(d) : fmtStamp(d));
  function fmtDay(d) {
    const today = new Date();
    const yest = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1);
    const label = sameDay(d, today) ? t('day.today') : sameDay(d, yest) ? t('day.yesterday') : d.toLocaleDateString(i18n.locale, { weekday: 'short' });
    return t('day.full', { date: fmtMonthDay(d), label });
  }
  // The year shows only when it is not this year.
  function fmtMonthDay(d) {
    const year = d.getFullYear() === new Date().getFullYear() ? undefined : 'numeric';
    return d.toLocaleDateString(i18n.locale, { year, month: i18n.lang === 'zh' ? 'long' : 'short', day: 'numeric' });
  }
  const fmtNum = (n, digits = 0) => Number(n).toLocaleString(i18n.locale, { minimumFractionDigits: digits, maximumFractionDigits: digits });
  function fmtSize(b) {
    if (b < 1024) return fmtNum(b) + ' B';
    if (b < 1024 * 1024) return fmtNum(b / 1024) + ' KB';
    if (b < 1024 ** 3) return fmtNum(b / 1024 / 1024, b < 10 * 1024 * 1024 ? 2 : 1) + ' MB';
    return fmtNum(b / 1024 ** 3, 1) + ' GB';
  }
  function fmtAgo(d, saved = true) {
    const s = Math.max(0, (Date.now() - d.getTime()) / 1000);
    const ago = s < 60 ? t('ago.now')
      : s < 3600 ? t('ago.minutes', { n: Math.floor(s / 60) })
        : s < 86400 ? t('ago.hours', { n: Math.floor(s / 3600) })
          : t('ago.days', { n: Math.floor(s / 86400) });
    return saved ? t('ago.saved', { ago }) : ago;
  }
  function fmtUntil(d) {
    const s = Math.ceil((d.getTime() - Date.now()) / 1000);
    if (s <= 0) return t('until.soon');
    if (s < 60) return t('until.seconds', { s });
    if (s < 3600) return t('until.minutes', { m: Math.floor(s / 60), s: pad(s % 60) });
    if (s < 86400) return t('until.hours', { h: Math.floor(s / 3600), m: Math.floor((s % 3600) / 60) });
    return fmtShort(d);
  }
  // Rounds before picking the unit, so 59.6 s reads "1 min" rather than "60 s".
  function fmtGap(s) {
    s = Math.round(s);
    if (s < 60) return t('gap.seconds', { n: s });
    const m = Math.round(s / 60);
    if (m < 60) return t('gap.minutes', { n: m });
    if (m < 1440) return m % 60 ? t('gap.hours', { h: Math.floor(m / 60), m: m % 60 }) : t('gap.wholeHours', { n: m / 60 });
    return t('gap.days', { n: Math.floor(m / 1440) });
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
    document.querySelectorAll('[data-ago]').forEach((n) => set(n, fmtAgo(new Date(n.dataset.ago), n.dataset.saved === '1')));
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

  // A dialog may refuse to close (unsaved edits) through d.beforeClose; force skips the check.
  function closeDialog(d, force = false) {
    if (!d.open || d.classList.contains('closing')) return;
    if (!force && d.beforeClose && !d.beforeClose()) return;
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

  // Screen readers hear changes through two regions that are always in the page: a region inserted or unhidden
  // together with its text is often not announced. Clearing first lets the same message be announced again.
  // An open modal dialog makes the rest of the page inert, so each dialog carries a pair of its own.
  document.querySelectorAll('dialog.modal').forEach((d) => {
    d.insertAdjacentHTML('beforeend', '<div class="sr-only" role="status" aria-live="polite" data-announce></div><div class="sr-only" role="alert" aria-live="assertive" data-alert></div>');
  });
  const announceTimers = new Map();
  function announce(msg, urgent = false) {
    const d = document.querySelector('dialog.modal[open]:not(.closing)');
    const node = d ? d.querySelector(urgent ? '[data-alert]' : '[data-announce]') : urgent ? el.alert : el.announce;
    node.textContent = '';
    clearTimeout(announceTimers.get(node));
    announceTimers.set(node, setTimeout(() => { node.textContent = msg; }, 60));
  }

  const TOAST_ICON = { info: '#i-info', ok: '#i-ok', error: '#i-alert' };
  let toastTimer;
  function toast(msg, kind = 'info') {
    announce(msg, kind === 'error');
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
    if (!res.ok) throw Object.assign(new Error(errorText(body) || res.statusText), { status: res.status });
    return body;
  }

  // The server's message is Chinese; known codes are shown in the page's language instead.
  function errorText(body) {
    const key = 'err.' + body.code;
    return body.code && t(key) !== key ? t(key, { n: state.guestLimit }) : body.error;
  }

  function download(href, filename = '') {
    const a = document.createElement('a');
    a.href = href;
    a.download = filename;
    a.dataset.checked = ''; // already checked; the click below must not be checked again
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  // A file that is gone, or an archive the server refuses, would leave the browser saving the error as a file.
  // Downloads ask first: HEAD for a single file, ?check for an archive, which validates without streaming it.
  async function preflight(url, opts) {
    try {
      const res = await fetch(url, Object.assign({ credentials: 'same-origin' }, opts));
      if (res.ok) return true;
      if (res.status === 401) { handleAuthError(res); return false; }
      const body = await res.json().catch(() => ({}));
      toast(errorText(body) || t(res.status === 404 ? 'err.image_not_found' : 'download.failed'), 'error');
    } catch (e) {
      toast(t('download.failed'), 'error');
    }
    return false;
  }

  async function downloadFile(href, filename) {
    const ok = await preflight(href, { method: 'HEAD' });
    if (ok) download(href, filename);
    return ok;
  }

  // Posted as a form, since a large selection outgrows a URL; the attachment downloads without leaving the page.
  async function downloadZip(names) {
    if (!(await preflight('/download/zip?check=1', { method: 'POST', body: new URLSearchParams(names.map((n) => ['f', n])) }))) return false;
    const form = document.createElement('form');
    form.method = 'post';
    form.action = '/download/zip';
    form.hidden = true;
    for (const n of names) form.append(Object.assign(document.createElement('input'), { type: 'hidden', name: 'f', value: n }));
    document.body.appendChild(form);
    form.submit();
    form.remove();
    return true;
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
  // The address names the page (#p=), a day (#date=) or a picture (#img=), so any view can be shared or reloaded.
  // A day or picture is returned for the caller to look up; only a page is applied here.
  function readHash() {
    const q = new URLSearchParams(location.hash.slice(1));
    const img = q.get('img') || '';
    const date = /^\d{4}-\d{2}-\d{2}$/.test(q.get('date') || '') ? q.get('date') : '';
    if (!img && !date) state.page = Math.max(1, Math.floor(Number(q.get('p'))) || 1);
    return { img, date };
  }
  const imgHash = (name) => `#img=${encodeURIComponent(name)}`;
  // Shows what the address names; on start-up and when retrying a failed load. A retried picture link that failed
  // while being restored is restored again, so its entry is kept rather than doubled.
  function followHash(retry = false) {
    const link = readHash();
    if (link.img) openLinked(link.img, retry && linkFailedRestore);
    else if (link.date) goDate(link.date, false);
    else load();
  }
  // Turning pages adds history entries (push); corrections replace the current one. While the lightbox is open its
  // own entry carries #img=, and closing it puts the page back.
  function writeHash(push = false) {
    if (lbHistory) return;
    const h = state.date && state.date.page === state.page ? `#date=${state.date.key}` : state.page > 1 ? `#p=${state.page}` : '';
    if (location.hash !== h) history[push ? 'pushState' : 'replaceState'](null, '', h || location.pathname + location.search);
  }
  const viewerZone = () => {
    try { return Intl.DateTimeFormat().resolvedOptions().timeZone || ''; } catch (e) { return ''; } // server time then
  };

  const onFirstPage = () => state.sort === 'desc' && state.page === 1;

  let loadSeq = 0;
  let foreground = 0; // non-silent loads in flight; polling must not supersede one (a link being looked up, a page turn)
  // silent reloads come from polling: no skeleton, no progress bar, new tiles are highlighted.
  // Resolves true once the page is shown, false when the request failed, null when it was superseded.
  // around names a picture whose page to show instead of state.page; guard, when given, must still hold when the
  // answer arrives or nothing is shown.
  // date (YYYY-MM-DD in the viewer's zone) does the same for the first picture of a day.
  async function load({ scroll = false, silent = false, around = '', date = '', guard = null } = {}) {
    const seq = ++loadSeq;
    if (!silent) {
      foreground++;
      if (!state.items.length) renderSkeleton();
      else el.gallery.classList.add('stale');
      busy(true);
    }
    const before = silent ? new Set(state.items.map((it) => it.name)) : null;
    const viewing = state.lbIndex >= 0 && state.items[state.lbIndex] ? state.items[state.lbIndex].name : null;
    const anchor = state.lastIndex !== null && state.items[state.lastIndex] ? state.items[state.lastIndex].name : null;
    try {
      const at = around ? `&around=${enc(around)}` : date ? `&date=${date}&tz=${enc(viewerZone())}` : '';
      const d = await api(`/api/images?page=${state.page}&size=${state.size}&sort=${state.sort}${at}`);
      if (seq !== loadSeq || (guard && !guard())) return null;
      Object.assign(state, { items: d.items, total: d.total, page: d.page, size: d.size, pages: d.pages, admin: d.admin, guestLimit: d.guest_limit, task: d.task });
      if (date) state.date = { key: date, page: d.page, focus: d.focus || '' };
      // A refresh that pushed the day off this page ends the day's address; the page number takes over.
      else if (state.date && !state.items.some((it) => fmtDate(new Date(it.time)) === state.date.key)) state.date = null;
      checkNotices(d.notice);
      if (d.version) { el.version.textContent = 'v' + d.version; show(el.version); }
      // New pictures shift indexes; keep the shift-click anchor on the same picture.
      if (anchor) { const j = state.items.findIndex((it) => it.name === anchor); state.lastIndex = j >= 0 ? j : null; }
      if (onFirstPage()) {
        const top = d.items[0] || null;
        // Polling refreshes the overview itself; a newer picture found any other way (fetch now, a reload) must too.
        const newer = state.latest !== undefined && (top && top.name) !== (state.latest && state.latest.name);
        state.latest = top;
        hide(el.fresh);
        if (newer) loadOverview();
      }
      writeHash();
      olderItems.clear();
      olderGen++;
      // Keep an open lightbox on the same picture even though indexes shifted.
      let lost = false;
      if (viewing && state.lbIndex >= 0) {
        const i = state.items.findIndex((it) => it.name === viewing);
        if (i >= 0) { state.lbIndex = i; updateLightboxMeta(); renderCmp(); } // its older neighbour may have changed
        else { state.lbIndex = Math.max(0, Math.min(state.lbIndex, state.items.length - 1)); lost = true; }
      }
      render({ animate: !silent, slide: silent ? 0 : slideDir });
      slideDir = 0;
      if (before && before.size) markNew(before);
      if (scroll) scrollToTop();
      // The picture left this page: step() opens the right one itself; otherwise show the nearest one.
      if (lost && !stepping) { if (state.items.length) openLightbox(state.lbIndex); else closeLightbox(); }
      return true;
    } catch (e) {
      if (seq !== loadSeq || (guard && !guard())) return null;
      if (!silent) renderError(e);
      return false;
    } finally {
      if (!silent) { busy(false); foreground--; }
      if (seq === loadSeq) el.gallery.classList.remove('stale');
      schedulePoll();
    }
  }

  let slideDir = 0; // pages slide in from the side they were reached from
  function go(page) {
    leaveRoute();
    slideDir = Math.sign(page - state.page);
    state.page = page;
    state.date = null;
    state.lastIndex = null;
    writeHash(true);
    return load({ scroll: true });
  }

  // Every change of route bumps this, so a day or picture still being looked up is dropped once the viewer moved on.
  let routeSeq = 0;
  let lbRestore = 0; // the routeSeq of the picture link being restored into the current entry, 0 for none
  let linking = false; // openLinked is opening the lightbox, as opposed to a click
  let linkFailedRestore = false; // the last picture link failed to load while being restored
  function leaveRoute() {
    ++routeSeq;
    if (state.lbIndex < 0) { lbHistory = false; lbRestore = 0; } // a link still being restored no longer owns the entry
  }

  // Shows the page holding a day, scrolled to its first picture.
  async function goDate(key, push = true) {
    leaveRoute();
    const seq = routeSeq;
    if (push) history.pushState(null, '', `#date=${key}`);
    state.lastIndex = null;
    const ok = await load({ date: key, guard: () => seq === routeSeq });
    // Another load on the same route (signing in or out) replaced it: show the day again.
    if (ok === null && seq === routeSeq) return goDate(key, false);
    if (!ok) return false;
    const focus = state.date && state.date.focus;
    const node = el.gallery.querySelector(`.group-title[data-date="${key}"]`)
      || (focus && el.gallery.querySelector(`.tile[data-i="${state.items.findIndex((it) => it.name === focus)}"]`));
    if (node) {
      const top = node.getBoundingClientRect().top + scrollY - el.bar.offsetHeight - el.historyBar.offsetHeight - 8;
      window.scrollTo({ top: Math.max(0, top), behavior: reduced() ? 'auto' : 'smooth' });
    }
    // The server lands on the nearest picture when the day has none.
    const hit = state.items.find((it) => it.name === focus);
    if (hit && fmtDate(new Date(hit.time)) !== key) toast(t('date.none'));
    return true;
  }

  // Opens the picture a link names; its page is found by around=, as when stepping across pages. restore means the
  // current history entry already names the picture (Forward into it): it is kept rather than replaced and pushed again.
  async function openLinked(name, restore = false) {
    const seq = ++routeSeq;
    state.date = null;
    if (restore) { lbHistory = true; lbBase = null; lbRestore = seq; } // keeps load() from rewriting the entry
    // Gives the entry back unless a newer restore has taken it over.
    const release = () => { if (lbRestore === seq) { lbRestore = 0; if (state.lbIndex < 0) lbHistory = false; } };
    const ok = await load({ around: name, guard: () => seq === routeSeq });
    if (seq !== routeSeq) { release(); return; }
    // Another load on the same route (signing in or out) replaced the lookup: look the picture up again.
    if (ok === null) return openLinked(name, restore);
    linkFailedRestore = restore && !ok;
    const i = ok ? state.items.findIndex((it) => it.name === name) : -1;
    if (i < 0) {
      release();
      // A picture that is gone hands the address back to the page; a failed request keeps it for Retry.
      if (ok) { writeHash(); toast(t('err.image_not_found'), 'error'); }
      return;
    }
    if (lbRestore === seq) lbRestore = 0;
    linking = true;
    try { openLightbox(i, 0, el.gallery.querySelector(`.tile[data-i="${i}"] .tile-open img`)); } finally { linking = false; }
  }

  // The newest pictures: page one, newest first.
  function goNewest() {
    if (state.sort !== 'desc') { state.sort = 'desc'; store.set('sort', 'desc'); }
    return go(1);
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
    el.gallery.innerHTML = `<div class="load-error"><svg class="mascot" aria-hidden="true"><use href="#mascot-oops"/></svg>
      <p>${esc(t('load.failed', { msg: e.message }))}</p>
      <button type="button" class="btn primary" data-retry><svg><use href="#i-refresh"/></svg>${t('load.retry')}</button></div>`;
    el.summary.textContent = '';
    const active = document.activeElement;
    const lost = !active || active === document.body || el.pager.contains(active);
    el.pager.innerHTML = '';
    announce(t('load.failed', { msg: e.message }), true);
    if (lost) el.gallery.querySelector('[data-retry]').focus({ preventScroll: true });
    // A hero that never got a picture must not keep shimmering above the error.
    if (!el.now.dataset.name) { el.now.classList.remove('pending'); el.now.hidden = true; }
  }

  function tileHTML(it, i) {
    const d = new Date(it.time);
    const sel = state.selected.has(it.name);
    const name = esc(it.name);
    const list = state.view === 'list';
    return `<figure class="tile${sel ? ' selected' : ''}" data-i="${i}" style="--d:${i}">
      <button type="button" class="tile-check" aria-label="${esc(t('tile.selectAt', { time: fmtFull(d) }))}" aria-pressed="${sel}"><svg><use href="#i-check"/></svg></button>
      <button type="button" class="tile-open" aria-label="${esc(t('tile.view', { time: fmtFull(d) }))}" title="${esc(fmtFull(d))}"><img loading="lazy" decoding="async" src="/thumb/${enc(it.name)}"${it.w ? ` width="${it.w}" height="${it.h}"` : ''} alt=""></button>
      <figcaption class="tile-cap">
        <time datetime="${esc(it.time)}">${list ? fmtFull(d) : fmtTile(d)}</time>
        ${list ? `<span class="name" title="${name}">${name}</span><span class="size">${it.w ? `<span class="dims">${it.w}×${it.h} · </span>` : ''}${fmtSize(it.size)}</span>` : ''}
        <a class="tile-dl" href="/download/${enc(it.name)}" download title="${t('tile.download')}" aria-label="${t('tile.download')}"><svg><use href="#i-download"/></svg></a>
      </figcaption>
    </figure>`;
  }

  const tilesHTML = (entries) => `<div class="tiles">${entries.map(([it, i]) => tileHTML(it, i)).join('')}</div>`;

  // The hero follows state.latest, which polling keeps current even when the gallery below is not reloaded.
  // While the reel is scrubbed it shows reel.at instead; leaving the reel returns to the newest picture.
  function renderNow() {
    const latest = onFirstPage() ? state.latest : null;
    if (latest === undefined) return; // the first page has not arrived: keep the placeholder
    el.now.classList.remove('pending');
    el.now.hidden = !latest;
    if (!latest) { if (reel.playing) reelStop(); return; }
    const shown = (reel.at >= 0 && reel.items[reel.at]) || latest;
    const live = shown.name === latest.name;
    const d = new Date(shown.time);
    el.now.classList.toggle('past', !live);
    el.nowBadge.textContent = live ? t('now.latest') : t('now.past');
    el.nowBack.hidden = live;
    el.nowDate.textContent = fmtDay(d);
    el.nowTime.dateTime = shown.time;
    el.nowTime.title = fmtFull(d);
    el.nowTime.textContent = fmtHM(d);
    el.nowAgo.dataset.ago = shown.time;
    el.nowAgo.textContent = fmtAgo(d);
    const idx = reel.items.findIndex((it) => it.name === shown.name);
    const prev = idx >= 0 ? reel.items[idx - 1] : null;
    el.nowFacts.innerHTML = `
      <div><dt>${t('now.gap')}</dt><dd>${prev ? fmtGap((new Date(shown.time) - new Date(prev.time)) / 1000) : '—'}</dd></div>
      <div><dt>${t('now.dims')}</dt><dd>${shown.w ? `${shown.w}×${shown.h}` : '—'}</dd></div>
      <div><dt>${t('now.size')}</dt><dd>${fmtSize(shown.size)}</dd></div>
      <div><dt>${t('now.recent')}</dt><dd>${overview.recent === null ? '—' : t('now.recentCount', { n: overview.recent })}</dd></div>`;
    el.nowDl.href = `/download/${enc(shown.name)}`;
    renderReelHead();
    // Playback shows frames of the chosen quality, which reelTick has already loaded and decoded. A quality change or a
    // thumbnail fallback swaps the frame of the same picture too.
    if (reel.playing && el.now.dataset.name) {
      nowVer++; // drops a preview still loading from before playback
      const frame = reelSrc(shown);
      if (frame.startsWith('/thumb/')) el.now.dataset.thumb = shown.name; else delete el.now.dataset.thumb;
      if (el.now.dataset.name === shown.name && el.nowImg.getAttribute('src') === frame) return;
      el.now.dataset.name = shown.name;
      if (shown.w) { el.nowImg.width = shown.w; el.nowImg.height = shown.h; }
      el.nowImg.src = frame;
      return;
    }
    // A thumbnail shown by scrubbing or smooth playback is replaced by the preview once the picture holds still.
    const upgrade = !reel.playing && el.now.dataset.thumb === shown.name;
    if (el.now.dataset.name === shown.name && !upgrade) return;
    // Every change of picture gets a version; a preview that finishes loading after a later change is dropped.
    const ver = ++nowVer;
    const first = !el.now.dataset.name;
    const scrubbing = reel.at >= 0 || reel.playing;
    el.now.dataset.name = shown.name;
    // Scrubbing swaps small thumbnails instantly; the preview replaces them once it has loaded.
    const src = `/preview/${enc(shown.name)}`;
    const swap = () => {
      if (ver !== nowVer) return;
      if (shown.w) { el.nowImg.width = shown.w; el.nowImg.height = shown.h; }
      el.nowImg.src = src;
      if (first || scrubbing || upgrade || !live) { markCached(el.now); return; }
      replay(el.now, 'updated');
      if (!reduced()) {
        el.nowImg.animate([{ opacity: 0.3, filter: 'blur(8px)' }, { opacity: 1, filter: 'blur(0)' }], { duration: 700, easing: 'ease-out' });
      }
    };
    if (first) return swap();
    if (scrubbing && !upgrade) el.nowImg.src = `/thumb/${enc(shown.name)}`;
    delete el.now.dataset.thumb;
    const pre = new Image();
    pre.src = src;
    pre.decode().then(swap, swap);
  }

  // ---------- reel: the last 24 hours as a heat strip, oldest on the left ----------
  const overview = { recent: null, first: '', days: new Map() }; // days: YYYY-MM-DD -> { count }
  let nowVer = 0; // see renderNow
  const REEL_SPEEDS = [0.5, 1, 2, 4];
  const REEL_FRAME = 140; // ms per picture at 1×
  // Playback quality: the 480px thumbnail, the 1600px preview (default) or the original file. Larger frames take
  // longer to arrive, so more of them are loaded ahead.
  const REEL_QUALITY = { thumb: { path: '/thumb/', ahead: 3 }, preview: { path: '/preview/', ahead: 6 }, original: { path: '/img/', ahead: 4 } };
  const reel = {
    items: [], times: [], heat: [], at: -1, playing: false, timer: 0, dragging: false, resume: false, from: 0, to: 0,
    speed: REEL_SPEEDS.includes(+store.get('reelSpeed')) ? +store.get('reelSpeed') : 1,
    quality: store.get('reelQuality', 'preview') in REEL_QUALITY ? store.get('reelQuality', 'preview') : 'preview',
    loop: store.get('reelLoop') === '1',
    timed: store.get('reelTimed') === '1', // space pictures by their real gaps instead of evenly
    run: 0, // bumped whenever playback stops or restarts, so a frame still loading cannot advance an old run
    frames: new Map(), // src -> promise settled once that frame is decoded or failed
    failed: new Set(), // frame URLs that did not load; playback shows that picture's thumbnail instead
  };
  let overviewSeq = 0;
  async function loadOverview() {
    const seq = ++overviewSeq;
    try {
      const tz = viewerZone();
      const o = await api(`/api/overview${tz ? `?tz=${enc(tz)}` : ''}`);
      if (seq !== overviewSeq) return; // a later request is on its way; an older answer must not overwrite it
      // A rolling 24 hours (the reel's window) rather than since midnight, so the count does not drop to zero
      // overnight; the server's heat counts are not capped like the recent pictures.
      overview.recent = o.heat ? o.heat.reduce((a, b) => a + b, 0) : o.recent.length;
      overview.first = validTime(o.first) ? o.first : '';
      overview.days = new Map(o.days.map((x) => [x.date, x]));
      // The window slides as pictures arrive, so a held or playing position is found again by name; once it has slid
      // out, the oldest picture left is the nearest.
      const held = reel.at >= 0 ? reel.items[reel.at].name : '';
      reel.items = o.recent.slice().reverse();
      reel.times = reel.items.map((it) => new Date(it.time).getTime());
      // Saves per slice of the whole 24 hours, counted by the server past the cap on recent pictures.
      reel.heat = o.heat || [];
      if (held) {
        reel.at = reel.items.findIndex((it) => it.name === held);
        if (reel.at < 0) reel.at = 0;
      }
      reel.to = o.recent.length ? new Date(o.last).getTime() : 0;
      reel.from = reel.to - 864e5;
      renderReel();
      renderOverview();
    } catch (e) { /* the reel is optional */ }
  }
  const reelX = (ms) => (ms - reel.from) / (reel.to - reel.from);
  const reelPct = (ms) => `${(Math.max(0, Math.min(1, reelX(ms))) * 100).toFixed(3)}%`;
  function renderReel() {
    el.reel.hidden = reel.items.length < 2;
    // A refresh can leave too few pictures to replay; playback must not carry on behind the hidden bar.
    if (el.reel.hidden) { reelStop(); reel.at = -1; return; }
    el.reelTrack.setAttribute('aria-valuemax', reel.items.length - 1);
    // One hard-edged gradient stop per slice; the square root keeps a lone save visible beside a busy hour.
    const counts = reel.heat;
    const peak = Math.max(1, ...counts);
    const w = 100 / counts.length;
    el.reelHeat.style.backgroundImage = !counts.length ? '' : `linear-gradient(to right, ${counts.map((c, i) => {
      const color = c ? `color-mix(in srgb, var(--pigment) ${Math.round(25 + 75 * Math.sqrt(c / peak))}%, transparent)` : 'transparent';
      return `${color} ${(i * w).toFixed(3)}% ${((i + 1) * w).toFixed(3)}%`;
    }).join(', ')})`;
    el.reelSpeed.value = String(reel.speed);
    el.reelQuality.value = reel.quality;
    el.reelLoop.setAttribute('aria-pressed', String(reel.loop));
    el.reelTimed.setAttribute('aria-pressed', String(reel.timed));
  }
  function renderReelHead() {
    const i = reel.at >= 0 ? reel.at : reel.items.length - 1;
    const it = reel.items[i];
    el.nowPrev.disabled = !it || i <= 0;
    el.nowNext.disabled = !it || i >= reel.items.length - 1;
    if (!it) return;
    const x = reelPct(reel.times[i]);
    el.reelHead.style.left = x;
    el.reelFill.style.width = x;
    el.reelPos.textContent = `${fmtStamp(new Date(it.time))} · ${i + 1}/${reel.items.length}`;
    el.reelTrack.setAttribute('aria-valuenow', i);
    el.reelTrack.setAttribute('aria-valuetext', fmtFull(new Date(it.time)));
  }
  function reelSeek(i) {
    i = Math.max(0, Math.min(reel.items.length - 1, i));
    reel.at = i === reel.items.length - 1 && !reel.playing ? -1 : i;
    renderNow();
  }
  function reelNearest(clientX) {
    const r = el.reelTrack.getBoundingClientRect();
    const ms = reel.from + ((clientX - r.left) / r.width) * (reel.to - reel.from);
    let best = 0;
    reel.times.forEach((t, i) => { if (Math.abs(t - ms) < Math.abs(reel.times[best] - ms)) best = i; });
    return best;
  }
  function reelStop() {
    clearTimeout(reel.timer);
    reel.run++;
    setReelBuffering(false);
    reelForget();
    reel.playing = false;
    el.reelPlay.setAttribute('aria-pressed', 'false');
    el.reelPlay.querySelector('use').setAttribute('href', '#i-play');
  }
  // Failures are kept per URL, so one at another quality never downgrades the frame chosen now.
  function reelSrc(it) {
    const src = REEL_QUALITY[reel.quality].path + enc(it.name);
    return reel.failed.has(src) ? `/thumb/${enc(it.name)}` : src;
  }

  // Loads and decodes the frame of picture it once. The promise settles either way: a failed frame falls back to the
  // thumbnail, unless its load was cancelled meanwhile (see reelForget), which ends it there.
  function reelFrame(it) {
    if (!it) return Promise.resolve();
    const src = reelSrc(it);
    let entry = reel.frames.get(src);
    if (!entry) {
      const img = new Image();
      img.src = src;
      entry = { img };
      const own = entry;
      entry.done = img.decode().catch(() => {
        if (reel.frames.get(src) !== own || src.startsWith('/thumb/')) return undefined;
        reel.failed.add(src);
        return reelFrame(it);
      });
      reel.frames.set(src, entry);
    }
    return entry.done;
  }

  // Cancels the loads of frames not in keep (all of them without it): removing src aborts the download.
  function reelForget(keep) {
    for (const [src, entry] of reel.frames) {
      if (keep && keep.has(src)) continue;
      reel.frames.delete(src);
      entry.img.removeAttribute('src');
    }
  }

  // Keeps the frames from i to i + ahead loading (wrapping round when looping) and cancels the rest, so the requests
  // in flight and the decoded images held stay few even for originals.
  function reelWarm(i) {
    const n = reel.items.length;
    const keep = new Set();
    for (let k = 0; k <= REEL_QUALITY[reel.quality].ahead; k++) {
      const j = reel.loop ? (i + k) % n : i + k;
      if (j >= n) break;
      keep.add(reelSrc(reel.items[j]));
      reelFrame(reel.items[j]);
    }
    reelForget(keep);
  }

  let reelBufferTimer = 0;
  function setReelBuffering(on) {
    clearTimeout(reelBufferTimer);
    el.reel.classList.toggle('buffering', on);
    if (on) el.reelPlay.setAttribute('aria-busy', 'true'); else el.reelPlay.removeAttribute('aria-busy');
  }

  // Slow networks pause playback (showing the buffering ring) instead of skipping frames: a frame's time starts once
  // it has been decoded, and the next frame shows only when its time is up and it has been decoded as well. The step
  // after is read from reel.at when it is due, so a refreshed window or a speed change applies to it.
  function reelTick(i) {
    if (!reel.playing) return;
    reelSeek(i);
    reelWarm(reel.at);
    const last = reel.items.length - 1;
    if (reel.at >= last && !reel.loop) { reelPause(); return; }
    const run = reel.run;
    const live = () => run === reel.run && reel.playing;
    // Linger on the newest picture before starting over. Timed playback spaces pictures by their real gaps, scaled
    // so the whole window takes as long as it does at one frame time per picture.
    const gap = () => Math.max(30, ((reel.times[reel.at + 1] - reel.times[reel.at]) / (reel.to - reel.from)) * reel.items.length * REEL_FRAME);
    const wait = reel.at < last ? (reel.timed ? gap() : REEL_FRAME) / reel.speed : 1200;
    // Only a step clearly longer than its frame time shows the ring, so it does not flicker on fast connections.
    clearTimeout(reelBufferTimer);
    reelBufferTimer = setTimeout(() => { if (live()) setReelBuffering(true); }, wait + 200);
    const cur = reel.items[reel.at];
    reelFrame(cur).then(() => {
      if (!live()) return;
      // A refresh that dropped this picture moved reel.at to another one, which has to be waited for in turn.
      if (reel.items[reel.at]?.name !== cur.name) { reelTick(reel.at); return; }
      renderNow(); // shows the thumbnail if the frame fell back to it
      const due = new Promise((resolve) => { reel.timer = setTimeout(resolve, wait); });
      const n = reel.items.length;
      reelAdvance(cur, reel.items[reel.at < n - 1 ? reel.at + 1 : 0], due, live);
    });
  }

  // Shows target once it has been decoded and due has passed. When the wait is over but a refresh changed the
  // pictures around cur, or looping was turned off on the newest picture, playback carries on from where reel.at
  // stands instead.
  function reelAdvance(cur, target, due, live) {
    Promise.all([due, reelFrame(target)]).then(() => {
      if (!live()) return;
      const n = reel.items.length;
      const next = reel.at < n - 1 ? reel.at + 1 : 0;
      if (reel.items[reel.at]?.name !== cur.name || reel.items[next]?.name !== target.name || (next === 0 && !reel.loop)) {
        reelTick(reel.at);
        return;
      }
      setReelBuffering(false);
      reelTick(next);
    });
  }
  function reelStart(from) {
    if (reel.items.length < 2) return; // a refresh during a drag can leave nothing to replay

    clearTimeout(reel.timer);
    reel.run++;
    setReelBuffering(false);
    reel.playing = true;
    el.reelPlay.setAttribute('aria-pressed', 'true');
    el.reelPlay.querySelector('use').setAttribute('href', '#i-pause');
    reelTick(from);
  }
  // Stopping on the newest picture counts as being back at the latest.
  function reelPause() {
    reelStop();
    reelSeek(reel.at >= 0 ? reel.at : reel.items.length - 1);
  }
  // From the newest picture playback starts over; elsewhere it carries on from where it stands.
  const reelFrom = () => (reel.at >= 0 && reel.at < reel.items.length - 1 ? reel.at : 0);
  function reelPlay() {
    // During a drag the press only decides whether playback carries on after the release.
    if (reel.dragging) { reel.resume = !reel.resume; return; }
    if (reel.playing) reelPause(); else reelStart(reelFrom());
  }
  function reelToLatest() {
    reelStop();
    reelSeek(reel.items.length - 1);
  }
  function setReelSpeed(v) {
    reel.speed = v;
    el.reelSpeed.value = String(v);
    store.set('reelSpeed', v);
  }
  // A new quality applies from the next frame; playback that is running warms the new frames from where it stands.
  function setReelQuality(v) {
    if (!(v in REEL_QUALITY)) return;
    reel.quality = v;
    el.reelQuality.value = v;
    store.set('reelQuality', v);
    reelForget();
    if (reel.playing) reelStart(reel.at >= 0 ? reel.at : 0);
  }
  function stepReelSpeed(d) {
    const i = REEL_SPEEDS.indexOf(reel.speed) + d;
    if (i >= 0 && i < REEL_SPEEDS.length) setReelSpeed(REEL_SPEEDS[i]);
  }
  // The tip previews any point of the strip without moving the big picture.
  function reelTip(i) {
    const it = reel.items[i];
    const img = el.reelTip.querySelector('img');
    const src = `/thumb/${enc(it.name)}`;
    if (img.getAttribute('src') !== src) img.src = src;
    const time = el.reelTip.querySelector('time');
    time.dateTime = it.time;
    time.textContent = fmtStamp(new Date(it.time));
    el.reelTip.hidden = false;
    // Near either end the tip slides inward instead of hanging past the strip.
    const w = el.reelTrack.clientWidth;
    const half = el.reelTip.offsetWidth / 2;
    el.reelTip.style.left = `${Math.max(half, Math.min(w - half, reelX(reel.times[i]) * w))}px`;
  }
  const hideReelTip = () => { el.reelTip.hidden = true; };
  // Only a press moves the picture: click a point, or hold and drag along the strip. Hovering only previews.
  el.reelTrack.addEventListener('pointerdown', (e) => {
    if (e.button !== 0) return;
    reel.resume = reel.playing;
    reelStop();
    reel.dragging = true;
    el.reel.classList.add('dragging');
    el.reelTrack.setPointerCapture(e.pointerId);
    const i = reelNearest(e.clientX);
    reelSeek(i);
    reelTip(i);
  });
  el.reelTrack.addEventListener('pointermove', (e) => {
    if (!reel.dragging && e.pointerType !== 'mouse') return;
    const i = reelNearest(e.clientX);
    if (reel.dragging) reelSeek(i);
    reelTip(i);
  });
  // Releasing a drag that interrupted playback carries on from the new spot; at the newest picture only a loop
  // starts over.
  const endReelDrag = (e) => {
    if (!reel.dragging) return;
    reel.dragging = false;
    el.reel.classList.remove('dragging');
    if (e.pointerType !== 'mouse') hideReelTip();
    if (reel.resume && (reel.at >= 0 || reel.loop)) reelStart(reelFrom());
  };
  // A capture can also end without a pointerup, for example when the reel is hidden mid-drag.
  for (const type of ['pointerup', 'pointercancel', 'lostpointercapture']) el.reelTrack.addEventListener(type, endReelDrag);
  el.reelTrack.addEventListener('pointerleave', () => { if (!reel.dragging) hideReelTip(); });
  el.reelTrack.addEventListener('keydown', (e) => {
    if (e.key === ' ') { e.preventDefault(); e.stopPropagation(); reelPlay(); return; }
    const cur = reel.at >= 0 ? reel.at : reel.items.length - 1;
    const step = { ArrowLeft: -1, ArrowRight: 1, Home: -cur, End: reel.items.length }[e.key];
    if (step === undefined) return;
    e.preventDefault();
    e.stopPropagation();
    reelStop();
    reelSeek(cur + step);
  });
  el.reelPlay.addEventListener('click', reelPlay);
  el.reelSpeed.addEventListener('change', () => setReelSpeed(+el.reelSpeed.value));
  el.reelQuality.addEventListener('change', () => setReelQuality(el.reelQuality.value));
  el.reelLoop.addEventListener('click', () => {
    reel.loop = !reel.loop;
    el.reelLoop.setAttribute('aria-pressed', String(reel.loop));
    store.set('reelLoop', reel.loop ? '1' : '0');
  });
  el.reelTimed.addEventListener('click', () => {
    reel.timed = !reel.timed;
    el.reelTimed.setAttribute('aria-pressed', String(reel.timed));
    store.set('reelTimed', reel.timed ? '1' : '0');
  });
  // Side buttons step through the same 24 hours as the reel; the newest picture is the last step.
  function nowStep(d) {
    if (reel.items.length < 2) return;
    reelStop();
    reelSeek((reel.at >= 0 ? reel.at : reel.items.length - 1) + d);
  }
  el.nowPrev.addEventListener('click', () => nowStep(-1));
  el.nowNext.addEventListener('click', () => nowStep(1));
  // A mouse near either edge of the picture reveals that side's button; touch screens always show them.
  el.nowView.addEventListener('pointermove', (e) => {
    if (e.pointerType !== 'mouse') return;
    const r = el.nowView.getBoundingClientRect();
    const zone = Math.min(160, r.width * 0.22);
    el.nowView.classList.toggle('near-prev', e.clientX - r.left < zone);
    el.nowView.classList.toggle('near-next', r.right - e.clientX < zone);
  });
  el.nowView.addEventListener('pointerleave', () => el.nowView.classList.remove('near-prev', 'near-next'));
  el.nowBack.addEventListener('click', reelToLatest);

  // Repeated failures mean the picture may be out of date. Guests are told just that; admins get the reason and a
  // way to the run history.
  function renderFail(task) {
    const failing = !!task && task.result === 'error' && task.failures > 0;
    el.nowWarn.hidden = !failing;
    if (!failing) return;
    if (!state.admin) {
      el.nowWarn.innerHTML = `<p class="now-warn-title"><svg><use href="#i-alert"/></svg>${t('fail.guest')}</p>`;
      return;
    }
    const key = `fail.${task.code}`;
    el.nowWarn.innerHTML = `<p class="now-warn-title"><svg><use href="#i-alert"/></svg>${t('fail.title', { n: task.failures })}</p>
      <p>${t(key) !== key ? t(key) : t('fail.other')}</p>
      <button type="button" class="linkish" id="now-warn-more">${t('fail.details')}</button>`;
  }

  function renderTask() {
    const task = state.task;
    renderFail(task);
    el.task.hidden = !task;
    if (!task) return;
    const at = validTime(task.last_fetched) ? new Date(task.last_fetched) : null;
    const next = !task.paused && validTime(task.next_run) ? task.next_run : '';
    const retry = !task.paused && backingOff(task) ? task.retry_at : '';
    el.task.classList.toggle('paused', task.paused);
    el.task.innerHTML = `<dt>${t('task.title')}</dt><dd><span class="task-state"><span class="task-dot" aria-hidden="true"></span>${t(task.paused ? 'task.paused' : 'task.running')}</span></dd>
      <dt>${t('task.lastFetched')}</dt><dd title="${at ? esc(fmtFull(at)) : t('task.none')}">${at ? `<span data-ago="${esc(task.last_fetched)}">${fmtAgo(at, false)}</span>` : t('task.none')}</dd>
      ${retry ? `<dt>${t('task.retry')}</dt><dd title="${esc(fmtFull(new Date(retry)))}"><span data-until="${esc(retry)}">${fmtUntil(new Date(retry))}</span></dd>`
    : next ? `<dt>${t('task.next')}</dt><dd title="${esc(fmtFull(new Date(next)))}"><span data-until="${esc(next)}">${fmtUntil(new Date(next))}</span></dd>` : ''}`;
  }

  // After repeated failures scheduled runs before retry_at are skipped (the server allows 2 s of slack), so the next
  // scheduled run is not the next attempt.
  function backingOff(task) {
    return validTime(task.retry_at) && validTime(task.next_run) && new Date(task.retry_at) - 2000 > new Date(task.next_run);
  }

  function renderSummary() {
    el.empty.hidden = state.total > 0;
    el.summary.hidden = state.total === 0;
    const from = (state.page - 1) * state.size + 1;
    const to = from + state.items.length - 1;
    const since = overview.first ? new Date(overview.first) : null;
    el.summary.innerHTML = state.total
      ? `<strong>${fmtNum(state.total)}</strong> ${t('summary.images', { n: state.total })}${since ? `<span>${t('summary.since', { date: fmtMonthDay(since) })}</span>` : ''}${state.pages > 1 ? `<span>${t('summary.range', { from: fmtNum(from), to: fmtNum(to) })}</span>` : ''}`
      : '';
  }

  // A day can run across pages: the header gives the whole day's count and this page's share of it.
  function dayCount(key, n) {
    const total = (overview.days.get(key) || { count: 0 }).count;
    return total > n ? t('day.pageOfTotal', { n: fmtNum(n), total: fmtNum(total) }) : t('day.count', { n: fmtNum(n) });
  }

  // New overview data only changes the hero, the summary and the day headers; the tiles stay as they are.
  function renderOverview() {
    renderNow();
    if (!state.items.length || el.gallery.matches('.skeleton') || el.gallery.querySelector('.load-error')) return;
    renderSummary();
    el.gallery.querySelectorAll('.group-title').forEach((h) => {
      const c = h.querySelector('.gt-count');
      const text = dayCount(h.dataset.date, Number(h.dataset.to) - Number(h.dataset.from) + 1);
      if (c.textContent !== text) c.textContent = text;
    });
  }

  // slide is the direction a new page arrives from; view and grouping changes animate in place.
  function render({ animate = false, slide = 0 } = {}) {
    el.gallery.style.setProperty('--sx', `${slide * 28}px`);
    renderTask();
    renderNow();
    renderSummary();

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
        const n = g.entries.length;
        const t0 = fmtHM(new Date(g.entries[0][0].time));
        const t1 = fmtHM(new Date(g.entries[n - 1][0].time));
        // Visitors may pick only part of a long day; the tooltip says so up front.
        const pick = esc(state.admin ? t('day.selectAll') : t('day.selectAllGuest', { n: state.guestLimit }));
        return `<h2 class="group-title" data-from="${first}" data-to="${last}" data-date="${g.key}">
          <button type="button" class="group-check" aria-label="${pick}" title="${pick}"><svg><use href="#i-check"/></svg></button>
          <span class="gt-day">${fmtDay(g.d)}</span><span class="gt-count">${dayCount(g.key, n)}</span><span class="gt-range">${n > 1 ? `${state.sort === 'desc' ? t1 : t0}–${state.sort === 'desc' ? t0 : t1}` : t0}</span></h2>${tilesHTML(g.entries)}`;
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
      btn(state.page - 1, '<svg><use href="#i-left"/></svg>', `class="step prev" aria-label="${t('pager.prev')}" title="${t('pager.prevTip')}"${state.page <= 1 ? ' disabled' : ''}`) +
      pageList(state.page, state.pages).map((p) => p === null
        ? '<span class="gap">…</span>'
        : btn(p, p, p === state.page ? 'aria-current="page"' : '')).join('') +
      btn(state.page + 1, '<svg><use href="#i-right"/></svg>', `class="step next" aria-label="${t('pager.next')}" title="${t('pager.nextTip')}"${state.page >= state.pages ? ' disabled' : ''}`) +
      `<label class="jump" title="${t('pager.jumpTip')}"><input type="number" min="1" max="${state.pages}" placeholder="${state.page}" aria-label="${t('pager.jump')}"><span>/ ${state.pages}</span></label>`;
  }

  function renderControls() {
    document.querySelectorAll('.seg [data-view]').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.view === state.view)));
    el.seg.style.setProperty('--seg-i', Math.max(0, VIEWS.indexOf(state.view)));
    const sortLabel = t(state.sort === 'desc' ? 'sort.desc' : 'sort.asc');
    el.sort.querySelector('use').setAttribute('href', state.sort === 'desc' ? '#i-sort-desc' : '#i-sort-asc');
    el.sort.title = t('sort.tip', { label: sortLabel });
    el.sort.setAttribute('aria-label', t('sort.label', { label: sortLabel }));
    el.size.value = String(state.size);
    el.group.setAttribute('aria-pressed', String(state.group));
    el.selectMode.setAttribute('aria-pressed', String(state.selectMode));
    const account = t(state.admin ? 'acct.manage' : 'acct.login');
    el.account.title = account;
    el.account.setAttribute('aria-label', account);
    if (state.admin) {
      el.account.setAttribute('aria-haspopup', 'menu');
      if (menuBtn !== el.account) el.account.setAttribute('aria-expanded', 'false');
    } else {
      if (menuBtn === el.account) closeMenu();
      el.account.removeAttribute('aria-haspopup');
      el.account.removeAttribute('aria-expanded');
    }
    el.account.querySelector('use').setAttribute('href', state.admin ? '#i-shield' : '#i-user');
    el.account.classList.toggle('is-admin', state.admin);
    renderFail(state.task); // admins see the reason, guests only that the picture may be stale
    el.reelVideo.hidden = !state.admin;
    if (!state.admin && el.videoDialog.open) { stopVideo(); closeDialog(el.videoDialog, true); }
  }

  // Reading computed styles forces a style pass, so the theme is read only when it may have changed, not per render.
  function renderTheme() {
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
    toast(t(state.sort === 'desc' ? 'sort.desc' : 'sort.asc'));
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
      renderTheme();
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

  // Everything on screen is redrawn in place, so open dialogs, the lightbox and the selection stay as they are.
  function toggleLang() {
    i18n.set(i18n.lang === 'zh' ? 'en' : 'zh');
    i18n.apply();
    renderReel();
    // A skeleton or an error has nothing to relabel yet; the next load renders in the new language.
    if (el.gallery.matches('.skeleton') || el.gallery.querySelector('.load-error')) renderControls(); else render();
    renderBell();
    renderAcct();
    tickTimes();
    if (state.lbIndex >= 0) { updateLightboxMeta(); applyZoom(); renderSelection(); renderCmp(); }
    setPasswordVisible(el.loginForm.elements.password.type === 'text');
    if (el.noticeDialog.open) renderNotices();
    if (el.naDialog.open) { resetDelete(); renderNaForm(); renderNaList(); }
    if (el.statusDialog.open || el.scheduleDialog.open) refreshStatus();
    if (el.videoDialog.open) renderVideoText();
    const res = $('#acct-result');
    if (res.textContent) res.textContent = t('status.error');
  }

  // ---------- selection ----------
  const limit = () => (state.admin ? Infinity : state.guestLimit);

  function toggleSelect(name) {
    if (state.selected.has(name)) {
      state.selected.delete(name);
    } else {
      if (state.selected.size >= limit()) {
        toast(t('sel.guestLimit', { n: state.guestLimit }), 'error');
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
    el.selCount.textContent = state.admin ? t('sel.count', { n }) : t('sel.countGuest', { n, max: state.guestLimit });
    if (Number(el.selCount.dataset.n || 0) !== n) replay(el.selCount, 'bump');
    el.selCount.dataset.n = n;
    $('#sel-zip').disabled = n === 0;
    el.selCompare.hidden = n !== 2;
    el.selVideo.hidden = !state.admin || n < 2;
    el.gallery.querySelectorAll('.tile[data-i]').forEach((t) => {
      const it = state.items[t.dataset.i];
      const on = state.selected.has(it.name);
      // The heart puffs only when a picture is newly picked; redrawn tiles arrive already selected.
      if (on && !t.classList.contains('selected')) t.classList.add('hearted');
      else if (!on) t.classList.remove('hearted');
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
      el.lbSelect.setAttribute('aria-label', t(on ? 'lb.unselect' : 'lb.select'));
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
  let lbBase = null; // the address of the entry under the lightbox's
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
    if (!it || !it.w) {
      img.style.width = img.style.height = '';
    } else {
      const cs = getComputedStyle(el.lbStage);
      const aw = el.lbStage.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
      const ah = el.lbStage.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom);
      const s = Math.min(aw / it.w, ah / it.h, 1);
      img.style.width = Math.round(it.w * s) + 'px';
      img.style.height = Math.round(it.h * s) + 'px';
    }
    if (cmp.on) placeCmp();
  }

  // The 1600px preview covers most screens; HiDPI displays and deep zoom get the original.
  function fullSrc(it, scale = 1) {
    const need = el.lbImg.offsetWidth * scale * (window.devicePixelRatio || 1);
    return it.w > 1600 && need > 1600 ? `/img/${enc(it.name)}` : `/preview/${enc(it.name)}`;
  }

  function updateLightboxMeta() {
    const i = state.lbIndex;
    const it = state.items[i];
    // Phones have room for a short time only; the detail panel and the tooltip keep the full one.
    el.lbTime.textContent = narrow.matches ? fmtStamp(new Date(it.time)) : fmtFull(new Date(it.time));
    el.lbTime.title = fmtFull(new Date(it.time));
    el.lbSub.textContent = `${it.w ? `${it.w}×${it.h} · ` : ''}${fmtSize(it.size)}`;
    el.lbCount.textContent = `${fmtNum((state.page - 1) * state.size + i + 1)} / ${fmtNum(state.total)}`;
    el.lbDownload.href = `/download/${enc(it.name)}`;
    el.lbPrev.disabled = i === 0 && state.page === 1;
    el.lbNext.disabled = i === state.items.length - 1 && state.page === state.pages;

    const d = new Date(it.time);
    el.lbpDate.textContent = fmtDay(d);
    el.lbpTime.textContent = fmtClock(d);
    el.lbpAgo.dataset.ago = it.time;
    el.lbpAgo.dataset.saved = '1';
    el.lbpAgo.textContent = fmtAgo(d);
    el.lbpDownload.href = `/download/${enc(it.name)}`;
    el.lbpName.textContent = it.name;
    const zone = nameZone(it);
    el.lbpZone.hidden = !zone;
    el.lbpZone.textContent = zone ? t('lbp.zoneHint', { zone }) : '';
    renderGrid(i);
    // Sizes the server already keeps, both JPEG: the 1600px preview, and the 480px thumbnail scaled down from it.
    // GIFs are served as-is for the preview, so only the thumbnail is offered.
    const dims = it.w ? `${it.w} × ${it.h}` : t('lbp.unknown');
    const base = it.name.replace(/\.[^.]+$/, '');
    const sizes = [[`/download/${enc(it.name)}`, t('lbp.original'), `${extOf(it.name)} · ${dims} · ${fmtSize(it.size)}`, '']];
    const preview = it.w ? fitEdge(it.w, it.h, 1600) : null;
    if (preview && Math.max(it.w, it.h) > 1600 && extOf(it.name) !== 'GIF') {
      const [w, h] = preview;
      sizes.push([`/preview/${enc(it.name)}`, t('lbp.large'), `JPG · ${w} × ${h}`, `${base}-${w}.jpg`]);
    }
    if (preview && Math.max(it.w, it.h) > 480) {
      const [w, h] = fitEdge(...preview, 480);
      sizes.push([`/thumb/${enc(it.name)}`, t('lbp.thumb'), `JPG · ${w} × ${h}`, `${base}-${w}.jpg`]);
    }
    el.lbpSizeMenu.innerHTML = sizes.map(([href, label, sub, file]) =>
      `<a role="menuitem" href="${href}" download="${esc(file)}"><strong>${label}</strong><span>${esc(sub)}</span></a>`).join('');
  }

  // File names carry the archive's clock time. When this browser's clock differs, the panel names the zone the file
  // name uses, so a name that reads 08:00 beside a picture taken at 09:00 does not look wrong. '' when they agree.
  function nameZone(it) {
    const m = /^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})_/.exec(it.name);
    if (!m) return '';
    const at = new Date(it.time);
    const off = Math.round((Date.UTC(m[1], m[2] - 1, m[3], m[4], m[5], m[6]) - at.getTime()) / 60000);
    if (Number.isNaN(off) || off === -at.getTimezoneOffset()) return '';
    const a = Math.abs(off);
    return `UTC${off < 0 ? '−' : '+'}${Math.floor(a / 60)}${a % 60 ? ':' + pad(a % 60) : ''}`;
  }

  function renderGrid(i) {
    const it = state.items[i];
    const prior = olderItem(i);
    const older = prior && prior.time;
    const gap = older ? (new Date(it.time) - new Date(older)) / 1000 : 0;
    el.lbpGrid.innerHTML = [
      [t('lbp.dims'), it.w ? `${it.w} × ${it.h}` : t('lbp.unknown')], [t('lbp.size'), fmtSize(it.size)], [t('lbp.format'), extOf(it.name) || '—'],
      [t('lbp.ratio'), it.w ? ratioOf(it.w, it.h) : '—'], [t('lbp.gap'), prior === undefined ? '…' : gap > 0 ? fmtGap(gap) : '—'],
      [t('lbp.position'), el.lbCount.textContent],
    ].map(([k, v]) => `<div><dt>${k}</dt><dd>${esc(v)}</dd></div>`).join('');
  }

  // The picture before the oldest one on a page sits on the neighbouring page: it is looked up by name, as when
  // stepping, since pictures saved meanwhile shift every position. Returns it, null when there is none, or undefined
  // while it is being looked up. Every load starts a new generation; an answer from an older one is dropped.
  const olderItems = new Map();
  let olderGen = 0;
  function olderItem(i) {
    const older = state.items[state.sort === 'desc' ? i + 1 : i - 1];
    if (older) return older;
    const { name } = state.items[i];
    if (olderItems.has(name)) return olderItems.get(name);
    const gen = olderGen;
    olderItems.set(name, undefined);
    neighbour(name, state.sort === 'desc' ? 1 : -1).then((n) => (n ? findItem(n) : n)).then((o) => {
      if (gen !== olderGen) return;
      // A failure is not redrawn, which would ask again at once; the next view of this picture does.
      if (o === undefined) { olderItems.delete(name); return; }
      olderItems.set(name, o);
      const shown = state.items[state.lbIndex];
      if (shown && shown.name === name) { renderGrid(state.lbIndex); renderCmp(); }
    }, () => { if (gen === olderGen) olderItems.delete(name); });
    return undefined;
  }

  // ---------- compare ----------
  // The picture shown is compared with the one before it, or with the other of two picked pictures: the older one
  // lies over it, cut at the slider, or the difference is drawn with the changed pixels marked.
  const cmp = { on: false, mode: 'slide', pos: 50, pin: null, seq: 0, diff: null };
  const cmpBase = (i) => (cmp.pin && cmp.pin.for === state.items[i].name ? cmp.pin.item : olderItem(i));

  // The overlay covers the picture exactly.
  function placeCmp() {
    const img = el.lbImg;
    Object.assign(el.cmp.style, { left: img.offsetLeft + 'px', top: img.offsetTop + 'px', width: img.offsetWidth + 'px', height: img.offsetHeight + 'px' });
  }

  function setCmpPos(p) {
    cmp.pos = Math.min(100, Math.max(0, p));
    el.cmp.style.setProperty('--pos', cmp.pos + '%');
    el.cmpLine.setAttribute('aria-valuenow', String(Math.round(cmp.pos)));
  }

  const decoded = (src) => { const img = new Image(); img.src = src; return img.decode().then(() => img); };

  // The newer picture dimmed with the changed pixels in pink, worked out at most DIFF_EDGE wide; also the share of
  // pixels that changed. The last pair is kept, so switching modes back and forth does not redo it.
  const DIFF_EDGE = 800;
  async function diffOf(older, newer) {
    const key = older + '|' + newer;
    if (cmp.diff && cmp.diff.key === key) return cmp.diff;
    const [a, b] = await Promise.all([older, newer].map((n) => decoded(`/preview/${enc(n)}`)));
    const s = Math.min(1, DIFF_EDGE / Math.max(b.naturalWidth, b.naturalHeight));
    const w = Math.max(1, Math.round(b.naturalWidth * s));
    const h = Math.max(1, Math.round(b.naturalHeight * s));
    const c = document.createElement('canvas');
    c.width = w;
    c.height = h;
    const g = c.getContext('2d', { willReadFrequently: true });
    g.drawImage(a, 0, 0, w, h);
    const pa = g.getImageData(0, 0, w, h).data;
    g.drawImage(b, 0, 0, w, h);
    const out = g.getImageData(0, 0, w, h);
    const pb = out.data;
    let changed = 0;
    for (let p = 0; p < pb.length; p += 4) {
      // JPEG previews differ a little even where nothing changed.
      if (Math.max(Math.abs(pa[p] - pb[p]), Math.abs(pa[p + 1] - pb[p + 1]), Math.abs(pa[p + 2] - pb[p + 2])) > 40) {
        changed++;
        pb[p] = 255; pb[p + 1] = 46; pb[p + 2] = 110;
      } else {
        pb[p] *= 0.3; pb[p + 1] *= 0.3; pb[p + 2] *= 0.3;
      }
    }
    g.putImageData(out, 0, 0);
    cmp.diff = { key, canvas: c, ratio: changed / (w * h) };
    return cmp.diff;
  }

  function renderCmp() {
    const on = cmp.on && state.lbIndex >= 0;
    const seq = ++cmp.seq;
    const base = on ? cmpBase(state.lbIndex) : null;
    el.lb.classList.toggle('comparing', on);
    el.lbCompare.setAttribute('aria-pressed', String(on));
    el.lbZoom.disabled = on;
    el.cmpBar.hidden = !on;
    el.cmp.hidden = !base;
    if (!on) return;
    for (const b of el.cmpBar.querySelectorAll('[data-mode]')) b.setAttribute('aria-pressed', String(b.dataset.mode === cmp.mode));
    if (!base) { el.cmpNote.textContent = t(base === null ? 'cmp.none' : 'cmp.loading'); return; }
    const label = t('cmp.with', { time: fmtStamp(new Date(base.time)) });
    $('#cmp-tag-old').textContent = fmtStamp(new Date(base.time));
    $('#cmp-tag-new').textContent = fmtStamp(new Date(state.items[state.lbIndex].time));
    placeCmp();
    setCmpPos(cmp.pos);
    const src = `/preview/${enc(base.name)}`;
    if (el.cmpOld.getAttribute('src') !== src) el.cmpOld.src = src;
    el.cmpOld.alt = t('cmp.old', { time: fmtFull(new Date(base.time)) });
    const diff = cmp.mode === 'diff';
    el.cmp.classList.toggle('diff', diff);
    el.cmpNote.textContent = diff ? `${label} · ${t('cmp.working')}` : label;
    if (!diff) return;
    el.cmpDiff.width = 0; // the previous pair's difference must not show meanwhile
    diffOf(base.name, state.items[state.lbIndex].name).then((d) => {
      if (seq !== cmp.seq) return;
      el.cmpDiff.width = d.canvas.width;
      el.cmpDiff.height = d.canvas.height;
      el.cmpDiff.getContext('2d').drawImage(d.canvas, 0, 0);
      el.cmpNote.textContent = `${label} · ${t('cmp.changed', { pct: fmtNum(d.ratio * 100, 1) + '%' })}`;
    }, () => { if (seq === cmp.seq) el.cmpNote.textContent = `${label} · ${t('cmp.failed')}`; });
  }

  // A picture by name: from this page, or from the page around it.
  async function findItem(name) {
    const here = state.items.find((it) => it.name === name);
    if (here) return here;
    const d = await api(`/api/images?around=${enc(name)}&size=12&sort=${state.sort}`);
    return (d.focus === name && d.items.find((it) => it.name === name)) || null;
  }

  // Two picked pictures: the newer one opens, compared with the older one.
  async function comparePicked() {
    if (state.selected.size !== 2) return;
    // Moving on (another page, a picture opened, a different selection) while the pair is looked up drops it.
    let seq = routeSeq;
    const names = [...state.selected];
    const current = () => seq === routeSeq && state.selected.size === 2 && names.every((n) => state.selected.has(n));
    let pair;
    try { pair = await Promise.all(names.map(findItem)); } catch (e) { if (current()) toast(t('load.failedLater'), 'error'); return; }
    if (!current()) return;
    if (pair.includes(null)) { toast(t('err.image_not_found'), 'error'); return; }
    const [older, newer] = pair.sort((a, b) => new Date(a.time) - new Date(b.time));
    let i = state.items.findIndex((it) => it.name === newer.name);
    if (i < 0) {
      // Its page is shown first, as when a link is opened; the same checks keep a stale pair from showing it. A day
      // being shown stays until that page arrives, which ends it unless the day is on the page.
      leaveRoute();
      seq = routeSeq;
      if (!(await load({ around: newer.name, guard: current }))) return;
      i = state.items.findIndex((it) => it.name === newer.name);
      if (i < 0) return;
    }
    openLightbox(i, 0, imgFor(i));
    cmp.pin = { for: newer.name, item: older };
    cmp.on = true;
    resetZoom();
    renderCmp();
  }

  const wideScreen = window.matchMedia('(min-width: 1100px)');
  const narrow = window.matchMedia('(max-width: 640px)');
  const sheet = window.matchMedia('(max-width: 900px)'); // the detail panel covers the picture as a bottom sheet
  narrow.addEventListener('change', () => { if (state.lbIndex >= 0) updateLightboxMeta(); });
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
      // A picture opened by hand ends any link still being looked up. Its entry is the one being restored, if any;
      // otherwise the address is put back to the page (a link given up on may still be in it) and a new entry pushed.
      if (!linking) { ++routeSeq; lbRestore = 0; }
      if (lbHistory) history.replaceState(history.state, '', imgHash(it.name));
      else { writeHash(); lbBase = location.hash; history.pushState({ lightbox: true }, '', imgHash(it.name)); lbHistory = true; }
      $('#lb-close').focus({ preventScroll: true });
    } else if (lbHistory) {
      history.replaceState(history.state, '', imgHash(it.name)); // the address follows the picture shown
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
    renderCmp();
  }

  function closeLightbox(fromHistory = false) {
    if (state.lbIndex < 0) return;
    const shown = imgFor(state.lbIndex);
    const rect = zoom.on ? null : visibleRect(shown);
    // A reload re-renders the tiles, so the button that opened the lightbox may be gone.
    const back = lbReturn && lbReturn.isConnected ? lbReturn : shown && shown.closest('button');
    ++lbSeq;
    state.lbIndex = -1;
    cmp.on = false;
    cmp.pin = null;
    renderCmp();
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

  // Pictures saved since this page loaded shift every page boundary, so stepping off the page looks up the neighbour
  // of the current picture by name, then shows the page holding that neighbour (around=) and opens it by name;
  // stepping by position would skip a picture or show one twice. Resolves to the neighbour's name, null when there
  // is none, or undefined when it could not be found (the caller then falls back to the next page).
  async function neighbour(name, delta) {
    const list = (q) => api(`/api/images?${q}&size=${state.size}&sort=${state.sort}`);
    try {
      for (let attempt = 0; attempt < 3; attempt++) {
        const d = await list(`around=${enc(name)}`);
        const j = d.focus === name ? d.items.findIndex((it) => it.name === name) : -1;
        if (j < 0) return undefined;
        if (d.items[j + delta]) return d.items[j + delta].name;
        const page = d.page + delta;
        if (page < 1 || page > d.pages) return null;
        const e = await list(`page=${page}`);
        if (e.page !== page) return undefined;
        // A picture saved between the two requests can bring the current one onto this page as well.
        const k = e.items.findIndex((it) => it.name === name);
        if (k >= 0) { const it = e.items[k + delta]; if (it) return it.name; continue; }
        // Otherwise the edge of this page is the neighbour only if nothing was saved or removed in between.
        if (e.total === d.total) { const it = e.items[delta > 0 ? 0 : e.items.length - 1]; return it ? it.name : undefined; }
      }
    } catch (e) { /* fall back below */ }
    return undefined;
  }

  let stepping = false;
  async function step(delta) {
    if (state.lbIndex < 0 || stepping) return;
    const i = state.lbIndex + delta;
    if (i >= 0 && i < state.items.length) return openLightbox(i, delta);
    if (state.page + delta < 1 || state.page + delta > state.pages) return;
    stepping = true;
    const from = state.page;
    // Closing or reopening the lightbox meanwhile bumps lbSeq; this step then leaves the new view alone.
    const seq = lbSeq;
    const current = () => seq === lbSeq && state.lbIndex >= 0;
    try {
      const name = await neighbour(state.items[state.lbIndex].name, delta);
      if (name === null || !current()) return;
      // With the neighbour's name its page is found by around=; without it, the next page is the best guess.
      const page = name ? state.page : state.page + delta;
      slideDir = delta;
      state.page = page;
      state.lastIndex = null;
      // The guard keeps a page that arrives after the lightbox was closed or reopened from replacing the gallery.
      if (await load({ scroll: true, around: name || '', guard: current })) {
        const k = name ? state.items.findIndex((it) => it.name === name) : delta < 0 ? state.items.length - 1 : 0;
        if (k >= 0) openLightbox(k, delta);
      } else if (state.page === page) {
        // Nothing new was shown: put the page back under the pictures still on screen.
        state.page = from;
        writeHash();
        if (current()) { render(); toast(t('load.failedLater'), 'error'); }
      }
    } finally {
      stepping = false;
    }
  }

  function applyZoom() {
    el.lbImg.style.transform = zoom.on ? `translate(${zoom.x}px, ${zoom.y}px) scale(${zoom.s})` : '';
    el.lbStage.classList.toggle('zoomed', zoom.on);
    el.lbZoom.setAttribute('aria-pressed', String(zoom.on));
    el.lbZoom.setAttribute('aria-label', t(zoom.on ? 'lb.unzoom' : 'lb.zoom'));
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
    if (state.lbIndex < 0 || cmp.on || !img.offsetWidth) return;
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
    if (state.lbIndex < 0 || cmp.on) return;
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
    const page = new URL(imgHash(it.name), location.href).href; // this page with the picture open
    return { url, page, md: `![${alt}](${url})`, html: `<img src="${url}" alt="${alt}">`, bb: `[img]${url}[/img]` }[fmt];
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
    toast(ok ? (fmt === 'url' ? t('copy.url') : fmt === 'page' ? t('copy.page') : t('copy.fmt', { label })) : t('copy.failed'), ok ? 'ok' : 'error');
    return ok;
  }

  // ---------- admin ----------
  // The avatar opens a menu of admin sections; each section is its own dialog.
  function renderAcct() {
    $('#acct-sub').textContent = t('acct.archived', { n: fmtNum(state.total) });
    const task = state.task;
    const sch = $('#acct-schedule');
    sch.textContent = task ? t(task.paused ? 'task.paused' : 'task.running') : '';
    sch.className = task && !task.paused ? 'ok' : '';
    const count = na.items.length || notices.items.length;
    $('#acct-notices').textContent = count ? t('acct.noticeCount', { n: count }) : '';
  }

  function openAcctMenu() {
    renderAcct();
    openMenu(el.account);
    // Fill in the last fetch result and catch an expired session without waiting for a click.
    api('/api/status').then(renderStats, (e) => { if (e.status === 401) handleAuthError(e); });
  }

  async function refreshStatus() {
    try {
      renderStats(await api('/api/status'));
    } catch (e) { handleAuthError(e); }
  }

  function openStatus() {
    el.adminStats.innerHTML = `<dt class="loading-row">${t('status.loadingRow')}</dt><dd class="loading-row">${t('dialog.loading')}</dd>`;
    openDialog(el.statusDialog);
    refreshStatus();
  }

  function openSchedule() {
    el.scheduleStats.innerHTML = `<dt class="loading-row">${t('schedule.plan')}</dt><dd class="loading-row">${t('dialog.loading')}</dd>`;
    el.toggleSchedule.disabled = true;
    el.scheduleHint.textContent = t('dialog.loading');
    openDialog(el.scheduleDialog);
    refreshStatus();
  }

  function renderStats(s) {
    const f = s.fetch;
    const at = (v) => (validTime(v) ? fmtFull(new Date(v)) : t('status.none'));
    const result = { saved: `<span class="ok">${t('status.saved')}</span>`, unchanged: t('status.unchanged'), error: `<span class="bad">${t('status.error')}</span>` }[f.result] || t('status.never');
    el.adminStats.innerHTML = `
      <dt>${t('status.count')}</dt><dd>${t('status.countValue', { n: fmtNum(s.count) })}</dd>
      <dt>${t('status.lastRun')}</dt><dd>${at(f.last_run)}</dd>
      <dt>${t('status.lastFetched')}</dt><dd>${at(f.last_fetched)}</dd>
      <dt>${t('status.result')}</dt><dd>${result}${f.message ? `<br><span class="bad">${esc(f.message)}</span>` : ''}</dd>
      <dt>${t('status.lastSaved')}</dt><dd>${at(f.last_saved)}</dd>
      <dt>${t('status.session')}</dt><dd>${t('status.sessionValue', { runs: f.runs, saved: f.saved })}</dd>${tokenRow(s.token)}${cookieRow(s.cookie)}${diskRow(s.disk)}`;
    renderRuns(s.history || []);
    state.cookie = s.cookie;
    renderCookieState();
    el.notifyTest.hidden = !s.notify;
    const next = !f.paused && validTime(s.next_run) ? s.next_run : '';
    const plan = describeSchedule(s.schedule);
    el.scheduleStats.innerHTML = `
      <dt>${t('schedule.plan')}</dt><dd>${plan ? `${esc(plan)} ` : ''}<code>${esc(s.schedule)}</code></dd>
      <dt>${t('schedule.next')}</dt><dd>${next ? `<span data-until="${esc(next)}">${fmtUntil(new Date(next))}</span>` : f.paused ? t('schedule.paused') : '—'}</dd>
      <dt>${t('schedule.lastFetched')}</dt><dd>${at(f.last_fetched)}</dd>`;
    el.toggleSchedule.disabled = false;
    el.toggleSchedule.setAttribute('aria-checked', String(!f.paused));
    el.scheduleHint.textContent = t(f.paused ? 'schedule.pausedHint' : 'schedule.activeHint');
    const res = $('#acct-result');
    res.textContent = f.result === 'error' ? t('status.error') : '';
    res.className = f.result === 'error' ? 'bad' : '';
    state.task = Object.assign({}, state.task, {
      paused: f.paused, last_fetched: f.last_fetched, next_run: s.next_run, last_run: f.last_run,
      result: f.result, code: f.code || '', failures: f.failures || 0, retry_at: f.retry_at || '',
    });
    renderTask();
    renderAcct();
    schedulePoll();
  }

  // Common schedules in words beside the expression; '' for the rest. Times of day follow the server's clock.
  function describeSchedule(spec) {
    spec = String(spec).trim();
    const every = /^@every\s+(\S+)$/.exec(spec);
    if (every) {
      const sec = goDuration(every[1]);
      return sec >= 1 ? t('cron.every', { gap: fmtGap(sec) }) : '';
    }
    if (spec === '@hourly') return t('cron.hourlyAt', { m: '00' });
    if (spec === '@daily' || spec === '@midnight') return t('cron.dailyAt', { time: '00:00' });
    const f = spec.split(/\s+/);
    if (f.length !== 5 || f.slice(2).some((x) => x !== '*')) return '';
    const [m, h] = f;
    const num = (x, max) => /^\d+$/.test(x) && Number(x) <= max;
    const step = /^\*\/(\d+)$/.exec(m);
    if (h === '*' && m === '*') return t('cron.everyMinute');
    // */N restarts at each full hour, so only a divisor of 60 gives an even interval.
    if (h === '*' && step && Number(step[1]) >= 1 && 60 % Number(step[1]) === 0) return t('cron.every', { gap: fmtGap(step[1] * 60) });
    if (h === '*' && num(m, 59)) return t('cron.hourlyAt', { m: pad(Number(m)) });
    if (num(h, 23) && num(m, 59)) return t('cron.dailyAt', { time: `${pad(Number(h))}:${pad(Number(m))}` });
    return '';
  }

  // Seconds in a Go duration such as 90s, 5m or 1h30m; NaN when it is not one.
  function goDuration(v) {
    const units = { h: 3600, m: 60, s: 1, ms: 1e-3 };
    let sec = 0;
    const rest = v.replace(/(\d+(?:\.\d+)?)(ms|h|m|s)/g, (_, n, u) => { sec += n * units[u]; return ''; });
    return rest === '' && v !== '' ? sec : NaN;
  }

  function tokenRow(tok) {
    if (!tok) return '';
    const exp = new Date(tok.expires);
    const days = Math.floor((exp - Date.now()) / 86400000);
    const left = days < 0 ? `<span class="bad">${t('token.expired')}</span>`
      : `<span class="${days < 7 ? 'bad' : 'ok'}">${t('token.daysLeft', { n: days })}</span>`;
    return `<dt>${t('token.label')}</dt><dd>${t('token.value', { cookie: esc(tok.cookie), time: fmtShort(exp), left })}</dd>`;
  }

  function cookieRow(c) {
    if (!c) return '';
    const value = c.source === 'page' && validTime(c.updated) ? t('cookie.fromPage', { time: fmtShort(new Date(c.updated)) }) : t('cookie.fromConfig');
    return `<dt>${t('cookie.label')}</dt><dd>${value}</dd>`;
  }

  function diskRow(d) {
    if (!d || !d.total) return '';
    const pct = Math.round((1 - d.free / d.total) * 100);
    return `<dt>${t('disk.label')}</dt><dd><span class="meter${pct >= 90 ? ' bad' : ''}" aria-hidden="true"><span style="width: ${pct}%"></span></span>${t('disk.value', { pct, archive: fmtSize(d.archive), free: fmtSize(d.free) })}</dd>`;
  }

  // The recorded runs, newest first: what each attempt got, so an outage can be read without the server log.
  function renderRuns(runs) {
    el.adminRuns.hidden = !runs.length;
    el.runsList.innerHTML = runs.map((r) => {
      const key = `fail.${r.code}`;
      const what = r.result === 'saved' ? t('status.saved') : r.result === 'unchanged' ? t('status.unchanged') : t(key) !== key ? t(key) : t('fail.other');
      const meta = [r.http ? `HTTP ${r.http}` : '', r.bytes ? fmtSize(r.bytes) : '', t('runs.took', { s: fmtNum(r.ms / 1000, 1) })].filter(Boolean).join(' · ');
      const d = new Date(r.time);
      return `<li class="run ${esc(r.result)}"${r.message ? ` title="${esc(r.message)}"` : ''}>
        <time datetime="${esc(r.time)}" title="${esc(fmtFull(d))}">${fmtShort(d)}</time>
        <span class="run-what">${what}${r.manual ? ` <em>${t('runs.manual')}</em>` : ''}</span>
        <span class="run-meta">${meta}</span></li>`;
    }).join('');
  }

  function renderCookieState() {
    const c = state.cookie;
    el.cookieState.textContent = !c ? '' : c.source === 'page' && validTime(c.updated)
      ? t('cookie.fromPage', { time: fmtShort(new Date(c.updated)) }) : t('cookie.fromConfig');
  }

  function openCookie() {
    el.cookieForm.reset();
    showCookieError('');
    renderCookieState();
    openDialog(el.cookieDialog);
    el.cookieForm.elements.cookie.focus();
    refreshStatus();
  }

  function showCookieError(msg) {
    el.cookieError.hidden = !msg;
    el.cookieError.textContent = msg;
    el.cookieForm.elements.cookie.toggleAttribute('aria-invalid', !!msg);
    if (msg) announce(msg, true);
  }

  el.cookieForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const value = el.cookieForm.elements.cookie.value.trim();
    if (!value) { showCookieError(t('err.invalid_cookie')); el.cookieForm.elements.cookie.focus(); return; }
    el.cookieSave.disabled = true;
    el.cookieSave.classList.add('loading');
    try {
      const s = await api('/api/cookie', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ cookie: value }) });
      renderStats(s);
      closeDialog(el.cookieDialog, true);
      toast(t('cookie.saved'), 'ok');
    } catch (err) {
      if (err.status === 401) handleAuthError(err); else showCookieError(err.message);
    } finally {
      el.cookieSave.disabled = false;
      el.cookieSave.classList.remove('loading');
    }
  });
  el.cookieForm.elements.cookie.addEventListener('input', () => { if (!el.cookieError.hidden) showCookieError(''); });

  el.notifyTest.addEventListener('click', async () => {
    closeMenu(true);
    try {
      await api('/api/notify/test', { method: 'POST' });
      toast(t('notify.sent'), 'ok');
    } catch (e) { handleAuthError(e); }
  });

  function handleAuthError(e) {
    if (e.status === 401) {
      state.admin = false;
      closeMenu();
      [el.statusDialog, el.scheduleDialog, el.naDialog, el.cookieDialog, el.videoDialog].forEach((d) => closeDialog(d, true));
      renderControls();
      toast(t('login.expired'), 'error');
    } else {
      toast(e.message, 'error');
    }
  }

  // ---------- timelapse video ----------
  // Recorded in the browser, for admins only: each picture is drawn on a canvas whose stream a MediaRecorder encodes,
  // so recording takes as long as the video lasts.
  const FRAME_MS = [100, 200, 500, 1000];
  let videoJob = null; // { stop, wake } while recording
  const videoType = () => (window.MediaRecorder && HTMLCanvasElement.prototype.captureStream
    && ['video/webm;codecs=vp9', 'video/webm;codecs=vp8', 'video/webm', 'video/mp4'].find((m) => MediaRecorder.isTypeSupported(m))) || '';
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  // Oldest first: the last 24 hours, or the picked pictures. File names start with the time they were saved, so
  // sorting the names sorts by time; a picture known from a page or the reel also brings its time and size.
  function videoFrames(range) {
    if (range === 'recent') return reel.items.slice();
    const known = new Map([...reel.items, ...state.items].map((it) => [it.name, it]));
    return [...state.selected].sort().map((name) => known.get(name) || { name });
  }

  // The archive's clock time a file name carries, for a picture whose saved time is not known here.
  function nameClock(name) {
    const m = /^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})/.exec(name);
    return m ? `${m[1]}-${m[2]}-${m[3]} ${m[4]}:${m[5]}` : name;
  }

  function drawFrame(g, img, f) {
    const { width: W, height: H } = g.canvas;
    g.fillStyle = '#000';
    g.fillRect(0, 0, W, H);
    const s = Math.min(W / img.naturalWidth, H / img.naturalHeight);
    const w = img.naturalWidth * s;
    const h = img.naturalHeight * s;
    g.drawImage(img, (W - w) / 2, (H - h) / 2, w, h);
    // The time in the corner, on a dark plate so it reads over any picture.
    const label = f.time ? fmtFull(new Date(f.time)) : nameClock(f.name);
    const fs = Math.max(12, Math.round(H / 28));
    const pad = Math.round(fs / 2);
    g.font = `600 ${fs}px system-ui, sans-serif`;
    g.fillStyle = 'rgba(0, 0, 0, 0.55)';
    g.fillRect(pad, H - fs - pad * 3, g.measureText(label).width + pad * 2, fs + pad * 2);
    g.fillStyle = '#fff';
    g.textBaseline = 'middle';
    g.fillText(label, pad * 2, H - pad * 2 - fs / 2);
  }

  // Resolves the video, or null when stopped. Pictures load a few ahead; one that is late holds the one before it
  // longer rather than being skipped, and one that fails to load is left out.
  async function recordVideo(frames, size, frameMs, job, progress) {
    const type = videoType();
    // Every wait gives way to Cancel or to the recorder failing, so a picture that never finishes loading cannot hold
    // the recording open. A wait listens only while it lasts: one raced against a promise that stays pending would keep
    // every decoded picture alive until the end.
    let failure = null;
    const until = (p) => new Promise((resolve, reject) => {
      if (failure) { reject(failure); return; }
      if (job.stop) { resolve(undefined); return; }
      const wake = (err) => (err ? reject(err) : resolve(undefined));
      job.wake.add(wake);
      p.then(resolve, reject).finally(() => job.wake.delete(wake));
    });
    const fail = (err) => { failure = err; for (const wake of job.wake) wake(err); };
    const loads = new Map();
    const get = (k) => {
      if (!loads.has(k)) loads.set(k, decoded(`/${size}/${enc(frames[k].name)}`).catch(() => null));
      return loads.get(k);
    };
    // The first picture that loads sets the size; encoders want even sides.
    let first = null;
    for (let k = 0; k < frames.length && !first && !job.stop; k++) first = await until(get(k));
    if (job.stop) return null;
    if (!first) throw new Error(t('video.empty'));
    const c = document.createElement('canvas');
    c.width = Math.max(2, first.naturalWidth & ~1);
    c.height = Math.max(2, first.naturalHeight & ~1);
    const g = c.getContext('2d');
    const stream = c.captureStream(30);
    const chunks = [];
    let skipped = 0;
    try {
      const rec = new MediaRecorder(stream, { mimeType: type, videoBitsPerSecond: size === 'preview' ? 8e6 : 3e6 });
      rec.ondataavailable = (e) => { if (e.data.size) chunks.push(e.data); };
      let ending = false;
      const stopped = new Promise((resolve, reject) => {
        // Stopping before it was asked to means it failed.
        rec.onstop = () => { if (!ending) fail(new Error('MediaRecorder stopped')); resolve(); };
        rec.onerror = (e) => { const err = e.error || new Error('MediaRecorder'); fail(err); reject(err); };
      });
      stopped.catch(() => {}); // awaited below; an early error must not be reported as unhandled meanwhile
      rec.start(1000);
      let last = null;
      try {
        for (let k = 0; k < frames.length && !job.stop; k++) {
          for (let j = k + 1; j < Math.min(frames.length, k + 4); j++) get(j);
          const img = await until(get(k));
          loads.delete(k);
          if (job.stop) break;
          progress(k + 1, frames.length);
          if (!img) { skipped++; continue; }
          drawFrame(g, img, frames[k]);
          last = [img, frames[k]];
          await until(sleep(frameMs));
        }
        // Drawn once more, so the last picture is held for its full time instead of ending the video at once.
        if (last && !job.stop) { drawFrame(g, ...last); await sleep(100); }
      } finally {
        ending = true;
        if (rec.state !== 'inactive') rec.stop();
      }
      await stopped;
      if (failure) throw failure;
    } finally {
      stream.getTracks().forEach((tr) => tr.stop());
    }
    if (job.stop) return null;
    return { blob: new Blob(chunks, { type: type.split(';')[0] }), ext: type.startsWith('video/mp4') ? 'mp4' : 'webm', skipped };
  }

  function saveBlob(blob, name) {
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = name;
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 60000);
  }

  function renderVideoEst() {
    const f = el.videoForm.elements;
    const s = (videoFrames(f.range.value).length * Number(f.frame.value)) / 1000;
    // Short videos keep a tenth of a second, which fmtGap would round away.
    const dur = s < 60 ? t('video.sec', { n: fmtNum(s, s < 10 && s % 1 ? 1 : 0) }) : fmtGap(s);
    el.videoEst.textContent = t('video.est', { dur });
  }

  function showVideoError(msg) {
    el.videoError.hidden = !msg;
    el.videoError.textContent = msg;
    if (msg) announce(msg, true);
  }

  function setVideoBusy(on) {
    el.videoProgress.hidden = !on;
    el.videoStart.classList.toggle('loading', on);
    el.videoStart.disabled = on || !videoType();
    for (const n of el.videoForm.querySelectorAll('.video-range, .video-opts')) n.inert = on;
    if (on) { el.videoBar.value = 0; el.videoStep.textContent = ''; }
  }

  // The dialog's text that depends on the language or the counts.
  function renderVideoText() {
    const frame = el.videoFrame.value || '200';
    el.videoFrame.innerHTML = FRAME_MS.map((ms) => `<option value="${ms}">${esc(t('video.sec', { n: ms / 1000 }))}</option>`).join('');
    el.videoFrame.value = frame;
    $('#video-recent').textContent = t('video.recent', { n: reel.items.length });
    $('#video-picked').textContent = t('video.picked', { n: state.selected.size });
    renderVideoEst();
  }

  function openVideo(range) {
    if (!state.admin || videoJob) return;
    const f = el.videoForm.elements;
    renderVideoText();
    const recent = reel.items.length;
    const picked = state.selected.size;
    f.range[0].disabled = recent < 2;
    f.range[1].disabled = picked < 2;
    f.range.value = (range === 'picked' && picked >= 2) || recent < 2 ? 'picked' : 'recent';
    setVideoBusy(false);
    showVideoError(!videoType() ? t('video.unsupported') : recent < 2 && picked < 2 ? t('video.empty') : '');
    if (recent < 2 && picked < 2) el.videoStart.disabled = true;
    renderVideoEst();
    openDialog(el.videoDialog);
    if (!el.videoStart.disabled) el.videoStart.focus();
  }

  el.videoForm.addEventListener('change', renderVideoEst);
  el.videoForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (videoJob || !state.admin) return;
    const f = el.videoForm.elements;
    const frames = videoFrames(f.range.value);
    if (frames.length < 2) { showVideoError(t('video.empty')); return; }
    showVideoError('');
    const job = { stop: false, wake: new Set() }; // wake: the waits Cancel ends early
    videoJob = job;
    setVideoBusy(true);
    try {
      const out = await recordVideo(frames, f.quality.value, Number(f.frame.value), job, (k, n) => {
        el.videoBar.value = k / n;
        el.videoStep.textContent = k < n ? t('video.progress', { k: fmtNum(k), n: fmtNum(n) }) : t('video.finishing');
      });
      if (!out || job.stop) return;
      const stamp = (fr) => (/^\d{8}-\d{4}/.exec(fr.name) || ['video'])[0];
      saveBlob(out.blob, `magma-${stamp(frames[0])}_${stamp(frames[frames.length - 1])}.${out.ext}`);
      toast(out.skipped ? t('video.doneSkipped', { n: out.skipped }) : t('video.done'), 'ok');
      closeDialog(el.videoDialog, true);
    } catch (err) {
      if (!job.stop) showVideoError(t('video.failed', { msg: err.message }));
    } finally {
      if (videoJob === job) videoJob = null;
      setVideoBusy(false);
    }
  });
  // Closing the dialog (Cancel, Esc, signing out) stops the recording at once, not when the closing animation ends,
  // so a recording finishing meanwhile is not saved.
  function stopVideo() {
    if (videoJob) { videoJob.stop = true; for (const wake of videoJob.wake) wake(); }
    return true;
  }
  el.videoDialog.beforeClose = stopVideo;
  el.videoDialog.addEventListener('close', stopVideo);
  el.reelVideo.addEventListener('click', () => openVideo('recent'));
  el.selVideo.addEventListener('click', () => openVideo('picked'));
  el.selCompare.addEventListener('click', comparePicked);

  // ---------- notices ----------
  // Visitors only see published notices. The bell glows while one is newer than the last
  // "mark as read", which each browser keeps for itself; nothing ever opens on its own.
  const notices = { items: [], stamp: undefined, seen: Date.parse(store.get('noticeSeen', '')) || 0 };
  const noticeTime = (n) => new Date(n.published).getTime();
  const isUnread = (n) => noticeTime(n) > notices.seen;

  // stamp is the newest publish time the image list reports; a change means the list is stale.
  function checkNotices(stamp) {
    if (stamp === notices.stamp) return;
    notices.stamp = stamp;
    loadNotices();
  }

  async function loadNotices() {
    try {
      notices.items = (await api('/api/notices')).items;
    } catch (e) { return; }
    renderBell();
    if (el.noticeDialog.open) renderNotices();
  }

  function renderBell() {
    const n = notices.items.filter(isUnread).length;
    const label = n ? t('bell.unread', { n }) : t('bell.label');
    el.noticeBtn.classList.toggle('unread', n > 0);
    el.noticeBtn.title = t('bell.tip', { label });
    el.noticeBtn.setAttribute('aria-label', label);
    renderBanner();
  }

  // The newest unread notice also shows as a strip above the picture until it is read or dismissed.
  function renderBanner() {
    const n = notices.items.find(isUnread);
    el.noticeBanner.hidden = !n;
    if (n) el.noticeBannerTitle.textContent = n.title;
  }
  // Opening the notices from the strip reads them; they are marked once the dialog closes, so its "new" tags still show.
  let readOnClose = false;
  $('#notice-banner-open').addEventListener('click', () => { readOnClose = true; openNotices(); });
  $('#notice-banner-close').addEventListener('click', () => { if (notices.items.length) setNoticesSeen(noticeTime(notices.items[0])); });
  el.noticeDialog.addEventListener('close', () => {
    if (readOnClose && notices.items.length) setNoticesSeen(noticeTime(notices.items[0]));
    readOnClose = false;
  });

  // Plain text with line breaks kept; only http(s) addresses become links.
  function linkify(text) {
    let out = '';
    let last = 0;
    for (const m of text.matchAll(/https?:\/\/[^\s<>"'，。；！？、）》」]+/g)) {
      const url = m[0].replace(/[.,;:!?)\]]+$/, '');
      out += esc(text.slice(last, m.index)) + `<a href="${esc(url)}" target="_blank" rel="noopener noreferrer">${esc(url)}</a>`;
      last = m.index + url.length;
    }
    return out + esc(text.slice(last));
  }

  function noticeHTML(n, lead = false) {
    const d = new Date(n.published);
    const unread = isUnread(n);
    const edited = new Date(n.updated) - d > 60000;
    return `<article class="notice${lead ? ' lead' : ''}${unread ? ' unread' : ''}">
      <p class="notice-meta">${unread ? `<span class="notice-new">${t('notice.new')}</span>` : ''}<time datetime="${esc(n.published)}" title="${esc(fmtFull(d))}">${fmtDay(d)} ${fmtHM(d)}</time>${edited ? `<span title="${esc(fmtFull(new Date(n.updated)))}">${t('notice.edited')}</span>` : ''}</p>
      <h3>${esc(n.title)}</h3>
      ${n.body ? `<div class="notice-text">${linkify(n.body)}</div>` : ''}
    </article>`;
  }

  // The newest notice is shown in full; earlier ones fold away, opened when one of them is unread.
  function renderNotices() {
    const [lead, ...older] = notices.items;
    el.noticeActions.hidden = !notices.items.some(isUnread);
    if (!lead) {
      el.noticeList.innerHTML = `<p class="notice-empty"><svg class="mascot" aria-hidden="true"><use href="#mascot-sleep"/></svg>${t('notice.empty')}</p>`;
      return;
    }
    const wasOpen = el.noticeList.querySelector('.notice-history')?.open;
    el.noticeList.innerHTML = noticeHTML(lead, true) + (older.length
      ? `<details class="notice-history"${wasOpen || older.some(isUnread) ? ' open' : ''}><summary><svg><use href="#i-right"/></svg>${t('notice.history', { n: older.length })}</summary>${older.map((n) => noticeHTML(n)).join('')}</details>`
      : '');
  }

  function openNotices() {
    renderNotices();
    openDialog(el.noticeDialog);
    el.noticeList.scrollTop = 0;
    loadNotices();
  }

  function setNoticesSeen(t) {
    notices.seen = Math.max(notices.seen, t);
    store.set('noticeSeen', new Date(notices.seen).toISOString());
    renderBell();
    if (el.noticeDialog.open) renderNotices();
  }

  // ---------- notice editor (admin) ----------
  const na = { items: [], id: null, base: { title: '', body: '' }, delTimer: 0, discardUntil: 0 };
  const naFields = () => el.naForm.elements;
  const naCurrent = () => na.items.find((n) => n.id === na.id) || null;
  const naDirty = () => naFields().title.value !== na.base.title || naFields().body.value !== na.base.body;
  // Leaving unsaved edits takes a second try within 4 seconds; the first one explains why nothing happened.
  function naDiscard() {
    if (!naDirty() || Date.now() < na.discardUntil) { na.discardUntil = 0; return true; }
    na.discardUntil = Date.now() + 4000;
    showNaError(t('na.discard'));
    return false;
  }
  el.naDialog.beforeClose = naDiscard;

  async function openNoticeAdmin() {
    editNotice(null);
    openDialog(el.naDialog);
    try {
      na.items = (await api('/api/notices/all')).items;
    } catch (e) { handleAuthError(e); return; }
    if (!naDirty() && na.items.length) editNotice(na.items[0].id); else renderNaList();
    renderAcct();
    if (!na.id) naFields().title.focus();
  }

  function editNotice(id) {
    const n = na.items.find((x) => x.id === id) || null;
    na.id = n && n.id;
    na.base = { title: n ? n.title : '', body: n ? n.body : '' };
    naFields().title.value = na.base.title;
    naFields().body.value = na.base.body;
    clearNaError();
    resetDelete();
    renderNaForm();
    renderNaList();
  }

  function renderNaList() {
    el.naList.innerHTML = na.items.length ? na.items.map((n) => {
      const pub = validTime(n.published);
      return `<li><button type="button" class="na-item" data-id="${esc(n.id)}"${n.id === na.id ? ' aria-current="true"' : ''}>
        <strong>${esc(n.title)}</strong>
        <span><span class="na-tag${pub ? ' on' : ''}">${t(pub ? 'na.tagPublished' : 'na.tagDraft')}</span>${fmtShort(new Date(pub ? n.published : n.updated))}</span></button></li>`;
    }).join('') : `<li class="na-empty">${t('na.empty')}</li>`;
  }

  function renderNaForm() {
    const n = naCurrent();
    const pub = n && validTime(n.published);
    el.naState.innerHTML = !n ? t('na.stateNew')
      : pub ? `<span class="na-tag on">${t('na.tagPublished')}</span>${t('na.statePublished', { time: fmtShort(new Date(n.published)) })}`
        : `<span class="na-tag">${t('na.tagDraft')}</span>${t('na.stateDraft', { time: fmtShort(new Date(n.updated)) })}`;
    el.naDelete.hidden = !n;
    el.naDraft.textContent = t(pub ? 'na.unpublish' : 'na.saveDraft');
    el.naPublish.lastElementChild.textContent = t(pub ? 'na.update' : 'na.publish');
    renderNaCount();
  }

  function renderNaCount() {
    const len = naFields().body.value.length;
    el.naCount.textContent = `${len} / 5000`;
    el.naCount.classList.toggle('near', len > 4500);
  }

  function showNaError(msg, field) {
    el.naError.innerHTML = `<svg><use href="#i-alert"/></svg>${esc(msg)}`;
    el.naError.hidden = false;
    if (field) field.setAttribute('aria-invalid', 'true');
    announce(msg, true);
  }

  function clearNaError() {
    el.naError.hidden = true;
    na.discardUntil = 0;
    naFields().title.removeAttribute('aria-invalid');
  }

  function resetDelete() {
    clearTimeout(na.delTimer);
    el.naDelete.classList.remove('confirm');
    el.naDelete.textContent = t('na.delete');
  }

  async function saveNotice(publish) {
    const f = naFields();
    if (!f.title.value.trim()) {
      showNaError(t('na.titleRequired'), f.title);
      f.title.focus();
      return;
    }
    const n = naCurrent();
    const wasPub = !!n && validTime(n.published);
    const btn = publish ? el.naPublish : el.naDraft;
    el.naPublish.disabled = el.naDraft.disabled = true;
    btn.classList.add('loading');
    try {
      const d = await api(n ? `/api/notices/${enc(n.id)}` : '/api/notices', {
        method: n ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: f.title.value, body: f.body.value, publish }),
      });
      na.items = d.items;
      editNotice(d.notice.id);
      renderAcct();
      toast(t(publish ? (wasPub ? 'na.updated' : 'na.published') : (wasPub ? 'na.unpublished' : 'na.draftSaved')), 'ok');
      // The author has read what they just published; their own bell stays calm.
      if (publish && !wasPub) setNoticesSeen(noticeTime(d.notice));
      loadNotices();
    } catch (e) {
      if (e.status === 401) handleAuthError(e); else showNaError(e.message);
    } finally {
      el.naPublish.disabled = el.naDraft.disabled = false;
      btn.classList.remove('loading');
    }
  }

  // Deleting takes a second click within 3 seconds.
  async function deleteNotice() {
    const n = naCurrent();
    if (!n) return;
    if (!el.naDelete.classList.contains('confirm')) {
      el.naDelete.classList.add('confirm');
      el.naDelete.textContent = t('na.confirmDelete');
      na.delTimer = setTimeout(resetDelete, 3000);
      return;
    }
    resetDelete();
    try {
      na.items = (await api(`/api/notices/${enc(n.id)}`, { method: 'DELETE' })).items;
      editNotice(na.items.length ? na.items[0].id : null);
      renderAcct();
      toast(t('na.deleted'), 'ok');
      loadNotices();
    } catch (e) {
      if (e.status === 401) handleAuthError(e); else showNaError(e.message);
    }
  }

  // ---------- events: header & gallery ----------
  document.querySelectorAll('.seg [data-view]').forEach((b) => b.addEventListener('click', () => setView(b.dataset.view)));
  el.sort.addEventListener('click', toggleSort);
  el.size.addEventListener('change', () => { state.size = Number(el.size.value); store.set('size', state.size); go(1); });
  el.group.addEventListener('click', toggleGroup);
  el.selectMode.addEventListener('click', () => setSelectMode(!state.selectMode));
  el.theme.addEventListener('click', toggleTheme);
  el.lang.addEventListener('click', toggleLang);
  el.noticeBtn.addEventListener('click', openNotices);
  $('#notice-read').addEventListener('click', () => {
    if (!notices.items.length) return;
    setNoticesSeen(noticeTime(notices.items[0]));
    toast(t('notice.markedRead'), 'ok');
  });
  // Another tab marked the notices read.
  window.addEventListener('storage', (e) => {
    if (e.key !== 'mv.noticeSeen') return;
    notices.seen = Date.parse(e.newValue || '') || 0;
    renderBell();
    if (el.noticeDialog.open) renderNotices();
  });
  $('#brand').addEventListener('click', (e) => {
    e.preventDefault();
    goNewest();
  });

  el.gallery.addEventListener('click', (e) => {
    if (e.target.closest('[data-retry]')) { followHash(true); return; }
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

  // On touch screens a long press on a picture starts selecting with it, as in a phone's photo gallery.
  let press = null;
  let swallowClick = false; // the press selected; the click that ends the same gesture is not a tap
  el.gallery.addEventListener('pointerdown', (e) => {
    endPress();
    swallowClick = false;
    if (e.pointerType !== 'touch' || state.selectMode || !e.target.closest('.tile-open')) return;
    const tile = e.target.closest('.tile[data-i]');
    if (!tile) return;
    press = { x: e.clientX, y: e.clientY, timer: setTimeout(() => {
      press = null;
      const i = Number(tile.dataset.i);
      const it = state.items[i];
      if (!it || !tile.isConnected) return;
      swallowClick = true;
      setSelectMode(true);
      if (!state.selected.has(it.name) && toggleSelect(it.name)) state.lastIndex = i;
      renderSelection();
      if (navigator.vibrate) navigator.vibrate(15);
    }, 500) };
  });
  function endPress() {
    if (press) clearTimeout(press.timer);
    press = null;
  }
  el.gallery.addEventListener('pointermove', (e) => { if (press && Math.hypot(e.clientX - press.x, e.clientY - press.y) > 10) endPress(); });
  el.gallery.addEventListener('pointerup', endPress);
  el.gallery.addEventListener('pointercancel', () => { endPress(); swallowClick = false; });
  // The click that ends a long press must not toggle the picture straight back, nor the press open a context menu.
  el.gallery.addEventListener('click', (e) => {
    if (swallowClick) { swallowClick = false; e.stopImmediatePropagation(); e.preventDefault(); }
  }, true);
  el.gallery.addEventListener('contextmenu', (e) => { if (press || swallowClick) e.preventDefault(); });

  // The hero may already show a newer picture than the gallery holds; catch the gallery up first.
  async function openLatest() {
    const name = el.now.dataset.name;
    if (!name) return;
    reelStop();
    let i = state.items.findIndex((it) => it.name === name);
    // A picture entry being restored (or whose restore failed) is taken over rather than a new one added.
    const takeover = state.lbIndex < 0 && (lbHistory || (linkFailedRestore && location.hash.startsWith('#img=')));
    if (i >= 0 && takeover) lbHistory = true; // openLightbox renames the entry
    if (i < 0) {
      // Renamed to this picture and restored like a link, so a failure leaves Retry on it.
      if (takeover) {
        history.replaceState(history.state, '', imgHash(name));
        openLinked(name, true);
        return;
      }
      // On page one the hero is usually just newer than the gallery: a quiet refresh catches it up. A route taken
      // meanwhile (a day, a page) wins over this click; polling waits for it, as for any foreground load.
      if (onFirstPage()) {
        leaveRoute();
        const seq = routeSeq;
        foreground++;
        let ok;
        try { ok = await load({ silent: true, guard: () => seq === routeSeq }); } finally { foreground--; }
        if (!ok) return;
        i = state.items.findIndex((it) => it.name === name);
      }
      // Not on this page (an older reel frame, say) the picture gets an entry of its own and is restored into it like
      // a link: its page is shown under it, and closing goes back to the entry left, which shows its page again.
      if (i < 0) {
        history.pushState({ lightbox: true }, '', imgHash(name));
        openLinked(name, true);
        return;
      }
    }
    openLightbox(i, 0, el.nowImg);
  }
  $('#now-open').addEventListener('click', openLatest);
  $('#now-zoom').addEventListener('click', openLatest);

  // Paging redraws the pager; focus returns to the same control so Enter keeps paging, and the new page is announced.
  // focus names that control: 'prev', 'next', 'jump', 'current', or null when paging started elsewhere.
  async function turnPage(page, focus = null) {
    if (!(await go(page))) return;
    announce(t('pager.at', { page: fmtNum(state.page), pages: fmtNum(state.pages) }));
    const active = document.activeElement;
    if (!focus || (active && active !== document.body && !el.pager.contains(active))) return;
    const target = focus === 'jump' ? el.pager.querySelector('.jump input')
      : focus === 'current' ? null : el.pager.querySelector(`.step.${focus}:not(:disabled)`);
    const node = target && target.offsetParent ? target : el.pager.querySelector('[aria-current="page"]');
    if (node) node.focus({ preventScroll: true });
  }
  el.pager.addEventListener('click', (e) => {
    const b = e.target.closest('button[data-page]');
    if (!b || b.disabled) return;
    turnPage(Number(b.dataset.page), b.classList.contains('prev') ? 'prev' : b.classList.contains('next') ? 'next' : 'current');
  });
  el.pager.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' || !e.target.matches('.jump input')) return;
    turnPage(Math.min(Math.max(1, Number(e.target.value) || 1), state.pages), 'jump');
  });

  $('#sel-clear').addEventListener('click', clearSelection);
  $('#sel-page').addEventListener('click', () => {
    for (const it of state.items) {
      if (!state.selected.has(it.name) && !toggleSelect(it.name)) break;
    }
    renderSelection();
  });
  $('#sel-zip').addEventListener('click', async () => {
    const names = [...state.selected];
    if (!names.length) return;
    if (await (names.length === 1 ? downloadFile(`/download/${enc(names[0])}`) : downloadZip(names))) toast(t('sel.started', { n: names.length }), 'ok');
  });
  // Every download link is checked first, the smaller sizes in the detail panel included; clicks with a modifier keep
  // the browser's own handling (new tab, save as).
  document.addEventListener('click', (e) => {
    const a = e.target.closest('a[href^="/download/"]:not([data-checked]), a[download]:is([href^="/preview/"], [href^="/thumb/"]):not([data-checked])');
    if (!a || e.defaultPrevented || e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    const href = a.getAttribute('href');
    if (href === '/download/all') preflight('/download/all?check=1').then((ok) => ok && download(href));
    else downloadFile(href, a.getAttribute('download') || '');
  });

  // ---------- events: lightbox ----------
  $('#lb-close').addEventListener('click', () => closeLightbox());
  el.lbPrev.addEventListener('click', () => step(-1));
  el.lbNext.addEventListener('click', () => step(1));
  el.lbSelect.addEventListener('click', lbToggleSelect);
  el.lbZoom.addEventListener('click', () => toggleZoom());
  el.lbCompare.addEventListener('click', () => {
    cmp.on = !cmp.on;
    if (cmp.on) resetZoom();
    renderCmp();
  });
  el.cmpBar.addEventListener('click', (e) => {
    const b = e.target.closest('[data-mode]');
    if (!b) return;
    cmp.mode = b.dataset.mode;
    renderCmp();
  });
  // Dragging on the pictures moves the cut. Touches there stay there in either mode: a swipe would move the picture
  // under the overlay and change pictures.
  let cmpDrag = null;
  const cmpAt = (e) => { const r = el.cmp.getBoundingClientRect(); setCmpPos(((e.clientX - r.left) / r.width) * 100); };
  el.cmp.addEventListener('pointerdown', (e) => {
    if (cmp.mode !== 'slide' || e.button !== 0) return;
    e.preventDefault();
    cmpDrag = e.pointerId;
    el.cmp.setPointerCapture(e.pointerId);
    cmpAt(e);
    el.cmpLine.focus({ preventScroll: true });
  });
  el.cmp.addEventListener('pointermove', (e) => { if (e.pointerId === cmpDrag) cmpAt(e); });
  const endCmpDrag = (e) => { if (e.pointerId === cmpDrag) cmpDrag = null; };
  el.cmp.addEventListener('pointerup', endCmpDrag);
  el.cmp.addEventListener('pointercancel', endCmpDrag);
  el.cmp.addEventListener('touchstart', (e) => e.stopPropagation(), { passive: true });
  el.cmpLine.addEventListener('keydown', (e) => {
    const k = { ArrowLeft: -5, ArrowRight: 5, Home: -100, End: 100 }[e.key];
    if (k === undefined) return;
    e.preventDefault();
    e.stopPropagation(); // the arrows move the cut here, not to another picture
    setCmpPos(cmp.pos + (e.shiftKey ? k / 5 : k));
  });
  el.lbImg.addEventListener('load', () => { if (cmp.on) placeCmp(); });
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
    // Stepping across pages in the lightbox moved the page; keep it rather than the older entry's hash. A jump further
    // back through the history menu lands elsewhere, and hashchange shows that entry's page instead.
    if (location.hash === lbBase) writeHash();
  });

  // ---------- calendar: pictures per day, deeper = more; a day opens the page holding it ----------
  const cal = { month: null }; // first day of the month shown
  const calBtn = $('#cal-btn');
  const calBox = $('#cal');
  function monthStart(d) { return new Date(d.getFullYear(), d.getMonth(), 1); }
  function renderCal() {
    const m = cal.month;
    const first = overview.first ? monthStart(new Date(overview.first)) : m;
    const last = monthStart(new Date());
    $('#cal-title').textContent = new Intl.DateTimeFormat(i18n.locale, { year: 'numeric', month: 'long' }).format(m);
    $('#cal-prev').disabled = m <= first;
    $('#cal-next').disabled = m >= last;
    const days = new Date(m.getFullYear(), m.getMonth() + 1, 0).getDate();
    const counts = [];
    for (let d = 1; d <= days; d++) counts.push((overview.days.get(fmtDate(new Date(m.getFullYear(), m.getMonth(), d))) || { count: 0 }).count);
    const peak = Math.max(1, ...counts);
    const wd = new Intl.DateTimeFormat(i18n.locale, { weekday: 'narrow' });
    // Weeks start on Monday; 2024-01-01 was one.
    let html = [0, 1, 2, 3, 4, 5, 6].map((i) => `<span class="cal-wd" aria-hidden="true">${wd.format(new Date(2024, 0, 1 + i))}</span>`).join('');
    html += '<span></span>'.repeat((m.getDay() + 6) % 7);
    const today = fmtDate(new Date());
    counts.forEach((n, i) => {
      const d = new Date(m.getFullYear(), m.getMonth(), i + 1);
      const key = fmtDate(d);
      // The square root keeps a quiet day visible beside a busy one, as on the reel.
      const level = n ? Math.max(1, Math.ceil(Math.sqrt(n / peak) * 4)) : 0;
      const label = `${fmtDay(d)} · ${n ? t('cal.count', { n }) : t('cal.empty')}`;
      html += `<button type="button" class="cal-day l${level}${key === today ? ' today' : ''}" data-date="${key}" aria-label="${esc(label)}" title="${esc(label)}"${n ? '' : ' disabled'}>${i + 1}</button>`;
    });
    $('#cal-grid').innerHTML = html;
  }
  function openCal() {
    const top = state.items[0];
    cal.month = monthStart(top ? new Date(top.time) : new Date());
    renderCal();
    calBox.hidden = false;
    calBtn.setAttribute('aria-expanded', 'true');
    // Without overview data every day is disabled; focus the box itself so its keys stay in the calendar.
    (calBox.querySelector('.cal-day:not(:disabled), .cal-head .icon-btn:not(:disabled)') || calBox).focus();
    if (!overview.days.size) loadOverview().then(() => { if (!calBox.hidden) renderCal(); });
  }
  function closeCal(refocus = false) {
    if (calBox.hidden) return;
    calBox.hidden = true;
    calBtn.setAttribute('aria-expanded', 'false');
    if (refocus) calBtn.focus();
  }
  calBtn.addEventListener('click', () => (calBox.hidden ? openCal() : closeCal(true)));
  $('#cal-prev').addEventListener('click', () => { cal.month = new Date(cal.month.getFullYear(), cal.month.getMonth() - 1, 1); renderCal(); });
  $('#cal-next').addEventListener('click', () => { cal.month = new Date(cal.month.getFullYear(), cal.month.getMonth() + 1, 1); renderCal(); });
  $('#cal-grid').addEventListener('click', (e) => {
    const b = e.target.closest('.cal-day');
    if (!b || b.disabled) return;
    closeCal(true);
    goDate(b.dataset.date);
  });
  // Keys pressed in the calendar are its own; page shortcuts (arrows turn pages, L switches language) must not fire.
  calBox.addEventListener('keydown', (e) => {
    e.stopPropagation();
    if (e.key === 'Escape') { e.preventDefault(); closeCal(true); }
  });
  document.addEventListener('pointerdown', (e) => { if (!calBox.hidden && !e.target.closest('.cal-wrap')) closeCal(); });
  calBox.addEventListener('focusout', (e) => { if (e.relatedTarget && !e.relatedTarget.closest('.cal-wrap')) closeCal(); });

  // ---------- keyboard ----------
  function lightboxKey(e) {
    if (menuBtn && menuKey(e)) return;
    const pan = { ArrowLeft: [80, 0], ArrowRight: [-80, 0], ArrowUp: [0, 80], ArrowDown: [0, -80] }[e.key];
    const k = e.key.toLowerCase();
    if (pan && zoom.on) panBy(...pan);
    // Esc undoes one layer at a time: the zoom, then a detail sheet covering the picture, then the lightbox.
    else if (e.key === 'Escape') {
      if (zoom.on) resetZoom();
      else if (sheet.matches && el.lb.classList.contains('info-open')) {
        const inside = $('#lb-panel').contains(document.activeElement);
        setInfo(false);
        if (inside) el.lbInfo.focus();
      } else closeLightbox();
    }
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
    if (menuBtn && menuKey(e)) return;
    if (document.querySelector('dialog[open]') || (e.target instanceof Element && e.target.closest('input, select, textarea, [contenteditable]'))) return;
    const k = e.key.length === 1 ? e.key.toLowerCase() : e.key;
    if (k === 'ArrowLeft' && state.page > 1) turnPage(state.page - 1);
    else if (k === 'ArrowRight' && state.page < state.pages) turnPage(state.page + 1);
    else if (k === '1' || k === '2') setView(VIEWS[k - 1]);
    else if (k === 's') setSelectMode(!state.selectMode);
    else if (k === 't') toggleTheme();
    else if (k === 'l') toggleLang();
    else if (k === 'o') toggleSort();
    else if (k === 'g') toggleGroup();
    else if (k === 'p' && !el.reel.hidden && !el.now.hidden) reelPlay();
    else if ((k === ',' || k === '.') && !el.reel.hidden && !el.now.hidden) { nowStep(k === ',' ? -1 : 1); }
    else if ((k === '[' || k === ']') && !el.reel.hidden && !el.now.hidden) stepReelSpeed(k === '[' ? -1 : 1);
    else if (k === '?') openDialog(el.keysDialog);
    else if (k === 'n') openNotices();
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
  document.querySelectorAll('dialog [data-close]').forEach((b) => b.addEventListener('click', () => closeDialog(b.closest('dialog'))));
  $('#show-keys').addEventListener('click', () => openDialog(el.keysDialog));

  el.account.addEventListener('click', () => {
    if (state.admin) return menuBtn === el.account ? closeMenu(true) : openAcctMenu();
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
    pwToggle.setAttribute('aria-label', t(on ? 'login.hide' : 'login.show'));
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
      toast(t('login.ok'), 'ok');
      await load();
    } catch (err) {
      el.loginError.innerHTML = `<svg><use href="#i-alert"/></svg>${esc(err.message)}`;
      el.loginError.hidden = false;
      announce(err.message, true);
      replay(el.loginForm, 'shake');
      el.loginForm.elements.password.select();
    } finally {
      btn.disabled = false;
      btn.classList.remove('loading');
    }
  });

  // Focus goes back to the avatar first, so closing the section's dialog returns it there.
  const SECTIONS = { 'status-dialog': openStatus, 'schedule-dialog': openSchedule, 'notice-admin-dialog': openNoticeAdmin, 'cookie-dialog': openCookie };
  el.acctMenu.addEventListener('click', (e) => {
    const item = e.target.closest('[data-open]');
    if (item) { closeMenu(true); SECTIONS[item.dataset.open](); }
    else if (e.target.closest('#download-all')) closeMenu(true);
  });

  el.nowWarn.addEventListener('click', (e) => { if (e.target.closest('#now-warn-more')) openStatus(); });
  $('#now-more').addEventListener('click', (e) => {
    const open = el.now.querySelector('.now-card').classList.toggle('expanded');
    const b = e.currentTarget;
    b.setAttribute('aria-expanded', String(open));
    b.querySelector('span').textContent = t(open ? 'now.less' : 'now.more');
    b.querySelector('span').dataset.i18n = open ? 'now.less' : 'now.more';
  });

  el.naList.addEventListener('click', (e) => {
    const b = e.target.closest('.na-item');
    if (b && b.dataset.id !== na.id && naDiscard()) editNotice(b.dataset.id);
  });
  $('#na-new').addEventListener('click', () => {
    if (!naDiscard()) return;
    editNotice(null);
    naFields().title.focus();
  });
  el.naForm.addEventListener('submit', (e) => { e.preventDefault(); saveNotice(true); });
  // Enter in the title would submit, and so publish; move on to the body instead.
  naFields().title.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); naFields().body.focus(); }
  });
  naFields().body.addEventListener('input', renderNaCount);
  // Typing answers whatever the message asked for.
  el.naForm.addEventListener('input', () => { if (!el.naError.hidden) clearNaError(); });
  el.naDraft.addEventListener('click', () => saveNotice(false));
  el.naDelete.addEventListener('click', deleteNotice);

  $('#logout').addEventListener('click', async () => {
    closeMenu(true);
    await api('/api/logout', { method: 'POST' }).catch(() => {});
    // Guests are capped, so trim an oversized selection.
    if (state.selected.size > state.guestLimit) state.selected = new Set([...state.selected].slice(0, state.guestLimit));
    toast(t('logout.ok'), 'ok');
    await load();
  });
  el.fetchNow.addEventListener('click', async () => {
    const label = el.fetchNow.querySelector('span');
    el.fetchNow.disabled = true;
    el.fetchNow.classList.add('loading');
    label.textContent = t('status.fetching');
    try {
      const s = await api('/api/fetch', { method: 'POST' });
      renderStats(s);
      if (s.fetch.result === 'saved') { toast(t('fetch.saved'), 'ok'); await goNewest(); }
      else if (s.fetch.result === 'unchanged') toast(t('fetch.unchanged'));
      else toast(t('fetch.failed', { msg: s.fetch.message }), 'error');
    } catch (e) {
      handleAuthError(e);
    } finally {
      el.fetchNow.disabled = false;
      el.fetchNow.classList.remove('loading');
      label.textContent = t('status.fetchNow');
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
      toast(t(paused ? 'schedule.pausedToast' : 'schedule.resumedToast'), 'ok');
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
      if (t && !t.paused && validTime(t.next_run) && !backingOff(t)) {
        const wait = new Date(t.next_run) - Date.now() + 5000;
        if (wait <= 65000) { delay = Math.max(wait, 5000); awaitRun = t.next_run; }
      }
    }
    pollTimer = setTimeout(poll, delay);
  }

  let pollSeq = 0;
  async function poll() {
    clearTimeout(pollTimer);
    if (document.hidden) return; // resumed by visibilitychange
    lastPoll = Date.now();
    const seq = ++pollSeq;
    let again = false;
    try {
      const d = await api('/api/images?page=1&size=12&sort=desc');
      // Polls overlap when the network comes back mid-tick; only the newest one applies and reschedules.
      if (seq !== pollSeq) return;
      checkNotices(d.notice);
      const t = d.task;
      state.task = t;
      renderTask();
      // last_fetched is the start time of the last successful download, so an older value means the expected run has
      // not finished, or failed (last_run then reaches it with an error); give it up to 45 s. Backoff skips it.
      const since = (v) => validTime(v) && new Date(v) >= new Date(awaitRun) - 1000;
      const finished = since(t.last_fetched) || (since(t.last_run) && t.result === 'error');
      if (awaitRun && !t.paused && !finished && !backingOff(t)) {
        if (!awaitUntil) awaitUntil = Date.now() + 45000;
        again = Date.now() < awaitUntil;
      }
      if (!again) awaitUntil = 0;
      const top = d.items[0] || null;
      if (state.latest === undefined) {
        state.latest = top;
      } else if ((top && top.name) !== (state.latest && state.latest.name)) {
        state.latest = top;
        loadOverview();
        if (onFirstPage()) {
          renderNow();
          // Re-rendering would drop keyboard focus anywhere in the history section, the toolbar and pager included.
          const idle = state.lbIndex < 0 && window.scrollY < 400 && !document.querySelector('dialog[open]') &&
            !el.history.contains(document.activeElement) && !foreground;
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
  window.addEventListener('offline', () => toast(t('net.offline'), 'error'));
  window.addEventListener('online', () => { toast(t('net.online'), 'ok'); poll(); });
  setInterval(() => { if (!document.hidden) tickTimes(); }, 1000);
  loadOverview();

  $('#fresh-btn').addEventListener('click', () => { hide(el.fresh); goNewest(); });

  // ---------- layout ----------
  let scrollQueued = false;
  let histShift = 0; // px of the history toolbar hidden under the header while stuck
  function onScroll() {
    scrollQueued = false;
    const y = window.scrollY;
    // Once the history toolbar is stuck it draws the header's bottom edge, so the two read as one band.
    const stuck = el.historyBar.getBoundingClientRect().top <= el.bar.offsetHeight - histShift + 1 && y > 4;
    el.historyBar.classList.toggle('stuck', stuck);
    el.bar.classList.toggle('scrolled', y > 4 && !stuck);
    el.toTop.classList.toggle('show', y > 800);
  }
  window.addEventListener('scroll', () => {
    if (scrollQueued) return;
    scrollQueued = true;
    requestAnimationFrame(onScroll);
  }, { passive: true });
  el.toTop.addEventListener('click', scrollToTop);

  // Sticky day headers and the "new images" pill sit just below the header and the history toolbar, whose heights wrap.
  // When the toolbar's heading wraps onto its own row, that row slides under the header and only the controls stay.
  function measureBars() {
    const root = document.documentElement.style;
    root.setProperty('--bar-h', el.bar.offsetHeight + 'px');
    const head = el.historyBar.firstElementChild;
    const controls = el.historyBar.lastElementChild;
    histShift = controls.offsetTop >= head.offsetTop + head.offsetHeight ? controls.offsetTop - 4 : 0;
    root.setProperty('--hist-h', el.historyBar.offsetHeight + 'px');
    root.setProperty('--hist-shift', histShift + 'px');
    onScroll();
  }
  if ('ResizeObserver' in window) {
    const ro = new ResizeObserver(measureBars);
    ro.observe(el.bar);
    ro.observe(el.historyBar);
  }

  let resizeTimer;
  window.addEventListener('resize', () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => {
      if (state.lbIndex >= 0) fitImage();
      if (zoom.on) { clampPan(); applyZoom(); }
    }, 150);
  });
  // Back and Forward between pages, or a link pasted into the address bar.
  window.addEventListener('hashchange', () => {
    leaveRoute();
    const before = state.page;
    const link = readHash();
    if (link.img) { if (state.lbIndex < 0) openLinked(link.img, true); }
    else if (link.date) goDate(link.date, false);
    else if (state.page !== before || state.date) { state.date = null; load({ scroll: true }); }
  });
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', renderTheme);
  motion.addEventListener('change', stillMascots);

  i18n.apply();
  stillMascots();
  renderControls();
  renderTheme();
  onScroll();
  followHash();
})();
