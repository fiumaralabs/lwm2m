package apiv1

import (
	"context"
	"encoding/json"
	"github.com/fiumaralabs/lwm2m/server"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestOperationsListPagination(t *testing.T) {
	st := memory.NewOperations()
	h := New(Options{Keys: deviceKeys(), Operations: st})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		op := operations.New("default", "pump", "read", nil, nil, time.Unix(int64(i), 0))
		op.ID = id
		if err := st.Create(context.Background(), "default", op); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/v1/devices/pump/operations", "/v1/operations"} {
		cursor := ""
		for page, want := range [][]string{{"e", "d"}, {"c", "b"}, {"a"}} {
			w := deviceRequest(h, "GET", path+"?limit=2&cursor="+url.QueryEscape(cursor), "")
			var response struct {
				Items []operations.Operation `json:"items"`
				Next  string                 `json:"nextCursor"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
			ids := []string{}
			for _, op := range response.Items {
				ids = append(ids, op.ID)
				get := deviceRequest(h, "GET", "/v1/operations/"+op.ID, "")
				var item operations.Operation
				if get.Code != 200 || json.Unmarshal(get.Body.Bytes(), &item) != nil || !reflect.DeepEqual(item, op) {
					t.Fatal("list/get mismatch")
				}
			}
			if !reflect.DeepEqual(ids, want) {
				t.Fatalf("page %d: %v", page, ids)
			}
			if (response.Next == "") != (page == 2) {
				t.Fatal("incorrect cursor", w.Body.String())
			}
			if page == 2 {
				var raw map[string]json.RawMessage
				json.Unmarshal(w.Body.Bytes(), &raw)
				if _, ok := raw["nextCursor"]; ok {
					t.Fatal("last cursor not omitted")
				}
			}
			cursor = response.Next
		}
	}
}

func TestOperationsListFiltersAndIsolation(t *testing.T) {
	st := memory.NewOperations()
	keys := deviceKeys()
	h := New(Options{Keys: keys, Operations: st})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"empty"}`)
	for _, spec := range []struct {
		id, dev, kind, job string
		status             operations.Status
	}{
		{"a", "pump", "read", "job1", operations.Queued}, {"b", "pump", "write", "job2", operations.Succeeded}, {"c", "other", "read", "job1", operations.Failed},
	} {
		op := operations.New("default", spec.dev, spec.kind, nil, nil, time.Unix(1, 0))
		op.ID, op.Status, op.Job = spec.id, spec.status, &spec.job
		if err := st.Create(context.Background(), "default", op); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/v1/operations", "/v1/devices/pump/operations"} {
		for _, tc := range []struct {
			query string
			want  []string
		}{
			{"status=queued", []string{"a"}}, {"status=queued,succeeded", []string{"b", "a"}}, {"type=write", []string{"b"}}, {"job=job2", []string{"b"}}, {"type=read&job=job1&status=queued", []string{"a"}},
		} {
			w := deviceRequest(h, "GET", path+"?"+tc.query, "")
			var page struct{ Items []operations.Operation }
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			ids := []string{}
			for _, op := range page.Items {
				ids = append(ids, op.ID)
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("%s?%s: %v", path, tc.query, ids)
			}
		}
	}
	w := deviceRequest(h, "GET", "/v1/operations?device=other", "")
	var page struct{ Items []operations.Operation }
	json.Unmarshal(w.Body.Bytes(), &page)
	if w.Code != 200 || len(page.Items) != 1 || page.Items[0].ID != "c" {
		t.Fatal(w.Body.String())
	}
	entry := keys.entries["test"]
	entry.tenant = "tenantB"
	keys.entries["test"] = entry
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"empty"}`)
	for _, path := range []string{"/v1/operations", "/v1/devices/empty/operations"} {
		w := deviceRequest(h, "GET", path, "")
		if w.Code != 200 || w.Body.String() != "{\"items\":[]}\n" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := deviceRequest(h, "GET", "/v1/devices/pump/operations", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestOperationsListInvalidQueriesAndScope(t *testing.T) {
	keys := deviceKeys()
	h := New(Options{Keys: keys})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	for _, path := range []string{"/v1/operations", "/v1/devices/pump/operations"} {
		for _, tc := range []struct{ query, code string }{
			{"limit=0", "invalid_limit"}, {"limit=501", "invalid_limit"}, {"limit=no", "invalid_limit"}, {"limit=", "invalid_limit"}, {"cursor=bad", "invalid_cursor"}, {"status=no", "invalid_filter"}, {"status=queued,no", "invalid_filter"}, {"status=", "invalid_filter"},
		} {
			w := deviceRequest(h, "GET", path+"?"+tc.query, "")
			var p struct{ Code string }
			json.Unmarshal(w.Body.Bytes(), &p)
			if w.Code != 400 || p.Code != tc.code {
				t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body.String())
			}
		}
	}
	entry := keys.entries["test"]
	entry.scopes = ",operations:write,"
	keys.entries["test"] = entry
	for _, path := range []string{"/v1/operations", "/v1/devices/pump/operations"} {
		if w := deviceRequest(h, "GET", path, ""); w.Code != 403 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestOperationsPrefixUnsupported(t *testing.T) {
	keys := deviceKeys()
	entry := keys.entries["test"]
	entry.scopes += "operations:write,"
	keys.entries["test"] = entry
	srv := server.New(server.Config{})
	defer srv.Close()
	st := memory.NewOperations()
	ex := operations.NewExecutor(srv, st, operations.ExecutorOptions{})
	h := New(Options{Keys: keys, Server: srv, Operations: st, Executor: ex})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	w := deviceRequest(h, "POST", "/v1/devices/pump/operations", `{"type":"write","prefix":"gateway","path":"/3/0/1","values":[{"path":"/3/0/1","type":"string","value":"x"}]}`)
	var p struct{ Code string }
	json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 501 || p.Code != "prefix_unsupported" {
		t.Fatal(w.Code, w.Body.String())
	}
}
