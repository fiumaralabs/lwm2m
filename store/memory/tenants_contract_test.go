package memory_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestTenants(t *testing.T) { storetest.Tenants(t, apiv1.NewMemoryTenantStore) }
