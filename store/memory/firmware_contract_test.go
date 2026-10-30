package memory_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestFirmware(t *testing.T) { storetest.Firmware(t, apiv1.NewMemoryFirmwareStore) }
