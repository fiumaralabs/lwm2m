package apiv1

import (
	"testing"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestJobRunnerReceivesProfileStores(t *testing.T) {
	for _, defaults := range []bool{false, true} {
		name := "supplied"
		if defaults {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			srv := server.New(server.Config{})
			t.Cleanup(func() { srv.Close() })
			executor := operations.NewExecutor(srv, memory.NewOperations(), operations.ExecutorOptions{})
			t.Cleanup(func() { executor.Close() })
			runner := &JobRunner{}
			opts := Options{JobRunner: runner, Executor: executor}
			profiles := NewMemoryDeviceProfileStore()
			observations := &memory.Observations{}
			if !defaults {
				opts.DeviceProfiles, opts.Observations = profiles, observations
			}
			New(opts)
			if runner.DeviceProfiles == nil || runner.Observations == nil {
				t.Fatal("runner profile stores are not wired")
			}
			if !defaults && (runner.DeviceProfiles != profiles || runner.Observations != observations) {
				t.Fatal("runner does not share handler profile stores")
			}
		})
	}
}
