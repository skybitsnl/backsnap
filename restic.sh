#!/bin/bash
set -e -o pipefail

# When the Pod is stopped, tini sends SIGTERM to restic as well. restic then
# removes its locks and exits. Wait for that, instead of exiting immediately.
trap 'echo -e "\n== Received SIGTERM, exiting ==\n"; exit 143' TERM

# Wait for locks held by other restic processes, such as a running restore,
# instead of failing immediately.
restic() {
  command restic --retry-lock "${BACKSNAP_RETRY_LOCK}" "$@"
}

restic version

RESTIC_HOSTNAME="${RESTIC_HOSTNAME:-$(hostname)}"

# restic init if necessary; exit code 10 means the repository does not exist
EXIT_CODE=0
restic cat config >/dev/null || EXIT_CODE=$?
if [ "$EXIT_CODE" = "10" ]; then
  echo -e "\n== No repository according to Restic, running restic init... ==\n"
  restic init -vvv
elif [ "$EXIT_CODE" != "0" ]; then
  echo -e "\n== Restic init check failed, exiting ==\n"
  exit 1
fi

echo -e "\n== Backing up... ==\n"

set -x
# First, remove stale locks. Restic processes refresh their locks every 5
# minutes, and `unlock` only removes locks that were not refreshed for 30
# minutes, so this only removes true stale locks. This can happen, for example,
# when backups or restores were interrupted, e.g. when a node has a power
# failure. Restic then refuses to check or prune the repository until the locks
# are removed.
restic --verbose unlock
restic --verbose backup --host "${RESTIC_HOSTNAME}" /data
restic --verbose check
set +x

echo -e "\n== Restic snapshots before pruning: ==\n"

restic snapshots

echo ""

set -x
restic forget --keep-last 1 --keep-hourly 12 --keep-daily 7 --keep-weekly 2 --keep-monthly 2
restic prune
set +x

echo -e "\n== Restic snapshots after pruning: ==\n"
restic snapshots

echo ""

# restic docs recommend another check after pruning
restic --verbose check --read-data-subset=10%

exit 0
