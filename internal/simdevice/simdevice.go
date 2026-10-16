// Package simdevice simulates a small battery-powered environment sensor on
// top of a testclient.Client: Server (/1), Device (/3), Connectivity
// Monitoring (/4), Location (/6), Temperature (/3303), Humidity (/3304) and
// Voltage (/3316). Step advances the simulation; values move smoothly, so
// Observe notifications look like a real device's.
//
// It is shared by cmd/lwm2m-client (the public demo client) and the
// lwm2m-demo probe.
package simdevice

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"math/rand/v2"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // the device's timezone works on hosts without zoneinfo

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/testclient"
)

// Options configures the simulated device.
type Options struct {
	Endpoint string // seeds the serial number and start values
	Lifetime uint32 // /1/0/1
	Binding  string // /1/0/7 and /3/0/16, default "U"
	// Firmware is /3/0/3; empty takes it from the build info.
	Firmware string
	// Drift moves the location by a few metres per hour.
	Drift bool
	// Seed fixes the random source (tests); 0 seeds from Endpoint.
	Seed uint64
}

// Timezone is the device's /3/0/15; the location is in the same zone.
const Timezone = "Europe/Amsterdam"

// Device is the simulated sensor.
type Device struct {
	c   *testclient.Client
	opt Options
	rng *rand.Rand
	tz  *time.Location

	mu         sync.Mutex
	last       time.Time
	battery    float64 // percent
	temp       float64 // degC, before rounding
	tempNoise  float64
	hum        float64
	humNoise   float64
	rsrp, sinr float64
	lat, lon   float64
	tMin, tMax float64
	hMin, hMax float64
	vMin, vMax float64
}

// Paths of the values that change over time.
var (
	PathTemperature = lwm2m.MustParsePath("/3303/0/5700")
	PathHumidity    = lwm2m.MustParsePath("/3304/0/5700")
	PathBattery     = lwm2m.MustParsePath("/3/0/9")
	PathVoltage     = lwm2m.MustParsePath("/3316/0/5700")
)

func p(s string) lwm2m.Path { return lwm2m.MustParsePath(s) }

func v(path string, val lwm2m.Value) lwm2m.Node { return lwm2m.ValueNode(p(path), val) }

// FirmwareVersion is the module version from the build info: a tag or
// pseudo-version under `go run ...@version`, else the VCS revision, else
// "dev".
func FirmwareVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if rev == "" {
		return "dev"
	}
	if dirty {
		rev += "-dirty"
	}
	return "dev-" + rev
}

