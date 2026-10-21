#!/usr/bin/env python3
import json
import pathlib
import ssl
import tempfile
import unittest
from unittest.mock import patch

from test_live_connection import CheckFailure, ConnectionTest


class LiveConnectionHarnessTests(unittest.TestCase):
    def instance(self, root):
        return ConnectionTest('https://pilot.example:10000/mcp', 'https://pilot.example:8444', root/'report.json', root/'clients.json')

    def test_private_dns_success_cannot_mask_public_tls_failure(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            test = self.instance(root)
            with patch('test_live_connection.public_addresses', return_value=['199.38.181.54', '209.177.145.137']), patch('test_live_connection.request', side_effect=ssl.SSLError('secret-bearing-error-detail')), patch.object(test, 'authorize') as authorize:
                self.assertFalse(test.run(full=True))
                authorize.assert_not_called()
            text = (root/'report.json').read_text()
            report = json.loads(text)
            self.assertEqual(sum(c['status']=='failed' for c in report['checks']), 3)
            self.assertNotIn('secret-bearing-error-detail', text)
            self.assertIn('unverified', report['workAcceptance'])

    def test_ephemeral_tokens_are_revoked_after_protocol_error(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = pathlib.Path(temporary)
            test = self.instance(root)
            def failed():
                test.tokens = ['hidden-access-token', 'hidden-refresh-token']
                raise ValueError('hidden-provider-response')
            with patch.object(test, 'probes'), patch.object(test, 'full', side_effect=failed), patch.object(test, 'public', return_value=(200, {}, b'')) as public:
                self.assertFalse(test.run(full=True))
                self.assertEqual(public.call_count, 2)
            text = (root/'report.json').read_text()
            for secret in ('hidden-access-token', 'hidden-refresh-token', 'hidden-provider-response'):
                self.assertNotIn(secret, text)
            self.assertEqual((root/'report.json').stat().st_mode & 0o777, 0o600)

    def test_report_cannot_include_secret_endpoint_parameters(self):
        for endpoint in ('https://user:secret@pilot.example/mcp', 'https://pilot.example/mcp?token=secret'):
            with self.assertRaises(CheckFailure):
                ConnectionTest(endpoint, 'https://pilot.example:8444', '/tmp/unused-report', '/tmp/unused-cache')


if __name__ == '__main__':
    unittest.main()
