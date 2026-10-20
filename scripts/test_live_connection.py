#!/usr/bin/env python3
"""Test the deployed public gateway without ChatGPT credentials or model calls.

Public TLS uses public DNS addresses explicitly, retaining hostname verification;
the consent browser route uses real private Tailscale Serve owner identity.
Reports contain only check names/statuses, never tokens, codes, or inbox data.
"""
import argparse
import base64
import hashlib
import http.client
import ipaddress
import json
import pathlib
import re
import secrets
import socket
import ssl
import time
import urllib.parse
import urllib.request


class CheckFailure(Exception):
    pass


def public_addresses(host):
    request = urllib.request.Request('https://dns.google/resolve?' + urllib.parse.urlencode({'name': host, 'type': 'A'}), headers={'Accept': 'application/dns-json'})
    with urllib.request.urlopen(request, timeout=15) as response:
        answer = json.load(response)
    return sorted({row['data'] for row in answer.get('Answer', []) if row.get('type') == 1})


def request(url, method='GET', body=None, headers=None, public_ip=None):
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme != 'https' or parsed.username or parsed.password:
        raise CheckFailure('HTTPS endpoint without embedded credentials required')
    connection = http.client.HTTPSConnection(parsed.hostname, parsed.port or 443, timeout=15, context=ssl.create_default_context())
    if public_ip:
        # Connect to public ingress while HTTPSConnection retains original SNI
        # and verifies that original hostname against system CA certificates.
        connection._create_connection = lambda address, timeout, source_address=None, **kwargs: socket.create_connection((public_ip, address[1]), timeout, source_address)
    try:
        path = urllib.parse.urlunsplit(('', '', parsed.path or '/', parsed.query, ''))
        connection.request(method, path, body=body, headers=headers or {})
        response = connection.getresponse()
        payload = response.read(10 * 1024 * 1024 + 1)
        if len(payload) > 10 * 1024 * 1024:
            raise CheckFailure('Response exceeded test limit')
        return response.status, dict(response.getheaders()), payload
    finally:
        connection.close()


