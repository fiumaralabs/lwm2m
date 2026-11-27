# Prepared pion/dtls patch

This patch adds `TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256` (0xC023) using pion's existing DTLS 1.2 CBC protection. It is prepared for owner review and has not been submitted upstream. It does not change the LwM2M runtime.

Base: pion/dtls `v4.0.0-rc.3`, commit `141c5f334f3120fdf6e57ef60f33da0f08b25660`, the v4 dependency pinned in `go.mod`. Pion's code and this patch are MIT licensed; see pion/dtls `LICENSE`.

## Check and apply

From the root of this repository, with Git configured with a committer name and email:

```sh
patch="$(pwd)/upstream/pion-dtls/0001-Add-ECDHE-ECDSA-AES-128-CBC-SHA-256-cipher-suite.patch"
scratch="$(mktemp -d)"
git clone https://github.com/pion/dtls.git "$scratch/dtls"
git -C "$scratch/dtls" checkout --detach 141c5f334f3120fdf6e57ef60f33da0f08b25660
git -C "$scratch/dtls" apply --check "$patch"
git -C "$scratch/dtls" am "$patch"
(cd "$scratch/dtls" && go test ./... > "$scratch/pion-test.log" 2>&1)
tail -20 "$scratch/pion-test.log"
```

Git has no `git am --check` option. The script uses `git apply --check` followed by a real `git am` in a disposable clone instead. The applied tree was checked against the prepared commit.

The command above passed in the applied clone: **30 packages passed**, with other packages reporting no test files. The added registry test fails on the base and passes with the patch. An extra OpenSSL 3.6.4 interoperability check passed in both client and server directions:

```sh
go test -tags openssl ./e2e -run '^TestPionOpenSSLE2E$/^Simple$/.*/TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256$' -v -count=1
```

This is software interoperability evidence, not a claim about hardware.
