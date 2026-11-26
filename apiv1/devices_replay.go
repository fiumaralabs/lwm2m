package apiv1

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/fiumaralabs/lwm2m/store"
)

// The marker is stored with the replay response but never sent to clients.
// Missing markers (records written by older versions) are treated as scoped.
const deviceReplayCredentialScope = "X-Internal-Device-Credential-Scope"
const deviceReplayBootstrapScope = "X-Internal-Device-Bootstrap-Scope"

func deviceCreateBodyRequiresCredentialScope(body []byte) bool {
	var input struct {
		Credentials json.RawMessage `json:"credentials"`
		Bootstrap   json.RawMessage `json:"bootstrap"`
	}
	if json.Unmarshal(body, &input) != nil {
		// Fail closed when the request cannot be classified.
		return true
	}
	var config bootstrapInput
	if len(input.Bootstrap) != 0 && json.Unmarshal(input.Bootstrap, &config) != nil {
		return true
	}
	for _, entry := range config.Servers {
		if entry.Server == "self" && entry.Security.PSK != nil && entry.Security.PSK.Generate {
			return true
		}
	}
	return len(input.Credentials) != 0
}

func authorizeDeviceReplayCredentials(w http.ResponseWriter, r *http.Request) bool {
	authorized := false
	probe := &deviceReplayWriter{header: make(http.Header)}
	RequireScope("credentials:write")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { authorized = true })).ServeHTTP(probe, r)
	if !authorized {
		if probe.status == http.StatusForbidden {
			WriteProblem(w, http.StatusForbidden, "insufficient_scope", "Insufficient scope")
		} else {
			WriteProblem(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		}
	}
	return authorized
}

func deviceCreateBodyRequiresBootstrapScope(body []byte) bool {
	var input struct {
		Bootstrap json.RawMessage `json:"bootstrap"`
	}
	return json.Unmarshal(body, &input) != nil || len(input.Bootstrap) != 0
}
func authorizeDeviceReplayBootstrap(w http.ResponseWriter, r *http.Request) bool {
	authorized := false
	RequireScope("bootstrap:write")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { authorized = true })).ServeHTTP(w, r)
	return authorized
}

type deviceCreateReplayStore interface {
	ReplayDeviceCreate(ctx context.Context, tenant, key string, run func() (int, http.Header, []byte)) (int, http.Header, []byte, error)
}

type deviceReplayWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *deviceReplayWriter) Header() http.Header { return w.header }
func (w *deviceReplayWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *deviceReplayWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(p)
}

func (o Options) replayDeviceCreate(w http.ResponseWriter, r *http.Request, tenant, key string) {
	if _, ok := o.Devices.(store.AtomicDeviceReplayStore); !ok {
		WriteProblem(w, 501, "not_implemented", "Atomic device create replay is not supported by this store")
		return
	}
	s, ok := o.Devices.(deviceCreateReplayStore)
	if !ok {
		WriteProblem(w, 501, "not_implemented", "Device create idempotency is not supported by this store")
		return
	}
	ran := false
	status, header, body, err := s.ReplayDeviceCreate(r.Context(), tenant, key, func() (int, http.Header, []byte) {
		ran = true
		recorder := &deviceReplayWriter{header: make(http.Header)}
		data, readErr := io.ReadAll(r.Body)
		requiresScope := readErr != nil || deviceCreateBodyRequiresCredentialScope(data)
		marker := "0"
		if requiresScope {
			marker = "1"
		}
		recorder.header.Set(deviceReplayCredentialScope, marker)
		bootstrapMarker := "0"
		if readErr != nil || deviceCreateBodyRequiresBootstrapScope(data) {
			bootstrapMarker = "1"
		}
		recorder.header.Set(deviceReplayBootstrapScope, bootstrapMarker)
		if readErr != nil {
			WriteProblem(recorder, 400, "invalid_json", "Cannot read request body")
			return recorder.status, recorder.header, recorder.body.Bytes()
		}
		if json.Valid(data) && requiresScope && !authorizeDeviceReplayCredentials(recorder, r) {
			return recorder.status, recorder.header, recorder.body.Bytes()
		}
		request := r.Clone(r.Context())
		request.Body = io.NopCloser(bytes.NewReader(data))
		request.Header.Del("Idempotency-Key")
		var savedStatus int
		var savedHeader http.Header
		var savedBody []byte
		replay := &store.DeviceCreateReplay{Key: key, Render: func(d store.Device) (int, http.Header, []byte) {
			response := &deviceReplayWriter{header: make(http.Header)}
			response.header.Set(deviceReplayCredentialScope, marker)
			response.header.Set(deviceReplayBootstrapScope, bootstrapMarker)
			response.header.Set("Location", "/v1/devices/"+url.PathEscape(d.Endpoint))
			deviceJSON(response, 201, o.view(tenant, d))
			savedStatus, savedHeader, savedBody = response.status, response.header.Clone(), append([]byte(nil), response.body.Bytes()...)
			return savedStatus, savedHeader, savedBody
		}}
		request = request.WithContext(store.WithDeviceCreateReplay(request.Context(), replay))
		o.serveDevices(recorder, request)
		if savedStatus != 0 && recorder.status == http.StatusCreated {
			return savedStatus, savedHeader, savedBody
		}
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		return recorder.status, recorder.header, recorder.body.Bytes()
	})
	if err != nil {
		return
	}
	if !ran {
		data, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			WriteProblem(w, 400, "invalid_json", "Cannot read request body")
			return
		}
		if ((header.Get(deviceReplayBootstrapScope) != "" && header.Get(deviceReplayBootstrapScope) != "0") || deviceCreateBodyRequiresBootstrapScope(data)) && !authorizeDeviceReplayBootstrap(w, r) {
			return
		}
		if (header.Get(deviceReplayCredentialScope) != "0" || deviceCreateBodyRequiresCredentialScope(data)) && !authorizeDeviceReplayCredentials(w, r) {
			return
		}
	}
	header.Del(deviceReplayBootstrapScope)
	header.Del(deviceReplayCredentialScope)
	for k, values := range header {
		w.Header()[k] = values
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
