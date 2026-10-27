package apiv1

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/selector"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

type selectorFailingDevices struct {
	store.Devices
	err error
}

func (s selectorFailingDevices) Get(string, string) (store.Device, error) {
	return store.Device{}, s.err
}

func TestOperationsSelectorLookupErrors(t *testing.T) {
	failure := errors.New("device lookup unavailable")
	st := memory.NewOperations()
	op := operations.New("default", "pump", "read", nil, nil, time.Now())
	if err := st.Create(context.Background(), "default", op); err != nil {
		t.Fatal(err)
	}
	o := Options{Keys: deviceKeys(), Operations: st, Devices: selectorFailingDevices{err: failure}}
	h := New(o)
	for _, path := range []string{"/v1/operations", "/v1/devices/pump/operations"} {
		w := deviceRequest(h, "GET", path+"?selector=team", "")
		if w.Code != 500 {
			t.Errorf("%s: got %d %s", path, w.Code, w.Body.String())
		}
	}
	match, _ := selector.Parse("team")
	r := httptest.NewRequest("GET", "/v1/operations?selector=team", nil)
	if _, _, err := o.listSelectedOperations(r, "default", operations.ListFilter{}, match, 1); !errors.Is(err, failure) {
		t.Fatal("lost lookup error", err)
	}
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	if _, _, err := o.listSelectedOperations(r.WithContext(ctx), "default", operations.ListFilter{}, match, 1); !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation", err)
	}
}

type selectorCountingOperations struct {
	operations.Operations
	calls int
}

func (s *selectorCountingOperations) List(ctx context.Context, tenant string, f operations.ListFilter, cursor string, limit int) ([]operations.Operation, string, error) {
	s.calls++
	return s.Operations.List(ctx, tenant, f, cursor, limit)
}

func TestOperationsSelectorPagination(t *testing.T) {
	st := &selectorCountingOperations{Operations: memory.NewOperations()}
	keys := deviceKeys()
	h := New(Options{Keys: keys, Operations: st})
	for _, ep := range []string{"pump", "other"} {
		label := "red"
		if ep == "other" {
			label = "blue"
		}
		deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"`+ep+`","labels":{"team":"`+label+`"}}`)
	}
	want := []string{}
	for i := 0; i < 1201; i++ {
		ep := "other"
		if i%173 == 0 {
			ep = "pump"
			want = append([]string{fmt.Sprintf("op%04d", i)}, want...)
		}
		op := operations.New("default", ep, "read", nil, nil, time.Unix(int64(i), 0))
		op.ID = fmt.Sprintf("op%04d", i)
		job := "job1"
		op.Job = &job
		if err := st.Create(context.Background(), "default", op); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/v1/operations?", "/v1/operations?device=pump", "/v1/devices/pump/operations?"} {
		base := path + "&selector=team%3Dred&type=read&status=queued&job=job1&limit=2"
		cursor := ""
		got := []string{}
		st.calls = 0
		for page := 0; ; page++ {
			if page > 5 {
				t.Fatal("pagination did not terminate")
			}
			w := deviceRequest(h, "GET", base+"&cursor="+url.QueryEscape(cursor), "")
			var p struct {
				Items []operations.Operation
				Next  string `json:"nextCursor"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			if len(p.Items) > 2 {
				t.Fatalf("limit exceeded: %d", len(p.Items))
			}
			for _, op := range p.Items {
				got = append(got, op.ID)
			}
			if p.Next == "" {
				if strings.Contains(w.Body.String(), "nextCursor") {
					t.Fatal("last cursor not omitted")
				}
				break
			}
			if !strings.HasPrefix(p.Next, "opsel.") {
				t.Fatalf("cursor prefix: %s", p.Next)
			}
			if page == 0 {
				for _, change := range []string{"selector=team%3Dblue", "device=other", "type=write", "status=sent", "job=job2"} {
					q, _ := url.ParseQuery(strings.SplitN(base, "?", 2)[1])
					if strings.HasPrefix(path, "/v1/devices/") {
						q.Set("device", "pump")
					}
					kv := strings.SplitN(change, "=", 2)
					v, _ := url.QueryUnescape(kv[1])
					q.Set(kv[0], v)
					q.Set("cursor", p.Next)
					target := "/v1/operations?" + q.Encode()
					bad := deviceRequest(h, "GET", target, "")
					if bad.Code != 400 || !strings.Contains(bad.Body.String(), `"code":"invalid_cursor"`) {
						t.Fatalf("%s: %d %s", change, bad.Code, bad.Body.String())
					}
				}
				entry := keys.entries["test"]
				entry.tenant = "tenantB"
				keys.entries["test"] = entry
				q, _ := url.ParseQuery(strings.SplitN(base, "?", 2)[1])
				if strings.HasPrefix(path, "/v1/devices/") {
					q.Set("device", "pump")
				}
				q.Set("cursor", p.Next)
				bad := deviceRequest(h, "GET", "/v1/operations?"+q.Encode(), "")
				if bad.Code != 400 {
					t.Fatal("tenant cursor binding", bad.Code, bad.Body.String())
				}
				entry.tenant = "default"
				keys.entries["test"] = entry
				b, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(p.Next, "opsel."))
				var payload map[string]any
				json.Unmarshal(b, &payload)
				payload["Offset"] = 500
				mutated, _ := json.Marshal(payload)
				for _, raw := range []string{"null", "{}", string(mutated), strings.Replace(string(mutated), `"Offset":500`, `"Offset":-1`, 1)} {
					bad := deviceRequest(h, "GET", base+"&cursor=opsel."+base64.RawURLEncoding.EncodeToString([]byte(raw)), "")
					if bad.Code != 400 {
						t.Fatalf("malformed cursor: %s: %d", raw, bad.Code)
					}
				}
			}
			cursor = p.Next
		}
		if st.calls > 6 {
			t.Fatalf("sparse pagination used %d List calls, want <=6", st.calls)
		}
		t.Logf("%s: %d matches in %d List calls", path, len(got), st.calls)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	// Sparse filtering scans the unfiltered store in batches, not one row per call.
	st.calls = 0
	w := deviceRequest(h, "GET", "/v1/operations?selector=missing&limit=1", "")
	if w.Code != 200 || w.Body.String() != "{\"items\":[]}\n" || st.calls != 3 {
		t.Fatalf("no-match scan calls=%d: %d %s", st.calls, w.Code, w.Body.String())
	}
}

