// kbridge admin console. Plain DOM: the CSP forbids inline script and style,
// and building nodes with textContent keeps every server string inert.
'use strict';

/* ---------- DOM helpers ---------- */

function h(tag, attrs, ...kids) {
  const n = document.createElement(tag);
  setAttrs(n, attrs);
  append(n, kids);
  return n;
}

function setAttrs(n, attrs) {
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') n.className = v;
    else if (k === 'text') n.textContent = v;
    else if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v === true ? '' : String(v));
  }
}

function append(n, kids) {
  for (const k of kids.flat(Infinity)) if (k != null && k !== false) n.append(k);
}

const SVG_NS = 'http://www.w3.org/2000/svg';
const ICONS = {
  clusters: [['rect', { x: 3.5, y: 4, width: 17, height: 6.5, rx: 2 }], ['rect', { x: 3.5, y: 13.5, width: 17, height: 6.5, rx: 2 }], ['path', { d: 'M7.5 7.25h.01M7.5 16.75h.01' }]],
  audit: [['rect', { x: 4, y: 3.5, width: 16, height: 17, rx: 2.5 }], ['path', { d: 'M8 8.5h8M8 12h8M8 15.5h5' }]],
  tokens: [['circle', { cx: 8, cy: 15, r: 4 }], ['path', { d: 'M11 12l8-8M16 7l2.5 2.5M14 9l2 2' }]],
  users: [['circle', { cx: 9, cy: 8.5, r: 3.5 }], ['path', { d: 'M2.5 19.5c.8-3.3 3.4-5 6.5-5s5.7 1.7 6.5 5' }], ['circle', { cx: 17, cy: 9.5, r: 2.5 }], ['path', { d: 'M16.5 14.6c2.4.2 4.2 1.7 4.9 4.4' }]],
  refresh: [['path', { d: 'M20 11a8 8 0 1 0-2.3 5.7' }], ['path', { d: 'M20 5v6h-6' }]],
  download: [['path', { d: 'M12 15V3' }], ['path', { d: 'm7 10 5 5 5-5' }], ['path', { d: 'M4 15v4a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-4' }]],
  search: [['circle', { cx: 11, cy: 11, r: 7 }], ['path', { d: 'm20 20-3.5-3.5' }]],
  left: [['path', { d: 'm15 18-6-6 6-6' }]],
  right: [['path', { d: 'm9 18 6-6-6-6' }]],
  first: [['path', { d: 'm17 18-6-6 6-6M11 18l-6-6 6-6' }]],
  last: [['path', { d: 'm7 18 6-6-6-6M13 18l6-6-6-6' }]],
  close: [['path', { d: 'M6 6l12 12M18 6 6 18' }]],
  arrow: [['path', { d: 'M5 12h14' }], ['path', { d: 'm13 6 6 6-6 6' }]],
  eye: [['path', { d: 'M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12Z' }], ['circle', { cx: 12, cy: 12, r: 3 }]],
  eyeOff: [['path', { d: 'M3 3l18 18' }], ['path', { d: 'M10.6 10.6a2 2 0 0 0 2.8 2.8' }], ['path', { d: 'M9.9 5.1A10.5 10.5 0 0 1 12 5c6 0 10 7 10 7a17.6 17.6 0 0 1-3.2 4.1' }], ['path', { d: 'M6.6 6.6C3.9 8.4 2 12 2 12s4 7 10 7a9.8 9.8 0 0 0 5.4-1.6' }]],
  sun: [['circle', { cx: 12, cy: 12, r: 4 }], ['path', { d: 'M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4' }]],
  moon: [['path', { d: 'M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5Z' }]],
};

function icon(name, size = 18, width = 1.7) {
  const s = svgEl('svg', { width: size, height: size, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor',
    'stroke-width': width, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true' });
  for (const [tag, attrs] of ICONS[name]) s.append(svgEl(tag, attrs));
  return s;
}

function svgEl(tag, attrs) {
  const n = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, String(v));
  return n;
}

/* ---------- Formatting ---------- */

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const pad2 = (n) => (n < 10 ? '0' : '') + n;
const fmtInt = (n) => n.toLocaleString('en-US');

function clockUTC(d) { return pad2(d.getUTCHours()) + ':' + pad2(d.getUTCMinutes()) + ':' + pad2(d.getUTCSeconds()); }
function dayUTC(d) { return MONTHS[d.getUTCMonth()] + ' ' + d.getUTCDate(); }
function dateUTC(d) { return dayUTC(d) + ', ' + d.getUTCFullYear(); }
function sameDayUTC(a, b) { return a.toISOString().slice(0, 10) === b.toISOString().slice(0, 10); }

// Audit time cell: the clock alone today, the day too for older rows.
function auditTime(iso) {
  const d = new Date(iso);
  return sameDayUTC(d, new Date()) ? clockUTC(d) : dayUTC(d) + ' ' + clockUTC(d);
}

function ago(iso, now = Date.now()) {
  if (!iso) return 'never';
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 60) return s + ' s ago';
  if (s < 3600) return Math.floor(s / 60) + ' min ago';
  if (s < 86400) return Math.floor(s / 3600) + ' h ago';
  return Math.floor(s / 86400) + ' d ago';
}

// Last Used: relative within a day, a date after that.
function usedWhen(iso) {
  if (!iso) return 'Never';
  return Date.now() - new Date(iso).getTime() < 86400000 ? ago(iso) : dateUTC(new Date(iso));
}

function duration(ms) {
  if (ms == null) return '–';
  if (ms < 1000) return ms + ' ms';
  if (ms < 60000) return (Math.round(ms / 100) / 10) + ' s';
  return Math.floor(ms / 60000) + ' m ' + Math.round((ms % 60000) / 1000) + ' s';
}

/* ---------- Theme ---------- */

function storedTheme() {
  try { return localStorage.getItem('kb.theme') === 'light' ? 'light' : 'dark'; } catch (e) { return 'dark'; }
}

function setTheme(t) {
  document.documentElement.dataset.theme = t;
  try { localStorage.setItem('kb.theme', t); } catch (e) { /* storage blocked: the choice lasts this page only */ }
  for (const img of document.querySelectorAll('img.logo')) img.src = logoSrc();
  for (const sw of document.querySelectorAll('.switch')) sw.setAttribute('aria-checked', String(t === 'dark'));
}

const isDark = () => document.documentElement.dataset.theme !== 'light';
const logoSrc = () => (isDark() ? '/assets/logo-dark.svg' : '/assets/logo.svg');
const logo = () => h('img', { class: 'logo', src: logoSrc(), alt: 'kbridge' });

/* ---------- Session and API ---------- */

const session = {
  get access() { return sessionStorage.getItem('kb.access'); },
  get refresh() { return sessionStorage.getItem('kb.refresh'); },
  save(pair) { sessionStorage.setItem('kb.access', pair.access_token); sessionStorage.setItem('kb.refresh', pair.refresh_token); },
  clear() { sessionStorage.removeItem('kb.access'); sessionStorage.removeItem('kb.refresh'); },
};

// claims decodes the JWT payload for display and the admin gate. The server
// verifies the token on every call; nothing here is trusted for access.
function claims(token) {
  try {
    const b64 = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
    const bin = atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4));
    return JSON.parse(new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0))));
  } catch (e) {
    return {};
  }
}

