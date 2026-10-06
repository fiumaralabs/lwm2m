#!/bin/bash
# Build the interop image and run the Zephyr LwM2M interop suite against
# lwm2md. Args go to twister, e.g.:
#
#	interop/zephyr/run.sh                               # whole suite (~30 min)
#	interop/zephyr/run.sh --pytest-args='-k int_102'    # one test
#
# Results: interop/zephyr/out/ (twister-out/, lwm2md.log).
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
out=${OUT:-$root/interop/zephyr/out}
image=${IMAGE:-lwm2m-zephyr-interop}
docker build ${DOCKER_BUILD_ARGS:-} -f "$root/interop/zephyr/Dockerfile" -t "$image" "$root"
rm -rf "$out" && mkdir -p "$out"
# zeth is a TAP device: NET_ADMIN and /dev/net/tun are enough.
exec docker run --rm --cap-add NET_ADMIN --device /dev/net/tun \
	-e PLATFORM -e LWM2MD_ARGS -v "$out:/out" "$image" "$@"
