# Moving backups to another S3 provider

Backsnap keeps one restic repository per PVC, under `<namespace>/<pvc>/` in its
S3 bucket. A restic repository consists of plain files, which don't depend on
the S3 provider. So to move all backups to another provider, you copy the
bucket, and then point Backsnap to the copy.

If you don't need the existing backups in the new location, you can also just
change the S3 settings of Backsnap. Its next backups then start new
repositories in the new bucket. Keep the old bucket for as long as you may need
its backups: restoring from it is only possible using restic directly, as shown
at the end of this document.

The example below uses [rclone](https://rclone.org/) to copy the bucket.

## Copy the bucket

Create a bucket and credentials at the new provider. Then, configure rclone
with both buckets, here named `old` and `new`, for example using environment
variables:

```
$ export RCLONE_CONFIG_OLD_TYPE=s3 RCLONE_CONFIG_OLD_PROVIDER=Other \
    RCLONE_CONFIG_OLD_ENDPOINT=https://s3.old-provider.example \
    RCLONE_CONFIG_OLD_ACCESS_KEY_ID=... RCLONE_CONFIG_OLD_SECRET_ACCESS_KEY=...
$ export RCLONE_CONFIG_NEW_TYPE=s3 RCLONE_CONFIG_NEW_PROVIDER=Other \
    RCLONE_CONFIG_NEW_ENDPOINT=https://s3.new-provider.example \
    RCLONE_CONFIG_NEW_ACCESS_KEY_ID=... RCLONE_CONFIG_NEW_SECRET_ACCESS_KEY=...
```

Copy the bucket while Backsnap keeps making backups on the old location. This
first copy can take long, depending on the size of your backups:

```
$ rclone sync --fast-list --progress old:backsnap new:backsnap
```

## Stop backups and copy the rest

Once the initial sync is done, stop the operator, so that it starts no new backups:

```
$ kubectl scale deployment -n backsnap backsnap-operator --replicas=0
```

Backup Jobs that are already running continue without the operator. Wait until
the following shows no Jobs anymore:

```
$ kubectl get jobs -A -o json | jq -r '.items[]
  | select(any(.metadata.ownerReferences[]?; .kind == "PVCBackup"))
  | select(.status.active > 0)
  | "\(.metadata.namespace)/\(.metadata.name)"'
```

Then, copy what changed since the first copy, and check that both buckets are
the same:

```
$ rclone sync --fast-list --progress old:backsnap new:backsnap
$ rclone check --fast-list old:backsnap new:backsnap
```

## Point Backsnap to the new bucket

Change the `app.s3` settings to the new bucket and credentials, and keep the
restic password the same. A `helm upgrade` with the new settings also starts
the operator again. If you installed Backsnap with kubectl, change the `--s3-*`
flags in your `manager.yaml` and apply it.

To check that the new bucket works, create a PVCBackup as described in the
README, and look at the log of its Job. It should add a snapshot to the
existing repository, rather than create a new one.

Keep the old bucket until you are confident that the new one works. Make sure at
least a PVCBackup and a PVCRestore have succeeded against the new location.

## Using restic directly

To check or restore a repository by hand, for example in the old bucket, run
restic with the same settings as Backsnap:

```
$ docker run --rm -it \
    -e AWS_ACCESS_KEY_ID=... -e AWS_SECRET_ACCESS_KEY=... \
    -e RESTIC_REPOSITORY=s3:<s3 host>/<bucket>/<namespace>/<pvc> \
    -e RESTIC_PASSWORD='<restic password>' \
    restic/restic check
```

Replace `check` by `snapshots` to list the snapshots of the repository. See the
[restic documentation](https://restic.readthedocs.io/en/stable/050_restore.html)
on restoring them.
