# Interop peers for the LwM2M server: real open-source LwM2M clients built
# from pinned upstream sources at image build time, plus our lwm2md.
#
# Nothing from Anjay or Anjay Lite is vendored in this repository: both are
# under AVSystem non-commercial licences and are cloned and built here only
# (see README.md). Wakaama is EPL-2.0 / BSD-3-Clause.
#
#   docker build -f interop/peers/Dockerfile --target peers -t lwm2m-peers .
#   docker run --rm -v "$PWD:/src" -w /src lwm2m-peers \
#       go test -tags interop -v ./interop/peers/
#
# Target "test" (the default) also copies the module in and builds lwm2md and
# the test binary, for CI: docker run --rm lwm2m-peers-test.

ARG UBUNTU=ubuntu:24.04
ARG GO_IMAGE=golang:1.26.5

FROM ${UBUNTU} AS base
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -yq --no-install-recommends \
        build-essential cmake git ca-certificates python3 zlib1g-dev autoconf automake libtool pkg-config \
    && rm -rf /var/lib/apt/lists/*

# mbedTLS 3.6 LTS from source: the distro's 2.28 has no RFC 9146 Connection ID.
# 3.6 enables MBEDTLS_SSL_DTLS_CONNECTION_ID and CONTEXT_SERIALIZATION by default.
FROM base AS mbedtls
ARG MBEDTLS_REF=v3.6.7
RUN git clone --depth 1 --branch ${MBEDTLS_REF} --recurse-submodules --shallow-submodules \
        https://github.com/Mbed-TLS/mbedtls.git /build/mbedtls \
    && cd /build/mbedtls \
    && python3 scripts/config.py set MBEDTLS_SSL_DTLS_CONNECTION_ID \
    && cmake -S . -B b -DENABLE_TESTING=OFF -DENABLE_PROGRAMS=OFF \
        -DCMAKE_POSITION_INDEPENDENT_CODE=ON -DCMAKE_INSTALL_PREFIX=/opt/mbedtls \
    && cmake --build b -j"$(nproc)" && cmake --install b && rm -rf /build

# Eclipse Wakaama: lwm2mclient (NoSec, with client-initiated bootstrap -b),
# and lwm2mclient_tinydtls (PSK). No LwM2M 1.0 build: at this commit the example
# client does not compile with WAKAAMA_CLIENT_LWM2M_V_1_0 (object_server.c uses
# 1.1-only server_instance_t fields); Anjay -V 1.0 covers 1.0.
FROM base AS wakaama
ARG WAKAAMA_COMMIT=94ff56f77a2d24a5890e0e703809a47633aa7d4b
RUN git init /build/wakaama && cd /build/wakaama \
    && git remote add origin https://github.com/eclipse-wakaama/wakaama.git \
    && git fetch --depth 1 origin ${WAKAAMA_COMMIT} && git checkout FETCH_HEAD \
    && git submodule update --init --depth 1 \
    && cmake -S examples/client/udp -B b-udp -DWAKAAMA_UNIT_TESTS=OFF \
    && cmake --build b-udp -j"$(nproc)" \
    && cmake -S examples/client/tinydtls -B b-dtls -DWAKAAMA_UNIT_TESTS=OFF \
    && cmake --build b-dtls -j"$(nproc)" \
    && mkdir -p /out && cp b-udp/lwm2mclient /out/wakaama-client \
    && cp b-dtls/lwm2mclient_tinydtls /out/wakaama-client-dtls

# AVSystem Anjay demo client: LwM2M 1.2, mbedTLS (PSK, X.509, CID), CoAP/TCP,
# NoSec allowed (development option), Bootstrap-Pack, firmware update.
FROM mbedtls AS anjay
ARG ANJAY_REF=3.15.0
ARG ANJAY_COMMIT=fdd70854c46f676acda179ad3a1b760eceada4db
RUN git clone --depth 1 --branch ${ANJAY_REF} --recurse-submodules --shallow-submodules \
        https://github.com/AVSystem/Anjay.git /build/anjay \
    && cd /build/anjay && test "$(git rev-parse HEAD)" = "${ANJAY_COMMIT}" \
    && cmake -S . -B b -DCMAKE_BUILD_TYPE=RelWithDebInfo \
        -DDTLS_BACKEND=mbedtls -DMBEDTLS_ROOT_DIR=/opt/mbedtls \
        -DWITH_LIBRARY_SHARED=OFF -DWITH_UNSECURE_CONNECTIONS=ON \
        -DWITH_LWM2M11=ON -DWITH_LWM2M12=ON -DWITH_AVS_COAP_TCP=ON \
        -DWITH_BOOTSTRAP_PACK=ON -DWITH_SEND=ON -DWITH_OBSERVATION_ATTRIBUTES=ON \
        -DWITH_MODULE_fw_update=ON -DWITH_COAP_DOWNLOAD=ON -DWITH_DEMO=ON \
        -DWITH_EXAMPLES=OFF -DWITH_TEST=OFF \
    && cmake --build b --target demo -j"$(nproc)" \
    && mkdir -p /out && cp "$(find b -type f -name demo -perm -u+x | head -1)" /out/anjay-demo

# AVSystem Anjay Lite integration test_app (JSON-RPC driven), mbedTLS PSK.
FROM mbedtls AS anjaylite
ARG ANJAY_LITE_COMMIT=b33821b042637315c83c548e0bc8286d6dbf7316
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -yq --no-install-recommends g++ \
    && rm -rf /var/lib/apt/lists/*
RUN git init /build/al && cd /build/al \
    && git remote add origin https://github.com/AVSystem/Anjay-lite.git \
    && git fetch --depth 1 origin ${ANJAY_LITE_COMMIT} && git checkout FETCH_HEAD \
    && cmake -S tests/integration/app -B b -DMBEDTLS_ROOT_DIR=/opt/mbedtls \
    && cmake --build b -j"$(nproc)" \
    && mkdir -p /out && cp b/test_app /out/anjay-lite-app

FROM ${GO_IMAGE} AS go

FROM ${UBUNTU} AS peers
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -yq --no-install-recommends \
        ca-certificates git gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:/root/go/bin:$PATH CGO_ENABLED=1
COPY --from=wakaama /out/ /usr/local/bin/
COPY --from=anjay /out/ /usr/local/bin/
COPY --from=anjaylite /out/ /usr/local/bin/
ENV WAKAAMA_CLIENT=/usr/local/bin/wakaama-client \
    WAKAAMA_CLIENT_DTLS=/usr/local/bin/wakaama-client-dtls \
    ANJAY_DEMO=/usr/local/bin/anjay-demo \
    ANJAY_LITE_APP=/usr/local/bin/anjay-lite-app

FROM peers AS test
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /usr/local/bin/lwm2md ./cmd/lwm2md \
    && go test -c -tags interop -race -o /usr/local/bin/peers.test ./interop/peers
CMD ["/usr/local/bin/peers.test", "-test.v", "-test.timeout=30m"]