class ApiError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}

const UNREACHABLE = 'Cannot reach the control plane.';

async function send(path, opts = {}, token = session.access) {
  const headers = { Accept: 'application/json' };
  if (token) headers.Authorization = 'Bearer ' + token;
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  const body = opts.body === undefined ? undefined : JSON.stringify(opts.body);
  try {
    return await fetch(path, { method: opts.method || 'GET', headers, body, credentials: 'omit', cache: 'no-store' });
  } catch (e) {
    throw new ApiError(0, UNREACHABLE);
  }
}

async function readJSON(res) {
  try { return await res.json(); } catch (e) { return {}; }
}

async function api(path, opts) {
  let res = await send(path, opts);
  if (res.status === 401 && (await refreshSession())) res = await send(path, opts);
  if (res.status === 401) {
    endSession();
    throw new ApiError(401, 'session ended');
  }
  const body = await readJSON(res);
  if (!res.ok) throw new ApiError(res.status, body.error || res.statusText || 'request failed');
  return body;
}

let refreshing = null;

// refreshSession shares one refresh among concurrent 401s: the server rotates
// the refresh token, so a second refresh with the old one would fail.
function refreshSession() {
  if (!refreshing) {
    refreshing = doRefresh().finally(() => { refreshing = null; });
  }
  return refreshing;
}

async function doRefresh() {
  const token = session.refresh;
  if (!token) return false;
  try {
    const res = await send('/auth/refresh', { method: 'POST', body: { refresh_token: token } }, null);
    if (!res.ok) return false;
    session.save(await res.json());
    return true;
  } catch (e) {
    return false;
  }
}

function logout(access, refresh) {
  if (!access || !refresh) return Promise.resolve();
  return send('/api/v1/auth/logout', { method: 'POST', body: { refresh_token: refresh } }, access).catch(() => {});
}

let returnTo = '';

function endSession() {
  const here = location.hash;
  session.clear();
  if (!here.startsWith('#/login')) returnTo = here;
  location.hash = '#/login?reason=expired';
}

async function signOut() {
  const access = session.access, refresh = session.refresh;
  session.clear();
  returnTo = '';
  await logout(access, refresh);
  location.hash = '#/login';
}

/* ---------- Router ---------- */

let timers = [];

function every(fn, ms) { timers.push(setInterval(fn, ms)); }
function stopTimers() { timers.forEach(clearInterval); timers = []; }
const visible = () => document.visibilityState === 'visible';

function parseHash() {
  const raw = location.hash.replace(/^#\/?/, '');
  const i = raw.indexOf('?');
  const name = (i < 0 ? raw : raw.slice(0, i)) || 'clusters';
  return { name, params: new URLSearchParams(i < 0 ? '' : raw.slice(i + 1)) };
}

function routeHash(name, params) {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params || {})) if (v !== '' && v != null) q.set(k, v);
  const qs = q.toString();
  return '#/' + name + (qs ? '?' + qs : '');
}

function go(name, params) { location.hash = routeHash(name, params); }

const SCREENS = { clusters: clustersScreen, audit: auditScreen, tokens: tokensScreen, users: usersScreen };
let current = { name: '', update: null };

function route() {
  const { name, params } = parseHash();
  if (name === 'login') return showLogin(params);
  if (!session.access) return go('login');
  if (!SCREENS[name]) return go('clusters');
  if (name === current.name && current.update) return current.update(params);
  stopTimers();
  const screen = SCREENS[name](params);
  current = { name, update: screen.update || null };
  mount(shell(name, screen.node));
}

function mount(node) {
  const app = document.getElementById('app');
  app.replaceChildren(node);
  window.scrollTo(0, 0);
}

/* ---------- Shell ---------- */

const NAV = [['clusters', 'Clusters'], ['audit', 'Audit Log'], ['tokens', 'Agent Tokens'], ['users', 'Users']];

function shell(active, content) {
  return h('div', { class: 'shell' },
    h('aside', { class: 'side' },
      h('a', { class: 'brand', href: '#/clusters', 'aria-label': 'kbridge home' }, logo()),
      h('nav', { class: 'nav', 'aria-label': 'Main' }, NAV.map(([id, label]) =>
        h('a', { href: '#/' + id, 'aria-current': id === active ? 'page' : null }, icon(id), label))),
      sideFoot()),
    content);
}

function sideFoot() {
  const email = claims(session.access).email || '';
  return h('div', { class: 'side-foot' },
    h('div', { class: 'appearance' },
      h('span', { id: 'dm-label', text: 'Dark Appearance' }),
      h('button', { type: 'button', class: 'switch', role: 'switch', 'aria-checked': String(isDark()),
        'aria-labelledby': 'dm-label', onclick: () => setTheme(isDark() ? 'light' : 'dark') }, h('span'))),
    h('div', {}, h('div', { class: 'signed-label', text: 'Signed in as' }), h('div', { class: 'signed-email', text: email })),
    h('button', { type: 'button', class: 'linkbtn', text: 'Sign Out', onclick: signOut }));
}

function mainArea(cls, ...kids) { return h('main', { class: 'main' + (cls ? ' ' + cls : '') }, kids); }

function pageHead(title, sub, ...right) {
  return h('div', { class: 'page-head' },
    h('div', {}, h('h1', { text: title }), h('p', { class: 'sub', text: sub })), right);
}

// errorBanner shows a failed call in place. A 401 already moved to sign-in.
function errorBanner(err, retry) {
  const title = err.status ? 'Request failed (' + err.status + ')' : UNREACHABLE;
  const text = err.status ? err.message : 'Check that it is running and that this browser can reach it.';
  return h('div', { class: 'banner', role: 'alert' },
    h('div', {}, h('div', { class: 'banner-title', text: title }), h('div', { class: 'banner-text', text })),
    h('button', { type: 'button', class: 'btn', text: 'Retry', onclick: retry }));
}

function showError(slot, err, retry) {
  if (err.status === 401) return;
  slot.replaceChildren(errorBanner(err, retry));
}

async function copyText(btn, text) {
  let ok = false;
  try { await navigator.clipboard.writeText(text); ok = true; } catch (e) { ok = copyFallback(text); }
  const was = btn.textContent;
  btn.textContent = ok ? 'Copied' : 'Copy failed';
  setTimeout(() => { btn.textContent = was; }, 1500);
}

// copyFallback covers plain-http control planes, where the clipboard API is absent.
function copyFallback(text) {
  const ta = h('textarea', { class: 'vh', readonly: true });
  ta.value = text;
  document.body.append(ta);
  ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
  ta.remove();
  return ok;
}

function copyButton(text, label) {
  const b = h('button', { type: 'button', class: 'btn', text: 'Copy', 'aria-label': label || null });
  b.addEventListener('click', () => copyText(b, text));
  return b;
}

/* ---------- Clusters ---------- */

