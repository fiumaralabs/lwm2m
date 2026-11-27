package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// runBackup makes a SQLite snapshot without opening the store for migrations.
func runBackup(args []string) (err error) {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	source := flags.String("store", "", "source SQLite database path")
	output := flags.String("out", "", "new backup file path")
	if err = flags.Parse(args); err != nil {
		return err
	}
	if *source == "" || *output == "" || flags.NArg() != 0 {
		return errors.New("usage: lwm2md backup -store <db> -out <file>")
	}
	path, err := filepath.Abs(*source)
	if err != nil {
		return err
	}
	// Read-only mode must not create an empty database for a mistyped source path.
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return err
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	if version == 0 {
		return errors.New("source is not an initialized lwm2md store")
	}
	// SQLite permits overwriting an empty file, so reserve the destination exclusively.
	f, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	if err = f.Close(); err != nil {
		os.Remove(*output)
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(*output)
		}
	}()
	if _, err = db.Exec("VACUUM INTO ?", *output); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}
