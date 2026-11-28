package obs

import (
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/fiumaralabs/lwm2m/store"
)

// Counters are process-local and contain only bounded, non-identifying labels.
type Counters struct {
	mu         sync.Mutex
	operations map[[2]string]uint64
	deliveries map[string]uint64
}

// Process is the daemon's process-local metric registry.
var Process Counters

var operationTypes = map[string]bool{
	"read": true, "write": true, "execute": true, "create": true, "delete": true, "discover": true, "write-attributes": true,
	"read-acl": true, "set-rights": true, "set-lifetime": true, "set-binding": true, "set-preferred-transport": true,
	"set-profile-hash-algorithm": true, "advertise-versions": true, "trigger-update": true, "trigger-bootstrap": true,
	"disable": true, "sync-clock": true, "firmware-reset": true, "raw": true,
}

// Operation records a successful transition to a terminal status.
func (c *Counters) Operation(kind, status string) {
	if !operationTypes[kind] {
		return
	}
	switch status {
	case "succeeded", "failed", "timeout", "expired", "dropped", "cancelled":
	default:
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.operations == nil {
		c.operations = make(map[[2]string]uint64)
	}
	c.operations[[2]string{kind, status}]++
}

type deliveryCounter struct {
	store.WebhookDeliveries
	counters *Counters
}

func (d deliveryCounter) Append(tenant, webhook string, delivery store.WebhookDelivery) {
	d.WebhookDeliveries.Append(tenant, webhook, delivery)
	result := "success"
	if delivery.Error != "" {
		result = "failure"
	}
	d.counters.mu.Lock()
	defer d.counters.mu.Unlock()
	if d.counters.deliveries == nil {
		d.counters.deliveries = make(map[string]uint64)
	}
	d.counters.deliveries[result]++
}

// WebhookDeliveries records each attempt while retaining the history store.
func (c *Counters) WebhookDeliveries(d store.WebhookDeliveries) store.WebhookDeliveries {
	return deliveryCounter{WebhookDeliveries: d, counters: c}
}

func (c *Counters) write(w io.Writer) {
	c.mu.Lock()
	operations := make(map[[2]string]uint64, len(c.operations))
	for k, v := range c.operations {
		operations[k] = v
	}
	deliveries := make(map[string]uint64, len(c.deliveries))
	for k, v := range c.deliveries {
		deliveries[k] = v
	}
	c.mu.Unlock()
	fmt.Fprint(w, "# HELP lwm2m_operations_total Operations reaching a terminal status.\n# TYPE lwm2m_operations_total counter\n")
	keys := make([][2]string, 0, len(operations))
	for key := range operations {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] == keys[j][0] {
			return keys[i][1] < keys[j][1]
		}
		return keys[i][0] < keys[j][0]
	})
	for _, k := range keys {
		fmt.Fprintf(w, "lwm2m_operations_total{type=%q,status=%q} %d\n", k[0], k[1], operations[k])
	}
	fmt.Fprint(w, "# HELP lwm2m_webhook_deliveries_total Webhook delivery attempts.\n# TYPE lwm2m_webhook_deliveries_total counter\n")
	for _, result := range []string{"success", "failure"} {
		if n := deliveries[result]; n > 0 {
			fmt.Fprintf(w, "lwm2m_webhook_deliveries_total{result=%q} %d\n", result, n)
		}
	}
}
