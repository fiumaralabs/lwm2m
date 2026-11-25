package apiv1

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestProfileOnRegisterDiscoverExecutorPrerequisite(t *testing.T) {
	if !validProfileOperation(json.RawMessage(`{"type":"discover","path":"/3"}`)) {
		t.Fatal("discover profile operation must be accepted")
	}
	executor := operations.NewExecutor(nil, memory.NewOperations(), operations.ExecutorOptions{})
	op := operations.New("default", "device", "discover", json.RawMessage(`{"path":"/3"}`), nil, time.Now())
	if _, err := executor.Submit(context.Background(), "default", op); err != nil {
		t.Fatalf("accepted profile onRegister operation cannot be queued: %v", err)
	}
}
