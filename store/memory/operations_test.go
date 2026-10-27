package memory_test

import (
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestOperations(t *testing.T) {
	storetest.Operations(t, func(*testing.T) store.Operations { return memory.NewOperations() })
}