function clustersScreen() {
  const updated = h('div', { class: 'updated' });
  const body = h('div', { class: 'stack' });
  const node = mainArea('roomy', pageHead('Clusters', 'Agent connection and policy sync for each cluster.', updated), body);
  let loadedAt = 0;
  const tick = () => {
    if (loadedAt) updated.replaceChildren(icon('refresh', 14, 2), 'Updated ' + ago(new Date(loadedAt).toISOString()));
  };
  const load = async () => {
    try {
      const [cl, rb] = await Promise.all([api('/api/v1/admin/clusters'), api('/api/v1/admin/rbac/status')]);
      loadedAt = Date.now();
      tick();
      body.replaceChildren(...renderClusters(mergeClusters(cl.clusters || [], rb.clusters || [])));
    } catch (err) {
      showError(body, err, load);
    }
  };
  load();
  every(() => { if (visible()) load(); }, 10000);
  every(tick, 1000);
  return { node };
}

// Connected clusters first, then disconnected, then those still waiting for
// their first agent; the API's name order is kept within each group.
const CLUSTER_GROUP = { connected: 0, disconnected: 1, waiting: 2 };

function clusterGroup(c) {
  if (c.status === 'connected') return CLUSTER_GROUP.connected;
  return c.ever_connected ? CLUSTER_GROUP.disconnected : CLUSTER_GROUP.waiting;
}

function mergeClusters(clusters, rbac) {
  const sync = new Map(rbac.map((r) => [r.cluster, r]));
  return clusters
    .map((c) => ({ ...c, sync: sync.get(c.name) || null }))
    .sort((a, b) => clusterGroup(a) - clusterGroup(b));
}

function clusterKind(c) {
  if (c.status !== 'connected') return c.ever_connected ? 'disconnected' : 'waiting';
  const state = c.sync ? c.sync.state : 'unreported';
  return state === 'in sync' ? 'ok' : state === 'drift' ? 'drift' : 'unreported';
}

function renderClusters(rows) {
  if (!rows.length) return [emptyClusters()];
  const kinds = rows.map(clusterKind);
  const count = (pred) => kinds.filter(pred).length;
  const attention = count((k) => k === 'drift' || k === 'disconnected' || k === 'waiting');
  return [
    h('div', { class: 'stats' },
      stat('Connected', count((k) => k !== 'disconnected' && k !== 'waiting'), rows.length),
      stat('In sync with policy', count((k) => k === 'ok'), rows.length),
      stat('Need attention', attention, null, attention > 0)),
    h('section', { class: 'card', 'aria-label': 'Clusters' }, rows.map((c, i) => clusterRow(c, kinds[i]))),
    h('p', { class: 'note', text: 'Fixes run against the cluster. The console shows them; it never runs them.' }),
  ];
}

function stat(label, value, of, warn) {
  return h('div', { class: 'stat' },
    h('div', { class: 'stat-label', text: label }),
    h('div', { class: 'stat-value' + (warn ? ' warn' : '') }, String(value),
      of == null ? null : [' ', h('span', { class: 'stat-of', text: 'of ' + of })]));
}

const SYNC_BADGES = {
  ok: ['ok', 'In sync'], unreported: ['neu', 'Not reported'], disconnected: ['neu', 'Unknown'], waiting: ['neu', 'Unknown'],
};

function syncBadge(c, kind) {
  if (kind === 'drift') {
    const n = (c.sync.errors || []).length;
    return h('span', { class: 'badge warn', text: n ? 'Drift · ' + n + (n === 1 ? ' error' : ' errors') : 'Drift' });
  }
  const [cls, text] = SYNC_BADGES[kind];
  return h('span', { class: 'badge ' + cls, text });
}

function clusterRow(c, kind) {
  const connected = kind !== 'disconnected' && kind !== 'waiting';
  const meta = kind === 'waiting' ? 'Waiting for agent · no heartbeat yet'
    : (connected ? 'Connected' : 'Disconnected') + ' · heartbeat ' + ago(c.last_heartbeat);
  return h('div', { class: 'crow' },
    h('div', { class: 'crow-main' },
      h('div', { class: 'crow-id' },
        h('span', { class: 'dot ' + (connected ? 'bg-green' : 'bg-gray'), 'aria-hidden': 'true' }),
        h('div', {}, h('div', { class: 'crow-name', text: c.name }), h('div', { class: 'crow-meta', text: meta }))),
      h('div', { class: 'crow-side' }, syncBadge(c, kind),
        h('a', { href: routeHash('audit', { cluster: c.name }), text: 'Audit Log ›' }))),
    clusterIssue(c, kind));
}

function clusterIssue(c, kind) {
  if (kind === 'drift') return issueBox(true, driftIssue(c.sync));
  if (kind === 'disconnected') return issueBox(false, disconnectedIssue());
  if (kind === 'waiting') return issueBox(false, waitingIssue(c.name));
  return null;
}

function issueBox(warn, kids) {
  return h('div', { class: 'issue-wrap' }, h('div', { class: 'issue' + (warn ? ' warn' : '') }, kids));
}

function driftIssue(s) {
  const errors = s.errors || [];
  const title = errors.length ? 'The agent could not apply the policy' : 'The agent has not applied the current policy yet';
  return [
    h('div', { class: 'issue-title', text: title }),
    errors.length ? errors.map((e) => h('div', { class: 'issue-text', text: e }))
      : h('div', { class: 'issue-text', text: 'It applies policy changes on its next sync.' }),
    h('div', { class: 'versions' },
      h('span', {}, 'Applied ', h('span', { class: 'mono', text: (s.version || '–').slice(0, 8) })),
      h('span', {}, 'Wanted by policy ', h('span', { class: 'mono', text: (s.desired_version || '–').slice(0, 8) }))),
  ];
}

function disconnectedIssue() {
  const fix = 'kubectl -n kbridge logs deploy/kbridge-agent --tail=50';
  return [
    h('div', { class: 'issue-title', text: 'Agent not connected' }),
    h('div', { class: 'issue-text', text: 'Commands to this cluster fail with "agent is disconnected". Check the agent pod in that cluster.' }),
    h('div', { class: 'fix-row' }, h('code', { class: 'code-line', text: fix }), copyButton(fix, 'Copy the log command')),
  ];
}

function waitingIssue(name) {
  return [
    h('div', { class: 'issue-title', text: 'Waiting for agent' }),
    h('div', { class: 'issue-text', text: 'A token exists for this cluster, but no agent has connected with it yet.' }),
    h('ol', { class: 'steps' }, installStep(name), waitStep()),
  ];
}

// The agent chart version moves with each release (see the release skill).
function installCommand(name) {
  return 'helm install kbridge-agent oci://ghcr.io/why-xn/charts/kbridge-agent \\\n' +
    '  -n kbridge --create-namespace --version 0.3.0-alpha.1 \\\n' +
    '  --set controlPlane.url=<control-plane-host>:9090 \\\n' +
    '  --set controlPlane.token=<token from step 1> \\\n' +
    '  --set cluster.name=' + name;
}

function step(n, title, ...body) {
  return h('li', { class: 'step' },
    h('span', { class: 'step-num', 'aria-hidden': 'true', text: String(n) }),
    h('div', { class: 'step-body' }, h('div', { class: 'step-title', text: title }), body));
}

function cmdRow(text, label) {
  return h('div', { class: 'cmd-row' }, h('pre', { class: 'cmd', text }), copyButton(text, label));
}

function installStep(name) {
  return step(2, 'Install the agent in that cluster', cmdRow(installCommand(name), 'Copy the install command'),
    h('div', { class: 'step-hint' }, h('code', { text: 'cluster.name' }), ' must match the name in step 1.'));
}

