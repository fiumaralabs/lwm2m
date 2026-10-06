# Zephyr LwM2M interop suite (tests/net/lib/lwm2m/interop) against lwm2md
# instead of Leshan. Build from the repository root:
#
#	docker build -f interop/zephyr/Dockerfile -t lwm2m-zephyr-interop .
#
# The Zephyr layers come first so editing Go code only rebuilds the last
# two stages. See README.md.

ARG ZEPHYR_VERSION=v4.4.2

FROM golang:1.26 AS lwm2md
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party ./third_party
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /lwm2md ./cmd/lwm2md

FROM ubuntu:24.04 AS zephyr
ARG ZEPHYR_VERSION
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
	git cmake ninja-build gperf ccache device-tree-compiler wget file make \
	gcc g++ libc6-dev python3 python3-dev python3-venv python3-pip xz-utils \
	iproute2 iputils-ping procps curl ca-certificates \
	&& if [ "$(dpkg --print-architecture)" = amd64 ]; then \
		apt-get install -y --no-install-recommends gcc-multilib g++-multilib; fi \
	&& rm -rf /var/lib/apt/lists/*
RUN python3 -m venv /venv
ENV PATH=/venv/bin:$PATH
RUN pip install --no-cache-dir west
WORKDIR /work
# Shallow Zephyr at the pinned release, and only the modules the interop
# sample needs: mbedTLS (+ TF-PSA-Crypto) for DTLS-PSK, zcbor for (SenML)
# CBOR, net-tools for zeth. The prebuild fails on any dropped Kconfig.
RUN git clone --depth 1 -b ${ZEPHYR_VERSION} https://github.com/zephyrproject-rtos/zephyr \
	&& west init -l zephyr \
	&& west config manifest.project-filter -- '-.*,+mbedtls,+tf-psa-crypto,+zcbor,+net-tools' \
	&& west update --narrow -o=--depth=1 \
	&& pip install --no-cache-dir -r zephyr/scripts/requirements-base.txt \
		-r zephyr/scripts/requirements-build-test.txt -r zephyr/scripts/requirements-run-test.txt \
		-r zephyr/tests/net/lib/lwm2m/interop/requirements.txt
ENV ZEPHYR_BASE=/work/zephyr ZEPHYR_TOOLCHAIN_VARIANT=host
# Pre-build the DUT so a run only rebuilds what changed (nothing).
COPY interop/zephyr/entrypoint.sh /entrypoint.sh
RUN /entrypoint.sh --build-only

FROM zephyr
COPY --from=lwm2md /lwm2md /usr/local/bin/lwm2md
ENTRYPOINT ["/entrypoint.sh"]
