package apiv1

import (
	"encoding/json"
	"testing"
)

func TestOpenAPIEventStreamDocumented(t *testing.T) {
	var doc struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	var path struct {
		Get struct {
			Responses map[string]struct {
				Content map[string]json.RawMessage `json:"content"`
			} `json:"responses"`
		} `json:"get"`
	}
	if raw := doc.Paths["/v1/events"]; raw != nil {
		if err := json.Unmarshal(raw, &path); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := path.Get.Responses["200"].Content["text/event-stream"]; !ok {
		t.Fatal("GET /v1/events must document a 200 text/event-stream response")
	}
}
