package apiv1

import (
	"net/http/httptest"
	"sync"
	"testing"
)

func TestWebhooksETagConditionalLifecycle(t *testing.T) {
	h := keyHandler(t)
	created := webhookRequest(h, "POST", "/v1/webhooks", webhookBody, "create", "")
	path, tag := created.Header().Get("Location"), created.Header().Get("ETag")
	if tag == "" {
		t.Fatal("create missing ETag")
	}
	if w := webhookRequest(h, "GET", path, "", "", ""); w.Header().Get("ETag") != tag {
		t.Fatal("GET ETag differs")
	}
	patched := webhookRequest(h, "PATCH", path, `{"status":"disabled"}`, "", tag)
	if patched.Code != 200 || patched.Header().Get("ETag") == "" || patched.Header().Get("ETag") == tag {
		t.Fatalf("matching patch: %d %s", patched.Code, patched.Body)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		if w := webhookRequest(h, method, path, `{}`, "", tag); w.Code != 412 {
			t.Fatalf("stale %s: %d", method, w.Code)
		}
	}
	current := patched.Header().Get("ETag")
	rotated := webhookRequest(h, "POST", path+"/rotate-secret", "", "rotate", "")
	if rotated.Code != 200 || rotated.Header().Get("ETag") == "" || rotated.Header().Get("ETag") == current {
		t.Fatal("rotation failed to invalidate ETag")
	}
	if w := webhookRequest(h, "DELETE", path, "", "", current); w.Code != 412 {
		t.Fatalf("pre-rotation delete: %d", w.Code)
	}
	replay := webhookRequest(h, "POST", "/v1/webhooks", webhookBody, "create", "")
	if replay.Header().Get("ETag") != tag {
		t.Fatal("create replay lost original ETag")
	}
	replay = webhookRequest(h, "POST", path+"/rotate-secret", "", "rotate", "")
	if replay.Header().Get("ETag") != rotated.Header().Get("ETag") {
		t.Fatal("rotation replay lost original ETag")
	}
	if w := webhookRequest(h, "DELETE", path, "", "", rotated.Header().Get("ETag")); w.Code != 204 {
		t.Fatalf("matching delete: %d", w.Code)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		if w := webhookRequest(h, method, path, `{}`, "", tag); w.Code != 404 {
			t.Fatalf("missing priority %s: %d", method, w.Code)
		}
	}
}

func TestWebhooksConditionalCompetingWrites(t *testing.T) {
	for _, competing := range []string{"PATCH", "DELETE"} {
		t.Run(competing, func(t *testing.T) {
			h := keyHandler(t)
			created := webhookRequest(h, "POST", "/v1/webhooks", webhookBody, "", "")
			path, tag := created.Header().Get("Location"), created.Header().Get("ETag")
			if tag == "" {
				t.Fatal("missing ETag")
			}
			var wg sync.WaitGroup
			replies := make([]*httptest.ResponseRecorder, 16)
			for i := range replies {
				wg.Go(func() {
					method := "PATCH"
					if i == 0 {
						method = competing
					}
					replies[i] = webhookRequest(h, method, path, `{}`, "", tag)
				})
			}
			wg.Wait()
			successes := 0
			for _, w := range replies {
				switch w.Code {
				case 200, 204:
					successes++
				case 404, 412:
				default:
					t.Fatalf("unexpected %d", w.Code)
				}
			}
			if successes != 1 {
				t.Fatalf("conditional writes committed %d times", successes)
			}
		})
	}
}
