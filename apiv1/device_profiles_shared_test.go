package apiv1_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestMemoryDeviceProfilesContract(t *testing.T) {
	storetest.DeviceProfiles(t, apiv1.NewMemoryDeviceProfileStore)
}
