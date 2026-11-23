package memory_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestFirmwareContent(t *testing.T) { storetest.FirmwareContent(t, apiv1.NewMemoryFirmwareStore) }
