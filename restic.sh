#!/bin/bash
set -e -o pipefail

restic version

RESTIC_HOSTNAME="${RESTIC_HOSTNAME:-$(hostname)}"

# restic init if necessary
set +e
CAT_ERROR=$(restic cat config 2>&1)
EXIT_CODE=$?
set -e
if [ "$EXIT_CODE" != "0" ]; then
  if [ "$(echo "${CAT_ERROR}" | grep 'Is there a')" = "Is there a repository at the following location?" ]; then
    echo -e "\n== No repository according to Restic, running restic init... ==\n"
    restic init -vvv
  else
    echo "$CAT_ERROR"
    echo -e "\n== Restic init check failed, exiting ==\n"
    exit 1
  fi
fi

echo -e "\n== Backing up... ==\n"

set -x
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
