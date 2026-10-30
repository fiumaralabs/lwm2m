package operations_test

import (
	"testing"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestExecutorStore(t *testing.T) {
	s := server.New(server.Config{})
	defer s.Close()
	st := memory.NewOperations()
	e := operations.NewExecutor(s, st, operations.ExecutorOptions{})
	defer e.Close()
	if e.Store() != st {
		t.Fatal("executor did not expose its configured store")
	}
}
