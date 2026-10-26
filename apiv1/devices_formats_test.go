package apiv1

import (
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"strings"
	"testing"
)

func TestDeviceRegistrationFormatNames(t *testing.T) {
	srv := server.New(server.Config{})
	defer srv.Close()
	srv.Store().Add(&server.Registration{ID: "r", Endpoint: "pump", Version: "1.2", Binding: "U", ContentFormats: []lwm2m.ContentFormat{lwm2m.FormatSenMLCBOR, lwm2m.FormatLwM2MCBOR, lwm2m.FormatTLV}})
	h := New(Options{Keys: deviceKeys(), Server: srv})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	w := deviceRequest(h, "GET", "/v1/devices/pump", "")
	if !strings.Contains(w.Body.String(), `"contentFormats":["senml-cbor","lwm2m-cbor","tlv"]`) {
		t.Fatal(w.Body.String())
	}
}
