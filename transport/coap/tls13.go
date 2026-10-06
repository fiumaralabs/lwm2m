package coap

import "fmt"

// TLS13Feature is a bit of /0/x/22 "TLS 1.3 Features To Use by Client"
// (E.1 res 22). 0 means do not use TLS 1.3.
type TLS13Feature uint32

const (
	TLS13PSK         TLS13Feature = 1 << 0 // PSK, plain
	TLS13ZeroRTT     TLS13Feature = 1 << 1 // 0-RTT
	TLS13PSKWithPFS  TLS13Feature = 1 << 2 // PSK with (EC)DHE
	TLS13Certificate TLS13Feature = 1 << 3 // certificate-based authentication
	tls13Defined                  = TLS13PSK | TLS13ZeroRTT | TLS13PSKWithPFS | TLS13Certificate
)

// SupportedTLS13Features are the TLS 1.3 features this server offers: TLS
// 1.3 runs on the TCP/TLS binding with certificate authentication (Go's
// crypto/tls has no external PSK or 0-RTT for servers). DTLS 1.3 is not
// available.
const SupportedTLS13Features = TLS13Certificate

// CheckTLS13Features validates a /0/x/22 value before a Bootstrap-Server
// writes it for this server's account: reserved bits 4-31 must be 0, and
// only features the server implements may be requested (TLS13-05).
func CheckTLS13Features(v uint32) error {
	f := TLS13Feature(v)
	if f&^tls13Defined != 0 {
		return fmt.Errorf("coap: /0/x/22 reserved bits set: %#x", v)
	}
	if f&^SupportedTLS13Features != 0 {
		return fmt.Errorf("coap: /0/x/22 requests TLS 1.3 features %#x this server does not offer", uint32(f&^SupportedTLS13Features))
	}
	return nil
}
