#!/bin/bash

set -e

GIT_REF_NAME="$(git symbolic-ref -q --short HEAD || git describe --tags --exact-match)"

export DOCKER_BUILDKIT=1

docker buildx build . \
	--push \
	-f Dockerfile.restic \
	-t "sjorsgielen/backsnap-restic:latest-${GIT_REF_NAME}" \
	--platform "linux/amd64,linux/arm64"

docker buildx build . \
	--push \
	-f Dockerfile \
	-t "sjorsgielen/backsnap:latest-${GIT_REF_NAME}" \
	--platform "linux/amd64,linux/arm64"
