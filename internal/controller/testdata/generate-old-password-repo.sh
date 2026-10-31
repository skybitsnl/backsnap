#!/bin/sh
# Generates old-password-repo: an empty restic repository with the password
# "oldpass", to test rotating to the password "resticpass" used in the tests.
set -e
cd "$(dirname "$0")"
rm -rf old-password-repo
docker run --rm -u "$(id -u):$(id -g)" -v "$PWD:/out" --entrypoint sh restic/restic:0.19.1 -c '
  set -e
  export RESTIC_REPOSITORY=/tmp/repo RESTIC_PASSWORD=oldpass RESTIC_CACHE_DIR=/tmp/cache
  restic init
  mkdir /out/old-password-repo
  cp -r /tmp/repo/config /tmp/repo/keys /out/old-password-repo
'
