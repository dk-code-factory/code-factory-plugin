#!/usr/bin/env node
/** Independent SDK OAuth and MCP test. Never follows the synthetic ChatGPT callback. */
import { readFile, writeFile, mkdir, chmod } from 'node:fs/promises';
import { homedir } from 'node:os';
import { dirname, join } from 'node:path';
import { randomBytes } from 'node:crypto';
import https from 'node:https';
import { parseArgs } from 'node:util';
import { Client, StreamableHTTPClientTransport, auth } from '../web/node_modules/@modelcontextprotocol/client/dist/index.mjs';

const { values } = parseArgs({ options: {
  endpoint: { type: 'string' }, report: { type: 'string', default: '/tmp/code-factory-sdk-connection.json' },
  'data-dir': { type: 'string', default: join(homedir(), '.local/share/code-factory') },
  'approval-origin': { type: 'string' }, tailnet: { type: 'boolean', default: false },
} });
const config = JSON.parse(await readFile(join(values['data-dir'], 'tailscale.json'), 'utf8'));
const endpointText = values.endpoint ?? config.publicUrl + '/mcp';
const approvalText = values['approval-origin'] ?? config.approvalUrl;
function parseURL(value) { try { return new URL(value); } catch { return undefined; } }
const endpoint = parseURL(endpointText);
const approvalURL = parseURL(approvalText);
const approvalOrigin = approvalURL?.origin;
const publicBase = endpoint && endpoint.origin + endpoint.pathname.slice(0, -'/mcp'.length);
// Strip userinfo and query/fragment before any validation error can be reported.
const report = { at: new Date().toISOString(), endpoint: endpoint ? endpoint.origin + endpoint.pathname : '[invalid endpoint]', sdk: '@modelcontextprotocol/client 2.3.1',
  mode: values.tailnet ? 'tailnet-local-only' : 'public-ingress', checks: [],
  workAcceptance: 'unverified: SDK test does not operate a signed-in ChatGPT session' };
