#!/usr/bin/env python3
"""Exercise a registered test endpoint through the native API."""
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

base = os.environ.get('LWM2M_API_URL', 'http://127.0.0.1:8080').rstrip('/')
token = os.environ.get('LWM2M_API_TOKEN', '')
ep = urllib.parse.quote(os.environ['LWM2M_REAL_DEVICE_EP'], safe='')
device = '/v1/devices/' + ep
write_path = os.environ.get('LWM2M_WRITE_PATH', '/3/0/15')
execute_path = os.environ.get('LWM2M_EXECUTE_PATH', '/3303/0/5605')
observe_path = os.environ.get('LWM2M_OBSERVE_PATH', '/3303/0/5700')
failed = False


def request(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base + path, data=data, method=method,
                                 headers={'Authorization': 'Bearer ' + token,
                                          'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=40) as response:
        raw = response.read()
        return json.loads(raw) if raw else {}


def check(name, fn):
    global failed
    try:
        fn()
        print('PASS: ' + name)
        return True
    except (ValueError, KeyError, IndexError, OSError, urllib.error.URLError) as exc:
        failed = True
        print('FAIL: ' + name + ' (' + str(exc) + ')')
        return False


def operation(body):
    result = request('POST', device + '/operations?wait=30', body)
    if result.get('status') != 'succeeded':
        raise ValueError('operation status ' + str(result.get('status')))
    return result


def registration():
    request('GET', device + '/registration')


def read():
    result = operation({'type': 'read', 'paths': [write_path]})
    values = result['result']['values']
    value = next((v for v in values if v.get('path') == write_path), None)
    if not value or 'value' not in value or 'type' not in value:
        raise ValueError('read did not return the requested typed value')
    return value


def write(value):
    operation({'type': 'write', 'path': write_path, 'values': [value]})
    if read()['value'] != value['value']:
        raise ValueError('readback differs from written value')


def observe():
    result = request('POST', device + '/observations?wait=30',
                     {'paths': [observe_path], 'persistent': False})
    obs_id = result['id']
    try:
        deadline = time.monotonic() + 30
        while result.get('status') == 'pending' and time.monotonic() < deadline:
            time.sleep(0.5)
            result = request('GET', '/v1/observations/' + urllib.parse.quote(obs_id, safe=''))
        if result.get('status') != 'active':
            raise ValueError('observation status ' + str(result.get('status')))
    finally:
        request('DELETE', '/v1/observations/' + urllib.parse.quote(obs_id, safe=''))


if not token:
    print('FAIL: LWM2M_API_TOKEN is required')
    sys.exit(1)
check('register', registration)
value = None

def capture_read():
    global value
    value = read()

if check('read', capture_read):
    check('write', lambda: write(value))
else:
    print('SKIP: write (read failed)')
check('execute', lambda: operation({'type': 'execute', 'path': execute_path}))
check('observe', observe)
print('SKIP: FOTA (manual test, requires a device-specific image)')
sys.exit(1 if failed else 0)