function waitStep() {
  return step(3, 'Wait here', h('div', { class: 'step-text', text: 'The cluster turns connected a few seconds after the agent starts. This page updates on its own.' }));
}

function emptyClusters() {
  const name = 'my-cluster';
  const create = 'kb admin agent-tokens create --cluster ' + name;
  return h('section', { class: 'empty', 'aria-labelledby': 'empty-title' },
    h('div', { class: 'empty-head' },
      h('div', { class: 'empty-icon' }, icon('clusters', 32, 1.6)),
      h('h2', { id: 'empty-title', text: 'No Clusters Yet' }),
      h('p', { text: 'kbridge reaches a cluster through an agent installed inside it. Run these from a terminal signed in as this admin.' })),
    h('ol', { class: 'steps' },
      step(1, 'Create a token for the cluster', cmdRow(create, 'Copy the token command'),
        h('div', { class: 'step-hint', text: 'The token is printed once. The cluster appears here right away, waiting for its agent.' })),
      installStep(name), waitStep()),
    h('div', { class: 'doc-links' },
      h('a', { href: 'https://github.com/why-xn/kbridge/blob/v0.3.1-alpha.1/docs/installation.md#helm-kubernetes', text: 'Installation Guide ›' }),
      h('a', { href: 'https://github.com/why-xn/kbridge/blob/v0.3.1-alpha.1/docs/production-install.md', text: 'Production Install ›' })));
}

/* ---------- Audit Log ---------- */

const AUDIT_KEYS = ['user', 'cluster', 'namespace', 'status', 'from', 'to', 'command'];
const STATUS_TABS = [['', 'All'], ['success', 'Success'], ['denied', 'Denied'], ['failed', 'Failed'], ['canceled', 'Canceled']];
const STATUS_COLOR = { success: 'green', denied: 'orange', failed: 'red', timeout: 'red', canceled: 'gray' };
const PAGE_SIZES = [25, 50, 100, 200];
const RANGES = [['', 'Any Time'], ['1h', 'Last Hour', 3600e3], ['24h', 'Last 24 Hours', 86400e3], ['7d', 'Last 7 Days', 7 * 86400e3], ['custom', 'Custom Range…']];
const EXPORT_CAP = 10000;
const ANY_TIME = '1970-01-01T00:00:00Z'; // Any Time is explicit so it survives in the hash
const presetFrom = new Map(); // from timestamp -> preset id, so the Time select can name what was picked

function auditFilters(params) {
  const f = {};
  for (const k of AUDIT_KEYS) f[k] = params.get(k) || '';
  f.page = Math.max(1, parseInt(params.get('page'), 10) || 1);
  const per = parseInt(params.get('per_page'), 10);
  f.per_page = PAGE_SIZES.includes(per) ? per : 25;
  return f;
}

function auditQuery(f, page, per) {
  const q = new URLSearchParams();
  for (const k of AUDIT_KEYS) if (f[k]) q.set(k, f[k]);
  if (!f.from && !f.to) q.set('from', new Date(Date.now() - 86400e3).toISOString().slice(0, 19) + 'Z');
  q.set('page', page);
  q.set('per_page', per);
  return '/api/v1/admin/audit?' + q.toString();
}

function auditScreen(params) {
  const st = { f: auditFilters(params), live: true, rows: [], total: 0, seen: null, selected: null, seq: 0 };
  const ui = auditLayout(st);
  st.ui = ui;
  const update = (p) => {
    st.f = auditFilters(p);
    st.seen = null;
    syncFilterForm(st);
    loadAudit(st);
  };
  syncFilterForm(st);
  loadAudit(st);
  every(() => { if (liveEligible(st) && st.live && visible()) loadAudit(st, true); }, 5000);
  return { node: ui.node, update };
}

const liveEligible = (st) => st.f.page === 1 && !st.f.to;

// setFilters reads the hash, not st.f: a blur commits a filter just before a
// click commits another, and hashchange has not caught up yet.
function setFilters(st, changes) {
  const f = auditFilters(parseHash().params);
  go('audit', { ...f, page: '', ...changes, per_page: f.per_page === 25 ? '' : f.per_page });
}

function auditLayout(st) {
  const ui = {};
  ui.live = h('button', { type: 'button', class: 'live', onclick: () => { st.live = !st.live; renderLive(st); } },
    h('span', { class: 'dot', 'aria-hidden': 'true' }), h('span'));
  ui.exportBtn = h('button', { type: 'button', class: 'btn', onclick: () => exportCSV(st) }, icon('download', 15, 2), 'Export CSV');
  ui.exportNote = h('div', { class: 'export-note hidden', role: 'status' });
  ui.seg = h('div', { class: 'seg', role: 'group', 'aria-label': 'Status' }, STATUS_TABS.map(([id, label]) =>
    h('button', { type: 'button', 'data-status': id, text: label, onclick: () => setFilters(st, { status: id }) })));
  ui.form = filterForm(st, ui);
  ui.error = h('div', { class: 'slot' });
  ui.tbody = h('tbody');
  ui.empty = h('p', { class: 'empty-row hidden', text: 'No entries match these filters.' });
  ui.pager = h('nav', { class: 'pager', 'aria-label': 'Pagination' });
  ui.insp = h('aside', { class: 'insp hidden', 'aria-label': 'Inspector' });
  ui.node = mainArea('',
    pageHead('Audit Log', 'Every command attempt through kbridge, newest first.',
      h('div', { class: 'actions' }, ui.live, ui.exportBtn, ui.exportNote)),
    h('div', { class: 'filters' }, ui.seg, ui.form), ui.error,
    h('div', { class: 'split' },
      h('section', { class: 'card', 'aria-label': 'Audit entries' },
        h('div', { class: 'scroll' }, h('table', { class: 'tbl' }, auditHead(), ui.tbody)), ui.empty, ui.pager),
      ui.insp));
  return ui;
}

function auditHead() {
  const cols = [['Time (UTC)'], ['User'], ['Cluster'], ['Namespace'], ['Status'], ['Exit', 'num'], ['Duration', 'num'], ['Command']];
  return h('thead', {}, h('tr', {}, cols.map(([t, cls]) => h('th', { scope: 'col', class: cls || null, text: t }))));
}

function field(label, control) { return h('label', { class: 'field' }, label, control); }

function textFilter(st, key, placeholder, cls) {
  // change fires on Enter or blur: one history entry per committed filter.
  return h('input', { type: key === 'command' ? 'search' : 'text', class: 'input' + (cls ? ' ' + cls : ''), name: key, placeholder,
    autocomplete: 'off', onchange: (e) => setFilters(st, { [key]: e.target.value.trim() }) });
}

