package memory_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestModels(t *testing.T) { storetest.Models(t, apiv1.NewMemoryModelStore) }
