import http.server
import json
import os
import subprocess
import threading
import unittest


class Handler(http.server.BaseHTTPRequestHandler):
    failure = False

    def log_message(self, format, *args):
        pass

    def reply(self, obj):
        data = json.dumps(obj).encode()
        self.send_response(200)
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path.endswith('/registration'):
            self.reply({'endpoint': 'fixture'})
        else:
            self.reply({'status': 'active', 'id': 'obs1'})

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        if 'observations' in self.path:
            self.reply({'status': 'pending', 'id': 'obs1'})
        elif body['type'] == 'read':
            self.reply({'status': 'succeeded', 'result': {'values': [{'path': '/3/0/15', 'type': 'string', 'value': 'UTC'}]}})
        else:
            self.reply({'status': 'failed' if self.failure else 'succeeded'})

    def do_DELETE(self):
        self.send_response(204)
        self.end_headers()


class CheckTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()

    def run_check(self):
        env = dict(os.environ, LWM2M_REAL_DEVICE_EP='fixture', LWM2M_API_URL='http://127.0.0.1:%d' % self.server.server_port, LWM2M_API_TOKEN='fixture')
        return subprocess.run(['sh', 'scripts/real-device-check.sh'], env=env, capture_output=True, text=True)

    def test_success(self):
        Handler.failure = False
        result = self.run_check()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        for check in ('register', 'read', 'write', 'execute', 'observe'):
            self.assertIn('PASS: ' + check, result.stdout)

    def test_device_failure_is_not_success(self):
        Handler.failure = True
        result = self.run_check()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('FAIL: write', result.stdout)


if __name__ == '__main__':
    unittest.main()
