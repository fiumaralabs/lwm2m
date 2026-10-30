#!/bin/sh
set -eu
if [ -z "${LWM2M_REAL_DEVICE_EP:-}" ]; then
  echo 'SKIP: no device'
  exit 0
fi
exec python3 "$(dirname "$0")/real-device-check.py"