function filterForm(st, ui) {
  ui.user = textFilter(st, 'user', 'Any user');
  ui.namespace = textFilter(st, 'namespace', 'Any namespace');
  ui.command = textFilter(st, 'command', 'Search', 'mono');
  ui.cluster = h('select', { class: 'select', onchange: (e) => setFilters(st, { cluster: e.target.value }) });
  ui.range = h('select', { class: 'select', onchange: (e) => pickRange(st, e.target.value) },
    RANGES.map(([id, label]) => h('option', { value: id, text: label })));
  ui.fromInput = h('input', { type: 'datetime-local', class: 'input', step: 1, onchange: (e) => setFilters(st, { from: localToISO(e.target.value) }) });
  ui.toInput = h('input', { type: 'datetime-local', class: 'input', step: 1, onchange: (e) => setFilters(st, { to: localToISO(e.target.value) }) });
  ui.custom = [field('From (UTC)', ui.fromInput), field('To (UTC)', ui.toInput)];
  loadClusterOptions(st, ui.cluster);
  return h('form', { class: 'filter-grid', 'aria-label': 'Filters', onsubmit: (e) => e.preventDefault() },
    field('User', ui.user), field('Cluster', ui.cluster), field('Namespace', ui.namespace), field('Time', ui.range),
    field('Command', h('span', { class: 'search' }, icon('search', 15, 2), ui.command)), ui.custom);
}

// datetime-local carries no zone; the console shows UTC, so read it as UTC.
function localToISO(v) {
  if (!v) return '';
  const full = v.length === 16 ? v + ':00' : v;
  return full + 'Z';
}

function isoToLocal(iso) {
  const d = new Date(iso);
  return isNaN(d) ? '' : d.toISOString().slice(0, 19);
}

function pickRange(st, id) {
  const r = RANGES.find((x) => x[0] === id);
  if (id === 'custom') return showCustom(st.ui, true);
  if (id === '') return setFilters(st, { from: ANY_TIME, to: '' });
  if (id === '24h') return setFilters(st, { from: '', to: '' }); // the default: no from/to in the hash
  const from = new Date(Date.now() - r[2]).toISOString().slice(0, 19) + 'Z';
  presetFrom.set(from, id);
  setFilters(st, { from, to: '' });
}

function showCustom(ui, on) { ui.custom.forEach((n) => n.classList.toggle('hidden', !on)); }

async function loadClusterOptions(st, select) {
  try {
    const res = await api('/api/v1/admin/clusters');
    st.clusterNames = (res.clusters || []).map((c) => c.name);
  } catch (e) {
    st.clusterNames = [];
  }
  fillClusterSelect(st);
}

function fillClusterSelect(st) {
  const names = [...(st.clusterNames || [])];
  if (st.f.cluster && !names.includes(st.f.cluster)) names.push(st.f.cluster);
  st.ui.cluster.replaceChildren(h('option', { value: '', text: 'All Clusters' }), ...names.map((n) => h('option', { value: n, text: n })));
  st.ui.cluster.value = st.f.cluster;
}

function syncFilterForm(st) {
  const { ui, f } = st;
  ui.user.value = f.user;
  ui.namespace.value = f.namespace;
  ui.command.value = f.command;
  fillClusterSelect(st);
  for (const b of ui.seg.children) b.setAttribute('aria-pressed', String(b.dataset.status === f.status));
  const preset = !f.to && presetFrom.get(f.from);
  const id = !f.from && !f.to ? '24h' : f.from === ANY_TIME && !f.to ? '' : preset || 'custom';
  ui.range.value = id;
  ui.fromInput.value = f.from && f.from !== ANY_TIME ? isoToLocal(f.from) : '';
  ui.toInput.value = f.to ? isoToLocal(f.to) : '';
  showCustom(ui, id === 'custom');
  renderLive(st);
}

function renderLive(st) {
  const b = st.ui.live, eligible = liveEligible(st), on = st.live && eligible;
  b.disabled = !eligible;
  b.title = eligible ? '' : 'Live updates run on the first page without an end time';
  b.setAttribute('aria-pressed', String(on));
  b.lastChild.textContent = on ? 'Live' : 'Paused';
}

async function loadAudit(st, poll) {
  const seq = ++st.seq;
  try {
    const res = await api(auditQuery(st.f, st.f.page, st.f.per_page));
    if (seq !== st.seq) return; // a newer filter's load owns the table
    st.ui.error.replaceChildren();
    const fresh = poll && st.seen ? new Set((res.logs || []).map((r) => r.id).filter((id) => !st.seen.has(id))) : new Set();
    st.rows = res.logs || [];
    st.total = res.total || 0;
    st.seen = new Set(st.rows.map((r) => r.id));
    renderAuditRows(st, fresh);
    renderPager(st);
    renderInspector(st);
  } catch (err) {
    if (seq === st.seq) showError(st.ui.error, err, () => loadAudit(st));
  }
}

function renderAuditRows(st, fresh) {
  st.ui.tbody.replaceChildren(...st.rows.map((r) => auditRow(st, r, fresh.has(r.id))));
  st.ui.empty.classList.toggle('hidden', st.rows.length > 0);
}

function statusCell(status) {
  const c = STATUS_COLOR[status] || 'gray';
  return [h('span', { class: 'dot sm bg-' + c, 'aria-hidden': 'true' }), ' ', h('span', { class: 'status c-' + c, text: status })];
}

function auditRow(st, r, isFresh) {
  const sel = st.selected === r.id;
  return h('tr', { class: 'row' + (sel ? ' sel' : '') + (isFresh ? ' fresh' : '') },
    h('td', { class: 'time' }, h('button', { type: 'button', class: 'time-btn', text: auditTime(r.created_at),
      'aria-label': 'Show details for ' + r.command, 'aria-pressed': String(sel), onclick: () => selectRow(st, r.id) })),
    h('td', { text: r.user_email }),
    h('td', { text: r.cluster_name }),
    h('td', { text: r.namespace || '–' }),
    h('td', {}, statusCell(r.status)),
    h('td', { class: 'num', text: r.exit_code == null ? '–' : String(r.exit_code) }),
    h('td', { class: 'num', text: duration(r.duration_ms) }),
    h('td', { class: 'mono cmd-cell', text: r.command }));
}

function selectRow(st, id) {
  st.selected = st.selected === id ? null : id;
  renderAuditRows(st, new Set());
  renderInspector(st);
}

function renderInspector(st) {
  const r = st.rows.find((x) => x.id === st.selected);
  st.ui.insp.classList.toggle('hidden', !r);
  if (!r) return st.ui.insp.replaceChildren();
  st.ui.insp.replaceChildren(...[
    h('div', { class: 'insp-head' }, h('h2', { text: 'Details' }),
      h('button', { type: 'button', class: 'close', 'aria-label': 'Close details', onclick: () => selectRow(st, r.id) }, icon('close', 12, 3))),
    h('div', { class: 'code-block mono', text: 'kb ' + r.command }),
    inspectorMessage(r),
    inspectorFacts(r),
    inspectorLinks(r)].filter(Boolean));
}

function inspectorMessage(r) {
  if (!r.error_message) return null;
  const denied = r.status === 'denied';
  return h('div', { class: 'msg' + (denied ? ' warn' : '') },
    h('div', { class: 'msg-label', text: denied ? 'Shown to the user' : 'Error' }),
    h('div', { class: 'msg-text', text: r.error_message }));
}

