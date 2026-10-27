package memory_test

import (
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestSettingsContract(t *testing.T) {
	storetest.Settings(t, func(*testing.T) store.Settings { return &memory.Settings{} })
}
