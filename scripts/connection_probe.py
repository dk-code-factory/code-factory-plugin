#!/usr/bin/env python3
"""Temporary, credential-free OAuth discovery fixture. Never authorizes clients."""
import argparse
import datetime
import http.server
import json
import re
import signal
import threading
import urllib.parse

DEFAULT_BASE = 'https://dkropachev-ai-rig.tail62c9f9.ts.net/code-factory-probe'


def configuration(base_url=DEFAULT_BASE, bind='127.0.0.1'):
    parsed = urllib.parse.urlsplit(base_url)
    if (bind != '127.0.0.1' or parsed.scheme != 'https' or not parsed.hostname
            or parsed.username is not None or parsed.password is not None
            or parsed.query or parsed.fragment
            or not re.fullmatch(r'[A-Za-z0-9.-]+(?::[0-9]{1,5})?', parsed.netloc)
            or not re.fullmatch(r'(?:/[A-Za-z0-9_-]+)*/?', parsed.path)):
        raise ValueError('Use loopback bind and a plain HTTPS base URL with safe path segments')
    try:
        if parsed.port is not None and not 1 <= parsed.port <= 65535:
            raise ValueError('Invalid HTTPS port')
    except ValueError:
        raise ValueError('Invalid HTTPS port') from None
    return base_url.rstrip('/'), parsed.path.rstrip('/')


def handler(base_url=DEFAULT_BASE, logger=None):
    base, prefix = configuration(base_url)
    protected = {'resource': base + '/mcp', 'authorization_servers': [base], 'scopes_supported': ['code-factory']}
    authorization = {
        'issuer': base, 'authorization_endpoint': base + '/authorize',
        'token_endpoint': base + '/token', 'registration_endpoint': base + '/register',
        'revocation_endpoint': base + '/revoke', 'response_types_supported': ['code'],
        'grant_types_supported': ['authorization_code', 'refresh_token'],
        'token_endpoint_auth_methods_supported': ['none'],
        'code_challenge_methods_supported': ['S256'], 'scopes_supported': ['code-factory'],
        'authorization_response_iss_parameter_supported': True,
    }
    routes = {}
    for root in {'', prefix}:
        for suffix, label in {
            '/mcp': 'mcp', '/healthz': 'health',
            '/.well-known/oauth-protected-resource/mcp': 'resource_metadata',
            '/.well-known/oauth-protected-resource': 'resource_metadata',
            '/.well-known/oauth-authorization-server': 'authorization_metadata',
            '/authorize': 'authorize', '/token': 'token', '/register': 'register', '/revoke': 'revoke',
        }.items():
            routes[root + suffix] = label
    # RFC discovery for an issuer/resource URL containing a path prefix.
    routes['/.well-known/oauth-authorization-server' + prefix] = 'authorization_metadata'
    routes['/.well-known/oauth-protected-resource' + prefix + '/mcp'] = 'resource_metadata'
    counts = {}
    count_lock = threading.Lock()

    class Probe(http.server.BaseHTTPRequestHandler):
        server_version = 'CodeFactoryProbe'
        sys_version = ''

        def log_message(self, *_):
            pass  # Base class otherwise logs raw paths, queries and request lines.

        def record(self, label, status):
            with count_lock:
                counts[label] = counts.get(label, 0) + 1
                record = {'endpoint': label, 'status': status,
                          'timestamp': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                          'count': counts[label]}
                if logger:
                    logger(record)

        def respond(self, status, payload, label, headers=None):
            self.close_connection = True  # Never read or parse a submitted body.
            data = json.dumps(payload, separators=(',', ':')).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Cache-Control', 'no-store')
            self.send_header('Content-Length', str(len(data)))
            self.send_header('Connection', 'close')
            for key, value in (headers or {}).items():
                self.send_header(key, value)
            self.end_headers()
            if self.command != 'HEAD':
                try:
                    self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError):
                    pass
            self.record(label, status)

        def send_error(self, code, message=None, explain=None):
            self.respond(code, {'error': 'invalid_request'}, 'invalid_request')

        def dispatch(self):
            label = routes.get(urllib.parse.urlsplit(self.path).path, 'unknown')
            if label == 'mcp':
                return self.respond(401, {'error': 'probe_only'}, label, {
                    'WWW-Authenticate': 'Bearer resource_metadata="' + base + '/.well-known/oauth-protected-resource/mcp"'
                })
            if label in {'authorize', 'token', 'register', 'revoke'}:
                return self.respond(501, {'error': 'probe_only'}, label)
            if label == 'unknown':
                return self.respond(404, {'error': 'not_found'}, label)
            if self.command not in {'GET', 'HEAD'}:
                return self.respond(405, {'error': 'method_not_allowed'}, label)
            payload = {'status': 'probe_only'} if label == 'health' else protected if label == 'resource_metadata' else authorization
            return self.respond(200, payload, label)

        do_GET = dispatch
        do_HEAD = dispatch
        do_POST = dispatch
        do_PUT = dispatch
        do_DELETE = dispatch
        do_PATCH = dispatch
        do_OPTIONS = dispatch

    return Probe


class Server(http.server.ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = False

    def handle_error(self, *_):
        pass  # Never print request data or exception contents.

    def get_request(self):
        sock, address = super().get_request()
        sock.settimeout(5)
        return sock, address


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-url', default=DEFAULT_BASE)
    parser.add_argument('--bind', default='127.0.0.1')
    parser.add_argument('--port', type=int, default=8794)
    args = parser.parse_args()
    configuration(args.base_url, args.bind)
    if not 1 <= args.port <= 65535:
        parser.error('Invalid port')
    server = Server((args.bind, args.port), handler(args.base_url, lambda record: print(json.dumps(record), flush=True)))
    stop = lambda *_: threading.Thread(target=server.shutdown, daemon=True).start()
    timer = threading.Timer(15 * 60, stop)
    timer.daemon = True
    timer.start()
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        server.serve_forever()
    finally:
        timer.cancel()
        server.server_close()


if __name__ == '__main__':
    main()
