package memory_test

import (
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestDeviceCreateContract(t *testing.T) {
	storetest.DeviceCreates(t, func(t *testing.T) (store.DeviceCreateStore, store.Devices) {
		d := &memory.Devices{}
		return d, d
	})
}