func TestOperationsSelectorInvalid(t *testing.T) {
	h := New(Options{Keys: deviceKeys()})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	for _, path := range []string{"/v1/operations", "/v1/devices/pump/operations"} {
		w := deviceRequest(h, "GET", path+"?selector="+url.QueryEscape("team in ("), "")
		var p struct{ Code string }
		json.Unmarshal(w.Body.Bytes(), &p)
		if w.Code != 400 || p.Code != "invalid_selector" {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestOperationsSelectorLabels(t *testing.T) {
	st := memory.NewOperations()
	keys := deviceKeys()
	h := New(Options{Keys: keys, Operations: st})
	for _, tenant := range []string{"default", "tenantB"} {
		entry := keys.entries["test"]
		entry.tenant = tenant
		keys.entries["test"] = entry
		label := "red"
		if tenant == "tenantB" {
			label = "blue"
		}
		for _, body := range []string{`{"endpoint":"pump","labels":{"team":"` + label + `"}}`, `{"endpoint":"other","labels":{"team":"blue"}}`} {
			if w := deviceRequest(h, "POST", "/v1/devices", body); w.Code != 201 {
				t.Fatal(w.Body.String())
			}
		}
		for i, ep := range []string{"pump", "other", "missing"} {
			op := operations.New(tenant, ep, "read", nil, nil, time.Unix(int64(i), 0))
			op.ID = ep
			if err := st.Create(context.Background(), tenant, op); err != nil {
				t.Fatal(err)
			}
		}
		for _, path := range []string{"/v1/operations", "/v1/devices/pump/operations"} {
			for _, sel := range []string{"team=red", "!absent", ""} {
				want := []string{}
				if sel == "" {
					want = []string{"missing", "other", "pump"}
				} else if sel == "!absent" {
					want = []string{"other", "pump"}
				} else if tenant == "default" {
					want = []string{"pump"}
				}
				if path != "/v1/operations" && len(want) > 0 {
					want = []string{"pump"}
				}
				w := deviceRequest(h, "GET", path+"?selector="+url.QueryEscape(sel), "")
				var page struct{ Items []operations.Operation }
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
					t.Fatal(w.Code, w.Body.String())
				}
				ids := []string{}
				for _, op := range page.Items {
					ids = append(ids, op.ID)
				}
				if !reflect.DeepEqual(ids, want) {
					t.Errorf("%s %s %q: got %v want %v", tenant, path, sel, ids, want)
				}
			}
		}
	}
}