let publicIp;
let stage = 'configuration';
let client;
const issuedTokens = new Set();
class CheckFailure extends Error {}
function requireCheck(condition, reason) { if (!condition) throw new CheckFailure(reason); }
async function record(name, status, details = {}) {
  report.checks.push({ name, status, ...details });
  await mkdir(dirname(values.report), { recursive: true });
  await writeFile(values.report, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
  await chmod(values.report, 0o600);
  process.stdout.write(`${status.toUpperCase()}: ${name}\n`);
}
function safeError(error) {
  const value = { errorType: error?.constructor?.name ?? 'Error' };
  // Arbitrary upstream messages may contain an authorization URL, code, or token.
  if (error instanceof CheckFailure) value.reason = error.message;
  for (const key of ['code', 'status', 'statusCode']) {
    if (typeof error?.[key] === 'number' || /^[A-Z][A-Z0-9_]{1,70}$/.test(error?.[key] ?? '')) value[key] = error[key];
  }
  return value;
}
/** HTTPS fetch adapter pins public ingress while preserving original TLS SNI/hostname. */
async function request(input, init = {}, pinnedIp) {
  const req = new Request(input, init);
  const url = new URL(req.url);
  requireCheck(url.protocol === 'https:' && !url.username && !url.password, 'HTTPS URL without embedded credentials required');
  const body = req.body ? Buffer.from(await req.arrayBuffer()) : undefined;
  return new Promise((resolve, reject) => {
    // A shared keep-alive agent keys sockets by hostname, not custom DNS lookup.
    // Reusing that socket would silently test the previous public IP twice.
    const options = { agent: false, method: req.method, headers: Object.fromEntries(req.headers), signal: req.signal,
      ...(pinnedIp ? { lookup: (_name, opts, cb) => opts.all ? cb(null, [{ address: pinnedIp, family: 4 }]) : cb(null, pinnedIp, 4) } : {}) };
    const connection = https.request(url, options, (response) => {
      const remoteAddress = response.socket?.remoteAddress;
      const chunks = [];
      let bytes = 0;
      response.on('data', (chunk) => {
        bytes += chunk.length;
        if (bytes > 10 * 1024 * 1024) connection.destroy(new CheckFailure('Response exceeded test limit'));
        else chunks.push(chunk);
      });
      response.on('error', reject);
      response.on('end', () => {
        const responseBody = [204, 205, 304].includes(response.statusCode) ? null : Buffer.concat(chunks);
        const headers = new Headers();
        for (const [key, value] of Object.entries(response.headers)) {
          if (value !== undefined) headers.set(key, Array.isArray(value) ? value.join(', ') : value);
        }
        const result = new Response(responseBody, { status: response.statusCode, headers });
        Object.defineProperty(result, 'url', { value: url.href });
        Object.defineProperty(result, 'observedRemoteAddress', { value: remoteAddress });
        resolve(result);
      });
    });
    connection.once('socket', (socket) => socket.once('secureConnect', () => {
      if (pinnedIp && socket.remoteAddress !== pinnedIp && socket.remoteAddress !== '::ffff:' + pinnedIp) {
        connection.destroy(new CheckFailure('TLS connection did not reach the requested public ingress IP'));
      }
    }));
    connection.setTimeout(15000, () => connection.destroy(new CheckFailure('HTTPS request timed out')));
    connection.on('error', reject);
    if (body) connection.write(body);
    connection.end();
  });
}
async function sdkFetch(input, init) {
  const url = new URL(input instanceof Request ? input.url : input);
  // SDK discovery must not leak credentials to unexpected origins.
  requireCheck(url.origin === endpoint.origin, 'SDK requested an unexpected network origin');
  return request(input, init, publicIp);
}
const memory = { state: randomBytes(32).toString('base64url') };
const callback = 'https://chatgpt.com/connector_platform_oauth_redirect';
const provider = {
  redirectUrl: callback,
  clientMetadata: { client_name: 'Code Factory independent SDK self-test', redirect_uris: [callback],
    token_endpoint_auth_method: 'none', grant_types: ['authorization_code', 'refresh_token'], response_types: ['code'] },
  state: () => memory.state,
  clientInformation: () => memory.client,
  saveClientInformation: (value) => { memory.client = value; },
  tokens: () => memory.tokens,
  saveTokens: (value) => {
    memory.tokens = value;
    if (value.access_token) issuedTokens.add(value.access_token);
    if (value.refresh_token) issuedTokens.add(value.refresh_token);
  },
  redirectToAuthorization: (value) => { memory.authorization = value; },
  saveCodeVerifier: (value) => { memory.verifier = value; },
  codeVerifier: () => memory.verifier,
  discoveryState: () => memory.discovery,
  saveDiscoveryState: (value) => { memory.discovery = value; },
};
try {
  requireCheck(endpoint && endpoint.protocol === 'https:' && !endpoint.username && !endpoint.password, 'HTTPS endpoint without embedded credentials required');
  const rawEndpointPath = endpointText.match(/^https:\/\/[^/?#]+(\/[^?#]*)?/)?.[1];
  requireCheck(/^(?:\/[A-Za-z0-9_-]+)*\/mcp$/.test(endpoint.pathname)
    && rawEndpointPath === endpoint.pathname && !endpointText.includes('?') && !endpointText.includes('#'),
    'Endpoint must end in /mcp with a canonical unencoded base path and no query or fragment');
  requireCheck(approvalURL && approvalURL.protocol === 'https:' && !approvalURL.username && !approvalURL.password
    && approvalURL.pathname === '/' && approvalText === approvalURL.origin,
    'Approval URL must be an HTTPS origin without credentials, path, query or fragment');
  stage = 'public DNS and TLS';
  const dns = await request('https://dns.google/resolve?name=' + encodeURIComponent(endpoint.hostname) + '&type=A');
  const addresses = [...new Set((await dns.json()).Answer?.filter((row) => row.type === 1).map((row) => row.data) ?? [])];
  requireCheck(addresses.length > 0, 'Public DNS supplied no ingress address');
  for (const address of addresses) {
    try {
      const response = await request(publicBase + '/healthz', {}, address);
      requireCheck(response.status === 200, 'Public ingress returned unhealthy HTTP status');
      publicIp ??= address;
      await record('public TLS ingress ' + address, 'passed', { remoteAddress: response.observedRemoteAddress });
    } catch (error) { await record('public TLS ingress ' + address, 'failed', safeError(error)); }
  }
  const failedAddresses = report.checks.filter((check) => check.status === 'failed').length;
  report.publicIngress = !publicIp ? 'failed from this machine using public DNS IP'
    : failedAddresses ? 'partial: at least one public DNS IP failed from this machine'
    : 'verified from this machine using every public DNS IP';
  requireCheck(values.tailnet || publicIp, 'Public ingress failed; use --tailnet only for explicitly local verification');
  if (values.tailnet) publicIp = undefined;
  stage = 'SDK OAuth discovery, DCR and PKCE setup';
  const challenge = await sdkFetch(endpoint);
  requireCheck(challenge.status === 401 && challenge.headers.has('www-authenticate'), 'Unauthenticated MCP did not advertise OAuth');
  requireCheck(await auth(provider, { serverUrl: endpoint, fetchFn: sdkFetch }) === 'REDIRECT', 'SDK did not start authorization');
  requireCheck(memory.client && memory.verifier && memory.authorization, 'SDK OAuth setup incomplete');
  await record(stage, 'passed');
  stage = 'real Tailscale Serve owner approval';
  const authorization = new URL(memory.authorization);
  requireCheck(authorization.origin + authorization.pathname === publicBase + '/authorize', 'Unexpected authorization endpoint');
  const start = await sdkFetch(authorization);
  requireCheck(start.status === 302, 'Authorization did not redirect to private consent');
  const consent = new URL(start.headers.get('location'));
  requireCheck(consent.origin === approvalOrigin && consent.pathname === '/approve', 'Authorization selected unexpected approval origin');
  const page = await request(consent);
  requireCheck(page.status === 200, 'Private Serve did not recognize this Tailscale owner');
  const nonce = (await page.text()).match(/name="nonce" value="([^"]+)"/)?.[1];
  requireCheck(nonce && consent.searchParams.get('request'), 'Private consent form was incomplete');
  const approval = await request(approvalOrigin + '/approve', { method: 'POST', headers: { Origin: approvalOrigin,
    'Content-Type': 'application/x-www-form-urlencoded' }, body: new URLSearchParams({
      request: consent.searchParams.get('request'), nonce, decision: 'allow' }).toString() });
  requireCheck(approval.status === 303, 'Private owner consent failed');
  const returned = new URL(approval.headers.get('location'));
  requireCheck(returned.origin + returned.pathname === callback && returned.searchParams.get('state') === memory.state,
    'Synthetic OAuth callback or state mismatch');
  requireCheck(returned.searchParams.get('code'), 'OAuth callback omitted authorization code');
  // This URL is parsed in memory only, never fetched or written to the report.
  requireCheck(await auth(provider, { serverUrl: endpoint, authorizationCode: returned.searchParams.get('code'),
    iss: returned.searchParams.get('iss') ?? undefined, fetchFn: sdkFetch }) === 'AUTHORIZED', 'SDK rejected OAuth callback');
  await record('real owner consent and SDK OAuth PKCE token exchange', 'passed');
  stage = 'SDK transport negotiation';
  client = new Client({ name: 'code-factory-independent-sdk-test', version: '1' }, { capabilities: {} });
  const transport = new StreamableHTTPClientTransport(endpoint, { authProvider: provider, fetch: sdkFetch });
  await client.connect(transport);
  await record(stage, 'passed', { server: client.getServerVersion()?.name });
  stage = 'SDK tool discovery and open_factory';
  const tools = await client.listTools();
  const tool = tools.tools.find((value) => value.name === 'open_factory');
  requireCheck(tool, 'SDK tool list omitted open_factory');
  const result = await client.callTool({ name: 'open_factory', arguments: {} });
  requireCheck(!result.isError && result.structuredContent?.data, 'SDK open_factory did not return application data');
  await record(stage, 'passed', { toolCount: tools.tools.length });
  stage = 'SDK embedded UI resource';
  const uri = tool._meta?.ui?.resourceUri;
  requireCheck(typeof uri === 'string', 'open_factory omitted UI resource reference');
  const resource = await client.readResource({ uri });
  requireCheck(resource.contents.some((value) => value.mimeType === 'text/html;profile=mcp-app' && typeof value.text === 'string' && value.text.length > 0),
    'SDK embedded UI resource was missing or empty');
  await record(stage, 'passed');
  report.result = values.tailnet ? 'passed-local-only'
    : report.checks.some((check) => check.status === 'failed') ? 'failed' : 'passed';
} catch (error) {
  report.result = 'failed';
  await record(stage, 'failed', safeError(error));
} finally {
  if (client) await client.close().catch(() => {});
  let cleanup = true;
  for (const token of issuedTokens) {
    try {
      const response = await sdkFetch(publicBase + '/revoke', { method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: new URLSearchParams({ token }).toString() });
      cleanup = response.status === 200 && cleanup;
    } catch { cleanup = false; }
  }
  if (issuedTokens.size) await record('test access and refresh credentials revoked', cleanup ? 'passed' : 'failed');
  if (!cleanup) report.result = 'failed';
  // DCR has no client-deletion endpoint. One nonsecret test registration remains;
  // no authorization token or verifier is persisted by this script.
  report.testClientRegistrationCreated = Boolean(memory.client);
  memory.tokens = memory.verifier = memory.authorization = undefined;
  issuedTokens.clear();
  await writeFile(values.report, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
  await chmod(values.report, 0o600);
}
process.exitCode = report.result === 'failed' ? 1 : 0;
