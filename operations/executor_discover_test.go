package operations_test

import (
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m/operations"
)

func TestExecutorDiscover(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	op := h.wait(t, h.submit(t, "discover", `{"path":"/3","depth":1}`), operations.Succeeded)
	req, _ := c.LastRequest()
	if len(req.Queries) != 1 || req.Queries[0] != "depth=1" {
		t.Fatalf("discover query: %+v", req.Queries)
	}
	if !strings.Contains(string(op.Result), `"code":"2.05"`) {
		t.Fatalf("discover result: %s", op.Result)
	}
}
