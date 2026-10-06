#!/bin/sh
# Builds a throwaway module that requires this checkout the way a user's
# module would, importing every public package and building the commands.
# Go ignores replace directives outside the main module, so a replace in
# our go.mod passes our own build and breaks every consumer; this catches it.
# The replace below is in the consumer, where it is legitimate.
set -eu
repo=$(cd "$(dirname "$0")/.." && pwd)
mod=$(cd "$repo" && go list -m)
pkgs=$(cd "$repo" && go list -f '{{if and (ne .Name "main") .GoFiles}}{{.ImportPath}}{{end}}' ./... | grep -v /internal)
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
cd "$dir"
go mod init consumer >/dev/null 2>&1
go mod edit -require="$mod@v0.0.0" -replace="$mod=$repo"
{
	echo 'package main'
	echo 'import ('
	for p in $pkgs; do echo "	_ \"$p\""; done
	echo ')'
	echo 'func main() {}'
} >main.go
go mod tidy
go build -o /dev/null .
go build -o /dev/null "$mod/cmd/..."
echo "consumer build ok: $(echo "$pkgs" | wc -l | tr -d ' ') packages"
