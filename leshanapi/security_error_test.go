package leshanapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
)

type failingSecurityStore struct{ server.SecurityStore }

func (f failingSecurityStore) Remove(string) (server.SecurityInfo, bool, error) {
	return server.SecurityInfo{}, false, errors.New("storage failure")
}

func TestSecurityDeleteFailure(t *testing.T) {
	store := failingSecurityStore{server.NewMemorySecurityStore()}
	w := httptest.NewRecorder()
	serveSecurity(w, httptest.NewRequest(http.MethodDelete, "/security/clients/device", nil), store, "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
}
