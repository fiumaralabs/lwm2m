package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/admission"
)

type admissionSettings struct {
	URL                           string
	Timeout                       time.Duration
	RequestKeyRef, ResponseKeyRef string
}

// admissionHook holds the optional Register admission components.
type admissionHook struct {
	client   *admission.Client
	bindings *admission.SQLiteBindingStore
}

func (h *admissionHook) Close() error {
	if h == nil {
		return nil
	}
	return h.bindings.Close()
}

func buildAdmissionHook(cfg admissionSettings, store string) (*admissionHook, error) {
	if cfg.URL == "" {
		if cfg.RequestKeyRef != "" || cfg.ResponseKeyRef != "" {
			return nil, fmt.Errorf("admission-url is required when key references are configured")
		}
		return nil, nil
	}
	if !strings.HasPrefix(store, "sqlite:") || strings.TrimPrefix(store, "sqlite:") == "" {
		return nil, fmt.Errorf("admission requires -store=sqlite:<path>")
	}
	if cfg.Timeout <= 0 || cfg.Timeout > 30*time.Second {
		return nil, fmt.Errorf("admission-timeout must be positive and at most 30s")
	}
	if strings.TrimSpace(cfg.RequestKeyRef) == "" || strings.TrimSpace(cfg.ResponseKeyRef) == "" || cfg.RequestKeyRef == cfg.ResponseKeyRef {
		return nil, fmt.Errorf("admission request and response key-ref must be distinct and nonempty")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "/v1/admission-decisions" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid admission-url: expected https URL with path /v1/admission-decisions")
	}
	bindings, err := admission.OpenSQLiteBindingStore(strings.TrimPrefix(store, "sqlite:"))
	if err != nil {
		return nil, fmt.Errorf("admission binding store: %w", err)
	}
	client := admission.NewClient(admission.Config{Enabled: true, Endpoint: cfg.URL, Timeout: cfg.Timeout, RequestSecretRefs: []string{cfg.RequestKeyRef}, ResponseSecretRefs: []string{cfg.ResponseKeyRef}, BindingStore: bindings})
	return &admissionHook{client: client, bindings: bindings}, nil
}
