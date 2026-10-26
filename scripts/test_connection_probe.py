#!/usr/bin/env python3
import http.client
import json
import threading
import unittest

from connection_probe import DEFAULT_BASE, Server, configuration, handler


class ProbeTests(unittest.TestCase):
    def test_configuration(self):
        self.assertEqual(configuration()[0], "https://example.invalid/code-factory-probe")
        for base in ['http://host/path', 'https://user:pass@host/path', 'https://host/path?key=secret',
                     'https://host/path#secret', 'https://host/../unsafe', 'https://host/a%2fb', 'https://host:99999/a']:
            with self.assertRaises(ValueError):
                configuration(base)
        with self.assertRaises(ValueError):
            configuration(bind='0.0.0.0')

    def test_discovery_and_non_authorization(self):
        records = []
        server = Server(('127.0.0.1', 0), handler(logger=records.append))
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()

        def request(path, method='GET', body=None):
            client = http.client.HTTPConnection(*server.server_address, timeout=2)
            client.request(method, path, body, {'Authorization': 'Bearer NEVER_LOG_THIS'})
            response = client.getresponse()
            result = response.status, dict(response.getheaders()), json.loads(response.read())
            client.close()
            return result

        try:
            for path in ['/mcp', '/code-factory-probe/mcp']:
                for method in ['GET', 'POST']:
                    status, headers, payload = request(path + '?code=NEVER_LOG_THIS', method, 'secret-login-body')
                    self.assertEqual(status, 401)
                    self.assertEqual(headers['WWW-Authenticate'], 'Bearer resource_metadata="' + DEFAULT_BASE + '/.well-known/oauth-protected-resource/mcp"')
                    self.assertEqual(payload, {'error': 'probe_only'})
            for path in ['/.well-known/oauth-protected-resource/mcp', '/code-factory-probe/.well-known/oauth-protected-resource/mcp', '/.well-known/oauth-protected-resource/code-factory-probe/mcp']:
                status, _, payload = request(path)
                self.assertEqual(status, 200)
                self.assertEqual(payload, {'resource': DEFAULT_BASE + '/mcp', 'authorization_servers': [DEFAULT_BASE], 'scopes_supported': ['code-factory']})
            for path in ['/.well-known/oauth-authorization-server', '/code-factory-probe/.well-known/oauth-authorization-server', '/.well-known/oauth-authorization-server/code-factory-probe']:
                status, _, payload = request(path)
                self.assertEqual(status, 200)
                self.assertEqual(payload['issuer'], DEFAULT_BASE)
                for name, endpoint in [('authorization', 'authorize'), ('token', 'token'), ('registration', 'register'), ('revocation', 'revoke')]:
                    self.assertEqual(payload[name + '_endpoint'], DEFAULT_BASE + '/' + endpoint)
                self.assertEqual(payload['token_endpoint_auth_methods_supported'], ['none'])
                self.assertEqual(payload['code_challenge_methods_supported'], ['S256'])
            for path in ['/authorize', '/token', '/register', '/revoke', '/code-factory-probe/register']:
                for method in ['GET', 'POST']:
                    status, _, payload = request(path, method, 'NEVER_LOG_THIS')
                    self.assertEqual(status, 501)
                    self.assertEqual(payload, {'error': 'probe_only'})
            self.assertEqual(request('/healthz')[2], {'status': 'probe_only'})
            self.assertEqual(request('/.well-known/oauth-authorization-server', 'POST', 'secret')[0], 405)
            self.assertEqual(request('/NEVER_LOG_THIS')[0], 404)
            self.assertNotIn('NEVER_LOG_THIS', json.dumps(records))
            self.assertTrue(records)
            for record in records:
                self.assertEqual(set(record), {'endpoint', 'status', 'timestamp', 'count'})
        finally:
            server.shutdown()
            server.server_close()
            worker.join()


if __name__ == '__main__':
    unittest.main()
