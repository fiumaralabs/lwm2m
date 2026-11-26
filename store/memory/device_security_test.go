package memory_test

import (
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestDeviceSecurityContract(t *testing.T) {
	d := &memory.Devices{}
	storetest.DeviceSecurity(t, d, d.Security())
}

func TestDeviceBootstrapContract(t *testing.T) {
	d := &memory.Devices{}
	storetest.DeviceBootstrap(t, d, d.Security(), nil)
}
