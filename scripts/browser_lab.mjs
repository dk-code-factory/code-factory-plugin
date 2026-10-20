#!/usr/bin/env node
// Deliberately separate from the gateway. No main-browser profile access.
import http from 'node:http';
import net from 'node:net';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { spawn, execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

export function configuration(config, overrides = {}) {
  const approval = new URL(config.approvalUrl);
  const origin = overrides.origin || `https://${approval.hostname}:8445`;
  const parsed = new URL(origin);
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.pathname !== '/' || parsed.search || parsed.hash || parsed.port !== '8445' || parsed.hostname !== approval.hostname)
    throw new Error('Browser UI must use the private approval host on HTTPS port 8445');
  if (!config.owner || /[\r\n]/.test(config.owner)) throw new Error('Missing Tailscale owner identity');
  if ((overrides.bind || '127.0.0.1') !== '127.0.0.1') throw new Error('Only 127.0.0.1 bind is allowed');
  return { owner: config.owner, origin: parsed.origin, host: parsed.host,
    allowed: new Set(['https://chatgpt.com', 'https://auth.openai.com', new URL(config.publicUrl).origin, approval.origin]) };
}

export function safeLocation(raw) {
  try {
    const url = new URL(raw);
    // Never keep queries, fragments, userinfo, or arbitrary long path identifiers.
    return { origin: url.origin, path: url.pathname.split('/').map(s => /^[a-zA-Z_-]{1,48}$/.test(s) ? s : s ? '[id]' : '').join('/') };
  } catch { return { origin: '[unknown]', path: '/' }; }
}

export function createHandler(config, driver) {
  const sessions = new Map();
  const cookieName = '__Host-cf-browser-lab';
  const send = (res, status, body, type = 'text/plain; charset=utf-8', extra = {}) => {
    res.writeHead(status, { 'Content-Type': type, 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff', 'Referrer-Policy': 'no-referrer',
      'Content-Security-Policy': "default-src 'none'; frame-ancestors 'none'", ...extra });
    res.end(body);
  };
  return async (req, res) => {
    if (req.headers.host !== config.host || req.headers['tailscale-user-login'] !== config.owner)
      return send(res, 403, 'Owner-only private Tailscale access required');
    const pathname = req.url;
    const cookies = String(req.headers.cookie || '').split(';').map(s => s.trim());
    const sessionId = cookies.find(s => s.startsWith(cookieName + '='))?.slice(cookieName.length + 1);
    const session = sessions.get(sessionId);
    const now = Date.now();
    for (const [key, value] of sessions) if (value.expires < now) sessions.delete(key);
    if (pathname === '/' && req.method === 'GET') {
      if (sessions.size > 20) sessions.delete(sessions.keys().next().value);
      const id = crypto.randomBytes(32).toString('hex');
      const csrf = crypto.randomBytes(32).toString('hex');
      const nonce = crypto.randomBytes(20).toString('hex');
      sessions.set(id, { csrf, expires: now + 8 * 60 * 60 * 1000 });
      return send(res, 200, html(csrf, nonce), 'text/html; charset=utf-8', {
        'Set-Cookie': `${cookieName}=${id}; Secure; HttpOnly; SameSite=Strict; Path=/`,
        'Content-Security-Policy': `default-src 'none'; script-src 'nonce-${nonce}'; style-src 'nonce-${nonce}'; img-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'` });
    }
    if (!session || session.expires < now) return send(res, 403, 'Session expired; reload private UI');
    try {
      if (req.method === 'GET' && pathname === '/screenshot') return send(res, 200, await driver.screenshot(), 'image/png');
      if (req.method === 'GET' && pathname === '/status') return send(res, 200, JSON.stringify(await driver.status()), 'application/json');
      if (req.method !== 'POST' || pathname !== '/action') return send(res, 404, 'Not found');
      if (req.headers.origin !== config.origin || req.headers['x-csrf-token'] !== session.csrf || req.headers['content-type'] !== 'application/json')
        return send(res, 403, 'Origin or CSRF check failed');
      let body = '';
      for await (const chunk of req) {
        body += chunk.toString();
        if (Buffer.byteLength(body) > 16384) return send(res, 413, 'Input too large');
      }
      const action = JSON.parse(body);
      if (!action || typeof action !== 'object') return send(res, 400, 'Invalid action');
      if (action.type === 'navigate') {
        const url = new URL(action.url);
        if (!config.allowed.has(url.origin) || url.username || url.password || url.protocol !== 'https:') return send(res, 400, 'Navigation origin not allowed');
      } else if (action.type === 'click') {
        if (![action.x, action.y].every(Number.isInteger) || action.x < 0 || action.x >= 1280 || action.y < 0 || action.y >= 800) return send(res, 400, 'Invalid coordinates');
      } else if (action.type === 'text') {
        if (typeof action.text !== 'string' || action.text.length > 4096) return send(res, 400, 'Invalid input');
      } else if (action.type === 'key') {
        if (!['Tab', 'Shift+Tab', 'Enter', 'Escape', 'Backspace', 'Delete', 'ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight', 'Control+a'].includes(action.key)) return send(res, 400, 'Key not allowed');
      } else if (action.type === 'scroll') {
        if (!Number.isInteger(action.y) || Math.abs(action.y) > 1600) return send(res, 400, 'Invalid scroll');
      } else return send(res, 400, 'Action not allowed');
      await driver.action(action);
      return send(res, 200, '{"ok":true}', 'application/json');
    } catch {
      // Browser errors may contain input values and full URLs. Never emit them.
      return send(res, 500, 'Browser action failed; refresh viewport and retry');
    }
  };
}

