#!/bin/sh
set -eu
out=$(env -u LWM2M_REAL_DEVICE_EP sh scripts/real-device-check.sh)
[ "$out" = 'SKIP: no device' ] || { printf 'unexpected skip: %s\n' "$out"; exit 1; }
printf 'PASS: skip without device\n'
