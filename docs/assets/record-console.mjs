#!/usr/bin/env node
// Records the kbridge admin console flow (sign in, Clusters, Audit Log, Users)
// as PNG frames at a fixed rate, for assembling into the README GIF with ffmpeg.
//
// Usage:
//   KB_URL=http://localhost:8081/ KB_EMAIL=admin@kbridge.local KB_PASSWORD=... \
//   KB_THEME=dark|light KB_FRAMES=./frames-dark node record-console.mjs
// Optional: KB_FILTER_USER (default dev@example.com), KB_FPS (default 8),
//   KB_WIDTH x KB_HEIGHT viewport (default 1760x1080: Clusters fits with no scroll and the audit table is not clipped with details open),
//   CHROME (default: newest chrome-headless-shell under ~/.cache/ms-playwright).
// The demo needs at least one denied row for KB_FILTER_USER, or the run fails.
// Run it against a control plane seeded with a few clusters, users and audit
// rows (none of it real), once per theme, then assemble each frame set:
//   ffmpeg -framerate 8 -i frames-dark/f%04d.png \
//     -vf "scale=1200:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=256[p];[b][p]paletteuse" \
//     docs/assets/console.gif
// and the same with frames-light into docs/assets/console-light.gif.
import { spawn } from 'node:child_process';
import { mkdirSync, readdirSync, rmSync, writeFileSync, existsSync, copyFileSync } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import { join } from 'node:path';

const env = process.env;
const BASE = (env.KB_URL || process.argv[2] || 'http://localhost:8081/').replace(/\/?$/, '/');
const EMAIL = env.KB_EMAIL || 'admin@kbridge.local';
const PASSWORD = env.KB_PASSWORD;
const THEME = env.KB_THEME || 'dark';
const OUT = env.KB_FRAMES || `./frames-${THEME}`;
const FILTER_USER = env.KB_FILTER_USER || 'dev@example.com';
const FPS = Number(env.KB_FPS || 8);
const W = Number(env.KB_WIDTH || 1760), H = Number(env.KB_HEIGHT || 1080);
if (!PASSWORD) { console.error('set KB_PASSWORD to the admin password'); process.exit(2); }
if (!['dark', 'light'].includes(THEME)) { console.error('KB_THEME must be dark or light'); process.exit(2); }

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function findChrome() {
  if (env.CHROME) return env.CHROME;
  const root = join(homedir(), '.cache/ms-playwright');
  const dir = readdirSync(root).filter((d) => d.startsWith('chromium_headless_shell-')).sort().pop();
  if (!dir) throw new Error(`no chromium_headless_shell in ${root}; set CHROME`);
  const sub = readdirSync(join(root, dir)).find((d) => existsSync(join(root, dir, d, 'chrome-headless-shell')));
  return join(root, dir, sub, 'chrome-headless-shell');
}

async function launch() {
  const profile = join(tmpdir(), `kb-rec-${process.pid}`);
  const proc = spawn(findChrome(), ['--remote-debugging-port=0', `--user-data-dir=${profile}`,
    `--window-size=${W},${H}`, '--hide-scrollbars', '--font-render-hinting=none', 'about:blank'],
    { stdio: ['ignore', 'ignore', 'pipe'] });
  const ws = await new Promise((resolve, reject) => {
    let buf = '';
    proc.stderr.on('data', (d) => { buf += d; const m = buf.match(/ws:\/\/\S+/); if (m) resolve(m[0]); });
    proc.on('exit', (c) => reject(new Error(`chrome exited ${c}: ${buf}`)));
  });
  const list = await (await fetch(new URL('/json/list', ws.replace('ws:', 'http:')))).json();
  return { proc, profile, pageWs: list.find((t) => t.type === 'page').webSocketDebuggerUrl };
}

function cdp(url) {
  const sock = new WebSocket(url);
  let id = 0;
  const pending = new Map();
  sock.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id && pending.has(m.id)) {
      const { resolve, reject } = pending.get(m.id); pending.delete(m.id);
      m.error ? reject(new Error(m.error.message)) : resolve(m.result);
    }
  };
  const send = (method, params = {}) => new Promise((resolve, reject) => {
    pending.set(++id, { resolve, reject }); sock.send(JSON.stringify({ id, method, params }));
  });
  return new Promise((r) => { sock.onopen = () => r({ send, close: () => sock.close() }); });
}