function html(csrf, nonce) {
  return `<!doctype html><meta charset="utf-8"><title>Private Code Factory browser lab</title>
<style nonce="${nonce}">body{font:16px system-ui;margin:20px;background:#f6f7f8}button,input{font:inherit;margin:4px;padding:7px}#view{max-width:100%;border:1px solid #555;cursor:crosshair}#text{width:30em;max-width:80%}pre{white-space:pre-wrap}</style>
<h1>Private browser lab</h1><p>Separate test profile. Sign in yourself. Tell the agent “signed in” when finished. Agent must pause during login. Screenshots and input are never saved by this service.</p>
<p>Click viewport to focus a field. Enter text below, then press Send text; use Enter button to submit. Input clears immediately.</p>
<input id="url" aria-label="HTTPS URL" value="https://chatgpt.com"><button id="go">Navigate</button><button id="refresh">Refresh</button><br>
<input id="text" type="password" autocomplete="off" aria-label="Text to send to focused browser field"><button id="send">Send text</button>
<div id="keys"></div><button id="up">Scroll up</button><button id="down">Scroll down</button><p id="message"></p>
<img id="view" alt="Live test browser viewport" width="1280" height="800"><pre id="status"></pre>
<script nonce="${nonce}">
const csrf=${JSON.stringify(csrf)},view=document.getElementById('view'),message=document.getElementById('message');let imageURL;
async function refresh(){try{const r=await fetch('/screenshot',{cache:'no-store'});if(!r.ok)throw Error('Reload this page to renew session');const u=URL.createObjectURL(await r.blob());view.src=u;if(imageURL)URL.revokeObjectURL(imageURL);imageURL=u;const s=await fetch('/status',{cache:'no-store'});document.getElementById('status').textContent=JSON.stringify(await s.json(),null,2)}catch(e){message.textContent=e.message}}
async function act(a){try{const r=await fetch('/action',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(a)});message.textContent=r.ok?'Done':await r.text();setTimeout(refresh,350)}catch{message.textContent='Connection failed'}}
document.getElementById('go').onclick=()=>act({type:'navigate',url:document.getElementById('url').value});document.getElementById('refresh').onclick=refresh;
document.getElementById('send').onclick=()=>{const i=document.getElementById('text'),v=i.value;i.value='';act({type:'text',text:v})};
for(const key of ['Tab','Shift+Tab','Enter','Escape','Backspace','Control+a']){const b=document.createElement('button');b.textContent=key;b.onclick=()=>act({type:'key',key});document.getElementById('keys').append(b)}
view.onclick=e=>{const r=view.getBoundingClientRect();act({type:'click',x:Math.floor((e.clientX-r.left)*1280/r.width),y:Math.floor((e.clientY-r.top)*800/r.height)})};
document.getElementById('up').onclick=()=>act({type:'scroll',y:-600});document.getElementById('down').onclick=()=>act({type:'scroll',y:600});refresh();
</script>`;
}

async function freePort(port) {
  await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', () => reject(new Error(`Loopback port ${port} already occupied; no process was stopped`)));
    server.listen(port, '127.0.0.1', () => server.close(resolve));
  });
}

