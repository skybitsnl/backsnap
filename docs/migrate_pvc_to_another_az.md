# Migrating a PVC to another availability zone using Backsnap

Suppose you have a `myapplication` StatefulSet with two replicas, both in availability
zone `nl-ams-1`. Now, you want to move one of the two replicas (`myapplication-1` PVC)
to availability zone `nl-ams-2`.

First, scale down the application to 1 replica, so that only `myapplication-0` is
used.

```
$ kubectl scale statefulset -n myapplication myapplication --replicas=1
```

Then, create a backup of the last contents of the `myapplication-1` PVC.

```
$ cat <<EOF >backup.yaml
apiVersion: backsnap.skyb.it/v1alpha1
kind: PVCBackup
metadata:
  name: myapplication-1-manual
  namespace: myapplication
spec:
  pvc: myapplication-1
  ttl: 1h
EOF
$ kubectl apply -f backup.yaml
$ kubectl wait -n myapplication --for=jsonpath='{.status.result}'=Succeeded --timeout=1h pvcbackup myapplication-1-manual
``` 

Once the backup is completed, delete the PVC:

```
$ kubectl delete pvc -n myapplication myapplication-1
```

Then, create a PVCRestore to the new AZ and wait for it to complete:

```
$ cat <<EOF >restore.yaml
apiVersion: backsnap.skyb.it/v1alpha1
kind: PVCRestore
metadata:
  name: myapplication-1-restore
  namespace: myapplication
spec:
  sourcePvc: myapplication-1
  targetPvcSize: "10Gi"
  nodeSelector:
    topology.kubernetes.io/zone: nl-ams-2
EOF
$ kubectl apply -f restore.yaml
$ kubectl wait -n myapplication --for=jsonpath='{.status.result}'=Succeeded --timeout=1h pvcrestore myapplication-1-restore
```

The PVC should now exist again, but on the `nl-ams-2` AZ, and with the same
contents as before. Now, we can scale the StatefulSet back up:

```
$ kubectl scale statefulset -n myapplication myapplication --replicas=2
```

Using `kubectl get pods -o wide -n myapplication`, you should see the new
`myapplication-1` Pod being scheduled in the `nl-ams-2` availability zone.
