# Self-hosting `lwm2md`

This guide covers a single daemon using its SQLite store. It is not a production
readiness or high-availability claim. Test the procedure on a disposable copy
before relying on it.

## Build and configure

From a checkout of the desired revision, build with:

```sh
go build -o lwm2md ./cmd/lwm2md
./lwm2md -h
```

Configure listeners, API keys and the store as described in [config.md](config.md).
Use `-store=sqlite:/path/to/store.db -registration-store=sqlite` to persist
registrations as well as the device store. The default registration store is
in memory. Configure and protect the API key file separately; it is not part
of the SQLite backup. The daemon process tests in `cmd/lwm2md/backup_test.go`
exercise the build and these SQLite flags. The configuration parser is tested
in `cmd/lwm2md/config_test.go`.

## Back up

While the daemon is running:

```sh
./lwm2md backup -store /path/to/store.db -out /safe/private/new-backup.db
```

`-store` here is a filesystem path, not the `sqlite:` flag value. The command
uses a SQLite snapshot, including committed changes in the WAL, and refuses an
existing destination, including an empty file. It fails rather than creating a
missing source. Use a trusted private destination directory. The file is
created with owner-only permissions and can contain device credentials and
other sensitive data. Protect and separately back up configuration, API key
files, and any other external files; they are not included in this snapshot.

`TestOnlineBackupRestore` exercises a backup while the daemon runs, rejects an
existing destination, and checks a restored registration, security information,
operation and event. `TestBackupRejectsMissingSourceAndExistingEmptyFile` tests
missing source and empty destination handling.

## Restore

1. Stop the daemon and ensure no process has the target database open. Retain
   the old database and its `-wal` and `-shm` files together in a secure place
   for recovery. Do not mix sidecars from the old database with the restored
   database.
2. Copy the backup to the configured database path, with permissions appropriate
   to the daemon user. Restore the matching configuration and external key files
   separately. Keep the original backup unchanged.
3. Start the daemon with the SQLite and registration-store settings above. Check
   `/readyz` and verify the records important to your installation.

The daemon checks SQLite `user_version` on startup and rejects versions newer
than it supports. Older schemas can be migrated on startup. `TestDaemonRejectsNewerSchema`
checks rejection. `TestOnlineBackupRestore` starts a fresh daemon on the backup
and checks the restored records. The test does not cover external configuration
or key files.

## Update and rollback

Before updating, take and securely retain a backup and the matching configuration
and external files. Build the new revision as above, stop the daemon, retain the
old binary, then start the new binary against the store. Check `/readyz` and the
records you depend on. Startup can migrate the database.

For rollback, stop the daemon and restore the pre-update backup, matching
configuration and external files before starting the retained old binary. Do
not assume an old binary can read a database migrated by a newer binary. Writes
made after the backup will not be present after rollback. The schema rejection
and backup/restore tests above cover these primitives, not a complete
cross-version upgrade or rollback drill.
