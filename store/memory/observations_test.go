package memory_test

import (
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestObservations(t *testing.T) {
	storetest.Observations(t, func() store.Observations { return &memory.Observations{} })
}
