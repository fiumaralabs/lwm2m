package sqlite

import (
	"database/sql"
	_ "embed"
	"errors"
)

//go:embed 032_sms_counters.sql
var smsCountersMigration string

// NextSMSCounter atomically reserves a transmit counter, including across store instances.
func (s *Devices) NextSMSCounter(msisdn string, minimum, maximum uint64) (uint64, error) {
	var next uint64
	err := s.db.QueryRow(`INSERT INTO sms_counters (msisdn, tx) SELECT ?, max(1, ?) WHERE max(1, ?) <= ?
 ON CONFLICT(msisdn) DO UPDATE SET tx=max(sms_counters.tx+1, excluded.tx)
 WHERE max(sms_counters.tx+1, excluded.tx) <= ? RETURNING tx`, msisdn, minimum, minimum, maximum, maximum).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("sms: counter blocked")
	}
	return next, err
}

// AcceptSMSCounter atomically advances the receive replay barrier.
func (s *Devices) AcceptSMSCounter(msisdn string, counter uint64) (bool, error) {
	result, err := s.db.Exec(`INSERT INTO sms_counters (msisdn, rx) SELECT ?, ? WHERE ? > 0
 ON CONFLICT(msisdn) DO UPDATE SET rx=excluded.rx WHERE excluded.rx > sms_counters.rx`, msisdn, counter, counter)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// LastSMSCounter returns the latest reserved transmit counter, or zero if absent.
func (s *Devices) LastSMSCounter(msisdn string) (uint64, error) {
	var n uint64
	err := s.db.QueryRow(`SELECT tx FROM sms_counters WHERE msisdn=?`, msisdn).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