export async function main(args = process.argv.slice(2)) {
  if (args.some(a => a !== '--headless' && a !== '--check')) throw new Error('Usage: node scripts/browser_lab.mjs [--headless] [--check]');
  process.umask(0o077);
  const data = path.join(os.homedir(), '.local/share/code-factory/tailscale.json');
  const config = configuration(JSON.parse(await fs.readFile(data, 'utf8')));
  const state = path.join(os.homedir(), '.local/state/code-factory/browser-lab');
  await fs.mkdir(state, { recursive: true, mode: 0o700 });
  const st = await fs.lstat(state);
  if (!st.isDirectory() || st.isSymbolicLink() || st.uid !== process.getuid()) throw new Error('Unsafe browser-lab state directory');
  await fs.chmod(state, 0o700);
  await freePort(8790); await freePort(9223);
  const { chromium } = await import('../web/node_modules/playwright/index.mjs');
  await fs.access(chromium.executablePath());
  if (args.includes('--check')) { console.log(`Browser lab preflight passed; private UI origin ${config.origin}; not started`); return; }
  const lock = path.join(state, 'lab.lock');
  let handle;
  try { handle = await fs.open(lock, 'wx', 0o600); }
  catch { throw new Error('Browser lab lock exists; inspect its PID and profile locks before removing it. No process was stopped'); }
  await handle.writeFile(String(process.pid));
  let xvfb, context, server, authfile;
  let stopping = false;
  const cleanup = async () => {
    if (stopping) return; stopping = true;
    server?.close();
    await context?.close().catch(() => {});
    if (xvfb) { xvfb.kill('SIGTERM'); await new Promise(resolve => { const t=setTimeout(resolve,2000);xvfb.once('exit',()=>{clearTimeout(t);resolve()}) }); }
    if (authfile) await fs.unlink(authfile).catch(() => {});
    await handle.close(); await fs.unlink(lock).catch(() => {});
  };
  process.once('SIGTERM', () => cleanup().finally(() => process.exit(0)));
  process.once('SIGINT', () => cleanup().finally(() => process.exit(0)));
  try {
    const env = { ...process.env };
    const profile = path.join(state, 'profile');
    await fs.mkdir(profile, { mode: 0o700 }).catch(e => { if (e.code !== 'EEXIST') throw e; });
    const profileStat = await fs.lstat(profile);
    if (!profileStat.isDirectory() || profileStat.isSymbolicLink() || profileStat.uid !== process.getuid()) throw new Error('Unsafe dedicated profile directory');
    await fs.chmod(profile, 0o700);
    if (!args.includes('--headless')) {
      let display;
      for (let n = 93; n < 120; n++) {
        try { await fs.lstat(`/tmp/.X${n}-lock`); } catch (e) { if (e.code === 'ENOENT') { display = n; break; } else throw e; }
      }
      if (!display) throw new Error('No free dedicated X display');
      authfile = path.join(state, 'Xauthority');
      await fs.writeFile(authfile, '', { mode: 0o600 });
      execFileSync('xauth', ['-f', authfile, 'add', `:${display}`, '.', crypto.randomBytes(16).toString('hex')], { stdio: 'ignore' });
      xvfb = spawn('Xvfb', [`:${display}`, '-screen', '0', '1280x800x24', '-nolisten', 'tcp', '-auth', authfile], { stdio: 'ignore' });
      await new Promise((resolve, reject) => { const timer = setTimeout(resolve, 500); xvfb.once('error', reject); xvfb.once('exit', () => { clearTimeout(timer); reject(new Error('Dedicated Xvfb startup failed')); }); });
      env.DISPLAY = `:${display}`; env.XAUTHORITY = authfile;
    }
    context = await chromium.launchPersistentContext(profile, { headless: args.includes('--headless'), env,
      viewport: { width: 1280, height: 800 }, args: ['--remote-debugging-address=127.0.0.1', '--remote-debugging-port=9223'], acceptDownloads: false });
    let page = context.pages()[0] || await context.newPage();
    const failures = [];
    context.on('page', p => { page = p; });
    context.on('response', response => {
      if (response.status() < 400 || new URL(response.url()).origin !== 'https://chatgpt.com') return;
      const headers = response.headers();
      const rawId = headers['x-request-id'] || headers['request-id'] || headers['cf-ray'];
      const requestId = typeof rawId === 'string' && /^[a-zA-Z0-9._-]{1,128}$/.test(rawId) ? rawId : undefined;
      failures.push({ ...safeLocation(response.url()), status: response.status(), ...(requestId ? { requestId } : {}), at: new Date().toISOString() });
      if (failures.length > 100) failures.shift();
    });
    const driver = {
      screenshot: () => page.screenshot({ type: 'png', timeout: 5000 }),
      status: async () => ({ page: safeLocation(page.url()), failures, login: 'User-managed; agent must wait for explicit signed-in confirmation' }),
      action: async a => {
        if (a.type === 'navigate') await page.goto(a.url, { waitUntil: 'domcontentloaded', timeout: 30000 });
        if (a.type === 'click') await page.mouse.click(a.x, a.y);
        if (a.type === 'text') await page.keyboard.insertText(a.text);
        if (a.type === 'key') await page.keyboard.press(a.key);
        if (a.type === 'scroll') await page.mouse.wheel(0, a.y);
      }
    };
    server = http.createServer(createHandler(config, driver));
    server.requestTimeout = 40000; server.headersTimeout = 10000;
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(8790, '127.0.0.1', resolve); });
    await page.goto('https://chatgpt.com', { waitUntil: 'domcontentloaded', timeout: 30000 }).catch(() => {});
    console.log(`Browser lab ready at private origin ${config.origin}; CDP loopback 127.0.0.1:9223. No Tailscale route was added.`);
  } catch (error) { await cleanup(); throw error; }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1]))
  main().catch(() => { console.error('Browser lab failed. Check private configuration, free ports, lab/profile locks, Xvfb and installed Chromium. No credentials or browser errors logged.'); process.exitCode = 1; });