// A drawn pointer: headless screenshots carry no cursor, and the viewer needs to see the clicks.
const CURSOR_JS = `(() => {
  if (document.getElementById('rec-cursor')) return;
  const c = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  c.id = 'rec-cursor'; c.setAttribute('width', '22'); c.setAttribute('height', '22'); c.setAttribute('viewBox', '0 0 22 22');
  c.innerHTML = '<path d="M3 2 L3 18 L7.5 14 L10.5 20.5 L13 19.4 L10 13 L16 13 Z" fill="#fff" stroke="#111" stroke-width="1.4" stroke-linejoin="round"/>';
  Object.assign(c.style, { position: 'fixed', left: '0px', top: '0px', zIndex: 2147483647, pointerEvents: 'none', transition: 'none' });
  document.documentElement.appendChild(c);
})()`;

async function main() {
  rmSync(OUT, { recursive: true, force: true });
  mkdirSync(OUT, { recursive: true });
  const { proc, profile, pageWs } = await launch();
  const c = await cdp(pageWs);
  const evalJs = async (expr) => {
    const r = await c.send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(`${expr}: ${r.exceptionDetails.exception?.description || r.exceptionDetails.text}`);
    return r.result.value;
  };
  await c.send('Page.enable');
  await c.send('Runtime.enable');
  await c.send('Page.setBypassCSP', { enabled: true }); // lets the drawn cursor in; the console itself is unchanged
  await c.send('Emulation.setDeviceMetricsOverride', { width: W, height: H, deviceScaleFactor: 1, mobile: false });
  await c.send('Page.addScriptToEvaluateOnNewDocument', { source: `try { localStorage.setItem('kb.theme', ${JSON.stringify(THEME)}) } catch (e) {}` });

  const waitFor = async (expr, what, ms = 10000) => {
    for (const end = Date.now() + ms; Date.now() < end; await sleep(50)) if (await evalJs(expr)) return;
    throw new Error(`timed out waiting for ${what}`);
  };
  const center = (sel) => evalJs(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); if (!e) return null;
    e.scrollIntoView({ block: 'nearest' }); const r = e.getBoundingClientRect(); return [r.x + r.width / 2, r.y + r.height / 2]; })()`);
  let mouse = [W - 180, H - 100];
  const moveTo = async ([x, y], ms = 450) => {
    const [x0, y0] = mouse, steps = Math.max(1, Math.round(ms / 45));
    for (let i = 1; i <= steps; i++) {
      const t = i / steps, e = t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2;
      const p = [x0 + (x - x0) * e, y0 + (y - y0) * e];
      await c.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: p[0], y: p[1] });
      await evalJs(`(() => { const s = document.getElementById('rec-cursor'); if (s) { s.style.left = '${p[0] - 3}px'; s.style.top = '${p[1] - 2}px'; } })()`);
      await sleep(30);
    }
    mouse = [x, y];
  };
  const click = async (sel) => {
    const p = await center(sel);
    if (!p) throw new Error(`no element ${sel}`);
    await moveTo(p);
    for (const type of ['mousePressed', 'mouseReleased']) {
      await c.send('Input.dispatchMouseEvent', { type, x: p[0], y: p[1], button: 'left', clickCount: 1 });
      await sleep(60);
    }
  };
  const type = async (text, perChar = 55) => {
    for (const ch of text) { await c.send('Input.insertText', { text: ch }); await sleep(perChar); }
  };
  const enter = async () => {
    for (const t of ['keyDown', 'keyUp']) {
      await c.send('Input.dispatchKeyEvent', { type: t, key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, ...(t === 'keyDown' ? { text: '\r' } : {}) });
    }
  };
  const addCursor = () => evalJs(CURSOR_JS).then(() => moveTo(mouse, 1));

  // Load and settle before the clock starts, so frame 0 is a finished sign-in page.
  await c.send('Page.navigate', { url: BASE + '#/login' });
  await waitFor(`!!document.querySelector('input[type=email]')`, 'sign-in form');
  await sleep(600);
  const theme = await evalJs(`document.documentElement.dataset.theme`);
  if (theme !== THEME) throw new Error(`console is in ${theme} theme, wanted ${THEME}`);
  await addCursor();

  // Frame clock: frame n is the screen at n/FPS seconds; a slow capture repeats the last frame.
  const frameMs = 1000 / FPS;
  let recording = true, last = -1;
  const t0 = Date.now();
  const capture = (async () => {
    while (recording) {
      const n = Math.floor((Date.now() - t0) / frameMs);
      if (n > last) {
        const shot = await c.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
        const file = join(OUT, `f${String(n).padStart(4, '0')}.png`);
        writeFileSync(file, Buffer.from(shot.data, 'base64'));
        for (let k = last + 1; k < n; k++) copyFileSync(join(OUT, `f${String(last).padStart(4, '0')}.png`), join(OUT, `f${String(k).padStart(4, '0')}.png`));
        last = n;
      }
      await sleep(5);
    }
  })();

  const mark = (what) => console.log(`${((Date.now() - t0) / 1000).toFixed(1)}s ${what}`);
  try {
    await sleep(300);
    await click('input[type=email]');
    await type(EMAIL, 25);
    await click('input[type=password]');
    await type(PASSWORD, 25);
    await sleep(200);
    mark('typed');
    await click('button.signin');
    await waitFor(`location.hash.startsWith('#/clusters') && document.querySelectorAll('section.card > *').length > 0`, 'clusters');
    if (await evalJs(`!!document.querySelector('.pw-err:not(.hidden), [role=alert]:not(.hidden)')`)) throw new Error('sign-in error shown');
    mark('clusters');
    await addCursor();
    const fit = await evalJs(`[document.documentElement.scrollHeight, innerHeight]`);
    console.log(`clusters page ${fit[0]}px tall in a ${fit[1]}px viewport`);
    if (fit[0] > fit[1]) throw new Error(`Clusters needs scrolling: raise KB_HEIGHT to at least ${fit[0]}`);
    await moveTo([W - 130, H - 80], 400);
    await sleep(2200);

    mark('audit');
    await click('a[href="#/audit"]');
    await waitFor(`document.querySelectorAll('tr.row').length > 0`, 'audit rows');
    await sleep(900);
    await click('input[name=user]');
    await type(FILTER_USER, 25);
    await enter();
    mark('filtered');
    await waitFor(`[...document.querySelectorAll('tr.row')].length > 0 && [...document.querySelectorAll('tr.row')].every((r) => r.children[1].textContent === ${JSON.stringify(FILTER_USER)})`, 'filtered rows');
    await sleep(700);
    const denied = await evalJs(`(() => { const rows = [...document.querySelectorAll('tr.row')];
      const i = rows.findIndex((r) => r.querySelector('.status')?.textContent === 'denied'); if (i < 0) return null;
      rows[i].querySelector('.time-btn').dataset.rec = 'denied'; return i; })()`);
    if (denied == null) throw new Error(`no denied row for ${FILTER_USER}`);
    mark('row');
    await click('[data-rec=denied]');
    await waitFor(`[...document.querySelectorAll('.msg-label')].some((e) => e.textContent === 'Shown to the user')`, 'details panel');
    const clip = await evalJs(`(() => { const w = document.querySelector('.scroll'); return w.scrollWidth - w.clientWidth; })()`);
    if (clip > 0) throw new Error(`audit table clipped by ${clip}px with details open: raise KB_WIDTH`);
    await moveTo([W / 2, H - 40], 400);
    await sleep(2300);

    mark('users');
    await click('a[href="#/users"]');
    await waitFor(`[...document.querySelectorAll('th')].some((e) => e.textContent === 'Cluster Access') && document.querySelectorAll('tbody tr').length > 1`, 'users');
    await moveTo([W - 130, H - 60], 400);
    await sleep(2200);
  } finally {
    recording = false;
    await capture;
    c.close();
    proc.kill();
    await sleep(300);
    rmSync(profile, { recursive: true, force: true });
  }
  console.log(`${last + 1} frames at ${FPS} fps (${((last + 1) / FPS).toFixed(1)} s) in ${OUT}`);
}

main().catch((e) => { console.error(e.message); process.exit(1); });
