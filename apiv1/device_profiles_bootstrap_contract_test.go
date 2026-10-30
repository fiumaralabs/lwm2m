package apiv1

import "testing"

func TestDeviceProfileBootstrapTemplateValidation(t *testing.T) {
	h := New(Options{Keys: profileWriteKeys()})
	invalid := []string{
		`{"typo":true}`, `{"servers":"invalid"}`, `{"servers":[{"shortServerId":1,"typo":true}]}`,
		`{"servers":[{"shortServerId":1,"security":{"psk":{"generate":"yes"}}}]}`,
		`{"servers":[{"shortServerId":1},{"shortServerId":1}]}`,
		`{"servers":[{"shortServerId":1,"securityInstance":0},{"shortServerId":2,"securityInstance":0}]}`,
		`{"servers":[{"shortServerId":1}],"acls":[{"object":3,"instance":0,"owner":2}]}`,
		`{"bootstrapServer":{"clientHoldOffSeconds":"bad"}}`, `{"options":{"discover":"yes"}}`,
		`{"servers":null}`, `{"servers":[{"shortServerId":1,"security":null}]}`, `{"options":{"format":"nonsense"}}`,
		`{"servers":[{"shortServerId":1}],"acls":[{"object":3,"instance":0,"owner":1,"rights":{"bad":["read"]}}]}`,
		`{"writes":[{"path":"/23/0","values":[{"path":"/23/0/0","type":"boolean","value":"bad"}]}]}`,
		`{"writes":[{"path":"/23/0","values":[{"path":"/23","type":"string","value":"bad"}]}]}`,
		`{"writes":[{"path":"/23/0","values":[{"path":"/23/0/1","value":{"bad":1}}]}]}`,
		`{"writes":[{"path":"/23/0","values":[{"path":"/23/0/1","value":[1]}]}]}`,
		`{"writes":[{"path":"/23/0","values":[{"path":"/23/0/1","type":"string"}]}]}`,
		`{"writes":[{"path":"/23/0","values":[{"path":"/23/0/0","type":"string","value":"ok","typo":1}]}]}`,
		`{"servers":[{"shortServerId":1,"security":{"psk":{"key":"hex:000102030405060708090a0b0c0d0e0f"}}}]}`,
	}
	for _, template := range invalid {
		t.Run(template, func(t *testing.T) {
			profileProblem(t, profileWriteRequest(h, "POST", "/v1/device-profiles", `{"name":"probe","bootstrapTemplate":`+template+`}`, "lwm_test_secret", ""), 400, 422)
		})
	}
	for _, values := range []string{
		`[{"path":"/23/0"},{"path":"/23/0/1"}]`,
		`[{"path":"/23/0/1","value":42},{"path":"/23/0/2/0","type":"boolean","value":true}]`,
	} {
		body := `{"name":"probe","bootstrapTemplate":{"writes":[{"path":"/23/0","values":` + values + `}]}}`
		w := profileWriteRequest(h, "POST", "/v1/device-profiles", body, "lwm_test_secret", "")
		if w.Code != 201 {
			t.Fatalf("valid values %s: %d %s", values, w.Code, w.Body)
		}
	}
	valid := `{"servers":[{"server":"self","shortServerId":1,"security":{"psk":{"generate":true}},"lifetimeSeconds":86400,"binding":"UQ"}],"bootstrapServer":{"accountTimeoutSeconds":0,"clientHoldOffSeconds":0},"acls":[{"object":3,"instance":0,"owner":1,"rights":{"1":["read","write"]}}],"delete":["/0","/1"],"writes":[{"path":"/23/0","values":[{"path":"/23/0/0","type":"string","value":"ok"}]}],"options":{"format":"senml-cbor","renumberSecurityInstances":true,"discover":false,"read":["/1"],"refusePack":false}}`
	w := profileWriteRequest(h, "POST", "/v1/device-profiles", `{"name":"probe","bootstrapTemplate":`+valid+`}`, "lwm_test_secret", "")
	if w.Code != 201 {
		t.Fatalf("valid template: %d %s", w.Code, w.Body)
	}
}
