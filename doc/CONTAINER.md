# Running lwm2md in a container

Build an image from a checked-out revision. The image is local; this does not
publish it to a registry.

```sh
docker build -t lwm2md:current .
docker volume create lwm2md-data
docker run -d --name lwm2md \
  -p 127.0.0.1:8080:8080 -p 5683:5683/udp -p 5684:5684/udp \
  -v lwm2md-data:/data lwm2md:current -store sqlite:/data/devices.db
curl -fsS http://127.0.0.1:8080/readyz
```

The process runs as UID/GID 10001. A new named volume is initialized with a
writable `/data` directory. For bind mounts, make the directory writable by
UID 10001, including space for SQLite journal files. Keep the volume when
removing or replacing a container. Without `-store sqlite:/data/devices.db`,
the default store is in memory and is lost on restart.

The native `/v1` API denies requests unless `-api-keys` points to a readable
key file. Mount that file read-only, for example with
`--mount type=bind,src=/absolute/path/to/keys,dst=/run/keys,readonly`, and add
`-api-keys /run/keys` after the image name. See [API.md](API.md) for the
key format and scopes. Do not bake credentials into an image. Limit access to
the HTTP and UDP ports using network controls; the Leshan-compatible API is
not protected by the native API key mechanism. Bootstrap ports 8081/tcp,
5783/udp and 5784/udp are exposed by the image but are not published in the
example. Publish them only if needed.

## Upgrade and rollback

Stop the daemon before taking a consistent backup of the SQLite database and
its volume. Retain the old image and backup before building a new revision.
SQLite schema migrations may make an older binary incompatible with a newer
database. Do not assume that switching images alone rolls back data.

```sh
docker stop lwm2md
# Back up the stopped volume using your normal backup procedure here.
docker rm lwm2md
docker tag lwm2md:current lwm2md:previous
docker build -t lwm2md:current .
# Re-run the docker run command above with the same volume and flags.
```

To roll back, stop and remove the new container, restore the pre-upgrade
volume backup, and re-run the same command using `lwm2md:previous`. Test
backup and restore procedures before relying on them.
