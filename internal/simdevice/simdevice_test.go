package simdevice

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/testclient"
)

func get(t *testing.T, c *testclient.Client, path string) lwm2m.Value {
	t.Helper()
	val, ok := c.Get(lwm2m.MustParsePath(path))
	if !ok {
		t.Fatalf("%s not set", path)
	}
	return val
}

func TestObjectsAndStaticValues(t *testing.T) {
	c := testclient.New(testclient.Config{Endpoint: "demo-abc123"})
	now := time.Date(2026, 10, 16, 14, 0, 0, 0, time.UTC)
	New(c, Options{Endpoint: "demo-abc123", Lifetime: 300, Firmware: "v1.2.3"}, now)
	if got := c.ObjectLinks(); got != "</1/0>,</3/0>,</4/0>,</6/0>,</3303/0>,</3304/0>,</3316/0>" {
		t.Errorf("object links = %s", got)
	}
	for path, want := range map[string]string{
		"/3/0/0": "Fiumara Labs", "/3/0/3": "v1.2.3", "/3/0/15": "Europe/Amsterdam",
		"/3/0/14": "+02:00", "/3/0/16": "U", "/3303/0/5701": "Cel", "/3304/0/5701": "%RH", "/3316/0/5701": "V",
	} {
		if got := get(t, c, path).Str; got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if s := get(t, c, "/3/0/2").Str; !strings.HasPrefix(s, "FL-") || len(s) != 11 {
		t.Errorf("serial = %q", s)
	}
	if get(t, c, "/1/0/1").Int != 300 || get(t, c, "/3/0/6/0").Int != 1 || get(t, c, "/4/0/0").Int != 6 {
		t.Error("lifetime, power source or bearer wrong")
	}
	// Same endpoint, same serial; another endpoint, another serial.
	c2 := testclient.New(testclient.Config{})
	New(c2, Options{Endpoint: "demo-abc124"}, now)
	if get(t, c2, "/3/0/2").Str == get(t, c, "/3/0/2").Str {
		t.Error("serial does not depend on the endpoint")
	}
	// Winter time: UTC offset follows the timezone.
	c3 := testclient.New(testclient.Config{})
	New(c3, Options{Endpoint: "x"}, time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC))
	if got := get(t, c3, "/3/0/14").Str; got != "+01:00" {
		t.Errorf("winter offset = %q", got)
	}
}

func TestValuesChangeSmoothlyWithinRange(t *testing.T) {
	c := testclient.New(testclient.Config{})
	now := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	d := New(c, Options{Endpoint: "demo-000001", Seed: 7, Drift: true}, now)
	batt0 := get(t, c, "/3/0/9").Int
	lat0 := get(t, c, "/6/0/0").Float
	prev := get(t, c, "/3303/0/5700").Float
	distinct := map[float64]bool{prev: true}
	minT, maxT := prev, prev
	for i := 0; i < 720; i++ { // one hour in 5 s steps
		now = now.Add(5 * time.Second)
		d.Step(now)
		temp := get(t, c, "/3303/0/5700").Float
		if math.Abs(temp-prev) > 0.6 {
			t.Fatalf("step %d: temperature jumped %v -> %v", i, prev, temp)
		}
		prev = temp
		distinct[temp] = true
		minT, maxT = math.Min(minT, temp), math.Max(maxT, temp)
		if temp < 15 || temp > 28 {
			t.Fatalf("temperature %v out of range", temp)
		}
		if h := get(t, c, "/3304/0/5700").Float; h < 20 || h > 80 {
			t.Fatalf("humidity %v out of range", h)
		}
		if r := get(t, c, "/4/0/2").Int; r < -120 || r > -70 {
			t.Fatalf("RSRP %v out of range", r)
		}
		if q := get(t, c, "/4/0/3").Int; q < 0 || q > 34 {
			t.Fatalf("link quality %v out of range", q)
		}
	}
	if len(distinct) < 5 {
		t.Errorf("temperature barely changed: %d distinct values", len(distinct))
	}
	if get(t, c, "/3303/0/5601").Float != minT || get(t, c, "/3303/0/5602").Float != maxT {
		t.Errorf("min/max = %v/%v, want %v/%v", get(t, c, "/3303/0/5601").Float, get(t, c, "/3303/0/5602").Float, minT, maxT)
	}
	if b := get(t, c, "/3/0/9").Int; b >= batt0 || b < batt0-4 {
		t.Errorf("battery %d -> %d after an hour", batt0, b)
	}
	if mv := get(t, c, "/3/0/7/0").Int; mv < 3300 || mv > 4150 || float64(mv)/1000 != get(t, c, "/3316/0/5700").Float {
		t.Errorf("voltage %d mV vs /3316 %v", mv, get(t, c, "/3316/0/5700").Float)
	}
	if lat := get(t, c, "/6/0/0").Float; lat == lat0 || math.Abs(lat-lat0) > 0.01 {
		t.Errorf("drift %v -> %v", lat0, lat)
	}
	if got := get(t, c, "/3/0/13").Int; got != now.Unix() {
		t.Errorf("current time = %v, want %v", got, now.Unix())
	}

	if !d.ResetMinMax(3303) || d.ResetMinMax(3) {
		t.Fatal("ResetMinMax result")
	}
	cur := get(t, c, "/3303/0/5700").Float
	if get(t, c, "/3303/0/5601").Float != cur || get(t, c, "/3303/0/5602").Float != cur {
		t.Error("min/max not reset to the current value")
	}
	if n := d.Sample(); len(n) != 3 || n[0].Path != PathTemperature {
		t.Errorf("sample = %v", n)
	}
}

func TestLowBatteryStatus(t *testing.T) {
	c := testclient.New(testclient.Config{})
	now := time.Now()
	d := New(c, Options{Endpoint: "x"}, now)
	d.Step(now.Add(48 * time.Hour))
	if get(t, c, "/3/0/9").Int != 5 || get(t, c, "/3/0/20").Int != 4 {
		t.Errorf("battery %d status %d, want 5 / Low Battery", get(t, c, "/3/0/9").Int, get(t, c, "/3/0/20").Int)
	}
}

func TestFirmwareVersion(t *testing.T) {
	if FirmwareVersion() == "" {
		t.Error("empty firmware version")
	}
}
