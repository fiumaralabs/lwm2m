package main

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
)

func TestTimeSeriesCapability(t *testing.T) {
	const token = "lwm_ts_synthetic"
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_ts sha256:%x devices:read,devices:write", sha256.Sum256([]byte(token)))))
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		srv, h, executor, dispatcher := newDaemonWithTimeSeries(server.Config{}, leshanapi.New(leshanapi.Options{}), keys, enabled)
		request := func(method, path, body string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			return w
		}
		info := request("GET", "/v1/info", "")
		if info.Code != 200 || !strings.Contains(info.Body.String(), fmt.Sprintf(`"timeseries":%t`, enabled)) {
			t.Fatalf("info: %d %s", info.Code, info.Body.String())
		}
		if w := request("POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		w := request("GET", "/v1/devices/pump/timeseries?path=/1/0/1", "")
		if enabled && w.Code != 200 || !enabled && (w.Code != 501 || !strings.Contains(w.Body.String(), "timeseries_disabled")) {
			t.Fatal(w.Code, w.Body.String())
		}
		_ = dispatcher.Close()
		_ = executor.Close()
		_ = srv.Close()
	}
}
