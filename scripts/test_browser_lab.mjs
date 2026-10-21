#!/usr/bin/env node
import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { configuration, createHandler, safeLocation } from './browser_lab.mjs';

const input = { owner: 'owner@example.test', approvalUrl: 'https://rig.example.ts.net:8444', publicUrl: 'https://rig.example.ts.net:10000' };
test('configuration refuses nonloopback binds and nonprivate UI origins', () => {
  assert.throws(() => configuration(input, { bind: '0.0.0.0' }));
  assert.throws(() => configuration(input, { origin: input.publicUrl }));
  assert.throws(() => configuration(input, { origin: 'https://evil.example:8445' }));
  assert.throws(() => configuration(input, { origin: 'https://rig.example.ts.net:8445/?secret=x' }));
});
test('location metadata drops secret query, fragment and dynamic identifiers', () => {
  assert.deepEqual(safeLocation('https://chatgpt.com/backend-api/apps/1234?code=secret#token'), { origin: 'https://chatgpt.com', path: '/backend-api/apps/[id]' });
  assert(!JSON.stringify(safeLocation('https://chatgpt.com/a/long_secret_123?password=secret')).includes('secret'));
});
test('owner, Host, session, Origin and CSRF protect every browser action', async () => {
  const config = configuration(input), actions = [];
  const server = http.createServer(createHandler(config, {
    screenshot: async () => Buffer.from('fake image'), status: async () => ({ failures: [] }), action: async a => actions.push(a)
  }));
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  const request = (url, headers = {}, method = 'GET', body) => new Promise((resolve, reject) => {
    const req = http.request({ hostname: '127.0.0.1', port, path: url, method, headers }, res => {
      const chunks = []; res.on('data', c => chunks.push(c)); res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks).toString() }));
    }); req.once('error', reject); req.end(body);
  });
  const identity = { Host: config.host, 'Tailscale-User-Login': input.owner };
  try {
    assert.equal((await request('/')).status, 403);
    assert.equal((await request('/', { ...identity, 'Tailscale-User-Login': 'other@example.test' })).status, 403);
    assert.equal((await request('/', { ...identity, Host: 'evil.example' })).status, 403);
    assert.equal((await request('/screenshot', identity)).status, 403);
    const home = await request('/', identity);
    assert.equal(home.status, 200);
    assert.equal(home.headers['cache-control'], 'no-store');
    assert.match(home.headers['set-cookie'][0], /Secure; HttpOnly; SameSite=Strict/);
    assert.match(home.headers['content-security-policy'], /frame-ancestors 'none'/);
    const cookie = home.headers['set-cookie'][0].split(';')[0];
    const csrf = home.body.match(/const csrf="([a-f0-9]+)"/)[1];
    const headers = { ...identity, Cookie: cookie, Origin: config.origin, 'Content-Type': 'application/json', 'X-CSRF-Token': csrf };
    const action = JSON.stringify({ type: 'key', key: 'Tab' });
    assert.equal((await request('/action', { ...headers, Origin: 'https://evil.example' }, 'POST', action)).status, 403);
    assert.equal((await request('/action', { ...headers, 'X-CSRF-Token': 'wrong' }, 'POST', action)).status, 403);
    assert.equal((await request('/action?csrf=' + csrf, headers, 'POST', action)).status, 404);
    assert.equal((await request('/action', headers, 'POST', JSON.stringify({ type: 'eval', code: 'secret' }))).status, 400);
    for (const url of ['http://chatgpt.com', 'https://evil.example', 'https://chatgpt.com@evil.example', 'https://user:secret@chatgpt.com'])
      assert.equal((await request('/action', headers, 'POST', JSON.stringify({ type: 'navigate', url }))).status, 400);
    assert.equal((await request('/action', headers, 'POST', action)).status, 200);
    assert.equal(actions.length, 1);
    const text = 'password-must-not-be-returned';
    const response = await request('/action', headers, 'POST', JSON.stringify({ type: 'text', text }));
    assert.equal(response.status, 200); assert(!response.body.includes(text));
    assert.equal((await request('/action', headers, 'POST', JSON.stringify({ type: 'click', x: -1, y: 10 }))).status, 400);
    assert.equal((await request('/action', headers, 'POST', 'x'.repeat(17000))).status, 413);
    const screenshot = await request('/screenshot', { ...identity, Cookie: cookie });
    assert.equal(screenshot.status, 200); assert.equal(screenshot.headers['cache-control'], 'no-store');
  } finally { await new Promise(resolve => server.close(resolve)); }
});
test('browser errors never expose submitted input or URLs', async () => {
  const config = configuration(input);
  const server = http.createServer(createHandler(config, { screenshot: async () => { throw new Error('secret login payload'); } }));
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const request = (pathname, headers) => new Promise((resolve, reject) => {
    const req = http.get({ hostname: '127.0.0.1', port: server.address().port, path: pathname, headers }, res => {
      let body = ''; res.on('data', c => { body += c; }); res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body }));
    }); req.once('error', reject);
  });
  const headers = { Host: config.host, 'Tailscale-User-Login': input.owner };
  try {
    const home = await request('/', headers); const cookie = home.headers['set-cookie'][0].split(';')[0];
    const result = await request('/screenshot', { ...headers, Cookie: cookie });
    assert.equal(result.status, 500); assert(!result.body.includes('secret'));
  } finally { await new Promise(resolve => server.close(resolve)); }
});
