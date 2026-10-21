#!/usr/bin/env python3
"""Exercise the compiled daemon and its stdio tunnel proxy without provider calls."""
import json
import os
import pathlib
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

root = pathlib.Path(__file__).resolve().parents[1]
binary = root / 'bin/code-factory'
with tempfile.TemporaryDirectory(prefix='code-factory-smoke-') as directory:
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    address = f'127.0.0.1:{port}'
    process = subprocess.Popen([str(binary), 'serve', '--data-dir', directory, '--listen', address], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    try:
        token_file = pathlib.Path(directory) / 'owner.token'
        deadline = time.monotonic() + 10
        while not token_file.exists():
            if process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError('Daemon failed to start')
            time.sleep(.02)
        token = token_file.read_text()
        meta = {'io.modelcontextprotocol/protocolVersion': '2026-07-28', 'io.modelcontextprotocol/clientInfo': {'name': 'code-factory-smoke', 'version': '1'}, 'io.modelcontextprotocol/clientCapabilities': {}}
        request = {'jsonrpc': '2.0', 'id': 1, 'method': 'tools/call', 'params': {'name': 'read_snapshot', 'arguments': {}, '_meta': meta}}
        for attempt in range(100):
            try:
                req = urllib.request.Request(f'http://{address}/mcp', data=json.dumps(request).encode(), headers={'Authorization': f'Bearer {token}', 'Content-Type': 'application/json', 'Accept': 'application/json, text/event-stream', 'MCP-Protocol-Version': '2026-07-28', 'Mcp-Method': 'tools/call', 'Mcp-Name': 'read_snapshot'})
                with urllib.request.urlopen(req, timeout=5) as response:
                    data = json.load(response)
                break
            except urllib.error.URLError:
                if attempt == 99:
                    raise
                time.sleep(.02)
        assert len(data['result']['structuredContent']['data']['profiles']) == 3
        result = subprocess.run([str(binary), 'mcp', '--data-dir', directory, '--listen', address], input=json.dumps(request)+'\n', text=True, capture_output=True, timeout=10, check=True)
        assert json.loads(result.stdout)['result']['structuredContent']['data']['inbox'] == []
        assert token not in result.stdout
        for filename in ('owner.token', 'vault.key', 'state.db'):
            assert (pathlib.Path(directory) / filename).stat().st_mode & 0o777 == 0o600
        print('PASS: compiled daemon, authenticated MCP 2.0, stdio tunnel proxy, private credential files')
    finally:
        process.send_signal(signal.SIGTERM)
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
        if process.returncode != 0:
            raise RuntimeError('Daemon exited abnormally')
