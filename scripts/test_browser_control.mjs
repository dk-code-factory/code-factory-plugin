#!/usr/bin/env node
// All tests import only pure options; none starts a transport or reads a profile.
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseBrowserOptions, validateBrowserUrl, PILOT_BROWSER_URL } from './browser_options.mjs';

test('no arguments preserve owner-approved auto-connect and all privacy flags', () => {
  assert.deepEqual(parseBrowserOptions(), {
    mode: 'auto', browserUrl: undefined,
    transportArgs: ['--autoConnect', '--no-usage-statistics', '--no-performance-crux', '--redact-network-headers'],
  });
});

test('pilot mode attaches only to the dedicated existing loopback browser', () => {
  const options = parseBrowserOptions(['--pilot']);
  assert.equal(options.mode, 'pilot');
  assert.equal(options.browserUrl, 'http://127.0.0.1:9224');
  assert.equal(PILOT_BROWSER_URL, options.browserUrl);
  assert.deepEqual(options.transportArgs.slice(0, 2), ['--browserUrl', PILOT_BROWSER_URL]);
  assert(!options.transportArgs.includes('--autoConnect'));
  assert(!options.transportArgs.some(value => /headless|user-data-dir|cookie|launch/i.test(value)));
});

test('explicit endpoint accepts split and equals options and exact IPv4 or IPv6 loopback literals', () => {
  for (const [value, canonical] of [
    ['http://127.0.0.1:9224', 'http://127.0.0.1:9224'],
    ['http://[::1]:9224', 'http://[::1]:9224'],
    ['http://127.0.0.1:65535/', 'http://127.0.0.1:65535'],
    ['http://127.0.0.1', 'http://127.0.0.1'],
    ['http://[::1]:80/', 'http://[::1]'],
  ]) {
    assert.equal(validateBrowserUrl(value), canonical);
    for (const argv of [['--browser-url', value], ['--browser-url=' + value]]) {
      const options = parseBrowserOptions(argv);
      assert.equal(options.mode, 'browser-url');
      assert.equal(options.browserUrl, canonical);
      assert.deepEqual(options.transportArgs.slice(0, 2), ['--browserUrl', canonical]);
      assert(!options.transportArgs.includes('--autoConnect'));
    }
  }
});

test('every supported mode retains statistics, CrUX and header protections exactly once', () => {
  for (const argv of [[], ['--pilot'], ['--browser-url=http://[::1]:9224']]) {
    const { transportArgs } = parseBrowserOptions(argv);
    for (const flag of ['--no-usage-statistics', '--no-performance-crux', '--redact-network-headers']) {
      assert.equal(transportArgs.filter(value => value === flag).length, 1);
    }
  }
});

test('conflicting, repeated, missing and unknown options fail closed', () => {
  for (const argv of [
    ['--pilot', '--pilot'], ['--pilot', '--browser-url', PILOT_BROWSER_URL],
    ['--browser-url=' + PILOT_BROWSER_URL, '--pilot'],
    ['--browser-url', PILOT_BROWSER_URL, '--browser-url=' + PILOT_BROWSER_URL],
    ['--browser-url'], ['--browser-url='], ['--browser-url', '--pilot'],
    ['--autoConnect'], ['--headless'], ['--user-data-dir=/tmp/profile'],
    ['--pilot=true'], ['--'], [PILOT_BROWSER_URL], ['--pilot', '--unknown'],
  ]) assert.throws(() => parseBrowserOptions(argv));
  for (const argv of [null, '--pilot', [null], [true], [9224]]) {
    assert.throws(() => parseBrowserOptions(argv));
  }
});

test('endpoint validation rejects remote hosts, normalized aliases and URL metadata', () => {
  for (const value of [
    'https://127.0.0.1:9224', 'ws://127.0.0.1:9224', '//127.0.0.1:9224',
    'http://localhost:9224', 'http://0.0.0.0:9224', 'http://192.168.0.100:9224',
    'http://127.0.0.2:9224', 'http://127.0.0.1.example.test:9224',
    'http://127.1:9224', 'http://2130706433:9224', 'http://0x7f000001:9224',
    'http://127.000.000.001:9224', 'http://%31%32%37.0.0.1:9224',
    'http://[::]:9224', 'http://[::ffff:127.0.0.1]:9224', 'http://[0:0:0:0:0:0:0:1]:9224',
    'http://user:private-secret@127.0.0.1:9224', 'http://127.0.0.1:9224@evil.example',
    'http://127.0.0.1:9224/path', 'http://127.0.0.1:9224/.', 'http://127.0.0.1:9224/%2f',
    'http://127.0.0.1:9224?token=private-secret', 'http://127.0.0.1:9224#private-secret',
    'http://127.0.0.1:9224?', 'http://127.0.0.1:9224#',
    ' http://127.0.0.1:9224', 'http://127.0.0.1:9224\n',
    'http:\\127.0.0.1:9224', 'http://127.0.0.1:0', 'http://127.0.0.1:65536',
    'http://127.0.0.1:abc', '', null, 9224,
  ]) {
    assert.throws(() => validateBrowserUrl(value), error => {
      assert(!error.message.includes('private-secret'));
      assert(!error.message.includes(String(value)) || value === '');
      return true;
    });
    assert.throws(() => parseBrowserOptions(['--browser-url', value]));
  }
});

test('argument parsing is deterministic and cannot mutate inputs or later calls', () => {
  const argv = ['--pilot'];
  const options = parseBrowserOptions(argv);
  assert.deepEqual(argv, ['--pilot']);
  options.transportArgs.push('--untrusted-option');
  assert(!parseBrowserOptions(argv).transportArgs.includes('--untrusted-option'));
  assert.deepEqual(parseBrowserOptions([]), parseBrowserOptions([]));
});
