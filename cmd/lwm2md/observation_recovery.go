package main

import (
	"database/sql"
	"fmt"
	"strings"
)

// A daemon restart loses every registration session. Session-bound observations
// from the old process must not be delivered to a new registration, including
// those whose delivery was still pending when the process stopped.
func recoverObservations(path string) error {
	// In-memory databases are empty on process start and are not shared by
	// separate connections unless explicitly configured as shared cache.
	if path == ":memory:" || strings.HasPrefix(path, "file::memory:") || (strings.HasPrefix(path, "file:") && strings.Contains(path, "mode=memory")) {
		return nil
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`UPDATE observations
		SET body = json_set(body, '$.status', 'ended', '$.establishedAt', NULL)
		WHERE tenant = ? AND json_extract(body, '$.persistent') = 0
		AND json_extract(body, '$.status') != 'ended'`, "default")
	if err != nil {
		return fmt.Errorf("recover session-bound observations: %w", err)
	}
	return nil
}
