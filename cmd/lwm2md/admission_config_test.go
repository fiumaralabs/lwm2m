package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdmissionConfiguration(t *testing.T) {
	valid := admissionSettings{URL: "https://example.com/v1/admission-decisions", Timeout: 5 * time.Second, RequestKeyRef: "ADMISSION_REQUEST_KEY", ResponseKeyRef: "ADMISSION_RESPONSE_KEY"}
	db := "sqlite:" + filepath.Join(t.TempDir(), "daemon.db")
	tests := []struct {
		name, store string
		cfg         admissionSettings
		want        string
	}{
		{"disabled", "memory", admissionSettings{}, ""},
		{"enabled needs sqlite", "memory", valid, "requires -store=sqlite"},
		{"bad timeout", db, admissionSettings{URL: valid.URL, Timeout: -time.Second, RequestKeyRef: valid.RequestKeyRef, ResponseKeyRef: valid.ResponseKeyRef}, "admission-timeout"},
		{"timeout too long", db, admissionSettings{URL: valid.URL, Timeout: 31 * time.Second, RequestKeyRef: valid.RequestKeyRef, ResponseKeyRef: valid.ResponseKeyRef}, "admission-timeout"},
		{"missing key reference", db, admissionSettings{URL: valid.URL, Timeout: valid.Timeout}, "key-ref"},
		{"bad url", db, admissionSettings{URL: "http://example.com/", Timeout: valid.Timeout, RequestKeyRef: valid.RequestKeyRef, ResponseKeyRef: valid.ResponseKeyRef}, "admission-url"},
		{"valid", db, valid, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook, err := buildAdmissionHook(tt.cfg, tt.store)
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("error %v, want %q", err, tt.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.cfg.URL == "" {
				if hook != nil {
					t.Fatal("disabled hook must be nil")
				}
				return
			}
			if hook == nil || hook.client == nil || hook.bindings == nil {
				t.Fatal("enabled hook missing client or SQLite binding store")
			}
			if err := hook.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
	var hook *admissionHook
	if err := hook.Close(); err != nil {
		t.Fatal(err)
	}
}
