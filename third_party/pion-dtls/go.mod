module github.com/pion/dtls/v3

require (
	github.com/pion/logging v0.2.4
	github.com/pion/transport/v5 v5.0.0
	golang.org/x/crypto v0.48.0
)

require golang.org/x/sys v0.41.0 // indirect

go 1.24.0

// Retract version with broken RSA interop with OpenSSL DTLS 1.2.
retract v3.1.0

// Retract version with broken interoperability with firefox.
retract v3.1.3
