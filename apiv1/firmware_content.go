package apiv1

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
)

// FirmwareContentStore atomically updates content and metadata for an artifact.
// A ready artifact cannot be uploaded again; API.md does not specify replacement.
type FirmwareContentStore interface {
	FirmwareContentLimit() int64
	PutFirmwareContent(tenant, id string, content []byte) (found, conflict bool)
	GetFirmwareContent(tenant, id string) ([]byte, bool)
}

func (s *memoryFirmwareStore) FirmwareContentLimit() int64 { return s.maxContent }
func (s *memoryFirmwareStore) PutFirmwareContent(tenant, id string, data []byte) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.items[tenant][id]
	if !ok {
		return false, false
	}
	if f.Status == "ready" {
		return true, true
	}
	if s.content[tenant] == nil {
		s.content[tenant] = make(map[string][]byte)
	}
	s.content[tenant][id] = append([]byte{}, data...)
	sum := sha256.Sum256(data)
	f.Status = "ready"
	f.Size = int64(len(data))
	f.SHA256 = hex.EncodeToString(sum[:])
	s.items[tenant][id] = f
	return true, false
}
func (s *memoryFirmwareStore) GetFirmwareContent(tenant, id string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.content[tenant][id]
	if !ok {
		return nil, false
	}
	return append([]byte{}, data...), true
}
func serveFirmwareContent(metadata FirmwareStore, w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPut && r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, PUT")
		WriteProblem(w, 405, "method_not_allowed", "Method not allowed")
		return
	}
	scope := "firmware:write"
	if r.Method == http.MethodGet {
		scope = "devices:read"
	}
	RequireScope(scope)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant := TenantFrom(r.Context())
		if _, ok := metadata.Get(tenant, id); !ok {
			WriteProblem(w, 404, "not_found", "Unknown firmware artifact")
			return
		}
		s, ok := metadata.(FirmwareContentStore)
		if !ok {
			WriteProblem(w, 501, "not_implemented", "Firmware content storage unavailable")
			return
		}
		if r.Method == http.MethodGet {
			data, ok := s.GetFirmwareContent(tenant, id)
			if !ok {
				WriteProblem(w, 404, "not_found", "Firmware content not found")
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(data)
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/octet-stream" {
			WriteProblem(w, 415, "unsupported_media_type", "Expected application/octet-stream")
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, s.FirmwareContentLimit()+1))
		if err != nil {
			WriteProblem(w, 400, "invalid_request", "Cannot read content")
			return
		}
		if int64(len(data)) > s.FirmwareContentLimit() {
			WriteProblem(w, 413, "payload_too_large", "Firmware content too large")
			return
		}
		sum := sha256.Sum256(data)
		if digest := r.Header.Get("Digest"); digest != "" && digest != "sha-256="+base64.StdEncoding.EncodeToString(sum[:]) {
			WriteProblem(w, 400, "invalid_digest", "Invalid or mismatched digest")
			return
		}
		found, conflict := s.PutFirmwareContent(tenant, id, data)
		if !found {
			WriteProblem(w, 404, "not_found", "Unknown firmware artifact")
			return
		}
		if conflict {
			WriteProblem(w, 409, "conflict", "Firmware content already exists")
			return
		}
		w.WriteHeader(204)
	})).ServeHTTP(w, r)
}