function inspectorFacts(r) {
  const d = new Date(r.created_at);
  const c = STATUS_COLOR[r.status] || 'gray';
  const facts = [
    ['Status', h('span', { class: 'status c-' + c, text: r.status })],
    ['Time', dayUTC(d) + ', ' + clockUTC(d) + ' UTC'],
    ['User', h('a', { href: routeHash('audit', { user: r.user_email }), text: r.user_email })],
    ['Cluster', r.cluster_name],
    ['Namespace', r.namespace || '–'],
    ['Exit Code', r.exit_code == null ? '–' : String(r.exit_code)],
    ['Duration', duration(r.duration_ms)],
    ['Client IP', h('span', { class: 'mono', text: r.client_ip || '–' })],
  ];
  return h('dl', { class: 'kv' }, facts.map(([k, v]) => h('div', {}, h('dt', { text: k }), h('dd', {}, v))));
}

// entryLink pins one entry by its exact second; the API has no id filter.
function entryLink(r) {
  const t = new Date(r.created_at).toISOString().slice(0, 19) + 'Z';
  return location.origin + location.pathname + routeHash('audit', { user: r.user_email, cluster: r.cluster_name, from: t, to: t });
}

function inspectorLinks(r) {
  const copy = h('button', { type: 'button', text: 'Copy Link to Entry' });
  copy.addEventListener('click', () => copyText(copy, entryLink(r)));
  return h('div', { class: 'insp-links' },
    h('a', { href: routeHash('audit', { user: r.user_email }), text: 'Show All from This User ›' }), copy);
}

/* Pagination */

// pageList returns page numbers to show (first, current ±1, last) with 0 marking a gap.
function pageList(page, count) {
  const want = [...new Set([1, page - 1, page, page + 1, count])].filter((n) => n >= 1 && n <= count).sort((a, b) => a - b);
  const out = [];
  want.forEach((n, i) => {
    if (i > 0 && n - want[i - 1] > 1) out.push(0);
    out.push(n);
  });
  return out;
}

function renderPager(st) {
  const { page, per_page: per } = st.f;
  const count = Math.max(1, Math.ceil(st.total / per));
  const start = (page - 1) * per;
  const shown = st.rows.length;
  const range = shown ? fmtInt(start + 1) + '–' + fmtInt(start + shown) + ' of ' + fmtInt(st.total) : 'No entries';
  const goPage = (n) => go('audit', { ...st.f, page: n === 1 ? '' : n, per_page: per === 25 ? '' : per });
  const sizes = h('select', { onchange: (e) => go('audit', { ...st.f, page: '', per_page: e.target.value }) },
    PAGE_SIZES.map((n) => h('option', { value: n, text: String(n) })));
  sizes.value = String(per);
  st.ui.pager.replaceChildren(
    h('div', { class: 'pager-left' }, h('label', {}, 'Rows per page', sizes), h('span', { class: 'range', 'aria-live': 'polite', text: range })),
    h('div', { class: 'pages' },
      edgeButton('first', 'First page', page <= 1, () => goPage(1)),
      edgeButton('left', 'Previous page', page <= 1, () => goPage(page - 1)),
      pageList(Math.min(page, count), count).map((n) => (n === 0 ? h('span', { class: 'pg-gap', 'aria-hidden': 'true', text: '…' })
        : h('button', { type: 'button', class: 'pg', text: String(n), 'aria-label': 'Page ' + n,
          'aria-current': n === page ? 'page' : null, onclick: () => goPage(n) }))),
      edgeButton('right', 'Next page', page >= count, () => goPage(page + 1)),
      edgeButton('last', 'Last page', page >= count, () => goPage(count))));
}

function edgeButton(name, label, off, onclick) {
  return h('button', { type: 'button', class: 'pg-edge', 'aria-label': label, disabled: off, onclick }, icon(name, 16, 2.2));
}

/* Export */

const CSV_COLUMNS = [['time', 'created_at'], ['user', 'user_email'], ['cluster', 'cluster_name'], ['namespace', 'namespace'],
  ['status', 'status'], ['exit_code', 'exit_code'], ['duration_ms', 'duration_ms'], ['command', 'command'],
  ['client_ip', 'client_ip'], ['error_message', 'error_message']];

