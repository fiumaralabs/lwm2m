package main

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

type daemonInfo struct {
	endpoints   map[string]map[string]string
	certificate string
}

func advertisedInfo(coapBind, coapsBind, coapURI, coapsURI, certificatePath string) (daemonInfo, error) {
	info := daemonInfo{endpoints: map[string]map[string]string{}}
	server := map[string]string{}
	for _, endpoint := range []struct{ scheme, bind, uri string }{{"coap", coapBind, coapURI}, {"coaps", coapsBind, coapsURI}} {
		if endpoint.uri == "" {
			continue
		}
		u, err := url.Parse(endpoint.uri)
		if err != nil || u.Scheme != endpoint.scheme || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(u.Host, "*") {
			return info, fmt.Errorf("invalid advertise-%s URI", endpoint.scheme)
		}
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsUnspecified() {
			return info, fmt.Errorf("invalid advertise-%s URI", endpoint.scheme)
		}
		if endpoint.bind != "" {
			server[endpoint.scheme] = endpoint.uri
		}
	}
	if len(server) != 0 {
		info.endpoints["server"] = server
	}
	if certificatePath == "" {
		return info, nil
	}
	data, err := os.ReadFile(certificatePath)
	if err != nil {
		return info, fmt.Errorf("cannot read public server certificate: %w", err)
	}
	rest := bytes.TrimSpace(data)
	count := 0
	for len(rest) > 0 {
		if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return info, fmt.Errorf("server certificate file must contain only PEM certificates")
		}
		block, remaining := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return info, fmt.Errorf("invalid server certificate PEM")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return info, fmt.Errorf("invalid server certificate")
		}
		count++
		rest = bytes.TrimSpace(remaining)
	}
	if count == 0 {
		return info, fmt.Errorf("server certificate file is empty")
	}
	info.certificate = string(data)
	return info, nil
}
