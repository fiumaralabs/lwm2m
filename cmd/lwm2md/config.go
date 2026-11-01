package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
)

// Pointer fields distinguish an omitted setting from an explicit zero value.
type fileConfig struct {
	CoAP            *string `json:"coap"`
	CoAPS           *string `json:"coaps"`
	TCP             *string `json:"tcp"`
	HTTP            *string `json:"http"`
	Metrics         *bool   `json:"metrics"`
	TimeSeries      *bool   `json:"timeseries"`
	JobTickInterval *string `json:"job-tick-interval"`
	Store           *string `json:"store"`
	Prefix          *string `json:"prefix"`
	APIKeys         *string `json:"api-keys"`
	BSCoAP          *string `json:"bs-coap"`
	BSCoAPS         *string `json:"bs-coaps"`
	BSHTTP          *string `json:"bs-http"`
	CID             *int    `json:"cid"`
	FWDir           *string `json:"fw-dir"`
	FWCoAP          *string `json:"fw-coap"`
	FWCoAPS         *string `json:"fw-coaps"`
	Verbose         *bool   `json:"v"`
}

// applyConfig runs after flag parsing, preserving every explicitly set flag.
func applyConfig(fs *flag.FlagSet, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("config %q: %w", path, err)
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return fmt.Errorf("config %q: %w", path, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return fmt.Errorf("config %q: expected JSON object", path)
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("config %q: %s must not be null", path, name)
		}
	}
	var cfg fileConfig
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&cfg); err != nil {
		return fmt.Errorf("config %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("config %q: multiple JSON values", path)
		}
		return fmt.Errorf("config %q: %w", path, err)
	}
	explicit := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	v := reflect.ValueOf(cfg)
	typ := v.Type()
	for i := 0; i < v.NumField(); i++ {
		name := typ.Field(i).Tag.Get("json")
		value := v.Field(i)
		if value.IsNil() || explicit[name] {
			continue
		}
		value = value.Elem()
		var s string
		switch value.Kind() {
		case reflect.String:
			s = value.String()
		case reflect.Bool:
			s = strconv.FormatBool(value.Bool())
		case reflect.Int:
			s = strconv.FormatInt(value.Int(), 10)
		}
		if err := fs.Set(name, s); err != nil {
			return fmt.Errorf("config %q: %s: %w", path, name, err)
		}
	}
	return nil
}
