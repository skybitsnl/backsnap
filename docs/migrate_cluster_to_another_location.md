# Migrating a cluster to another location using Backsnap

Suppose you are moving applications from an old cluster to a new one, for
example to another datacenter or cloud provider. Backsnap can move the data of
their PVCs along: you make a final backup of the PVCs in the old cluster, and
restore them in the new cluster, before deploying the applications there.

This document moves one namespace at a time, so that each application is only
down while its own namespace moves. To move a whole cluster, repeat it for
every namespace.

Both clusters must use the same S3 bucket and the same restic password. If you
want to move to another S3 provider as well, do that first, see
[Moving backups to another S3 provider](migrate_s3_provider.md). Also, if you
want to rotate the restic password, finish the rotation first, because rotations
only use the new password.

The commands below need [jq](https://jqlang.org/). Run the commands for the old
cluster with its kubectl context, and those for the new cluster with the new
one.

## Install Backsnap in the new cluster

Install Backsnap in the new cluster with the same `app.s3` and `app.restic`
settings as in the old cluster.

If storage classes are named differently in the new cluster, set
`app.restoreStorageClass`, or `storageClassName` in the PVCRestores below.

While a namespace moves, both clusters run Backsnap against the same backups.
The steps below make sure that only one cluster backs up a namespace at a
time.

## For each namespace

The commands below use the name of the namespace to move:

```
$ NS=my-app
```

### In the old cluster: prepare the restores

Generate a PVCRestore for every PVC in the namespace, leaving out the temporary
PVCs that Backsnap creates during backups. Do this first, because the next
steps change the annotations of the PVCs.

```
$ kubectl get pvc -n $NS -o json | jq '{apiVersion: "v1", kind: "List", items: [.items[]
  | select(all(.metadata.ownerReferences[]?; .kind != "PVCBackup"))
  | {apiVersion: "backsnap.skyb.it/v1alpha1", kind: "PVCRestore",
     metadata: {name: (.metadata.name + "-migrate"), namespace: .metadata.namespace,
                labels: {"backsnap-migration": "restore"}},
     spec: {sourcePvc: .metadata.name,
            targetPvcSize: .spec.resources.requests.storage,
            labels: (.metadata.labels // {}),
            annotations: (.metadata.annotations // {} | with_entries(select(
              (.key | startswith("meta.helm.sh/")) or .key == "backsnap.skyb.it/schedule")))}}]}' >restores-$NS.json
```

The restored PVCs get the labels of the old PVCs, and two kinds of their
annotations: `backsnap.skyb.it/schedule`, so that they keep their backup
schedule, and `meta.helm.sh/`, so that Helm accepts the restored PVCs as part
of their release. Other annotations are left out, because they belong to the
old cluster.

Review `restores-$NS.json`, and remove the PVCs you don't want to move.

If the namespace gets another name in the new cluster, change the namespace of
the PVCRestores, and let them restore from the old namespace by setting
sourceNamespace. Backups in the new cluster then start new restic repositories,
under the new namespace name. The old repositories are kept as they are. Use the
new name for `$NS` in the steps for the new cluster. Don't forget to remove the
old restic repositories to clear space once you don't need them anymore.

### In the old cluster: make a final backup

Stop the applications in the namespace, so that their data no longer changes,
for example by scaling their Deployments and StatefulSets to zero replicas.
Keep their PVCs.

Then, stop automatic backups of the PVCs in the old cluster:

```
$ kubectl annotate pvc -n $NS --all backsnap.skyb.it/schedule="" --overwrite
```

> [!CAUTION]
> Annotate the PVCs, not only the namespace: a schedule on a PVC overrides the
> schedule of its namespace. If the old cluster kept backing up a PVC after it
> moved, those backups would contain its old data, and would be newer than the
> backups made by the new cluster. A later restore would then restore the old
> data.

Then, create a PVCBackup for every PVC, and wait until they all succeed. These
PVCBackups run regardless of the schedule.

```
$ kubectl get pvc -n $NS -o json | jq '{apiVersion: "v1", kind: "List", items: [.items[]
  | select(all(.metadata.ownerReferences[]?; .kind != "PVCBackup"))
  | {apiVersion: "backsnap.skyb.it/v1alpha1", kind: "PVCBackup",
     metadata: {name: (.metadata.name + "-final"), namespace: .metadata.namespace,
                labels: {"backsnap-migration": "final"}},
     spec: {pvc: .metadata.name, ttl: "24h"}}]}' | kubectl apply -f -
$ kubectl wait -n $NS -l backsnap-migration=final --for=jsonpath='{.status.result}'=Succeeded --timeout=6h pvcbackup
```

If this times out, `kubectl get pvcbackup -n $NS -l backsnap-migration=final`
shows which backups did not succeed.

### In the new cluster: restore the PVCs

Create the namespace. If the old namespace had a `backsnap.skyb.it/schedule`
annotation, copy it to the new one (or simply apply its manifest for it).

```
$ kubectl create namespace $NS
```

> [!CAUTION]
> Do not deploy the applications in the namespace before all its restores have
> succeeded. A PVCRestore fails if its PVC already exists. And Backsnap might
> back up a PVC that an application created, before its data is restored. That
> empty backup would then be the latest one, which is what a restore restores.

Then, apply the restores and wait until they all succeed:

```
$ kubectl apply -f restores-$NS.json
$ kubectl wait -n $NS -l backsnap-migration=restore --for=jsonpath='{.status.result}'=Succeeded --timeout=6h pvcrestore
```

The restored PVCs are `ReadWriteOnce`. If an application needs another access
mode, restore its PVC under another name using `targetPvc`, and copy the data
over yourself.

### In the new cluster: start the applications

Deploy the applications. They use the restored PVCs, because those have the
same names as in the old cluster. For a StatefulSet, these are the PVCs named
`<volumeClaimTemplate>-<statefulset>-<ordinal>`.

From now on, the new cluster backs up the namespace, in the same repositories as
the old cluster did. Once everything works, you can delete the PVCRestores:

```
$ kubectl delete pvcrestore -n $NS -l backsnap-migration=restore
```

You may choose to await the automatic creation of new PVCBackups and their
results, to ensure that Backsnap successfully creates backups from the new
cluster before cleaning up the old cluster.

### In the old cluster: clean up

Once you are confident that the namespace works in the new cluster, delete it
from the old cluster. This keeps its backups.

## Once all namespaces have moved

Uninstall Backsnap from the old cluster, as described in the README.