// csvCell quotes every cell and defuses spreadsheet formulas.
function csvCell(v) {
  let s = v == null ? '' : String(v);
  if (/^[=+\-@\t\r]/.test(s)) s = "'" + s;
  return '"' + s.replace(/"/g, '""') + '"';
}

function toCSV(rows) {
  const lines = [CSV_COLUMNS.map(([name]) => csvCell(name)).join(',')];
  for (const r of rows) lines.push(CSV_COLUMNS.map(([, key]) => csvCell(r[key])).join(','));
  return lines.join('\r\n') + '\r\n';
}

// exportWindow pins the time range at export start, so rows that arrive while
// pages load neither repeat at a page boundary nor push rows past the end.
// It ends at the last whole second: rows in the current one may still land.
function exportWindow(f, now) {
  const iso = (ms) => new Date(ms).toISOString().slice(0, 19) + 'Z';
  const end = Math.floor(now / 1000) * 1000 - 1000;
  const from = f.from || (f.to ? '' : iso(now - 86400e3));
  return { ...f, from, to: iso(Math.min(end, f.to ? Date.parse(f.to) : end)) };
}

async function fetchAllAudit(f) {
  const rows = [];
  for (let page = 1; rows.length < EXPORT_CAP; page++) {
    const res = await api(auditQuery(f, page, 200));
    const logs = res.logs || [];
    rows.push(...logs);
    if (!logs.length || rows.length >= (res.total || 0)) return { rows, capped: false };
  }
  return { rows: rows.slice(0, EXPORT_CAP), capped: true };
}

async function exportCSV(st) {
  const { exportBtn: btn, exportNote: note } = st.ui;
  btn.disabled = true;
  note.classList.add('hidden');
  try {
    const { rows, capped } = await fetchAllAudit(exportWindow(st.f, Date.now()));
    download(toCSV(rows), 'kbridge-audit-' + new Date().toISOString().replace(/[-:]/g, '').slice(0, 15) + 'Z.csv');
    note.textContent = capped ? 'First 10,000 rows exported; narrow the filters for the rest.' : '';
    note.classList.toggle('hidden', !capped);
  } catch (err) {
    showError(st.ui.error, err, () => exportCSV(st));
  } finally {
    btn.disabled = false;
  }
}

function download(text, name) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/csv;charset=utf-8' }));
  const a = h('a', { href: url, download: name, class: 'hidden' });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/* ---------- Agent Tokens ---------- */

const TOKEN_VIEWS = [['active', 'Active'], ['revoked', 'Revoked'], ['all', 'All']];
const DAY = 86400000;

function tokensScreen(params) {
  const view = TOKEN_VIEWS.some(([id]) => id === params.get('view')) ? params.get('view') : 'active';
  const seg = h('div', { class: 'seg', role: 'group', 'aria-label': 'Show' }, TOKEN_VIEWS.map(([id, label]) =>
    h('button', { type: 'button', text: label, 'aria-pressed': String(id === view), onclick: () => go('tokens', { view: id === 'active' ? '' : id }) })));
  const body = h('div', { class: 'stack' });
  const node = mainArea('', pageHead('Agent Tokens', 'One per cluster. Secrets are never shown here.', seg), body, tokensNote());
  const load = async () => {
    try {
      const res = await api('/api/v1/admin/agent-tokens');
      body.replaceChildren(tokensTable((res.tokens || []).filter((t) => tokenInView(t, view))));
    } catch (err) {
      showError(body, err, load);
    }
  };
  load();
  return { node };
}

function tokenInView(t, view) {
  return view === 'all' || (view === 'revoked') === t.is_revoked;
}

// tokenState names a token's condition; "needs attention" is never used a week
// after creation, or expiring within two weeks.
function tokenState(t, now = Date.now()) {
  if (t.is_revoked) return ['neu', 'Revoked'];
  const exp = t.expires_at ? new Date(t.expires_at).getTime() : null;
  if (exp !== null && exp <= now) return ['neu', 'Expired'];
  if (exp !== null && exp - now <= 14 * DAY) {
    const days = Math.ceil((exp - now) / DAY);
    return ['warn', 'Expires in ' + days + (days === 1 ? ' day' : ' days')];
  }
  if (!t.last_used_at && now - new Date(t.created_at).getTime() > 7 * DAY) return ['warn', 'Never used'];
  return ['ok', 'Active'];
}

function tokensTable(tokens) {
  const cols = ['Cluster', 'Prefix', 'State', 'Last Used', 'Expires', 'Created'];
  return h('section', { class: 'card', 'aria-label': 'Agent tokens' },
    h('div', { class: 'scroll' }, h('table', { class: 'tbl edged' },
      h('thead', {}, h('tr', {}, cols.map((c) => h('th', { scope: 'col', text: c })))),
      h('tbody', {}, tokens.map(tokenRow)))),
    tokens.length ? null : h('p', { class: 'empty-row', text: 'No tokens in this view.' }));
}

function tokenRow(t) {
  const [cls, state] = tokenState(t);
  return h('tr', {},
    h('td', { class: 'wrap' }, h('div', { class: 'tk-name', text: t.cluster_name }),
      t.description ? h('div', { class: 'tk-desc', text: t.description }) : null),
    h('td', { class: 'mono prefix', text: t.token_prefix + '…' }),
    h('td', {}, h('span', { class: 'badge ' + cls, text: state })),
    h('td', { text: usedWhen(t.last_used_at) }),
    h('td', { text: t.expires_at ? dateUTC(new Date(t.expires_at)) : 'Never' }),
    h('td', { class: 'dim', text: dateUTC(new Date(t.created_at)) }));
}

function tokensNote() {
  return h('div', { class: 'panel' },
    h('strong', { text: 'Needs attention' }),
    ' means never used 7 days after creation, or expiring within 14 days. To rotate, create a token with ',
    h('code', { text: 'kb admin agent-tokens create' }), ', roll the agent, then revoke the old one.');
}

/* ---------- Users ---------- */

const SHOW_CLUSTERS = 3;

function usersScreen() {
  const st = { users: [], query: '', open: new Set() };
  const search = h('input', { type: 'search', placeholder: 'Search', autocomplete: 'off',
    oninput: (e) => { st.query = e.target.value.trim().toLowerCase(); renderUsers(st); } });
  st.body = h('div', { class: 'stack' });
  const node = mainArea('',
    pageHead('Users', 'Accounts that can sign in. What each may run comes from the policy.',
      h('label', { class: 'user-search' }, h('span', { class: 'vh', text: 'Search users' }), icon('search', 15, 2), search)),
    st.body,
    h('p', { class: 'note' }, 'Console Admin is the seeded account. The policy\'s ', h('code', { text: 'admin' }), ' role only governs kubectl access.'));
  const load = async () => {
    try {
      st.users = (await api('/api/v1/admin/users')).users || [];
      renderUsers(st);
    } catch (err) {
      showError(st.body, err, load);
    }
  };
  load();
  return { node };
}

function renderUsers(st) {
  const q = st.query;
  const users = st.users.filter((u) => !q || u.email.toLowerCase().includes(q) || (u.name || '').toLowerCase().includes(q));
  const cols = [['User'], ['Roles'], ['Cluster Access'], ['Last Command'], ['Denied, 7 Days', 'num']];
  st.body.replaceChildren(h('section', { class: 'card', 'aria-label': 'Users' },
    h('div', { class: 'scroll' }, h('table', { class: 'tbl edged' },
      h('thead', {}, h('tr', {}, cols.map(([c, cls]) => h('th', { scope: 'col', class: cls || null, text: c })))),
      h('tbody', {}, users.map((u) => userRow(st, u))))),
    users.length ? null : h('p', { class: 'empty-row', text: q ? 'No users match.' : 'No users yet.' })));
}

function tint(email) {
  let n = 0;
  for (const ch of email) n = (n + ch.charCodeAt(0)) % 5;
  return 'tint' + n;
}

function accountBadge(u) {
  if (!u.is_active) return h('span', { class: 'badge neu', text: 'Disabled' });
  if (u.is_admin) return h('span', { class: 'badge admin', text: 'Console Admin' });
  return null;
}

function userRow(st, u) {
  return h('tr', {},
    h('td', {}, h('div', { class: 'who' },
      h('span', { class: 'avatar ' + tint(u.email), 'aria-hidden': 'true', text: u.email.charAt(0).toUpperCase() }),
      h('div', {}, h('div', { class: 'u-email', text: u.email }), accountBadge(u)))),
    h('td', {}, rolesCell(u)),
    h('td', {}, accessCell(st, u)),
    h('td', {}, lastCommandCell(u)),
    h('td', { class: 'num' }, h('span', { class: 'denied' + (u.denied_7d > 0 ? ' some' : ''), text: String(u.denied_7d || 0) })));
}

function rolesCell(u) {
  if (!u.roles) return h('span', { class: 'dflt', text: 'No policy loaded' });
  return h('div', { class: 'roles' },
    u.roles.length ? u.roles.map((r) => h('span', { class: 'role', text: r })) : h('span', { class: 'dflt', text: 'none' }),
    u.default_role ? h('span', { class: 'dflt', text: 'default' }) : null);
}

// groupAccess folds access lines that share namespaces and level into one
// line per group, clusters sorted by name.
function groupAccess(access) {
  const groups = new Map();
  for (const a of access) {
    const ns = [...(a.namespaces || [])].sort();
    const key = ns.join('\u0000') + '\u0001' + a.level;
    if (!groups.has(key)) groups.set(key, { key, namespaces: ns, level: a.level, clusters: [] });
    groups.get(key).clusters.push(a.cluster);
  }
  for (const g of groups.values()) g.clusters.sort();
  return [...groups.values()];
}

function accessCell(st, u) {
  if (!u.access) return h('span', { class: 'dflt', text: 'Every command allowed' });
  if (!u.access.length) return h('span', { class: 'dflt', text: 'No access' });
  return h('div', { class: 'access' }, groupAccess(u.access).map((g) => accessLine(st, u.email + '\u0002' + g.key, g)));
}

function accessLine(st, key, g) {
  const all = g.clusters.includes('*');
  const more = !all && g.clusters.length > SHOW_CLUSTERS;
  const open = st.open.has(key);
  const names = all ? 'All clusters' : (more && !open ? g.clusters.slice(0, SHOW_CLUSTERS) : g.clusters).join(', ');
  const ns = g.namespaces.includes('*') ? 'all namespaces' : g.namespaces.join(', ');
  const line = h('div', { class: 'acc' },
    h('span', { class: 'acc-where', text: names }),
    more ? h('button', { type: 'button', class: 'acc-toggle', 'aria-expanded': String(open),
      'aria-label': open ? 'Show fewer clusters' : 'Show all ' + g.clusters.length + ' clusters',
      text: open ? 'Show less' : '+' + (g.clusters.length - SHOW_CLUSTERS) + ' more',
      onclick: () => { open ? st.open.delete(key) : st.open.add(key); line.replaceWith(accessLine(st, key, g)); } }) : null,
    h('span', { class: 'muted', text: ns }),
    h('span', { class: 'lvl' + (g.level === 'read-write' ? ' rw' : ''), text: g.level }));
  return line;
}

function lastCommandCell(u) {
  if (!u.last_command) return h('span', { class: 'last-when', text: 'No commands yet' });
  const d = new Date(u.last_command_at);
  const when = sameDayUTC(d, new Date()) ? clockUTC(d).slice(0, 5) : dayUTC(d);
  return [
    h('a', { class: 'mono last-cmd', href: routeHash('audit', { user: u.email }), text: u.last_command }),
    h('div', { class: 'last-when', text: (u.last_cluster ? u.last_cluster + ' · ' : '') + when }),
  ];
}

/* ---------- Sign in ---------- */

const LOGIN_BANNERS = {
  expired: ['info', 'Your session ended', 'Sign in again to continue where you left off.'],
  disabled: ['warn', 'This account is disabled', 'Ask a kbridge admin to re-enable it.'],
};

function showLogin(params) {
  stopTimers();
  current = { name: 'login', update: null };
  if (session.access) return go('clusters');
  const reason = params.get('reason');
  if (reason !== 'expired') returnTo = '';
  const ui = loginLayout();
  if (LOGIN_BANNERS[reason]) loginBanner(ui, ...LOGIN_BANNERS[reason]);
  mount(ui.node);
  ui.email.focus();
}

function loginLayout() {
  const ui = {};
  ui.banner = h('div', { class: 'hidden', role: 'alert' });
  ui.email = h('input', { type: 'email', class: 'linput', autocomplete: 'username', required: true });
  ui.pw = h('input', { id: 'lpw', type: 'password', class: 'linput pw', autocomplete: 'current-password', required: true, 'aria-describedby': 'lpw-err' });
  ui.pwErr = h('div', { id: 'lpw-err', class: 'pw-err hidden', text: 'Email or password is incorrect.' });
  ui.eye = h('button', { type: 'button', class: 'eye', onclick: () => togglePassword(ui) });
  ui.submit = h('button', { type: 'submit', class: 'signin' }, 'Sign In', icon('arrow', 16, 2.2));
  togglePassword(ui, false);
  const form = h('form', { onsubmit: (e) => { e.preventDefault(); signIn(ui); } },
    h('label', { class: 'lfield' }, 'Email', ui.email),
    h('div', { class: 'lfield' }, h('label', { for: 'lpw', text: 'Password' }), h('div', { class: 'pw-wrap' }, ui.pw, ui.eye), ui.pwErr),
    ui.submit);
  ui.node = h('div', { class: 'login' }, arc(),
    h('main', { class: 'login-card' },
      h('div', { class: 'login-top' }, logo(),
        h('div', {}, h('h1', { text: 'Sign in' }), h('p', {}, 'Admin console. Everyone else uses ', h('code', { text: 'kb login' }), '.'))),
      ui.banner, form),
    h('footer', { class: 'login-foot' }, versionLabel(), themeButton()));
  return ui;
}

// versionLabel reads the release tag from /health; without it the footer says just kbridge.
function versionLabel() {
  const span = h('span', { text: 'kbridge' });
  fetch('/health', { cache: 'no-store' }).then((r) => (r.ok ? r.json() : {})).then((b) => {
    if (b && typeof b.version === 'string' && b.version) span.textContent = 'kbridge ' + b.version;
  }).catch(() => {});
  return span;
}

function arc() {
  const s = svgEl('svg', { class: 'arc', viewBox: '0 0 1200 600', preserveAspectRatio: 'xMidYMid slice', 'aria-hidden': 'true' });
  s.append(svgEl('path', { d: 'M120 640 Q600 -120 1080 640', fill: 'none', stroke: 'currentColor', 'stroke-width': 56, 'stroke-linecap': 'round' }));
  return s;
}

function themeButton() {
  const b = h('button', { type: 'button', class: 'theme-btn' });
  const paint = () => {
    b.setAttribute('aria-label', isDark() ? 'Switch to light appearance' : 'Switch to dark appearance');
    b.replaceChildren(icon(isDark() ? 'sun' : 'moon', 14, 2), isDark() ? 'Light' : 'Dark');
  };
  b.addEventListener('click', () => { setTheme(isDark() ? 'light' : 'dark'); paint(); });
  paint();
  return b;
}

function togglePassword(ui, show = ui.pw.type === 'password') {
  ui.pw.type = show ? 'text' : 'password';
  ui.eye.setAttribute('aria-label', show ? 'Hide password' : 'Show password');
  ui.eye.setAttribute('aria-pressed', String(show));
  ui.eye.replaceChildren(icon(show ? 'eyeOff' : 'eye', 18, 1.8));
}

function loginBanner(ui, kind, title, text) {
  ui.banner.className = 'lbanner' + (kind === 'info' ? ' info' : '');
  ui.banner.replaceChildren(h('div', { class: 'lbanner-title', text: title }), h('div', { class: 'lbanner-text', text }));
}

function clearLoginErrors(ui) {
  ui.banner.className = 'hidden';
  ui.pwErr.classList.add('hidden');
  ui.pw.removeAttribute('aria-invalid');
}

async function signIn(ui) {
  clearLoginErrors(ui);
  ui.submit.disabled = true;
  try {
    const res = await send('/auth/login', { method: 'POST', body: { email: ui.email.value.trim(), password: ui.pw.value } }, null);
    await handleLogin(ui, res);
  } catch (err) {
    loginBanner(ui, 'warn', UNREACHABLE, 'Check that it is running and that this browser can reach it.');
  } finally {
    ui.submit.disabled = false;
  }
}

async function handleLogin(ui, res) {
  const body = await readJSON(res);
  if (res.status === 401) return wrongPassword(ui);
  if (res.status === 403) return loginBanner(ui, ...LOGIN_BANNERS.disabled);
  if (!res.ok) return loginBanner(ui, 'warn', 'Sign-in failed (' + res.status + ')', body.error || 'Try again in a moment.');
  const who = claims(body.access_token);
  if (!who.is_admin) {
    await logout(body.access_token, body.refresh_token);
    return loginBanner(ui, 'warn', 'This account cannot use the console',
      (who.email || 'This account') + ' can run commands with kb, but the console is for kbridge admins only.');
  }
  session.save(body);
  const next = returnTo || '#/clusters';
  returnTo = '';
  location.hash = next;
}

function wrongPassword(ui) {
  ui.pw.setAttribute('aria-invalid', 'true');
  ui.pwErr.classList.remove('hidden');
  ui.pw.select();
}

/* ---------- Boot ---------- */

if (typeof document !== 'undefined' && document.getElementById) {
  document.documentElement.dataset.theme = storedTheme();
  window.addEventListener('hashchange', route);
  route();
}
