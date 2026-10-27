package apiv1

import "testing"

func TestWebhooksCreateReplayPrecedesBodyValidation(t *testing.T) {
	h := keyHandler(t)
	first := webhookRequest(h, "POST", "/v1/webhooks", webhookBody, "retry", "")
	if first.Code != 201 {
		t.Fatal(first.Code)
	}
	for _, body := range []string{"", `null`, `{"unknown":true}`} {
		w := webhookRequest(h, "POST", "/v1/webhooks", body, "retry", "")
		if w.Code != first.Code || w.Body.String() != first.Body.String() || w.Header().Get("Location") != first.Header().Get("Location") || w.Header().Get("ETag") != first.Header().Get("ETag") {
			t.Fatalf("same-key retry did not return original response: %d", w.Code)
		}
	}
}
