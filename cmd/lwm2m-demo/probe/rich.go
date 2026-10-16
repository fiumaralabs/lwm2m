package main

import (
	"context"
	"log"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/simdevice"
	"github.com/fiumaralabs/lwm2m/testclient"
)

// richDevice fills in the simulated sensor that cmd/lwm2m-client also
// runs (internal/simdevice), so the web page has names, units,
// enumerations and multi-instance resources to show.
func richDevice(c *testclient.Client, ep string, lifetime uint32) *simdevice.Device {
	return simdevice.New(c, simdevice.Options{Endpoint: ep, Lifetime: lifetime}, time.Now())
}

// holdFor stays registered for d, sending an Update every half lifetime
// and, for a rich device, advancing the simulation every 5 s (observers
// are notified by the store).
func holdFor(c *testclient.Client, d time.Duration, dev *simdevice.Device, lifetime uint32) {
	if dev != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if _, err := c.Send(ctx, dev.Sample(), lwm2m.FormatSenMLCBOR); err != nil {
			log.Printf("send: %v", err)
		}
		cancel()
	}
	end := time.Now().Add(d)
	nextUpdate := time.Now().Add(time.Duration(lifetime) * time.Second / 2)
	for time.Now().Before(end) {
		wait := time.Until(end)
		if wait > 5*time.Second {
			wait = 5 * time.Second
		}
		time.Sleep(wait)
		if dev != nil {
			dev.Step(time.Now())
		}
		if time.Now().After(nextUpdate) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if _, err := c.Update(ctx, nil, nil); err != nil {
				log.Printf("update: %v", err)
			}
			cancel()
			nextUpdate = time.Now().Add(time.Duration(lifetime) * time.Second / 2)
		}
	}
}
