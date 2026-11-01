package apiv1

import (
	"bytes"
	"context"
	"net/http"
)

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
	s, ok := o.Devices.(deviceCreateReplayStore)
	if !ok {
		WriteProblem(w, 501, "not_implemented", "Device create idempotency is not supported by this store")
		return
	}
	status, header, body, err := s.ReplayDeviceCreate(r.Context(), tenant, key, func() (int, http.Header, []byte) {
		recorder := &deviceReplayWriter{header: make(http.Header)}
		request := r.Clone(r.Context())
		request.Header.Del("Idempotency-Key")
		o.serveDevices(recorder, request)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		return recorder.status, recorder.header, recorder.body.Bytes()
	})
	if err != nil {
		return
	}
	for k, values := range header {
		w.Header()[k] = values
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
