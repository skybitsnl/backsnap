# backsnap - a kubernetes backup operator

Backsnap helps performing off-site backups of persistent volumes in a Kubernetes
cluster, for use in your disaster recovery scenarios. It works by enumerating
all PersistentVolumeClaims in your cluster, taking a point-in-time
VolumeSnapshot of them, then using `restic` to take a backup of the snapshot.

By using VolumeSnapshots we are certain that a backup is internally consistant,
which is important when backing up workloads such as databases. By using
`restic`, we automatically support all its features, such as restoring from a
point in history, client-side encryption and multiple storage backends.

Currently, Backsnap is a single utility which performs a one-off back-up of one
or more (or all) namespaces. You can run it locally as such, or use it in a
Kubernetes CronJob to back up all PVCs in your cluster at pre-scheduled moments.

Later, the tool will evolve into an operator so that you can set individual
schedules per PVC if you want.

## How to use it locally

You can run `go run ./cmd/backsnap -help` to get a list of flags. Example run:

```
go run ./cmd/backsnap \
    -s3-host s3.eu-west-1.amazonaws.com \
    -s3-bucket backsnap-example \
    -s3-access-key-id ... \
    -s3-secret-access-key ... \
    -restic-password ...
```

This will use your local credentials to access the cluster and create resources.
Of course, if you simply have a `backsnap` binary, just run it as `backsnap
-s3-host ...`.

## How to run it in Kubernetes

You can also run backsnap inside Kubernetes. It will use the Service Account of
its Pod to access the cluster. First, create a `backsnap` namespace:

```
kubectl create namespace backsnap
```

Then, create the Service Account and its required roles. The file below creates
a ClusterRole which allows creating VolumeSnapshots, PersistentVolumeClaims and
Jobs in any namespace, and allows reading various other resources. If you're
just backing up a single namespace, you can tweak this file to create a Role
which only allows this access to that namespace.

Once [cross-namespace data sources](https://kubernetes.io/blog/2023/01/02/cross-namespace-data-sources-alpha/)
are beta in Kubernetes, this application will also optionally support them,
and the set of necessary ClusterRole rules will be significantly reduced.

```
kubectl apply -f sa.yaml
```

Then, the following cronjob will create a daily backup of your cluster. Note
that if you want to do backups more often than daily, you need to make sure your
backup name (`backupname` flag) is periodically repeating, but unique for each
moment. For example, for a twice-per-day backup, set `-backupname` to
`monday-morning` then `morning-afternoon`. If you do this, any failed backup
resources will automatically be cleaned up by the next run with that name, e.g.
a week later. This will no longer be necessary once the tool becomes an
operator.

```
apiVersion: batch/v1
kind: CronJob
metadata:
  namespace: backsnap
  name: backsnap
spec:
  schedule: "12 4 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          serviceAccountName: backsnap
          containers:
          - name: default
            image: sjorsgielen/backsnap:latest-main
            imagePullPolicy: Always
            args:
            - -s3-host
            - s3.eu-west-1.amazonaws.com
            - -s3-bucket
            - backsnap-example
            - -s3-access-key-id
            - ...
            - -s3-secret-access-key
            - ...
            - -restic-password
            - ...
          restartPolicy: OnFailure
```

To run your first back-up immediately, run:

```
kubectl create job -n backsnap --from=cronjob/backsnap backsnap-manual
```

This will create a first backup Job + Pod in the `backsnap` namespace. Tail its
logs to follow the backup process:

```
kubectl logs -f -n backsnap job/backsnap-manual
```
