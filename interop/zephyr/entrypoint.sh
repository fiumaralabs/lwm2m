#!/bin/bash
# Runs inside the container: zeth TAP at 192.0.2.2 (net-tools), lwm2md in
# Leshan's place (same ports: CoAP 5683/5684, BS 5783/5784, REST 8080/8081
# on localhost, which is what conftest.py defaults to), then twister.
#
#	entrypoint.sh [--build-only] [twister args...]
#
# Extra args go to twister, e.g. --pytest-args='-k int_102'.
# Results land in /out (mount it): twister-out/, lwm2md.log.
set -euo pipefail

cd /work
# native_sim (32-bit, what upstream CI runs) needs an x86 host; elsewhere
# use the 64-bit variant. The suite only allows native_sim and qemu_x86,
# hence --force-platform, and the board conf is named after the board, so
# pass it explicitly for the qualified target.
if [ "$(uname -m)" = x86_64 ]; then
	PLATFORM=${PLATFORM:-native_sim}
else
	PLATFORM=${PLATFORM:-native_sim/native/64}
fi
OUT=${OUT:-/out}
TW=(./zephyr/scripts/twister -p "$PLATFORM" --force-platform -T zephyr/tests/net/lib/lwm2m/interop
	--enable-slow -vv --inline-logs --outdir "$OUT/twister-out"
	# tests.yaml's timeout (600 s) covers the whole pytest session; a green
	# run takes ~7 min, but a failing one (each blockwise failure waits out
	# the 93 s request timeout, int-7 may wait 600 s) hits it, and twister
	# then reports nothing but "Pytest timeout". Headroom keeps per-test results.
	--timeout-multiplier "${TIMEOUT_MULTIPLIER:-5}")
if [ "$PLATFORM" != native_sim ]; then
	TW+=(-x=EXTRA_CONF_FILE=boards/native_sim.conf)
fi

if [ "${1:-}" = --build-only ]; then
	shift
	# Warms ccache in the image; the build tree itself is thrown away.
	"${TW[@]}" --build-only --outdir /tmp/prebuilt "$@"
	# A missing module silently drops prj.conf options (e.g. no zcbor, no
	# SenML-CBOR): refuse such a DUT.
	if grep -rl "was assigned the value" /tmp/prebuilt --include=build.log; then
		grep -rh -A3 "^warning:" /tmp/prebuilt --include=build.log
		exit 1
	fi
	rm -rf /tmp/prebuilt
	exit 0
fi

mkdir -p "$OUT"
./tools/net-tools/net-setup.sh start >/dev/null
trap './tools/net-tools/net-setup.sh stop >/dev/null 2>&1 || true' EXIT

lwm2md -v ${LWM2MD_ARGS:-} >"$OUT/lwm2md.log" 2>&1 &
LWM2MD=$!
for _ in $(seq 50); do
	curl -sf http://localhost:8081/api/security/clients >/dev/null 2>&1 && break || sleep 0.1
done

set +e
"${TW[@]}" "$@"
rc=$?
kill "$LWM2MD" 2>/dev/null
exit $rc