// New fills c with the device's objects at time now.
func New(c *testclient.Client, opt Options, now time.Time) *Device {
	if opt.Binding == "" {
		opt.Binding = "U"
	}
	if opt.Lifetime == 0 {
		opt.Lifetime = 300
	}
	if opt.Firmware == "" {
		opt.Firmware = FirmwareVersion()
	}
	h := sha256.Sum256([]byte("fiumaralabs-sim:" + opt.Endpoint))
	seed := opt.Seed
	if seed == 0 {
		for _, b := range h[:8] {
			seed = seed<<8 | uint64(b)
		}
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	tz, err := time.LoadLocation(Timezone)
	if err != nil {
		tz = time.UTC
	}
	d := &Device{c: c, opt: opt, rng: rng, tz: tz, last: now}
	d.battery = 70 + 25*rng.Float64()
	d.temp = d.tempBase(now)
	d.hum = d.humBase(d.temp)
	d.rsrp = -95 + 10*rng.Float64()
	d.sinr = 8 + 6*rng.Float64()
	// Somewhere in Utrecht (NL), within about 2 km of the centre.
	d.lat = 52.0907 + (rng.Float64()-0.5)*0.03
	d.lon = 5.1214 + (rng.Float64()-0.5)*0.05
	t, hm := round(d.temp, 1), round(d.hum, 1)
	d.tMin, d.tMax, d.hMin, d.hMax = t, t, hm, hm
	vb := d.voltage()
	d.vMin, d.vMax = vb, vb

	serial := "FL-" + strings.ToUpper(hex.EncodeToString(h[8:12]))
	nodes := []lwm2m.Node{
		// Server
		v("/1/0/0", lwm2m.Integer(1)),
		v("/1/0/1", lwm2m.Integer(int64(opt.Lifetime))),
		v("/1/0/6", lwm2m.Boolean(false)),
		v("/1/0/7", lwm2m.String(opt.Binding)),
		// Device
		v("/3/0/0", lwm2m.String("Fiumara Labs")),
		v("/3/0/1", lwm2m.String("FL-ENV-1")),
		v("/3/0/2", lwm2m.String(serial)),
		v("/3/0/3", lwm2m.String(opt.Firmware)),
		v("/3/0/6/0", lwm2m.Integer(1)), // Internal Battery
		v("/3/0/8/0", lwm2m.Integer(4)), // mA
		v("/3/0/10", lwm2m.Integer(112)),
		v("/3/0/11/0", lwm2m.Integer(0)),
		v("/3/0/15", lwm2m.String(Timezone)),
		v("/3/0/16", lwm2m.String(opt.Binding)),
		v("/3/0/17", lwm2m.String("Environment sensor")),
		v("/3/0/18", lwm2m.String("rev B")),
		v("/3/0/19", lwm2m.String("lwm2m-client "+opt.Firmware)),
		v("/3/0/21", lwm2m.Integer(256)),
		// Connectivity Monitoring: LTE-M reports as LTE-FDD (6); NB-IoT (7) also available.
		v("/4/0/0", lwm2m.Integer(6)),
		v("/4/0/1/0", lwm2m.Integer(6)),
		v("/4/0/1/1", lwm2m.Integer(7)),
		v("/4/0/4/0", lwm2m.String("192.0.2."+strconv.Itoa(10+rng.IntN(200)))), // TEST-NET-1
		v("/4/0/7/0", lwm2m.String("iot.example")),
		v("/4/0/8", lwm2m.Integer(int64(20000+rng.IntN(40000)))),
		v("/4/0/9", lwm2m.Integer(8)),
		v("/4/0/10", lwm2m.Integer(204)),
		// Location
		v("/6/0/2", lwm2m.Float(4)),
		// Temperature
		v("/3303/0/5701", lwm2m.String("Cel")),
		v("/3303/0/5603", lwm2m.Float(-40)),
		v("/3303/0/5604", lwm2m.Float(85)),
		v("/3303/0/5750", lwm2m.String("Indoor")),
		// Humidity
		v("/3304/0/5701", lwm2m.String("%RH")),
		v("/3304/0/5603", lwm2m.Float(0)),
		v("/3304/0/5604", lwm2m.Float(100)),
		// Voltage (battery)
		v("/3316/0/5701", lwm2m.String("V")),
		v("/3316/0/5603", lwm2m.Float(0)),
		v("/3316/0/5604", lwm2m.Float(5)),
		v("/3316/0/5750", lwm2m.String("Battery")),
	}
	c.SetNodes(append(nodes, d.dynamic(now)...))
	return d
}

// tempBase is the slow indoor daily cycle: warmest mid-afternoon.
func (d *Device) tempBase(now time.Time) float64 {
	t := now.In(d.tz)
	hour := float64(t.Hour()) + float64(t.Minute())/60
	return 21.2 + 1.4*math.Sin((hour-9)/24*2*math.Pi)
}

// humBase falls as the room warms up.
func (d *Device) humBase(temp float64) float64 { return 46 - 2.5*(temp-21.2) }

func (d *Device) voltage() float64 { return round(3.30+0.85*d.battery/100, 2) }

// Step advances the simulation to now and stores the changed values, which
// notifies observers (each observation at most once per step).
func (d *Device) Step(now time.Time) {
	d.c.SetNodes(d.advance(now))
}

func (d *Device) advance(now time.Time) []lwm2m.Node {
	d.mu.Lock()
	defer d.mu.Unlock()
	dt := now.Sub(d.last).Seconds()
	if dt < 0 {
		dt = 0
	}
	d.last = now
	// Battery: about 1 % per 20 minutes of demo time, never below 5 %.
	d.battery = math.Max(5, d.battery-dt/1200)
	// Mean-reverting noise (Ornstein-Uhlenbeck) on top of the daily cycle.
	d.tempNoise = ou(d.rng, d.tempNoise, dt, 120, 0.25)
	d.temp = d.tempBase(now) + d.tempNoise
	d.humNoise = ou(d.rng, d.humNoise, dt, 180, 0.8)
	d.hum = clamp(d.humBase(d.temp)+d.humNoise, 0, 100)
	d.rsrp = clamp(-90+ou(d.rng, d.rsrp+90, dt, 60, 4), -120, -70)
	d.sinr = clamp(11+ou(d.rng, d.sinr-11, dt, 60, 3), -5, 25)
	if d.opt.Drift {
		d.lat += d.rng.NormFloat64() * 2e-6 * math.Sqrt(dt)
		d.lon += d.rng.NormFloat64() * 3e-6 * math.Sqrt(dt)
	}
	t, h, vb := round(d.temp, 1), round(d.hum, 1), d.voltage()
	d.tMin, d.tMax = math.Min(d.tMin, t), math.Max(d.tMax, t)
	d.hMin, d.hMax = math.Min(d.hMin, h), math.Max(d.hMax, h)
	d.vMin, d.vMax = math.Min(d.vMin, vb), math.Max(d.vMax, vb)
	return d.nodesLocked(now)
}

func (d *Device) dynamic(now time.Time) []lwm2m.Node {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nodesLocked(now)
}

func (d *Device) nodesLocked(now time.Time) []lwm2m.Node {
	batt := int64(math.Round(d.battery))
	status := int64(0) // Normal
	if batt < 20 {
		status = 4 // Low Battery
	}
	vb := d.voltage()
	// RSRQ-style link quality 0..34 from SINR.
	lq := int64(clamp(math.Round((d.sinr+5)*34/30), 0, 34))
	_, off := now.In(d.tz).Zone()
	return []lwm2m.Node{
		v("/3/0/7/0", lwm2m.Integer(int64(math.Round(vb*1000)))),
		v("/3/0/9", lwm2m.Integer(batt)),
		v("/3/0/13", lwm2m.TimeOf(now)),
		v("/3/0/14", lwm2m.String(utcOffset(off))),
		v("/3/0/20", lwm2m.Integer(status)),
		v("/4/0/2", lwm2m.Integer(int64(math.Round(d.rsrp)))),
		v("/4/0/3", lwm2m.Integer(lq)),
		v("/6/0/0", lwm2m.Float(round(d.lat, 6))),
		v("/6/0/1", lwm2m.Float(round(d.lon, 6))),
		v("/6/0/3", lwm2m.Float(round(clamp(40-d.sinr, 10, 60), 0))),
		v("/6/0/5", lwm2m.TimeOf(now)),
		v("/3303/0/5700", lwm2m.Float(round(d.temp, 1))),
		v("/3303/0/5601", lwm2m.Float(d.tMin)),
		v("/3303/0/5602", lwm2m.Float(d.tMax)),
		v("/3304/0/5700", lwm2m.Float(round(d.hum, 1))),
		v("/3304/0/5601", lwm2m.Float(d.hMin)),
		v("/3304/0/5602", lwm2m.Float(d.hMax)),
		v("/3316/0/5700", lwm2m.Float(vb)),
		v("/3316/0/5601", lwm2m.Float(d.vMin)),
		v("/3316/0/5602", lwm2m.Float(d.vMax)),
	}
}

// ResetMinMax implements Execute "Reset Min and Max Measured Values"
// (/33xx/0/5605) for oid 3303, 3304 or 3316. It reports whether oid is one.
func (d *Device) ResetMinMax(oid uint16) bool {
	d.mu.Lock()
	t, h, vb := round(d.temp, 1), round(d.hum, 1), d.voltage()
	switch oid {
	case 3303:
		d.tMin, d.tMax = t, t
	case 3304:
		d.hMin, d.hMax = h, h
	case 3316:
		d.vMin, d.vMax = vb, vb
	default:
		d.mu.Unlock()
		return false
	}
	nodes := d.nodesLocked(d.last)
	d.mu.Unlock()
	d.c.SetNodes(nodes)
	return true
}

// Sample is the Send payload: temperature, humidity and battery level.
func (d *Device) Sample() []lwm2m.Node {
	var out []lwm2m.Node
	for _, q := range []lwm2m.Path{PathTemperature, PathHumidity, PathBattery} {
		if val, ok := d.c.Get(q); ok {
			out = append(out, lwm2m.ValueNode(q, val))
		}
	}
	return out
}

func ou(r *rand.Rand, x, dt, tau, sigma float64) float64 {
	if dt == 0 {
		return x
	}
	a := math.Exp(-dt / tau)
	return x*a + sigma*math.Sqrt(1-a*a)*r.NormFloat64()
}

func round(x float64, n int) float64 {
	m := math.Pow(10, float64(n))
	return math.Round(x*m) / m
}

func clamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }

func utcOffset(sec int) string {
	sign := "+"
	if sec < 0 {
		sign, sec = "-", -sec
	}
	return sign + two(sec/3600) + ":" + two(sec%3600/60)
}

func two(n int) string { return string([]byte{byte('0' + n/10), byte('0' + n%10)}) }
