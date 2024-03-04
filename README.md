# backsnap - a kubernetes backup operator

*Backsnap: kubernetes backups, chiropractor approved!*

Backsnap helps performing off-site backups of persistent volumes in a Kubernetes
cluster, for use in your disaster recovery scenarios. It works by enumerating
all PersistentVolumeClaims in your cluster, taking a point-in-time
VolumeSnapshot of them, then using `restic` to take a backup of the snapshot.

By using VolumeSnapshots we are certain that a backup is internally consistant,
which is important when backing up workloads such as databases. By using
`restic`, we automatically support all its features, such as restoring from a
point in history, client-side encryption and multiple storage backends.

The operator can run in automatic or manual mode. In manual mode (`-manual`
flag), you create PVCBackup objects in the same namespace as a PVC you want to
be backed up. The operator reacts to this by creating a snapshot, a
point-in-time PVC and a Job to perform the backup, and cleans up afterwards. In
automatic mode, the operator creates PVCBackup objects automatically according
to schedule.

The automatic schedule can be adjusted using a `backsnap.skyb.it/schedule`
annotation on the target PVC or target namespace. By setting the annotation to
the empty string, the PVC (or all PVCs in the namespace) are not backed up. If
both the PVC and namespace have no annotation, the default schedule from the
`-schedule` flag is used. You can set `-schedule=""` to disable automatic
backups (this is the same as setting `-manual`, unless any PVCs or
namespaces do have a schedule set).

## Getting started

First, create a `backsnap` namespace:

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

Then, install the CRDs into your cluster:

```
make install
```

Then, deploy the operator.

```
make deploy IMG=sjorsgielen/backsnap:latest-main
```

If you want to build and deploy your own operator, use something like:

```
make docker-build docker-push deploy IMG=<some-registry>/backsnap:latest-main
````

These commands deploy an operator in "manual" mode by default, since the manager
YAMLs in `config/manager` pass `-schedule ""` in the args. So, if you deploy this
way, the operator will not start backing up anything, unless schedules are set in
namespaces and PVCs annotations. To run in full backup mode, remove
`-schedule ""` from the args (or set a specific schedule, e.g. `-schedule "@daily"`)
when you deploy the manager.

In manual mode, you can tell the operator to start the backup of a specific PVC by
submitting a CR like the one in `config/samples/backsnap_v1alpha1_pvcbackup.yaml`:

```
apiVersion: backsnap.skyb.it/v1alpha1
kind: PVCBackup
metadata:
  name: your-data-backup
  namespace: your-application
spec:
  pvc: your-data
  ttl: 1h
```

## To uninstall

Delete the CRDs from the cluster:

```sh
make uninstall
```

Undeploy the controller from the cluster:

```sh
make undeploy
```

## How to run it locally

You can run `go run ./cmd/backsnap -help` to get a list of flags. Example run:

```
go run ./cmd \
    -snapshotclass ... \
    -namespaces ... \
    -schedule "@daily" \
    -s3-host s3.eu-west-1.amazonaws.com \
    -s3-bucket backsnap-example \
    -s3-access-key-id ... \
    -s3-secret-access-key ... \
    -restic-password ...
```

This will use your local credentials to access the cluster and create resources.
Of course, if you simply have a `backsnap` binary, just run it as `backsnap
-s3-host ...`.

## Running the tests

The tests require a Kubernetes cluster that can run Pods, but does not run the
backsnap operator. That means envtest, the typical unit test framework, won't
work. Instead, you can run the tests against minikube.

```
minikube start --driver=docker --addons=volumesnapshots,csi-hostpath-driver
make test
```

The first minikube command starts minikube, adds it as a context to kubectl, and
sets it as the active context, so that every kubectl command after it uses
minikube.  You can use `kubectl config get-contexts` to see your configured
contexts, and can switch to the existing one you had using `kubectl config
use-context NAME`. Then, you can switch back using `kubectl config use-context
minikube` in order to run the tests again.

## Security considerations

This operator assumes full trust within the entire Kubernetes cluster. Because
of existing Kubernetes limitations, security risks are difficult to mitigate.

Once
[cross-namespace data sources](https://kubernetes.io/blog/2023/01/02/cross-namespace-data-sources-alpha/)
are beta in Kubernetes, this application will also optionally support them. This
will mitigate security risks to a point where absolute trust in anyone using the
cluster is no longer necessary.

### While making back-ups

This operator creates a point-in-time copy of the PVC to back up. This has to be
done in the same namespace, because snapshots cannot be taken or restored into
other namespaces. Theoretically, this PVC could be read from and written to by
anyone who can read/write PVCs in the target namespace, before the back-up
starts. But since such parties can typically also read/write the target PVC
itself, this is relatively low-risk.

Of a higher risk, this operator creates a Pod inside the target namespace which
consumes the PVC copy and backs it up to the backup location using restic. This
Pod contains the S3 credentials and the restic encryption password in its YAML.
Anyone who can read Job or Pod definitions, or exec into Pods, can read these
credentials and therefore read and write the back-ups.

### While keeping back-ups

The restic password is used for client-side encryption. This means that the
back-ups cannot be retrieved from the target location without also knowing the
decryption password. This is a feature provided by restic.

### While restoring back-ups

A PVCRestore object can be created in any namespace watched by the operator, and
will be able to restore any PVC from any namespace backed up on the same backup
location. This means that any user with access to any namespace on the cluster,
can eventually read the contents of any PVC in any other namespace.
