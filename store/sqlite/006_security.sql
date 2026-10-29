CREATE TABLE security (
    endpoint TEXT PRIMARY KEY NOT NULL,
    psk_identity TEXT NOT NULL,
    psk_key BLOB,
    x509 INTEGER NOT NULL
);
CREATE UNIQUE INDEX security_psk_identity ON security(psk_identity) WHERE psk_identity <> '';
PRAGMA user_version = 6;
