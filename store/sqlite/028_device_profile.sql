-- Devices are stored as JSON payloads; preserve older records with no profile.
UPDATE devices SET payload=json_set(payload, '$.profile', '')
WHERE json_type(payload, '$.profile') IS NULL;
