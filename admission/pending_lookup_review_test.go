package admission

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Readers use an independent connection so a concurrent commit can occur between reads.
func TestSQLiteLookupDuringCommitNeverUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "bindings.db")
	writer, err := OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for i := 0; i < 400; i++ {
		id := fmt.Sprintf("exchange-%d", i)
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(`{}`)))
		if _, err = writer.SavePending(ctx, id, []byte(`{}`), digest); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			b := Binding{ExchangeID: id, RequestDigest: digest, DecisionDigest: "digest"}
			r := Receipt{DecisionID: "decision", DecisionDigest: "digest", IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
			done <- writer.CommitAdmitted(ctx, b, r, func(*sql.Tx) error { return nil })
		}()
		for {
			status, _, e := reader.LookupExchange(ctx, id)
			if e != nil || (status != ExchangePending && status != ExchangeAccepted) {
				t.Fatalf("lookup %d: %s %v", i, status, e)
			}
			if status == ExchangeAccepted {
				break
			}
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
