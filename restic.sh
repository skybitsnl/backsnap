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

# restic init if necessary; exit code 10 means the repository does not exist,
# exit code 12 that the password is wrong
EXIT_CODE=0
restic cat config >/dev/null || EXIT_CODE=$?
if [ "$EXIT_CODE" = "10" ]; then
  echo -e "\n== No repository according to Restic, running restic init... ==\n"
  restic init -vvv
elif [ "$EXIT_CODE" = "12" ] && [ -n "${BACKSNAP_OLD_RESTIC_PASSWORD}" ]; then
  echo -e "\n== Adding the new password to the repository, using the old password... ==\n"
  RESTIC_PASSWORD="${BACKSNAP_OLD_RESTIC_PASSWORD}" restic key add \
    --new-password-file <(printf '%s' "${RESTIC_PASSWORD}")
elif [ "$EXIT_CODE" = "12" ]; then
  echo -e "\n== Wrong restic password. If you changed it, set the old one with -restic-old-password to start a rotation. ==\n"
  exit 1
elif [ "$EXIT_CODE" != "0" ]; then
  echo -e "\n== Restic init check failed, exiting ==\n"
  exit 1
fi

# While the password is being rotated, remove the key of the old password once
# the new password works. This also finishes a rotation that was interrupted
# after adding the new password. The removal is authenticated with the new
# password, and restic refuses to remove the key it is using, so this never
# removes the last working key.
if [ -n "${BACKSNAP_OLD_RESTIC_PASSWORD}" ] && [ "${BACKSNAP_OLD_RESTIC_PASSWORD}" != "${RESTIC_PASSWORD}" ]; then
  EXIT_CODE=0
  OLD_KEYS=$(RESTIC_PASSWORD="${BACKSNAP_OLD_RESTIC_PASSWORD}" restic key list --json) || EXIT_CODE=$?
  if [ "$EXIT_CODE" = "0" ]; then
    OLD_KEY=$(echo "${OLD_KEYS}" | grep -o '"current":true,"id":"[0-9a-f]*"' | cut -d '"' -f 6)
    if [ -z "${OLD_KEY}" ]; then
      echo -e "\n== Could not find the key of the old password in: ${OLD_KEYS} ==\n"
      exit 1
    fi
    echo -e "\n== Removing the key of the old password... ==\n"
    restic key remove "${OLD_KEY}"
    echo -e "\n== Moved this repository to the new password ==\n"
  elif [ "$EXIT_CODE" != "12" ]; then
    echo -e "\n== Checking for the old password failed, exiting ==\n"
    exit 1
  fi
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
restic --verbose check --read-data-subset="${BACKSNAP_CHECK_SUBSET}"

exit 0
