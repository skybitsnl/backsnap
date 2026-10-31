#!/bin/sh
# Generates stale-lock-repo: an empty restic repository with the lock of a
# restic backup that was killed, as happens when a node fails during a backup.
# Its password is "resticpass", like in the tests. restic only considers the
# lock stale 30 minutes after it was generated.
set -e
cd "$(dirname "$0")"
rm -rf stale-lock-repo
docker run --rm -u "$(id -u):$(id -g)" -v "$PWD:/out" --entrypoint sh restic/restic:0.19.1 -c '
  set -e
  export RESTIC_REPOSITORY=/tmp/repo RESTIC_PASSWORD=resticpass RESTIC_CACHE_DIR=/tmp/cache
  restic init
  head -c 500000000 /dev/urandom >/tmp/data
  restic backup /tmp/data &
  while [ -z "$(ls /tmp/repo/locks | grep -v tmp)" ]; do sleep 0.1; done
  kill -9 $!
  mkdir /out/stale-lock-repo
  cp -r /tmp/repo/config /tmp/repo/keys /tmp/repo/locks /out/stale-lock-repo
'