class ConnectionTest:
    def __init__(self, endpoint, approval_origin, report_path, cache_path):
        parsed = urllib.parse.urlsplit(endpoint)
        approval = urllib.parse.urlsplit(approval_origin)
        if (parsed.scheme != 'https' or not parsed.hostname or parsed.username or parsed.password
                or parsed.query or parsed.fragment or not parsed.path.endswith('/mcp')):
            raise CheckFailure('Endpoint must be an HTTPS MCP URL without credentials, query, or fragment')
        if (approval.scheme != 'https' or not approval.hostname or approval.username or approval.password
                or approval.query or approval.fragment or approval.path not in ('', '/')):
            raise CheckFailure('Approval URL must be an HTTPS origin without credentials')
        self.endpoint = endpoint.rstrip('/')
        self.origin = self.endpoint.removesuffix('/mcp')
        self.approval_origin = approval_origin.rstrip('/')
        self.report_path = pathlib.Path(report_path)
        self.cache_path = pathlib.Path(cache_path)
        self.report = {'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), 'endpoint': self.endpoint, 'checks': [], 'workAcceptance': 'unverified: this test does not operate a signed-in ChatGPT session'}
        self.public_ip = None
        self.tokens = []
        self.sequence = 0

    def record(self, name, status, **details):
        self.report['checks'].append({'name': name, 'status': status, **details})
        self.report_path.parent.mkdir(parents=True, exist_ok=True)
        self.report_path.write_text(json.dumps(self.report, indent=2) + '\n')
        self.report_path.chmod(0o600)
        print(status.upper() + ': ' + name, flush=True)

    def require(self, condition, message):
        if not condition:
            raise CheckFailure(message)

    def public(self, path, method='GET', value=None, form=False, headers=None):
        headers = dict(headers or {})
        payload = None
        if value is not None:
            headers['Content-Type'] = 'application/x-www-form-urlencoded' if form else 'application/json'
            payload = urllib.parse.urlencode(value).encode() if form else json.dumps(value).encode()
        return request(self.origin + path, method, payload, headers, self.public_ip)

    def rpc(self, method, params, token):
        self.sequence += 1
        headers = {'Authorization': 'Bearer ' + token, 'Accept': 'application/json, text/event-stream', 'MCP-Protocol-Version': '2025-11-25', 'Mcp-Method': method}
        if params.get('name'):
            headers['Mcp-Name'] = params['name']
        status, _, body = self.public('/mcp', 'POST', {'jsonrpc': '2.0', 'id': self.sequence, 'method': method, 'params': params}, headers=headers)
        self.require(status == 200, 'MCP HTTP request failed')
        result = json.loads(body)
        self.require(not result.get('error'), 'MCP protocol rejected request')
        return result['result']

    def probes(self):
        host = urllib.parse.urlsplit(self.origin).hostname
        ips = public_addresses(host)
        self.require(bool(ips), 'Public DNS supplied no ingress address')
        for ip in ips:
            self.require(ipaddress.ip_address(ip).is_global, 'DNS supplied a non-public address')
            try:
                status, _, _ = request(self.origin + '/healthz', public_ip=ip)
                self.require(status == 200, 'Public gateway health failed')
                self.public_ip = self.public_ip or ip
                self.record('public TLS ingress ' + ip, 'passed')
            except (OSError, http.client.HTTPException, CheckFailure) as error:
                self.record('public TLS ingress ' + ip, 'failed', errorType=type(error).__name__)
        self.require(self.public_ip is not None, 'No public ingress completed TLS and health; tailnet reachability cannot substitute')
        status, headers, _ = self.public('/mcp')
        self.require(status == 401 and any(k.lower() == 'www-authenticate' for k in headers), 'MCP did not advertise protected-resource discovery')
        status, _, body = self.public('/.well-known/oauth-protected-resource/mcp')
        metadata = json.loads(body)
        self.require(status == 200 and metadata['resource'] == self.endpoint and metadata['authorization_servers'] == [self.origin], 'Protected resource does not match endpoint')
        status, _, body = self.public('/.well-known/oauth-authorization-server')
        oauth = json.loads(body)
        self.require(status == 200 and oauth['issuer'] == self.origin and 'S256' in oauth['code_challenge_methods_supported'], 'OAuth issuer or PKCE discovery is incompatible')
        self.record('OAuth discovery and unauthenticated MCP challenge', 'passed')
        for path in ('/approve', '/owner.token', '/gateway-auth.json'):
            self.require(self.public(path)[0] == 404, 'Private endpoint is public')
        self.record('private application state and approval unavailable publicly', 'passed')

    def authorize(self):
        cached = {}
        if self.cache_path.exists():
            cached = json.loads(self.cache_path.read_text())
        client_id = cached.get(self.endpoint)
        redirect = 'https://chatgpt.com/connector_platform_oauth_redirect'
        if not client_id:
            status, _, body = self.public('/register', 'POST', {'client_name': 'Code Factory self-test', 'redirect_uris': [redirect], 'token_endpoint_auth_method': 'none', 'grant_types': ['authorization_code', 'refresh_token'], 'response_types': ['code']})
            self.require(status == 201, 'Public dynamic client registration failed')
            client_id = json.loads(body)['client_id']
            cached[self.endpoint] = client_id
            self.cache_path.parent.mkdir(parents=True, exist_ok=True)
            self.cache_path.write_text(json.dumps(cached) + '\n')
            self.cache_path.chmod(0o600)
        self.record('public OAuth client registration', 'passed')
        verifier = secrets.token_urlsafe(48)
        challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip('=')
        state = secrets.token_urlsafe(32)
        query = urllib.parse.urlencode({'client_id': client_id, 'redirect_uri': redirect, 'response_type': 'code', 'resource': self.endpoint, 'code_challenge_method': 'S256', 'code_challenge': challenge, 'state': state})
        status, headers, _ = self.public('/authorize?' + query)
        location = next((v for k, v in headers.items() if k.lower() == 'location'), '')
        self.require(status == 302 and location.startswith(self.approval_origin + '/approve?'), 'Authorization did not select private owner approval')
        status, _, page = request(location)
        self.require(status == 200, 'Private Tailscale approval cannot identify the owner')
        nonce = re.search(rb'name="nonce" value="([^"]+)"', page)
        self.require(nonce is not None, 'Approval nonce missing')
        session = urllib.parse.parse_qs(urllib.parse.urlsplit(location).query)['request'][0]
        body = urllib.parse.urlencode({'request': session, 'nonce': nonce.group(1).decode(), 'decision': 'allow'}).encode()
        status, headers, _ = request(self.approval_origin + '/approve', 'POST', body, {'Origin': self.approval_origin, 'Content-Type': 'application/x-www-form-urlencoded'})
        callback = next((v for k, v in headers.items() if k.lower() == 'location'), '')
        self.require(status == 303 and callback.startswith(redirect + '?'), 'Owner consent failed')
        result = urllib.parse.parse_qs(urllib.parse.urlsplit(callback).query)
        self.require(result.get('state') == [state] and result.get('iss') == [self.origin], 'OAuth callback state or issuer mismatch')
        # Do not follow a synthetic callback into the real ChatGPT account. Redeem
        # the code directly as this test client instead.
        status, _, body = self.public('/token', 'POST', {'grant_type': 'authorization_code', 'client_id': client_id, 'redirect_uri': redirect, 'code': result['code'][0], 'code_verifier': verifier, 'resource': self.endpoint}, form=True)
        self.require(status == 200, 'PKCE token exchange failed')
        tokens = json.loads(body)
        self.tokens.extend([tokens['access_token'], tokens['refresh_token']])
        self.record('real owner-only Tailscale consent and OAuth PKCE exchange', 'passed')
        return client_id, tokens

    def full(self):
        client_id, tokens = self.authorize()
        self.rpc('initialize', {'protocolVersion': '2025-11-25', 'capabilities': {}, 'clientInfo': {'name': 'code-factory-self-test', 'version': '1'}}, tokens['access_token'])
        result = self.rpc('tools/list', {}, tokens['access_token'])
        tool = next((t for t in result['tools'] if t['name'] == 'open_factory'), None)
        self.require(tool is not None, 'open_factory missing from deployed MCP tools')
        result = self.rpc('tools/call', {'name': 'open_factory', 'arguments': {}}, tokens['access_token'])
        self.require(not result.get('isError') and 'data' in result.get('structuredContent', {}), 'open_factory did not return app state')
        resource = tool.get('_meta', {}).get('ui', {}).get('resourceUri')
        self.require(bool(resource), 'MCP App resource is not advertised')
        result = self.rpc('resources/read', {'uri': resource}, tokens['access_token'])
        self.require(bool(result.get('contents')), 'Embedded app resource unavailable')
        self.record('MCP initialize, tools/list, open_factory and embedded UI resource', 'passed')
        status, _, body = self.public('/token', 'POST', {'grant_type': 'refresh_token', 'client_id': client_id, 'resource': self.endpoint, 'refresh_token': tokens['refresh_token']}, form=True)
        self.require(status == 200, 'Refresh failed')
        renewed = json.loads(body)
        self.tokens.extend([renewed['access_token'], renewed['refresh_token']])
        status, _, _ = self.public('/token', 'POST', {'grant_type': 'refresh_token', 'client_id': client_id, 'resource': self.endpoint, 'refresh_token': tokens['refresh_token']}, form=True)
        self.require(status == 400, 'Used refresh token was accepted')
        self.record('refresh rotation and replay rejection', 'passed')

    def run(self, full=False):
        try:
            self.probes()
            if full:
                self.full()
            self.report['result'] = 'passed' if all(c['status'] == 'passed' for c in self.report['checks']) else 'failed'
        except (CheckFailure, OSError, ValueError, KeyError, http.client.HTTPException) as error:
            self.record('connection test stopped', 'failed', errorType=type(error).__name__)
            # CheckFailure strings are fixed literals. Do not persist arbitrary
            # upstream exception strings which may include secret-bearing URLs.
            if isinstance(error, CheckFailure):
                self.report['reason'] = str(error)
            self.report['result'] = 'failed'
        finally:
            cleanup_ok = True
            for token in self.tokens:
                try:
                    cleanup_ok = self.public('/revoke', 'POST', {'token': token}, form=True)[0] == 200 and cleanup_ok
                except (OSError, http.client.HTTPException):
                    cleanup_ok = False
            if self.tokens:
                self.record('ephemeral test credentials revoked', 'passed' if cleanup_ok else 'failed')
            if not cleanup_ok:
                self.report['result'] = 'failed'
            self.report_path.write_text(json.dumps(self.report, indent=2) + '\n')
            self.report_path.chmod(0o600)
        return self.report['result'] == 'passed'


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data-dir', type=pathlib.Path, default=pathlib.Path.home() / '.local/share/code-factory')
    parser.add_argument('--endpoint')
    parser.add_argument('--approval-origin')
    parser.add_argument('--full', action='store_true', help='Also test private owner consent, ephemeral OAuth and read-only MCP calls')
    parser.add_argument('--report', type=pathlib.Path, default=pathlib.Path('/tmp/code-factory-live-connection.json'))
    args = parser.parse_args()
    config = json.loads((args.data_dir / 'tailscale.json').read_text())
    test = ConnectionTest(args.endpoint or config['publicUrl'] + '/mcp', args.approval_origin or config['approvalUrl'], args.report, args.data_dir / 'self-test-clients.json')
    raise SystemExit(0 if test.run(args.full) else 1)
