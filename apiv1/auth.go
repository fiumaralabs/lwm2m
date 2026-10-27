package apiv1

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type credential struct {
	hash   [32]byte
	scopes string
	tenant string
}

// Keys contains only credential hashes and scope metadata.
type Keys struct {
	entries map[string]credential
	state   *keyStore
}

const scopes = ",devices:read,devices:write,operations:write,operations:raw,credentials:write,bootstrap:write,firmware:write,jobs:write,admin,platform,"

// Authenticate accepts exactly one bearer credential and stores no raw token.
func Authenticate(keys Keys, next http.Handler) http.Handler {
	state := keys.state
	if state == nil {
		state = newKeyStore(keys.entries)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := r.Header.Values("Authorization")
		if len(headers) == 1 && strings.HasPrefix(headers[0], "Bearer lwm_") && !strings.ContainsAny(headers[0][7:], " \t\r\n,") {
			token := headers[0][7:]
			parts := strings.SplitN(strings.TrimPrefix(token, "lwm_"), "_", 2)
			if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
				state.mu.Lock()
				entry, ok := state.entries[parts[0]]
				hash := sha256.Sum256([]byte(token))
				if subtle.ConstantTimeCompare(hash[:], entry.hash[:]) == 1 && ok && (state.metadata[parts[0]].ExpiresAt == nil || time.Now().Before(*state.metadata[parts[0]].ExpiresAt)) {
					meta := state.metadata[parts[0]]
					now := time.Now().UTC()
					meta.LastUsedAt = &now
					state.metadata[parts[0]] = meta
					state.mu.Unlock()
					ctx := context.WithValue(r.Context(), keyStoreContext{}, state)
					next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, credentialKey{}, entry)))
					return
				}
				state.mu.Unlock()
			}
		}
		WriteProblem(w, http.StatusUnauthorized, "unauthorized", "Invalid or missing credentials")
	})
}

// LoadKeys rejects the entire input on malformed or duplicate entries.
func LoadKeys(r io.Reader) (Keys, error) {
	keys := Keys{entries: make(map[string]credential)}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return Keys{}, errors.New("invalid key entry")
		}
		id := strings.TrimPrefix(fields[0], "key_")
		hash, err := hex.DecodeString(strings.TrimPrefix(fields[1], "sha256:"))
		_, duplicate := keys.entries[id]
		if id == "" || id == fields[0] || strings.ContainsAny(id, "_,") || !strings.HasPrefix(fields[1], "sha256:") || err != nil || len(hash) != 32 || duplicate {
			return Keys{}, errors.New("invalid key entry")
		}
		for _, scope := range strings.Split(fields[2], ",") {
			if scope == "" || !strings.Contains(scopes, ","+scope+",") {
				return Keys{}, errors.New("invalid key scope")
			}
		}
		entry := credential{scopes: "," + fields[2] + ",", tenant: "default"}
		copy(entry.hash[:], hash)
		keys.entries[id] = entry
	}
	if err := scanner.Err(); err != nil {
		return Keys{}, errors.New("cannot read keys")
	}
	keys.state = newKeyStore(keys.entries)
	return keys, nil
}
