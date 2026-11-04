(() => {
  'use strict';

  const { i18n } = window;
  const t = (key, params) => i18n.t(key, params);
  const $ = (selector, root = document) => root.querySelector(selector);
  const esc = (value) => String(value ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
  // DOM helper for page renderers. Use textContent/children for all untrusted text.
  function el(tag, attrs = {}, ...children) {
    const node = ['svg', 'use'].includes(tag) ? document.createElementNS('http://www.w3.org/2000/svg', tag) : document.createElement(tag);
    for (const [key, value] of Object.entries(attrs)) {
      if (key === 'className' || key === 'textContent' || key === 'hidden' || key === 'disabled') node[key] = value;
      else node.setAttribute(key, value);
    }
    node.append(...children.flat().filter((child) => child != null));
    return node;
  }
  const state = { admin: false, route: '', generation: 0 };
  let historyIndex = Number.isInteger(history.state?.mvAdminIndex) ? history.state.mvAdminIndex : 0;
  history.replaceState({ ...history.state, mvAdminIndex: historyIndex }, '');
  let unsavedGuard = () => false;
  let toastTimer;
  let confirmPending = null;
  const setUnsavedGuard = (fn = () => false) => { unsavedGuard = fn; };
  const guardUnsaved = async () => !unsavedGuard() || await confirmDialog(t('admin.unsavedConfirm'));
  const reducedMotion = () => matchMedia('(prefers-reduced-motion: reduce)').matches;
  const TOAST_ICON = { info: '#i-info', ok: '#i-ok', error: '#i-alert' };
  // The gallery toast, plus a separate live region because a node that was hidden is not reliably announced.
  function toast(message, kind = 'info') {
    const node = $('#admin-toast');
    clearTimeout(toastTimer);
    node.className = `toast ${kind}`;
    $('use', node).setAttribute('href', TOAST_ICON[kind] || TOAST_ICON.info);
    $('span', node).textContent = message;
    // Replay the entrance when one toast replaces another.
    node.hidden = true; void node.offsetWidth; node.hidden = false;
    $('#admin-status').textContent = message;
    toastTimer = setTimeout(() => {
      node.classList.add('leaving');
      toastTimer = setTimeout(() => { node.hidden = true; node.classList.remove('leaving'); }, reducedMotion() ? 0 : 200);
    }, kind === 'error' ? 7000 : 4000);
  }
  function finishConfirm(value = 'cancel') {
    const dialog = $('#admin-confirm');
    if (dialog.open) dialog.close(value);
    const resolve = confirmPending;
    confirmPending = null;
    if (resolve) resolve(value === 'confirm');
  }
  function confirmDialog(message, requiredName, { label = t('admin.confirm'), danger = false } = {}) {
    if (confirmPending) return Promise.resolve(false);
    $('#confirm-message').textContent = message;
    const entry = $('#confirm-name');
    const typed = $('#confirm-name-input');
    const button = $('#admin-confirm [value=confirm]');
    button.textContent = label;
    button.className = `btn ${danger ? 'ghost danger' : 'primary'}`;
    entry.hidden = requiredName === undefined;
    typed.value = '';
    button.disabled = requiredName !== undefined;
    typed.oninput = () => { button.disabled = typed.value !== requiredName; };
    return new Promise((resolve) => {
      confirmPending = resolve;
      $('#admin-confirm').showModal();
      if (requiredName !== undefined) typed.focus();
    });
  }
  $('#admin-confirm form').addEventListener('submit', (event) => {
    event.preventDefault();
    finishConfirm(event.submitter?.value);
  });
  $('#admin-confirm').addEventListener('cancel', (event) => { event.preventDefault(); finishConfirm(); });
  async function api(path, options = {}) {
    let res;
    try { res = await fetch(path, { credentials: 'same-origin', ...options }); }
    catch (error) { throw new Error(t('admin.loadFailed')); }
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      const code = body.code || body.error?.code;
      const key = `err.${code}`;
      const message = code && t(key) !== key ? t(key) : (typeof body.error === 'string' ? body.error : body.error?.message) || body.message || t('admin.loadFailed');
      if (res.status === 401 && path !== '/api/login') showLogin(state.admin ? t('login.expired') : '');
      throw Object.assign(new Error(message), { status: res.status, code });
    }
    return body;
  }
  const json = (method, body) => ({ method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  const fmtNum = (n, digits = 0) => Number(n || 0).toLocaleString(i18n.locale, { maximumFractionDigits: digits });
  function bytes(n = 0) {
    if (n < 1024) return `${fmtNum(n)} B`;
    if (n < 1024 ** 2) return `${fmtNum(n / 1024)} KB`;
    if (n < 1024 ** 3) return `${fmtNum(n / 1024 ** 2, n < 10 * 1024 ** 2 ? 2 : 1)} MB`;
    return `${fmtNum(n / 1024 ** 3, 1)} GB`;
  }
  const validTime = (v) => !!v && !Number.isNaN(new Date(v).getTime()) && new Date(v).getFullYear() > 1;
  const dateTime = (v) => validTime(v) ? new Date(v).toLocaleString(i18n.locale, { dateStyle: 'medium', timeStyle: 'short' }) : t('status.none');
  function relTime(value) {
    if (!validTime(value)) return t('status.none');
    const s = (Date.now() - new Date(value).getTime()) / 1000;
    if (s < 0) return dateTime(value);
    return s < 60 ? t('ago.now') : s < 3600 ? t('ago.minutes', { n: Math.floor(s / 60) })
      : s < 86400 ? t('ago.hours', { n: Math.floor(s / 3600) }) : t('ago.days', { n: Math.floor(s / 86400) });
  }
  function tokenRow(tok) {
    if (!tok || !validTime(tok.expires)) return '';
    const days = Math.floor((new Date(tok.expires) - Date.now()) / 86400000);
    const left = `<span class="${days < 7 ? 'bad' : 'ok'}">${esc(t(days < 0 ? 'token.expired' : 'token.daysLeft', { n: days }))}</span>`;
    return `<dt>${esc(t('token.label'))}</dt><dd>${t('token.value', { cookie: esc(tok.cookie), time: esc(dateTime(tok.expires)), left })}</dd>`;
  }
  function cookieRow(cookie) {
    if (!cookie) return '';
    return `<dt>${esc(t('cookie.label'))}</dt><dd>${esc(cookie.source === 'page' ? t('cookie.fromPage', { time: dateTime(cookie.updated) }) : t('cookie.fromNone'))}</dd>`;
  }
  function diskRow(disk) {
    if (!disk?.total) return `<dt>${esc(t('disk.label'))}</dt><dd>${esc(t('admin.unknown'))}</dd>`;
    const pct = Math.max(0, Math.min(100, Math.round((1 - disk.free / disk.total) * 100)));
    return `<dt>${esc(t('disk.label'))}</dt><dd>${meter(pct, 100, t('disk.label'), pct >= 90)}${esc(t('disk.value', { pct, archive: bytes(disk.archive), free: bytes(disk.free) }))}</dd>`;
  }
  const icon = (id) => `<svg aria-hidden="true"><use href="#${id}"/></svg>`;
  function meter(done, total, label, warn = false) {
    const pct = Math.max(0, Math.min(100, Math.round((Number(done) || 0) / Math.max(1, Number(total) || 0) * 100)));
    return `<span class="meter${warn ? ' bad' : ''}" role="progressbar" aria-label="${esc(label)}" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${pct}"><span style="width:${pct}%"></span></span>`;
  }
  // Shimmering placeholders; they fade in late so quick loads never flash them.
  function loading(container) {
    container.replaceChildren(el('div', { className: 'admin-skeleton', role: 'status' }, el('span', { className: 'sr-only', textContent: t('admin.loading') }), el('i'), el('i'), el('i')));
  }
  // A floating mascot over one short line, like the gallery's empty page.
  const stateHTML = (mascot, text, extra = '') => `<div class="admin-state"><svg class="mascot" aria-hidden="true"><use href="#${mascot}"/></svg><p>${esc(text)}</p>${extra}</div>`;
  const emptyHTML = (text, extra) => stateHTML('mascot-sleep', text, extra);
  function showError(container, error, retry) {
    container.innerHTML = stateHTML('mascot-oops', error.message || t('admin.loadFailed'), `<button type="button" class="btn ghost">${icon('i-refresh')}<span>${esc(t('admin.retry'))}</span></button>`);
    const node = container.firstElementChild;
    node.classList.add('admin-error'); node.setAttribute('role', 'alert');
    $('button', node).addEventListener('click', retry);
  }
  function busy(button, on) { button.disabled = on; button.classList.toggle('loading', on); }
  const getOverview = () => api('/api/admin/overview');
  const panel = (title, html) => `<section class="admin-panel"><h2>${esc(t(title))}</h2>${html}</section>`;
  const stats = (html) => `<dl class="stats">${html}</dl>`;
  const stat = (key, value) => `<dt>${esc(t(key))}</dt><dd>${esc(value)}</dd>`;
  function boardState(board) {
    const value = ['syncing', 'paused', 'pending', 'ended'].includes(board.state) ? board.state : board.paused ? 'paused' : 'syncing';
    return `<span class="na-tag state-${value}${value === 'syncing' ? ' on' : ''}">${esc(t(`admin.${value}`))}</span>`;
  }
  async function renderOverview(container, route, context) {
    const data = context.overview || await getOverview();
    if (!context.isCurrent()) return;
    const boards = data.boards || [];
    const cards = [
      ['admin.activeBoards', fmtNum(boards.filter((b) => b.lifecycle === 'active').length)],
      ['admin.images', fmtNum(boards.reduce((n, b) => n + b.count, 0))],
      ['admin.originalBytes', bytes(boards.reduce((n, b) => n + b.bytes, 0))],
      ['admin.archiveCount', fmtNum(data.archives?.count), bytes(data.archives?.bytes)],
      ['admin.freeDisk', data.disk ? bytes(data.disk.free) : t('admin.unknown')],
    ];
    container.innerHTML = `<div class="admin-cards">${cards.map(([key, value, sub]) => `<dl class="admin-card"><dt>${esc(t(key))}</dt><dd>${esc(value)}${sub ? `<small>${esc(sub)}</small>` : ''}</dd></dl>`).join('')}</div>`;
    const table = boards.length ? `<div class="admin-table-wrap"><table class="admin-table"><thead><tr>${['admin.boardName', 'admin.state', 'admin.images', 'admin.latest', 'admin.nextRun'].map((k) => `<th scope="col">${esc(t(k))}</th>`).join('')}</tr></thead><tbody>${boards.map((b) => `<tr><td><a href="#boards/${encodeURIComponent(b.id)}">${esc(b.name)}</a></td><td>${boardState(b)}</td><td>${fmtNum(b.count)}</td><td title="${esc(dateTime(b.latest))}">${esc(relTime(b.latest))}</td><td>${esc(b.state === 'syncing' && !b.paused ? dateTime(b.next_run) : '—')}</td></tr>`).join('')}</tbody></table></div>` : emptyHTML(t('admin.emptyBoards'));
    container.insertAdjacentHTML('beforeend', panel('admin.boards', table) + panel('admin.sharedCredentials', stats(cookieRow(data.shared_cookie) + tokenRow(data.shared_token))));
    if (data.maintenance) {
      const op = data.maintenance;
      const label = (v) => t(`admin.operation.${v}`) !== `admin.operation.${v}` ? t(`admin.operation.${v}`) : t('admin.operation.working');
      container.insertAdjacentHTML('beforeend', panel('admin.maintenance', `<div class="admin-maintenance"><p>${esc(label(op.kind))} · ${esc(boards.find((b) => b.id === op.board_id)?.name || op.board_id)} · ${esc(label(op.phase))}</p>${meter(op.done, op.total, t('admin.maintenance'))}<p>${esc(t('admin.maintenanceProgress', { done: fmtNum(op.done), total: fmtNum(op.total) }))}</p>${op.error ? `<p class="form-error">${esc(op.error)}</p>` : ''}</div>`));
      // Follow the running operation, including one started from another tab; polling ends once it has finished.
      const again = () => renderOverview(container, route, { ...context, overview: null }).catch(() => { if (context.isCurrent()) routeTimer(context, again, 1000); });
      routeTimer(context, again, 1000);
    }
  }
  function historyHTML(runs, showBoard = true) {
    return runs.length ? `<ol class="runs-list admin-history${showBoard ? '' : ' admin-history-no-board'}">${runs.map((r) => {
          const key = `fail.${r.code}`;
          const result = r.result === 'saved' ? t('status.saved') : r.result === 'unchanged' ? t('status.unchanged') : t(key) !== key ? t(key) : t('fail.other');
          const meta = [r.http ? `HTTP ${Number(r.http)}` : '', r.bytes ? bytes(r.bytes) : '', t('runs.took', { s: fmtNum(r.ms / 1000, 1) })].filter(Boolean).join(' · ');
          return `<li class="run ${r.result === 'saved' ? 'saved' : r.result === 'error' ? 'error' : ''}" title="${esc(r.message || '')}"><time datetime="${esc(r.time)}">${esc(dateTime(r.time))}</time>${showBoard ? `<a class="admin-run-board" href="#boards/${encodeURIComponent(r.board_id)}">${esc(r.board_name)}</a>` : ''}<span class="run-what">${esc(result)}${r.manual ? ` <em>${esc(t('runs.manual'))}</em>` : ''}</span><span class="run-meta">${esc(meta)}</span></li>`;
        }).join('')}</ol>` : emptyHTML(t('admin.emptyHistory'));
  }
  async function renderHistory(container, route, context) {
    const overview = await getOverview();
    if (!context.isCurrent()) return;
    const select = el('select', { 'aria-label': t('admin.boardFilter') }, el('option', { value: '', textContent: t('admin.allBoards') }), ...(overview.boards || []).map((b) => el('option', { value: b.id, textContent: b.name })));
    const list = el('div', { className: 'admin-panel' });
    container.replaceChildren(el('label', { className: 'admin-filter' }, t('admin.boardFilter'), select), list);
    let request = 0;
    async function loadRuns() {
      const seq = ++request;
      loading(list);
      try {
        const data = await api('/api/admin/history' + (select.value ? `?board=${encodeURIComponent(select.value)}` : ''));
        if (!context.isCurrent() || seq !== request) return;
        list.innerHTML = historyHTML(data.items || []);
      } catch (error) { if (context.isCurrent() && seq === request) showError(list, error, loadRuns); }
    }
    select.addEventListener('change', loadRuns);
    await loadRuns();
  }
  async function renderCookie(container, route, context) {
    let cookie = await api('/api/cookie');
    if (!context.isCurrent()) return;
    container.innerHTML = `<section class="admin-panel"><p class="admin-hint">${esc(t('admin.cookieSharedHint'))}</p><dl class="stats" id="cookie-state"></dl><form class="admin-form" id="cookie-form"><label><span>${esc(t('cookie.field'))}</span><textarea class="admin-cookie" name="cookie" rows="5" maxlength="8192" spellcheck="false" autocomplete="off" required aria-describedby="cookie-error cookie-hint"></textarea></label><p class="admin-hint" id="cookie-hint">${esc(t('cookie.hint'))}</p><p class="form-error" id="cookie-error" role="alert" hidden></p><div class="modal-actions spread"><button type="button" class="btn ghost danger" id="cookie-clear">${esc(t('admin.cookieClear'))}</button><button class="btn primary" type="submit"><span class="spinner" aria-hidden="true"></span>${icon('i-check')}<span>${esc(t('cookie.save'))}</span></button></div></form></section>`;
    const form = $('#cookie-form', container);
    const input = form.elements.cookie;
    const save = $('[type=submit]', form);
    const clear = $('#cookie-clear', form);
    const error = $('#cookie-error', form);
    const renderState = () => { $('#cookie-state', container).innerHTML = cookieRow(cookie) + tokenRow(cookie.token); clear.hidden = cookie.source !== 'page'; };
    renderState();
    setUnsavedGuard(() => !!input.value);
    function showCookieError(message) { error.textContent = message; error.hidden = !message; input.toggleAttribute('aria-invalid', !!message); }
    input.addEventListener('input', () => showCookieError(''));
    async function update(remove) {
      if (save.disabled || clear.disabled) return;
      if (remove && !await confirmDialog(t('admin.cookieClearConfirm'))) return;
      if (!context.isCurrent()) return;
      if (!remove && !input.value.trim()) { showCookieError(t('err.invalid_cookie')); input.focus(); return; }
      busy(save, true); clear.disabled = true; input.disabled = true; showCookieError('');
      try {
        cookie = await api('/api/cookie', remove ? { method: 'DELETE' } : json('PUT', { cookie: input.value.trim() }));
        if (!context.isCurrent()) return;
        input.value = ''; renderState(); toast(t(remove ? 'admin.cookieCleared' : 'cookie.saved'), 'ok');
      } catch (err) { if (context.isCurrent()) showCookieError(err.message); }
      finally { busy(save, false); clear.disabled = false; input.disabled = false; }
    }
    form.addEventListener('submit', (event) => { event.preventDefault(); update(false); });
    clear.addEventListener('click', () => update(true));
  }
  async function renderSystem(container, route, context) {
    const data = await getOverview();
    if (!context.isCurrent()) return;
    container.innerHTML = `<div class="admin-system"><section class="admin-panel"><h2 class="admin-title-row">${esc(t('admin.notify'))}<span class="na-tag${data.notify ? ' on' : ''}">${esc(t(data.notify ? 'admin.enabled' : 'admin.disabled'))}</span></h2><button type="button" class="btn ghost" id="notify-test" ${data.notify ? '' : 'disabled'}><span class="spinner" aria-hidden="true"></span>${icon('i-send')}<span>${esc(t('notify.test'))}</span></button><p class="form-error" id="notify-result" role="alert" hidden></p></section>`
      + panel('admin.schedule', `<code class="admin-code">${esc(data.schedule)}</code><p class="admin-hint">${esc(t('admin.scheduleHint'))}</p>`)
      + `<section class="admin-panel admin-wide"><h2>${esc(t('admin.storage'))}</h2>${stats(diskRow(data.disk) + stat('admin.version', data.version || t('status.none')))}</section></div>`;
    const button = $('#notify-test', container);
    button.addEventListener('click', async () => {
      busy(button, true);
      const result = $('#notify-result', container); result.hidden = true;
      try { await api('/api/notify/test', { method: 'POST' }); if (context.isCurrent()) toast(t('notify.sent'), 'ok'); }
      catch (error) { if (context.isCurrent()) { result.textContent = error.message; result.hidden = false; } }
      finally { busy(button, false); }
    });
  }

  async function renderNotices(container, route, context) {
    const state = {
      items: [],
      id: null,
      base: { title: '', body: '' },
      busy: false,
    };

    const current = () => context.isCurrent();
    const notice = () => state.items.find((item) => item.id === state.id) || null;
    const fmtShort = dateTime;

    let data;
    try {
      data = await api('/api/notices/all');
    } catch (error) {
      if (current()) showError(container, error, () => { loading(container); renderNotices(container, route, context); });
      return;
    }
    if (!current()) return;
    state.items = Array.isArray(data.items) ? data.items : [];

    const newButton = el('button', { type: 'button', className: 'btn ghost' },
      el('svg', { 'aria-hidden': 'true' }, el('use', { href: '#i-plus' })),
      el('span', { textContent: t('na.new') }));
    const list = el('ul', { className: 'na-list', 'aria-label': t('na.list') });
    const side = el('div', { className: 'na-side' }, newButton, list);

    const stateLine = el('p', { className: 'na-state' });
    const titleInput = el('input', {
      name: 'title', maxlength: '100', autocomplete: 'off', required: '', 'aria-describedby': 'na-error',
    });
    const bodyInput = el('textarea', {
      name: 'body', maxlength: '5000', rows: '9', placeholder: t('na.placeholder'), 'aria-describedby': 'na-error',
    });
    const count = el('p', { className: 'na-count' });
    const errorLine = el('p', { className: 'form-error', id: 'na-error', role: 'alert', hidden: true });
    const deleteButton = el('button', { type: 'button', className: 'btn ghost danger', textContent: t('na.delete') });
    const draftButton = el('button', { type: 'button', className: 'btn ghost', textContent: t('na.saveDraft') });
    const publishText = el('span', { textContent: t('na.publish') });
    const publishButton = el('button', { type: 'submit', className: 'btn primary' },
      el('span', { className: 'spinner', 'aria-hidden': 'true' }), publishText);
    const form = el('form', { className: 'na-edit', novalidate: '' },
      stateLine,
      el('label', {}, t('na.fieldTitle'), titleInput),
      el('label', {}, t('na.fieldBody'), bodyInput),
      count,
      errorLine,
      el('div', { className: 'modal-actions spread' }, deleteButton,
        el('div', { className: 'row' }, draftButton, publishButton)));

    container.replaceChildren(el('div', { className: 'admin-panel' }, el('div', { className: 'na' }, side, form)));

    const dirty = () => titleInput.value !== state.base.title || bodyInput.value !== state.base.body;
    setUnsavedGuard(dirty);

    function showFormError(message, field) {
      errorLine.textContent = message;
      errorLine.hidden = false;
      if (field) field.setAttribute('aria-invalid', 'true');
    }

    function clearError() {
      errorLine.hidden = true;
      errorLine.textContent = '';
      titleInput.removeAttribute('aria-invalid');
      bodyInput.removeAttribute('aria-invalid');
    }

    function renderCount() {
      count.textContent = t('admin.noticeCount', { n: fmtNum(bodyInput.value.length), max: fmtNum(5000) });
      count.classList.toggle('near', bodyInput.value.length > 4500);
    }

    function renderList() {
      list.replaceChildren();
      if (!state.items.length) {
        list.append(el('li', { className: 'na-empty', textContent: t('na.empty') }));
        return;
      }
      for (const item of state.items) {
        const published = validTime(item.published);
        const button = el('button', { type: 'button', className: 'na-item', 'data-id': item.id },
          el('strong', { textContent: item.title }),
          el('span', {},
            el('span', { className: `na-tag${published ? ' on' : ''}`, textContent: t(published ? 'na.tagPublished' : 'na.tagDraft') }),
            fmtShort(published ? item.published : item.updated)));
        if (item.id === state.id) button.setAttribute('aria-current', 'true');
        button.addEventListener('click', () => selectNotice(item.id));
        list.append(el('li', {}, button));
      }
    }

    function renderForm() {
      const item = notice();
      const published = item && validTime(item.published);
      stateLine.replaceChildren();
      if (!item) {
        stateLine.textContent = t('na.stateNew');
      } else {
        stateLine.append(
          el('span', { className: `na-tag${published ? ' on' : ''}`, textContent: t(published ? 'na.tagPublished' : 'na.tagDraft') }),
          t(published ? 'na.statePublished' : 'na.stateDraft', { time: fmtShort(published ? item.published : item.updated) }),
        );
      }
      deleteButton.hidden = !item;
      draftButton.textContent = t(published ? 'na.unpublish' : 'na.saveDraft');
      publishText.textContent = t(published ? 'na.update' : 'na.publish');
      renderCount();
    }

    function editNotice(id) {
      const item = state.items.find((entry) => entry.id === id) || null;
      state.id = item ? item.id : null;
      state.base = { title: item ? item.title : '', body: item ? item.body : '' };
      titleInput.value = state.base.title;
      bodyInput.value = state.base.body;
      clearError();
      renderForm();
      renderList();
    }

    async function selectNotice(id) {
      if (state.busy || id === state.id) return;
      if (dirty() && !await confirmDialog(t('admin.unsavedConfirm'))) return;
      if (!current()) return;
      editNotice(id);
    }

    function setBusy(on, button) {
      state.busy = on;
      titleInput.disabled = on;
      bodyInput.disabled = on;
      newButton.disabled = on;
      list.querySelectorAll('button').forEach((button) => { button.disabled = on; });
      deleteButton.disabled = on;
      draftButton.disabled = on;
      publishButton.disabled = on;
      if (button) button.classList.toggle('loading', on);
    }

    async function save(publish) {
      if (state.busy) return;
      if (!titleInput.value.trim()) {
        showFormError(t('na.titleRequired'), titleInput);
        titleInput.focus();
        return;
      }
      const item = notice();
      const wasPublished = !!item && validTime(item.published);
      const button = publish ? publishButton : draftButton;
      clearError();
      setBusy(true, button);
      try {
        const data = await api(item ? `/api/notices/${encodeURIComponent(item.id)}` : '/api/notices', {
          method: item ? 'PUT' : 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ title: titleInput.value, body: bodyInput.value, publish }),
        });
        if (!current()) return;
        state.items = Array.isArray(data.items) ? data.items : [];
        editNotice(data.notice.id);
        toast(t(publish
          ? (wasPublished ? 'na.updated' : 'na.published')
          : (wasPublished ? 'na.unpublished' : 'na.draftSaved')), 'ok');
      } catch (error) {
        if (current()) showFormError(error.message || t('admin.saveFailed'), ['title_required', 'title_too_long'].includes(error.code) ? titleInput : error.code === 'body_too_long' ? bodyInput : null);
      } finally {
        if (current()) setBusy(false, button);
      }
    }

    async function remove() {
      const item = notice();
      if (!item || state.busy || !await confirmDialog(t('admin.deleteNoticeConfirm', { title: item.title }))) return;
      if (!current()) return;
      setBusy(true, deleteButton);
      try {
        const data = await api(`/api/notices/${encodeURIComponent(item.id)}`, { method: 'DELETE' });
        if (!current()) return;
        state.items = Array.isArray(data.items) ? data.items : [];
        editNotice(state.items.length ? state.items[0].id : null);
        toast(t('na.deleted'), 'ok');
      } catch (error) {
        if (current()) showFormError(error.message || t('admin.deleteFailed'));
      } finally {
        if (current()) setBusy(false, deleteButton);
      }
    }

    newButton.addEventListener('click', async () => {
      if (state.busy || state.id === null && !dirty()) return;
      if (dirty() && !await confirmDialog(t('admin.unsavedConfirm'))) return;
      if (current()) editNotice(null);
    });
    titleInput.addEventListener('input', clearError);
    bodyInput.addEventListener('input', () => { clearError(); renderCount(); });
    draftButton.addEventListener('click', () => save(false));
    deleteButton.addEventListener('click', remove);
    form.addEventListener('submit', (event) => { event.preventDefault(); save(true); });

    editNotice(state.items.length ? state.items[0].id : null);
    if (!state.id) titleInput.focus();
  }

  const boardURL = (id, tab = '') => `#boards/${encodeURIComponent(id)}${tab ? `/${tab}` : ''}`;
  const operationRunning = (op) => !!op && !validTime(op.finished);
  const operationLabel = (value) => {
    const key = `admin.operation.${value}`;
    return t(key) === key ? t('admin.operation.working') : t(key);
  };
  function operationHTML(op) {
    return `<div class="admin-operation${operationRunning(op) ? ' running' : ''}" role="status"><p>${esc(operationLabel(op.kind))} · ${esc(operationLabel(op.phase))}</p>${meter(op.done, op.total, t('admin.maintenance'))}<p>${esc(t('admin.maintenanceProgress', { done: fmtNum(op.done), total: fmtNum(op.total) }))}</p>${op.error ? `<p class="form-error">${esc(op.error)}</p>` : ''}</div>`;
  }
  function routeTimer(context, fn, delay) {
    const timer = setTimeout(() => { removeCleanup(); if (context.isCurrent()) fn(); }, delay);
    const removeCleanup = context.onCleanup(() => clearTimeout(timer));
  }
  function localDate(value) {
    if (!validTime(value)) return '';
    const d = new Date(value);
    const pad = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  }
  function renderBoardSettings(container, board, overview, context, onSaved) {
    const isNew = !board;
    container.innerHTML = `<section class="admin-panel"><form class="admin-form admin-board-settings" novalidate>
      <label>${esc(t('admin.board.name'))}<input name="name" required maxlength="60" autocomplete="off" aria-describedby="board-name-error"></label><p class="form-error" id="board-name-error" role="alert" hidden></p>
      <label>${esc(t('admin.board.url'))}<input name="url" type="url" required placeholder="https://magma.com/…" aria-describedby="board-url-error"></label><p class="form-error" id="board-url-error" role="alert" hidden></p>
      <fieldset class="admin-group"><legend>${esc(t('admin.board.frequency'))}</legend>
        <div class="admin-choices"><label class="admin-choice"><input type="radio" name="schedule_mode" value="global">${esc(t('admin.board.followGlobal', { schedule: overview.schedule || '—' }))}</label>
        <label class="admin-choice"><input type="radio" name="schedule_mode" value="custom">${esc(t('admin.board.custom'))}</label></div>
        <input name="schedule" id="board-schedule-custom" placeholder="*/10 * * * * / @every 5m" aria-label="${esc(t('schedule.plan'))}" aria-describedby="board-schedule-error"><p class="form-error" id="board-schedule-error" role="alert" hidden></p>
      </fieldset>
      <fieldset class="admin-group"><legend>${esc(t('admin.board.window'))}</legend><div class="admin-window">${['sync_from', 'sync_until'].map((name) => `<div><label class="admin-sublabel" for="board-${name}">${esc(t(name === 'sync_from' ? 'admin.board.start' : 'admin.board.end'))}</label><div class="admin-input-clear"><input id="board-${name}" name="${name}" type="datetime-local" step="1" aria-describedby="board-window-error board-window-hint"><button type="button" class="btn ghost" data-clear="${name}">${esc(t('admin.board.clear'))}</button></div></div>`).join('')}</div><p class="admin-hint" id="board-window-hint" aria-live="polite"></p><p class="form-error" id="board-window-error" role="alert" hidden></p></fieldset>
      <div class="admin-group"><label class="admin-choice"><input name="visible" type="checkbox" aria-describedby="board-visible-hint">${esc(t('admin.board.visible'))}</label><p class="admin-hint" id="board-visible-hint">${esc(t('admin.board.hiddenHint'))}</p></div>
      <div class="admin-group"><label for="board-sort">${esc(t('admin.board.sort'))}</label><div class="admin-inline"><input id="board-sort" name="sort" type="number" min="-100000" max="100000" step="1" required aria-describedby="board-sort-hint board-sort-error"><p class="admin-hint" id="board-sort-hint">${esc(t('admin.board.sortHint'))}</p></div><p class="form-error" id="board-sort-error" role="alert" hidden></p></div>
      <p class="form-error" id="board-save-error" role="alert" hidden></p><div class="modal-actions"><button type="submit" class="btn primary"><span class="spinner" aria-hidden="true"></span>${icon(isNew ? 'i-plus' : 'i-check')}<span>${esc(t(isNew ? 'admin.board.create' : 'admin.board.save'))}</span></button></div>
    </form></section>`;
    const form = $('form', container), f = form.elements, save = $('[type=submit]', form);
    let saving = false;
    let savedWindow;
    const snapshot = () => JSON.stringify([f.name.value, f.url.value, f.schedule_mode.value, f.schedule.value, f.sync_from.value, f.sync_until.value, f.visible.checked, f.sort.value]);
    function assign(value) {
      f.name.value = value?.name || ''; f.url.value = value?.url || '';
      f.schedule_mode.value = value?.schedule ? 'custom' : 'global'; f.schedule.value = value?.schedule || '';
      f.sync_from.value = localDate(value?.sync_from); f.sync_until.value = localDate(value?.sync_until);
      savedWindow = Object.fromEntries(['sync_from', 'sync_until'].map((key) => [key, { local: f[key].value, original: value?.[key] }]));
      f.visible.checked = !value?.hidden; f.sort.value = value?.sort || 0;
    }
    function windowValue(key) {
      const value = f[key].value;
      if (!value) return null;
      return value === savedWindow[key].local ? savedWindow[key].original : new Date(value).toISOString();
    }
    assign(board);
    let baseline = snapshot();
    const dirty = () => snapshot() !== baseline;
    function fieldError(field, message) {
      const node = $(`#board-${field}-error`, form);
      node.textContent = message; node.hidden = !message;
      const input = field === 'window' ? f.sync_from : f[field];
      if (field === 'schedule') { f.schedule_mode.value = 'custom'; update(); }
      if (input) { input.setAttribute('aria-invalid', 'true'); input.focus(); }
    }
    function clearErrors() {
      form.querySelectorAll('.form-error[role=alert]').forEach((node) => { node.hidden = true; node.textContent = ''; });
      form.querySelectorAll('[aria-invalid]').forEach((node) => node.removeAttribute('aria-invalid'));
    }
    function update() {
      const custom = f.schedule_mode.value === 'custom';
      $('#board-schedule-custom', form).hidden = !custom; f.schedule.disabled = !custom;
      const from = windowValue('sync_from'), until = windowValue('sync_until');
      const invalid = from && until && new Date(from) >= new Date(until);
      const hint = $('#board-window-hint', form);
      hint.textContent = invalid ? t('err.invalid_window') : `${from ? t(new Date(from) <= new Date() ? 'admin.board.startedAt' : 'admin.board.startsAt', { time: dateTime(from) }) : t('admin.board.startsNow')} · ${until ? t(new Date(until) <= new Date() ? 'admin.board.endedAt' : 'admin.board.endsAt', { time: dateTime(until) }) : t('admin.board.noEnd')} · ${Intl.DateTimeFormat().resolvedOptions().timeZone}`;
      hint.classList.toggle('form-error', !!invalid);
      save.disabled = saving || (!isNew && !dirty());
    }
    form.addEventListener('input', () => { clearErrors(); update(); });
    form.addEventListener('change', () => { clearErrors(); update(); });
    form.querySelectorAll('[data-clear]').forEach((button) => button.addEventListener('click', () => { f[button.dataset.clear].value = ''; clearErrors(); update(); }));
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      if (save.disabled) return;
      clearErrors();
      const name = f.name.value.trim(), url = f.url.value.trim();
      if (!name || [...name].length > 60) return fieldError('name', t(!name ? 'err.name_required' : 'err.name_too_long'));
      try { const parsed = new URL(url); if (!['http:', 'https:'].includes(parsed.protocol) || !parsed.hostname) throw new Error(); }
      catch { return fieldError('url', t('err.invalid_url')); }
      if (f.schedule_mode.value === 'custom' && !f.schedule.value.trim()) return fieldError('schedule', t('err.invalid_schedule'));
      const from = windowValue('sync_from'), until = windowValue('sync_until');
      if (f.sync_from.validity.badInput || f.sync_until.validity.badInput || (from && until && new Date(from) >= new Date(until))) return fieldError('window', t('err.invalid_window'));
      const sort = Number(f.sort.value);
      if (!f.sort.value || !Number.isInteger(sort) || sort < -100000 || sort > 100000) return fieldError('sort', t('err.invalid_sort'));
      const payload = { name, url, schedule: f.schedule_mode.value === 'custom' ? f.schedule.value.trim() : '', sync_from: from, sync_until: until, hidden: !f.visible.checked, sort };
      saving = true; busy(save, true);
      const controls = [...form.elements]; controls.forEach((input) => { input.disabled = true; });
      try {
        const result = await api(isNew ? '/api/admin/boards' : `/api/admin/boards/${encodeURIComponent(board.id)}`, json(isNew ? 'POST' : 'PUT', payload));
        if (!context.isCurrent()) return;
        assign(result); baseline = snapshot();
        toast(t(isNew ? 'admin.board.created' : 'admin.board.saved'), 'ok');
        await onSaved(result, isNew);
      } catch (error) {
        if (context.isCurrent()) {
          const field = ({ name_required: 'name', name_too_long: 'name', invalid_url: 'url', invalid_schedule: 'schedule', invalid_window: 'window', invalid_sort: 'sort' })[error.code] || 'save';
          // Restore controls before focusing the rejected field.
          controls.forEach((input) => { input.disabled = false; });
          fieldError(field, error.message);
        }
      } finally { saving = false; controls.forEach((input) => { input.disabled = false; }); busy(save, false); update(); }
    });
    update();
    return { dirty: () => saving || dirty() };
  }
  function renderBoardCookie(container, board, context) {
    let cookie = board.cookie || {}, token = board.token, saving = false;
    container.innerHTML = `<section class="admin-panel admin-board-cookie"><h2>${esc(t('admin.cookie'))}</h2><dl class="stats" id="board-cookie-state"></dl><form class="admin-form" novalidate><fieldset class="admin-group"><legend>${esc(t('admin.board.cookieMode'))}</legend><div class="admin-choices"><label class="admin-choice"><input type="radio" name="cookie_mode" value="shared">${esc(t('admin.board.cookieShared'))}</label><label class="admin-choice"><input type="radio" name="cookie_mode" value="override">${esc(t('admin.board.cookieOverride'))}</label></div></fieldset><div id="board-cookie-entry"><label>${esc(t('cookie.field'))}<textarea name="cookie" class="admin-cookie" rows="4" maxlength="8192" autocomplete="off" spellcheck="false" aria-describedby="board-cookie-error"></textarea></label><p class="admin-hint">${esc(t('cookie.hint'))}</p><button type="submit" class="btn primary">${icon('i-check')}<span>${esc(t('admin.board.save'))}</span></button></div><p class="form-error" id="board-cookie-error" role="alert" hidden></p></form></section>`;
    const form = $('form', container), input = form.elements.cookie, mode = form.elements.cookie_mode, error = $('#board-cookie-error', container);
    const path = `/api/cookie?board=${encodeURIComponent(board.id)}`;
    const save = $('[type=submit]', form);
    const updateSave = () => { save.disabled = saving || mode.value !== 'override' || !input.value.trim(); };
    mode.value = cookie.source === 'board' ? 'override' : 'shared';
    function paint() {
      $('#board-cookie-state', container).innerHTML = stat('cookie.label', t(`admin.board.cookieSource.${cookie.source || 'none'}`)) + stat('admin.board.cookieUpdated', dateTime(cookie.updated)) + tokenRow(token);
      $('#board-cookie-entry', container).hidden = mode.value !== 'override';
      updateSave();
    }
    function showCookieError(message) { error.textContent = message; error.hidden = !message; }
    async function update(remove) {
      if (saving || !context.isCurrent()) return;
      if (!remove && (mode.value !== 'override' || !input.value.trim())) { showCookieError(t('err.invalid_cookie')); input.focus(); return; }
      saving = true; [...form.elements].forEach((node) => { node.disabled = true; }); showCookieError('');
      try {
        const data = await api(path, remove ? { method: 'DELETE' } : json('PUT', { cookie: input.value.trim() }));
        if (!context.isCurrent()) return;
        cookie = data; token = data.token; input.value = ''; mode.value = cookie.source === 'board' ? 'override' : 'shared'; paint();
        toast(t(remove ? 'admin.board.cookieCleared' : 'cookie.saved'), 'ok');
      } catch (err) { if (context.isCurrent()) { showCookieError(err.message); mode.value = cookie.source === 'board' ? 'override' : 'shared'; paint(); } }
      finally { saving = false; [...form.elements].forEach((node) => { node.disabled = false; }); updateSave(); }
    }
    form.addEventListener('change', async (event) => {
      if (event.target.name !== 'cookie_mode') return;
      showCookieError('');
      if (mode.value === 'shared' && cookie.source === 'board') {
        if (await confirmDialog(t('admin.board.cookieClearConfirm')) && context.isCurrent()) await update(true);
        else mode.value = 'override';
      } else if (mode.value === 'shared' && input.value) {
        mode.value = 'override';
        if (await confirmDialog(t('admin.unsavedConfirm')) && context.isCurrent()) { input.value = ''; mode.value = 'shared'; }
      }
      paint();
    });
    form.addEventListener('submit', (event) => { event.preventDefault(); update(false); });
    input.addEventListener('input', () => { showCookieError(''); updateSave(); });
    paint();
    return { dirty: () => saving || !!input.value || (mode.value === 'override' && cookie.source !== 'board') };
  }
  async function renderBoards(container, route, context) {
    let [data, overview] = await Promise.all([api('/api/admin/boards'), getOverview()]);
    if (!context.isCurrent()) return;
    let items = data.items || [], others = data.others || [];
    if (!route.id && items.length && !matchMedia('(max-width: 899px)').matches) {
      history.replaceState(history.state, '', boardURL(items[0].id));
      await renderRoute(undefined, context.focusHeading); return;
    }
    const isNew = route.id === 'new';
    let board = items.find((item) => item.id === route.id);
    const side = el('aside', { className: 'admin-board-side', 'aria-label': t('admin.boards') });
    const detail = el('div', { className: 'admin-board-detail' });
    const layout = el('div', { className: `admin-boards${route.id ? ' has-detail' : ''}` }, side, detail);
    container.replaceChildren(layout);
    function paintList() {
      side.innerHTML = `<a class="btn primary" href="#boards/new">${icon('i-plus')}<span>${esc(t('admin.board.new'))}</span></a><ul class="admin-board-list">${items.map((item) => `<li><a class="admin-board-item" href="${boardURL(item.id)}" ${route.id === item.id ? 'aria-current="page"' : ''}><strong>${esc(item.name)}</strong>${boardState(item)}<span class="admin-board-meta">${esc(t('status.countValue', { n: item.count }))} · ${esc(relTime(item.latest))}${item.hidden ? ` · ${esc(t('board.hidden'))}` : ''}</span></a></li>`).join('')}</ul>${!items.length ? emptyHTML(t('admin.emptyBoards')) : ''}`;
      const maintaining = others.filter((item) => ['archiving', 'deleting'].includes(item.lifecycle));
      if (maintaining.length) side.insertAdjacentHTML('beforeend', `<section><h2>${esc(t('admin.board.maintenance'))}</h2>${maintaining.map((item) => `<div class="admin-panel"><strong>${esc(item.name)}</strong>${item.operation ? operationHTML(item.operation) : `<p>${esc(t('admin.operation.working'))}</p>`}</div>`).join('')}</section>`);
    }
    const back = () => el('a', { className: 'admin-board-back', href: '#boards', textContent: t('admin.board.back') });
    paintList();
    if (!route.id) {
      detail.innerHTML = emptyHTML(t(items.length ? 'admin.board.choose' : 'admin.emptyBoards'), `<a class="btn primary" href="#boards/new">${icon('i-plus')}<span>${esc(t('admin.board.new'))}</span></a>`);
    } else if (!isNew && !board) {
      detail.innerHTML = stateHTML('mascot-oops', t('admin.board.notFound'), `<a class="btn ghost" href="#boards">${esc(t('admin.board.back'))}</a>`);
    } else if (isNew) {
      detail.append(back(), el('h2', { textContent: t('admin.board.new') }));
      const form = el('div'); detail.append(form);
      const editor = renderBoardSettings(form, null, overview, context, async (result) => { setUnsavedGuard(); await navigate(boardURL(result.id, 'settings')); });
      setUnsavedGuard(editor.dirty);
    } else {
      detail.append(back());
      const header = el('header', { className: 'admin-board-header' });
      const tabs = el('nav', { className: 'admin-board-tabs', 'aria-label': t('admin.board.tabs') });
      const content = el('div');
      detail.append(header, tabs, content);
      const tab = route.tab || 'settings';
      tabs.innerHTML = ['settings', 'history', 'data'].map((key) => `<a href="${boardURL(board.id, key)}" ${key === tab ? 'aria-current="page"' : ''}>${esc(t(`admin.board.${key}`))}</a>`).join('');
      let headerBusy = false, headerSignature = '';
      const dataStats = () => stat('admin.images', fmtNum(board.count)) + stat('admin.originalBytes', bytes(board.bytes)) + stat('admin.latest', dateTime(board.latest)) + stat('admin.board.createdAt', dateTime(board.created)) + stat('admin.board.id', board.id);
      const locked = () => !!overview.maintenance || [...items, ...others].some((item) => operationRunning(item.operation));
      function paintHeader() {
        const signature = JSON.stringify([board.name, board.state, board.paused, headerBusy]);
        if (signature === headerSignature) return;
        headerSignature = signature;
        const focused = header.contains(document.activeElement) ? document.activeElement.id || '.switch' : '';
        header.innerHTML = `<h2>${esc(board.name)} ${boardState(board)}</h2><div class="admin-board-actions"><div class="switch-row"><span class="switch-text"><strong>${esc(t('schedule.auto'))}</strong><span>${esc(t(board.paused ? 'admin.paused' : 'admin.enabled'))}</span></span><button type="button" class="switch" role="switch" aria-checked="${!board.paused}" aria-label="${esc(t('schedule.auto'))}" ${headerBusy ? 'disabled' : ''}></button></div><button type="button" class="btn ghost${headerBusy === 'fetch' ? ' loading' : ''}" id="board-fetch" ${headerBusy ? 'disabled' : ''}>${icon('i-refresh')}<span>${esc(t('status.fetchNow'))}</span></button><a href="/?board=${encodeURIComponent(board.id)}">${esc(t('admin.board.gallery'))}</a></div>`;
        $('.switch', header).addEventListener('click', () => headerAction('schedule'));
        $('#board-fetch', header).addEventListener('click', () => headerAction('fetch'));
        if (focused) $(focused === '.switch' ? focused : `#${focused}`, header)?.focus({ preventScroll: true });
      }
      async function refresh() {
        const result = await Promise.all([api('/api/admin/boards'), getOverview()]);
        if (!context.isCurrent()) return;
        [data, overview] = result; items = data.items || []; others = data.others || [];
        const current = items.find((item) => item.id === board.id);
        if (current) board = current;
        paintList(); paintHeader();
        const details = $('#board-data-stats', content);
        if (details) details.innerHTML = dataStats();
        content.querySelectorAll('[data-maintenance]').forEach((button) => { button.disabled = locked(); });
      }
      async function headerAction(kind) {
        if (headerBusy) return;
        headerBusy = kind; paintHeader();
        try {
          const result = await api(`/api/${kind}?board=${encodeURIComponent(board.id)}`, kind === 'schedule' ? json('POST', { paused: !board.paused }) : { method: 'POST' });
          if (!context.isCurrent()) return;
          if (kind === 'schedule') { board = { ...board, ...result.board }; toast(t(board.paused ? 'schedule.pausedToast' : 'schedule.resumedToast'), 'ok'); }
          else {
            const fetch = result.fetch || {};
            toast(fetch.result === 'saved' ? t('fetch.saved') : fetch.result === 'unchanged' ? t('fetch.unchanged') : t('fetch.failed', { msg: fetch.message || t(`fail.${fetch.code || 'other'}`) }), fetch.result === 'error' ? 'error' : 'ok');
          }
          await refresh();
        } catch (err) { if (context.isCurrent()) toast(err.message, 'error'); }
        finally { headerBusy = false; if (context.isCurrent()) paintHeader(); }
      }
      paintHeader();
      if (tab === 'settings') {
        const settings = el('div'), cookies = el('div'); content.append(settings, cookies);
        const editor = renderBoardSettings(settings, board, overview, context, async (result) => { board = { ...board, ...result }; paintHeader(); await refresh(); });
        const cookieEditor = renderBoardCookie(cookies, board, context);
        setUnsavedGuard(() => editor.dirty() || cookieEditor.dirty());
      } else if (tab === 'history') {
        const load = async () => {
          loading(content);
          try { const result = await api(`/api/admin/history?board=${encodeURIComponent(board.id)}`); if (context.isCurrent()) content.innerHTML = panel('admin.board.history', historyHTML(result.items || [], false)); }
          catch (err) { if (context.isCurrent()) showError(content, err, load); }
        };
        await load();
      } else if (tab === 'data') {
        content.innerHTML = panel('admin.board.data', stats(dataStats()) + `<a class="btn ghost" href="/download/all?board=${encodeURIComponent(board.id)}" download>${icon('i-download')}<span>${esc(t('admin.board.downloadAll'))}</span></a>`) + `<section class="admin-panel admin-danger"><h2>${esc(t('admin.board.danger'))}</h2><div class="admin-danger-row"><p class="admin-hint">${esc(t('admin.board.archiveHint'))}</p><button type="button" class="btn ghost danger" data-maintenance="archive">${esc(t('admin.board.archive'))}</button></div><div class="admin-danger-row"><p class="admin-hint">${esc(t('admin.board.deleteHint'))}</p><button type="button" class="btn ghost danger" data-maintenance="delete">${esc(t('admin.board.delete'))}</button></div><div id="board-operation" aria-live="polite"></div><p class="form-error" id="board-operation-error" role="alert" hidden></p></section>`;
        $('.stats', content).id = 'board-data-stats';
        const progress = $('#board-operation', content), error = $('#board-operation-error', content);
        let operation = board.operation, polling = false, submitting = false;
        function paintOperation() {
          progress.innerHTML = operation ? operationHTML(operation) : '';
          content.querySelectorAll('[data-maintenance]').forEach((button) => { button.disabled = submitting || operationRunning(operation) || locked(); });
        }
        async function poll() {
          if (!context.isCurrent()) return;
          try {
            operation = await api(`/api/admin/boards/${encodeURIComponent(board.id)}/operation`);
            if (!context.isCurrent()) return;
            error.hidden = true; error.textContent = '';
            paintOperation();
            if (validTime(operation.finished)) {
              polling = false;
              if (operation.error) { await refresh(); paintOperation(); }
              else { setUnsavedGuard(); await navigate(operation.kind === 'archive' ? '#archives' : '#boards'); toast(t(operation.kind === 'archive' ? 'admin.board.archived' : 'admin.board.deleted'), 'ok'); }
              return;
            }
            // Boards and overview are refreshed by refreshMaintenance below; this loop only tracks progress.
          } catch (err) { if (context.isCurrent()) { error.textContent = err.message; error.hidden = false; } }
          if (context.isCurrent()) routeTimer(context, poll, 1000);
        }
        content.querySelectorAll('[data-maintenance]').forEach((button) => button.addEventListener('click', async () => {
          if (button.disabled || submitting) return;
          const kind = button.dataset.maintenance;
          const confirmedName = board.name;
          const accepted = kind === 'archive' ? await confirmDialog(t('admin.board.archiveConfirm', { name: board.name }), undefined, { label: t('admin.board.archive') }) : await confirmDialog(t('admin.board.deleteConfirm', { name: board.name }), board.name, { label: t('admin.board.delete'), danger: true });
          if (!accepted || !context.isCurrent()) return;
          submitting = true; paintOperation(); error.hidden = true;
          try {
            await refresh();
            if (!context.isCurrent()) return;
            if (locked()) throw new Error(t('err.maintenance_busy'));
            const result = await api(`/api/admin/boards/${encodeURIComponent(board.id)}${kind === 'archive' ? '/archive' : ''}`, kind === 'archive' ? { method: 'POST' } : json('DELETE', { confirm: confirmedName }));
            if (!context.isCurrent()) return;
            operation = result.operation; polling = true; paintOperation(); routeTimer(context, poll, 1000); await refresh(); paintOperation();
          } catch (err) { if (context.isCurrent()) { error.textContent = err.message; error.hidden = false; await refresh().catch(() => {}); } }
          finally { submitting = false; if (context.isCurrent()) paintOperation(); }
        }));
        paintOperation();
        if (operationRunning(operation) && !polling) { polling = true; routeTimer(context, poll, 1000); }
      }
      // Update maintenance locks without replacing dirty forms.
      async function refreshMaintenance() {
        try { await refresh(); } catch { /* Keep the current page; explicit actions surface errors. */ }
        if (context.isCurrent()) routeTimer(context, refreshMaintenance, locked() ? 1000 : 5000);
      }
      routeTimer(context, refreshMaintenance, locked() ? 1000 : 5000);
    }
    if (!board && others.some((item) => operationRunning(item.operation))) {
      async function refreshOthers() {
        try {
          const result = await api('/api/admin/boards');
          if (!context.isCurrent()) return;
          items = result.items || []; others = result.others || []; paintList();
        } catch { /* Retry while this page remains current. */ }
        if (context.isCurrent() && others.some((item) => operationRunning(item.operation))) routeTimer(context, refreshOthers, 1000);
      }
      routeTimer(context, refreshOthers, 1000);
    }
  }
  async function renderArchives(container, route, context) {
    const data = await api('/api/admin/archives');
    if (!context.isCurrent()) return;
    const items = data.items || [];
    container.innerHTML = items.length ? `<p class="admin-summary">${esc(t('admin.archive.total', { n: fmtNum(items.length), size: bytes(data.bytes) }))}</p><ul class="admin-panel admin-archive-list">${items.map((item) => `<li class="admin-archive-item"><div class="admin-archive-main"><strong>${esc(item.board_name)}</strong><span class="admin-board-meta">${esc([dateTime(item.created), t('status.countValue', { n: item.images }), bytes(item.size)].join(' · '))}</span></div><div class="admin-archive-actions"><a class="btn ghost" href="/api/admin/archives/${encodeURIComponent(item.id)}/download" download>${icon('i-download')}<span>${esc(t('common.download'))}</span></a><button type="button" class="btn ghost danger" data-archive="${esc(item.id)}"><span class="spinner" aria-hidden="true"></span><span>${esc(t('admin.archive.delete'))}</span></button></div></li>`).join('')}</ul>` : emptyHTML(t('admin.archive.empty'));
    let deleting = false;
    const deleteButtons = [...container.querySelectorAll('[data-archive]')];
    deleteButtons.forEach((button) => button.addEventListener('click', async () => {
      const item = items.find((entry) => entry.id === button.dataset.archive);
      if (deleting || button.disabled || !item || !await confirmDialog(t('admin.archive.deleteConfirm', { name: item.board_name }), undefined, { label: t('admin.archive.deleteConfirmLabel'), danger: true })) return;
      if (!context.isCurrent() || deleting) return;
      deleting = true;
      deleteButtons.forEach((node) => { node.disabled = true; });
      busy(button, true);
      try {
        await api(`/api/admin/archives/${encodeURIComponent(item.id)}`, { method: 'DELETE' });
        if (context.isCurrent()) { toast(t('admin.archive.deleted'), 'ok'); await renderArchives(container, route, context); }
      } catch (err) { if (context.isCurrent()) toast(err.message, 'error'); }
      finally { deleting = false; deleteButtons.forEach((node) => { node.disabled = false; }); busy(button, false); }
    }));
  }
  const PAGES = {
    overview: { title: 'admin.overview', render: renderOverview },
    boards: { title: 'admin.boards', render: renderBoards },
    history: { title: 'admin.history', render: renderHistory },
    cookie: { title: 'admin.cookie', render: renderCookie },
    notices: { title: 'admin.notices', render: renderNotices },
    system: { title: 'admin.system', render: renderSystem },
    archives: { title: 'admin.archives', render: renderArchives },
  };
  function parseRoute(hash = location.hash) {
    let parts;
    try { parts = hash.replace(/^#/, '').split('/').map(decodeURIComponent); }
    catch (error) { parts = []; }
    const page = parts[0] || 'overview';
    if (!parts.length) parts = ['overview'];
    else parts[0] = page;
    const valid = Object.hasOwn(PAGES, page) && (page === 'boards' ? parts.length <= 3 && parts.slice(1).every(Boolean) && (!parts[2] || ['settings', 'history', 'data'].includes(parts[2])) && (parts[1] !== 'new' || parts.length === 2) : parts.length === 1);
    return valid ? { page, id: parts[1] || '', tab: parts[2] || '', parts, hash: `#${parts.map(encodeURIComponent).join('/')}` } : { page: 'overview', id: '', tab: '', parts: ['overview'], hash: '#overview' };
  }
  function renderNav(page) {
    const nav = $('#admin-nav');
    nav.replaceChildren(...Object.entries(PAGES).map(([key, value]) => el('a', { href: `#${key}`, ...(key === page ? { 'aria-current': 'page' } : {}), textContent: t(value.title) })));
    if (matchMedia('(max-width: 899px)').matches) $('[aria-current="page"]', nav)?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }
  async function renderRoute(overview, focusHeading = false) {
    if (!state.admin) return;
    const route = parseRoute();
    if (location.hash !== route.hash) history.replaceState(history.state, '', route.hash);
    state.route = route.hash;
    setUnsavedGuard();
    (state.cleanups || []).forEach((fn) => fn());
    const cleanups = state.cleanups = new Set();
    const generation = ++state.generation;
    const context = { overview, focusHeading, onCleanup: (fn) => { cleanups.add(fn); return () => cleanups.delete(fn); }, isCurrent: () => state.admin && generation === state.generation };
    renderNav(route.page);
    const root = $('#admin-content');
    const body = el('div', { className: 'admin-page' });
    root.replaceChildren(el('h1', { tabindex: '-1', textContent: t(PAGES[route.page].title) }), body);
    root.setAttribute('aria-busy', 'true');
    document.body.classList.add('busy');
    loading(body);
    try { await PAGES[route.page].render(body, route, context); }
    catch (error) { if (context.isCurrent()) showError(body, error, () => renderRoute()); }
    finally {
      if (context.isCurrent() || !state.admin) document.body.classList.remove('busy');
      if (context.isCurrent()) {
        root.removeAttribute('aria-busy');
        if (focusHeading) $('h1', root).focus({ preventScroll: true });
      }
    }
  }
  // Keep the history cursor on the edited page while a navigation is confirmed.
  // Replacing a popped entry would lose the page that Back was trying to reach.
  let pendingNavigation = null;
  async function locationChanged() {
    let targetIndex = history.state?.mvAdminIndex;
    if (!Number.isInteger(targetIndex)) {
      targetIndex = historyIndex + 1;
      history.replaceState({ ...history.state, mvAdminIndex: targetIndex }, '');
    }
    if (pendingNavigation) {
      if (targetIndex !== historyIndex) {
        if (pendingNavigation.confirming && !pendingNavigation.restoring) {
          pendingNavigation.restoring = true;
          history.go(historyIndex - targetIndex);
        }
        return;
      }
      pendingNavigation.restoring = false;
      if (pendingNavigation.confirming) return;
      pendingNavigation.confirming = true;
      const delta = pendingNavigation.targetIndex - historyIndex;
      const accepted = await guardUnsaved();
      pendingNavigation = null;
      if (accepted && state.admin) { setUnsavedGuard(); history.go(delta); }
      return;
    }
    if (!state.admin) { historyIndex = targetIndex; return; }
    const next = parseRoute().hash;
    if (next === state.route) {
      historyIndex = targetIndex;
      history.replaceState(history.state, '', next);
      return;
    }
    if (unsavedGuard() && targetIndex !== historyIndex) {
      pendingNavigation = { targetIndex, confirming: false, restoring: true };
      history.go(historyIndex - targetIndex);
      return;
    }
    historyIndex = targetIndex;
    await renderRoute(undefined, true);
  }
  window.addEventListener('popstate', locationChanged);
  window.addEventListener('hashchange', locationChanged);
  async function navigate(hash) {
    const next = parseRoute(hash).hash;
    if (next === state.route || !await guardUnsaved() || !state.admin) return;
    history.pushState({ ...history.state, mvAdminIndex: ++historyIndex }, '', next);
    await renderRoute(undefined, true);
  }
  document.addEventListener('click', async (event) => {
    const link = event.target.closest('a');
    if (!link || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || link.target || link.hasAttribute('download')) return;
    const target = new URL(link.href, location.href);
    if (target.hash === '#admin-content') { event.preventDefault(); $('#admin-content').focus(); return; }
    const internal = target.origin === location.origin && target.pathname === location.pathname;
    if (internal && state.admin) { event.preventDefault(); await navigate(target.hash); }
    else if (unsavedGuard()) {
      event.preventDefault();
      if (await guardUnsaved()) { setUnsavedGuard(); location.href = target.href; }
    }
  });
  window.addEventListener('beforeunload', (event) => {
    if (unsavedGuard()) { event.preventDefault(); event.returnValue = ''; }
  });
  function showLogin(message = '') {
    state.admin = false;
    ++state.generation;
    (state.cleanups || []).forEach((fn) => fn());
    state.cleanups = new Set();
    setUnsavedGuard();
    finishConfirm();
    $('#admin-shell').hidden = true;
    $('#admin-logout').hidden = true;
    $('#admin-boot').hidden = true;
    document.body.classList.remove('busy');
    const login = $('#admin-login');
    const wasHidden = login.hidden;
    login.hidden = false;
    if (wasHidden) { $('#login-form').reset(); setPasswordVisible(false); $('#login-form').elements.username.focus(); }
    $('#login-error').textContent = message;
    $('#login-error').hidden = !message;
  }
  async function authenticate() {
    const overview = await getOverview();
    state.admin = true;
    $('#admin-login').hidden = true;
    $('#admin-boot').hidden = true;
    $('#admin-shell').hidden = false;
    $('#admin-logout').hidden = false;
    await renderRoute(overview);
  }
  function setPasswordVisible(on) {
    $('#login-form').elements.password.type = on ? 'text' : 'password';
    const button = $('#pw-toggle');
    button.setAttribute('aria-pressed', String(on));
    button.dataset.i18nLabel = on ? 'login.hide' : 'login.show';
    button.setAttribute('aria-label', t(button.dataset.i18nLabel));
    $('use', button).setAttribute('href', on ? '#i-eye-off' : '#i-eye');
  }
  $('#pw-toggle').addEventListener('click', () => setPasswordVisible($('#login-form').elements.password.type === 'password'));
  $('#login-form').addEventListener('submit', async (event) => {
    event.preventDefault();
    const form = event.currentTarget;
    const button = $('#login-submit');
    if (button.disabled) return;
    busy(button, true);
    $('#login-error').hidden = true;
    try {
      await api('/api/login', json('POST', { username: form.elements.username.value, password: form.elements.password.value }));
      form.elements.password.value = '';
      await authenticate();
      toast(t('login.ok'), 'ok');
    } catch (error) {
      showLogin(error.message);
      form.elements.password.select();
    } finally { busy(button, false); }
  });
  $('#admin-logout').addEventListener('click', async () => {
    if (!await guardUnsaved()) return;
    const button = $('#admin-logout'); busy(button, true);
    try { await api('/api/logout', { method: 'POST' }); showLogin(); }
    catch (error) { toast(error.message, 'error'); }
    finally { busy(button, false); }
  });
  function renderTheme() {
    const style = getComputedStyle(document.documentElement);
    $('#admin-theme use').setAttribute('href', style.colorScheme === 'dark' ? '#i-sun' : '#i-moon');
    document.querySelectorAll('meta[name="theme-color"]').forEach((node) => { node.content = style.getPropertyValue('--board').trim(); });
  }
  function toggleTheme() {
    const apply = () => {
      document.documentElement.dataset.theme = getComputedStyle(document.documentElement).colorScheme === 'dark' ? 'light' : 'dark';
      try { localStorage.setItem('mv.theme', document.documentElement.dataset.theme); } catch (error) { /* private mode */ }
      renderTheme();
    };
    if (!document.startViewTransition || reducedMotion()) return apply();
    // Reveal the new theme as a circle growing from the toggle button, as the gallery does.
    const r = $('#admin-theme').getBoundingClientRect();
    const x = r.left + r.width / 2, y = r.top + r.height / 2;
    const radius = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y));
    document.startViewTransition(apply).ready.then(() => {
      document.documentElement.animate(
        { clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${radius}px at ${x}px ${y}px)`] },
        { duration: 480, easing: 'cubic-bezier(.4, 0, .2, 1)', pseudoElement: '::view-transition-new(root)' });
    }).catch(() => {});
  }
  async function toggleLang() {
    if (!await guardUnsaved()) return;
    i18n.set(i18n.lang === 'zh' ? 'en' : 'zh');
    i18n.apply();
    if (state.admin) await renderRoute();
  }
  // Like the gallery bar: the border shows once content scrolls beneath it.
  const bar = $('#admin-bar');
  const markScrolled = () => bar.classList.toggle('scrolled', scrollY > 4);
  addEventListener('scroll', markScrolled, { passive: true });
  markScrolled();
  $('#admin-theme').addEventListener('click', toggleTheme);
  $('#admin-lang').addEventListener('click', toggleLang);
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', renderTheme);
  document.addEventListener('keydown', (event) => {
    if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey || event.target.closest('input, textarea, select, [contenteditable]') || $('dialog[open]')) return;
    if (event.key.toLowerCase() === 't') { event.preventDefault(); toggleTheme(); }
    else if (event.key.toLowerCase() === 'l') { event.preventDefault(); toggleLang(); }
  });
  async function boot() {
    $('#admin-boot').hidden = false;
    try { await authenticate(); }
    catch (error) {
      if (error.status === 401) return;
      showError($('#admin-boot'), error, boot);
    }
  }
  i18n.apply();
  renderTheme();
  boot();
})();
