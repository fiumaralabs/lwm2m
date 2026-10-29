package memory_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"testing"
)

func TestTimeSeries(t *testing.T) {
	storetest.TimeSeries(t, func() apiv1.TimeSeriesStore { return apiv1.NewMemoryTimeSeriesStore() })
}
