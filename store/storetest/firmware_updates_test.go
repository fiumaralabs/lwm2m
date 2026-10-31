package storetest_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestFirmwareUpdatesMemoryContract(t *testing.T) {
	storetest.FirmwareUpdates(t, apiv1.NewMemoryFirmwareUpdateStore)
}
