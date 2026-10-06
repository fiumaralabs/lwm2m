module github.com/fiumaralabs/lwm2m

go 1.26.5

require (
	github.com/eclipse/paho.mqtt.golang v1.5.1
	github.com/fxamacker/cbor/v2 v2.9.4
	github.com/gorilla/websocket v1.5.3
	github.com/mochi-mqtt/server/v2 v2.7.9
	github.com/pion/dtls/v3 v3.1.10
	github.com/plgd-dev/go-coap/v3 v3.5.4
)

require (
	github.com/dsnet/golib/memfile v1.0.0 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/transport/v5 v5.0.0 // indirect
	github.com/rs/xid v1.4.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/exp v0.0.0-20240904232852-e7e105dedf7e // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

// RFC 7250 raw public keys and unknown_psk_identity: see third_party/pion-dtls/LWM2M-PATCH.md.
replace github.com/pion/dtls/v3 => ./third_party/pion-dtls
