# backsnap - a kubernetes backup operator

*Backsnap: kubernetes backups, chiropractor approved!*

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/backsnap)](https://artifacthub.io/packages/search?repo=backsnap)

Backsnap performs backups of persistent volumes (PV/PVC) in a Kubernetes
cluster, for use in your disaster recovery scenarios.

It is unique in supporting point-in-time VolumeSnapshots, then making incremental
off-site backups of those using `restic`.

Backsnap by default assumes that all PVCs in your cluster should be backed up.
No per-PVC object is necessary to start backing up any data. You can change this
per PVC, per namespace or in operator configuration.

## How does it work?

By default, Backsnap enumerates all PersistentVolumeClaims in your cluster,
takes a point-in-time VolumeSnapshot of them, and uses `restic` to take a backup
of the snapshot.

By using VolumeSnapshots we are certain that a backup is internally consistant,
which is important when backing up workloads such as databases. By using
`restic` the backups are incremental and we automatically support all its
features, such as restoring from a point in history, client-side encryption and
multiple storage backends.

The operator can run in automatic or manual mode. In manual mode (`-manual`
flag), you create PVCBackup objects in the same namespace as a PVC you want to
be backed up. The operator reacts to this by creating a snapshot, a
point-in-time PVC and a Job to perform the backup, and cleans up afterwards. In
automatic mode, the operator creates PVCBackup objects automatically according
to schedule (you can still also create your own).

The automatic schedule can be adjusted using a `backsnap.skyb.it/schedule`
annotation on the target PVC or target namespace. By setting the annotation to
the empty string, the PVC (or all PVCs in the namespace) are not backed up. If
both the PVC and namespace have no annotation, the default schedule from the
`-schedule` flag is used. You can set `-schedule=""` to disable automatic
backups (this is the same as setting `-manual`, unless any PVCs or
namespaces do have an overriding schedule set).

## Getting started

### Quick start using Helm

```
helm repo add backsnap https://skybitsnl.github.io/backsnap
helm install --create-namespace --namespace backsnap backsnap -f values.yaml
```

An example `values.yaml` follows. For more configuration options, see the
["Default values" page on ArtifactHub](https://artifacthub.io/packages/helm/backsnap/backsnap?modal=values).

```
app:
  # In the default configuration for Backsnap, it creates a daily backup for all
  # PVCs in the cluster, starting immediately. In this example configuration, we
  # configure a single namespace instead, which causes it to back up only that
  # namespace. In order to back up the entire cluster state, it is recommended
  # not to configure any namespaces here, but configure per-namespace schedules
  # accordingly using annotations on the namespace or PVC.
  namespaces:
    allow: ["my-app"]

  # Default schedule for all PVCs within scope, unless overridden per namespace or
  # per PVC. The default is @daily, but any crontab syntax is supported.
  # See https://crontab.guru/ for examples and explanations.
  #schedule: "@daily"

  # Snapshot class, if no cluster-wide default is configured or you prefer
  # another one
  snapshotClass: ""

  # Storage class, if no cluster-wide default is configured or you prefer
  # another one
  storageClass: ""

  # S3 backup destination configuration
  s3:
    host: ""
    bucket: ""
    accessKey: ""
    secretKey: ""

  # Restic configuration
  restic:
    password: ""
```

After this, you can observe your backups and their status using:

```
kubectl get pvcbackup -A
```

See below on how to create your own PVCBackups, and restoring with PVCRestores.

### Manual installation with kubectl

First, import the Backsnap CRDs:

```
kubectl apply -f https://raw.githubusercontent.com/skybitsnl/backsnap/main/config/crd/bases/backsnap.skyb.it_pvcbackups.yaml
kubectl apply -f https://raw.githubusercontent.com/skybitsnl/backsnap/main/config/crd/bases/backsnap.skyb.it_pvcrestores.yaml
```

Then, create a `backsnap` namespace where the operator will run:

```
kubectl create namespace backsnap
```

Then, create the Service Account and its required roles. The files below create
a ClusterRole which allows creating VolumeSnapshots, PersistentVolumeClaims and
Jobs in any namespace, and allows reading various other resources. If you're
just backing up a single namespace, you can tweak this file to create a Role
which only allows this access to that namespace.

Once [cross-namespace data sources](https://kubernetes.io/blog/2023/01/02/cross-namespace-data-sources-alpha/)
are beta in Kubernetes, this application will also optionally support them,
and the set of necessary ClusterRole rules will be significantly reduced.

```
kubectl apply -f https://raw.githubusercontent.com/skybitsnl/backsnap/main/config/rbac/role.yaml
kubectl apply -f https://raw.githubusercontent.com/skybitsnl/backsnap/main/config/rbac/role_binding.yaml
kubectl apply -f https://raw.githubusercontent.com/skybitsnl/backsnap/main/config/rbac/leader_election_role.yaml
kubectl apply -f https://raw.githubusercontent.com/skybitsnl/backsnap/main/config/rbac/leader_election_role_binding.yaml
kubectl apply -f https://raw.githubusercontent.com/skybitsnl/backsnap/main/config/rbac/service_account.yaml
```

Then, we can deploy the operator.

> [!CAUTION]
> Note that depending on your operator configuration, Backsnap may start to take
> back-ups of all PVCs in your cluster immediately. If you don't want this to
> happen (yet), you can enable manual mode using the `-manual` flag. Eventually,
> we recommend running Backsnap in its default mode. This ensures that you have at
> least a daily snapshot of all PVCs in your cluster, even new ones, unless you
> opt out explicitly.

We download the Deployment YAML, so that we can edit its default configuration before
starting it.

```
wget https://raw.githubusercontent.com/skybitsnl/backsnap/main/config/manager/manager.yaml
```

Edit the manager.yaml and adjust as necessary.

- The version by default is set to `latest`, you may want to choose a specific tag here
  to prevent automatic updating.
- See the args inside the default YAML for any configuration you may want to set.

Then, deploy it:

```
kubectl apply -f manager.yaml
```

After this, you can observe your backups and their status using:

```
kubectl get pvcbackup -A
```

See below on how to create your own PVCBackups, and restoring with PVCRestores.

#### To uninstall

Stop the manager, the namespace, cluster roles and the CRDs created above:

```sh
kubectl delete deployment -n backsnap backsnap-operator
kubectl delete namespace backsnap
kubectl delete clusterrole backsnap-manager

kubectl delete -f https://raw.githubusercontent.com/skybitsnl/backsnap/main/config/crd/bases/backsnap.skyb.it_pvcbackups.yaml
kubectl delete -f https://raw.githubusercontent.com/skybitsnl/backsnap/main/config/crd/bases/backsnap.skyb.it_pvcrestores.yaml
```

## How to use it

In the default and recommended configuration, Backsnap automatically creates
PVCBackups on the configured schedule. You can override this schedule per
namespace and per PVC. The recommended way to do this is per namespace, e.g.
you can set your prod namespaces schedule to back up hourly and your testing
namespaces to an empty schedule to disable back-ups altogether.

This way, any new namespaces will be backed up automatically unless you disable
them explicitly.

You can tell the operator to start the backup of a specific PVC by submitting a
CR like the one in `config/samples/backsnap_v1alpha1_pvcbackup.yaml`:

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

You'll see that a VolumeSnapshot, PVC and backup Job are created in the
`your-application` namespace and you can follow along with the operator logs to
see its progression.

Similarly, to restore a PVC from backup, create a PVCRestore object like the
following:

```
apiVersion: backsnap.skyb.it/v1alpha1
kind: PVCRestore
metadata:
  name: your-data-restore
  namespace: your-application
spec:
  sourcePvc: your-data
  # By default, the sourceNamespace is the same namespace the PVCRestore
  # is in
  #sourceNamespace: "your-application-prod"
  # By default, the new PVC has the same name as the sourcePvc, but
  # you can override this
  #targetPvc: your-data-copy
  targetPvcSize: "10Gi"
```

For a concrete scenario of this, see
[Migrating a PVC to another availability zone using Backsnap](docs/migrate_pvc_to_another_az.md).

### Troubleshooting

If your PVCBackups or PVCRestores are failing, or not even starting, use the
following check-list to find out why:

- Is the operator running? (`kubectl get pods -n backsnap`)
- Is the operator picking up the jobs? (`kubectl logs -n backsnap deployment/backsnap`, possibly
  `grep` by namespace and/or object name)
- The operator will not delete the backup or restore Pods if they failed, so
  you can see if there are errors in their logs. (`kubectl logs -n your-application job/...`)

If you are still having issues after the above steps, be sure to file an issue
here on Github.

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

## Contributing

Suggestions are welcomed as GitHub issues - and pull requests are well
appreciated! This section should help you run the operator locally so that you
can test your changes.

If you need any help getting this to run, or would like to brainstorm about a
feature, please file a GitHub issue as well.

## How to run it locally

Run `go run ./cmd -help` to get a list of flags. Example run:

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
Of course, if you simply have a `backsnap` binary, just run it as
`backsnap -s3-host ...`.

If you've made changes to the Restic image that is used in backup jobs, you can
use `-image` to use your image. Of course, your Kubelet must be able to access
this image, so `imagePullSecret` can be used if it is hosted on a private
repository.

## Running the tests

The tests require a Kubernetes cluster that can run Pods, but does not run the
backsnap operator. That means envtest, the typical unit test framework, won't
work, because it won't run Pods. Instead, you can run the tests against
minikube.

```
minikube start --driver=docker --addons=volumesnapshots,csi-hostpath-driver
make test
```

The first minikube command starts minikube, adds it as a context to kubectl, and
sets it as the active context, so that every kubectl command after it uses
minikube. You can use `kubectl config get-contexts` to see your configured
contexts, and can switch to the existing one you had using `kubectl config
use-context NAME`. Then, you can switch back using `kubectl config use-context
minikube` in order to run the tests again.

## Building it locally / running your changes on a cluster

This project uses goreleaser. If you have local changes, you can use
`goreleaser build --snapshot --clean` to create new binaries in `dist/`. If you
need Docker images, you can run `goreleaser release --snapshot --clean` which
will create them locally. If you want to test them on a Kubernetes cluster, you
should push them to a (private) registry writable by you and readable from your
cluster. You can use the `retag-images-for-test.sh` script for this, e.g.:

```
$ goreleaser release --snapshot --clean
$ ./retag-images-for-test.sh --push my-private-registry/backsnap:test-new-feature
$ kubectl set image -n backsnap deployment/backsnap-operator manager=my-private-registry/backsnap:test-new-feature
```

Note, on subsequent runs, that the last command does nothing if the image is
already set to that value. If you just pushed a new image with the same name,
ensure that the imagePullPolicy is set to Always and simply delete the Pod.

Also, the commands above do not update the CRDs, so you may need to update them
manually:

```
$ make
$ kubectl apply -f config/crd/bases/backsnap.skyb.it_pvcbackups.yaml
$ kubectl apply -f config/crd/bases/backsnap.skyb.it_pvcrestores.yaml
```
