package storetest_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestBootstrapSessionsMemory(t *testing.T) {
	storetest.BootstrapSessions(t, apiv1.NewMemoryBootstrapSessionStore)
}
