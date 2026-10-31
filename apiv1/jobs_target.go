package apiv1

import (
	"context"
	"errors"
	"sort"

	"github.com/fiumaralabs/lwm2m/selector"
	"github.com/fiumaralabs/lwm2m/store"
)

// resolveJobTarget looks up only devices owned by tenant. A caller must persist
// the resulting snapshot before dispatch, rather than resolve it on every tick.
func resolveJobTarget(ctx context.Context, devices store.Devices, tenant string, target JobTarget) ([]string, error) {
	result := []string{}
	if target.Devices != nil {
		seen := make(map[string]bool, len(target.Devices))
		for _, endpoint := range target.Devices {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if seen[endpoint] {
				continue
			}
			seen[endpoint] = true
			_, err := devices.Get(tenant, endpoint)
			if errors.Is(err, store.ErrDeviceNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			result = append(result, endpoint)
		}
		sort.Strings(result)
		return result, nil
	}
	match, err := selector.Parse(target.Selector)
	if err != nil {
		return nil, err
	}
	cursor := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, next, err := devices.List(tenant, store.DeviceFilter{}, cursor, 500)
		if err != nil {
			return nil, err
		}
		for _, d := range batch {
			if match.Matches(d.Labels) {
				result = append(result, d.Endpoint)
			}
		}
		if next == "" {
			return result, nil
		}
		cursor = next
	}
}
