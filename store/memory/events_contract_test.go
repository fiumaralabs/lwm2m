package memory_test

import (
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestEventsContract(t *testing.T) {
	storetest.Events(t, func(*testing.T) store.Events { return &memory.Events{} })
}
