package memory_test

import (
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestAPIKeys(t *testing.T) {
	storetest.APIKeys(t, func(*testing.T) store.APIKeys { return &memory.APIKeys{} })
}
