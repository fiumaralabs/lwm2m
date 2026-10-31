package storetest

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"testing"
)

func TestMemoryJobs(t *testing.T) { Jobs(t, apiv1.NewMemoryJobStore) }
