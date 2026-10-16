package main

import (
	"context"
	"log"
	"math"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/testclient"
)

func set(c *testclient.Client, p string, v lwm2m.Value) { c.Set(lwm2m.MustParsePath(p), v) }

// richDevice fills in a device that looks like a small battery-powered
// sensor, so the web page has names, units, enumerations and
// multi-instance resources to show. Addresses use TEST-NET-1.
func richDevice(c *testclient.Client) {
	set(c, "/1/0/0", lwm2m.Integer(1))
	set(c, "/1/0/1", lwm2m.Integer(300))
	set(c, "/1/0/6", lwm2m.Boolean(true))
	set(c, "/1/0/7", lwm2m.String("U"))

	set(c, "/3/0/3", lwm2m.String("1.4.2"))
	set(c, "/3/0/6/0", lwm2m.Integer(1))
	set(c, "/3/0/6/1", lwm2m.Integer(5))
	set(c, "/3/0/7/0", lwm2m.Integer(3712))
	set(c, "/3/0/7/1", lwm2m.Integer(5020))
	set(c, "/3/0/9", lwm2m.Integer(87))
	set(c, "/3/0/10", lwm2m.Integer(112))
	set(c, "/3/0/11/0", lwm2m.Integer(0))
	set(c, "/3/0/13", lwm2m.TimeOf(time.Now()))
	set(c, "/3/0/14", lwm2m.String("+02:00"))
	set(c, "/3/0/15", lwm2m.String("Europe/Amsterdam"))
	set(c, "/3/0/17", lwm2m.String("Environment sensor"))
	set(c, "/3/0/18", lwm2m.String("rev B"))
	set(c, "/3/0/19", lwm2m.String("probe "+time.Now().Format("2006-01-02")))
	set(c, "/3/0/20", lwm2m.Integer(1))
	set(c, "/3/0/21", lwm2m.Integer(256))

	set(c, "/4/0/0", lwm2m.Integer(6))
	set(c, "/4/0/1/0", lwm2m.Integer(6))
	set(c, "/4/0/1/1", lwm2m.Integer(7))
	set(c, "/4/0/2", lwm2m.Integer(-87))
	set(c, "/4/0/3", lwm2m.Integer(18))
	set(c, "/4/0/4/0", lwm2m.String("192.0.2.10"))
	set(c, "/4/0/7/0", lwm2m.String("iot.example"))
	set(c, "/4/0/8", lwm2m.Integer(26113))
	set(c, "/4/0/9", lwm2m.Integer(4))
	set(c, "/4/0/10", lwm2m.Integer(204))

	set(c, "/6/0/0", lwm2m.Float(52.0907))
	set(c, "/6/0/1", lwm2m.Float(5.1214))
	set(c, "/6/0/2", lwm2m.Float(5))
	set(c, "/6/0/5", lwm2m.TimeOf(time.Now()))

	set(c, "/3303/0/5700", lwm2m.Float(21.4))
	set(c, "/3303/0/5701", lwm2m.String("Cel"))
	set(c, "/3303/0/5601", lwm2m.Float(19.8))
	set(c, "/3303/0/5602", lwm2m.Float(23.1))
	set(c, "/3303/0/5750", lwm2m.String("Indoor"))
	set(c, "/3303/1/5700", lwm2m.Float(11.6))
	set(c, "/3303/1/5701", lwm2m.String("Cel"))
	set(c, "/3303/1/5750", lwm2m.String("Outdoor"))
}

// holdFor stays registered for d, sending an Update every half lifetime
// and, for a rich device, changing the temperature every 5 s (observers
// are notified by Set).
func holdFor(c *testclient.Client, d time.Duration, rich bool, lifetime uint32) {
	if rich {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		nodes := []lwm2m.Node{
			{Path: lwm2m.MustParsePath("/3303/0/5700"), Kind: lwm2m.KindValue, Value: lwm2m.Float(21.4)},
			{Path: lwm2m.MustParsePath("/3/0/9"), Kind: lwm2m.KindValue, Value: lwm2m.Integer(87)},
		}
		if _, err := c.Send(ctx, nodes, lwm2m.FormatSenMLCBOR); err != nil {
			log.Printf("send: %v", err)
		}
		cancel()
	}
	end := time.Now().Add(d)
	nextUpdate := time.Now().Add(time.Duration(lifetime) * time.Second / 2)
	for start := time.Now(); time.Now().Before(end); {
		wait := time.Until(end)
		if wait > 5*time.Second {
			wait = 5 * time.Second
		}
		time.Sleep(wait)
		if rich {
			t := time.Since(start).Seconds()
			v := math.Round((21.4+0.6*math.Sin(t/40))*10) / 10
			set(c, "/3303/0/5700", lwm2m.Float(v))
			set(c, "/3/0/13", lwm2m.TimeOf(time.Now()))
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
