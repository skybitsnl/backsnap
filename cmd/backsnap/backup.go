package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	volumesnapshotclientv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/clientset/versioned/typed/volumesnapshot/v1"
	"github.com/samber/lo"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"kmodules.xyz/client-go/tools/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func BackupPvc(ctx context.Context, config *rest.Config, namespace, pvcName string) error {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	batchv1.AddToScheme(scheme)
	clientset := kubernetes.NewForConfigOrDie(config)
	kclient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	volumesnapshotclient := volumesnapshotclientv1.NewForConfigOrDie(config)

	backupName := fmt.Sprintf("%s-backup-%s", pvcName, *backupName)

	slog.InfoContext(ctx, "starting backup of pvc",
		slog.String("namespace", namespace),
		slog.String("pvc", pvcName),
		slog.String("name", backupName),
	)

	// Delete old resources for this backup name
	// TODO: implement deletion retry
	// TODO: this should not be necessary if we have a Backup resource that we can delete (including
	// foreground propagation), with the snapshot, pvc and Job as child objects
	waitVolumeSnapshotDeletion := true
	err = volumesnapshotclient.VolumeSnapshots(namespace).Delete(ctx, backupName, metav1.DeleteOptions{
		PropagationPolicy: lo.ToPtr(metav1.DeletePropagationBackground),
	})
	if errors.IsNotFound(err) {
		// Not Found is fine
		waitVolumeSnapshotDeletion = false
	} else if err != nil {
		return err
	}

	waitJobDeletion := true
	err = kclient.Delete(ctx, &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      backupName,
		},
	}, &client.DeleteOptions{
		PropagationPolicy: lo.ToPtr(metav1.DeletePropagationBackground),
	})
	if errors.IsNotFound(err) {
		// Not Found is fine
		waitJobDeletion = false
	} else if err != nil {
		return err
	}

	waitPvcDeletion := true
	err = kclient.Delete(ctx, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      backupName,
		},
	}, &client.DeleteOptions{
		PropagationPolicy: lo.ToPtr(metav1.DeletePropagationBackground),
	})
	if errors.IsNotFound(err) {
		// Not Found is fine
		waitPvcDeletion = false
	} else if err != nil {
		return err
	}

	if waitJobDeletion {
		if err := waitForDeletion(namespace, "job", backupName, config); err != nil {
			return err
		}
	}

	if waitPvcDeletion {
		if err := waitForDeletion(namespace, "pvc", backupName, config); err != nil {
			return err
		}
	}

	if waitVolumeSnapshotDeletion {
		if err := waitForDeletion(namespace, "volumesnapshot", backupName, config); err != nil {
			return err
		}
	}

	var snapshotClass *string
	if *snapshotClassFlag != "" {
		snapshotClass = snapshotClassFlag
	}

	snapshot := volumesnapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      backupName,
		},
		Spec: volumesnapshotv1.VolumeSnapshotSpec{
			Source: volumesnapshotv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: &pvcName,
			},
			VolumeSnapshotClassName: snapshotClass,
		},
	}

	// TODO: create with retry
	result, err := volumesnapshotclient.VolumeSnapshots(namespace).Create(ctx, &snapshot, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	slog.InfoContext(ctx, "created volumesnapshot",
		slog.String("name", result.Name),
		slog.String("namespace", result.Namespace),
	)

	// TODO: if there is something wrong with this VolumeSnapshot (e.g. no snapshot class is given and there
	// is no default snapshot class), this wait will simply time out, even if there are Warning events on the
	// VolumeSnapshot right away. Between creation and readiness of the VolumeSnapshot, we should log all events
	// that appear on it.
	// TODO: our wait library does not have support for condition=jsonpath=...
	/*
		conditionFn, err := wait.ConditionFuncFor("condition=jsonpath={.status.readyToUse}=true", os.Stderr)
		if err != nil {
			return err
		}
		o := wait.WaitOptions{
			ResourceFinder: genericclioptions.NewResourceBuilderFlags().ToBuilder(
				cliConfigFlag,
				[]string{"volumesnapshot/" + result.Name},
			),
			DynamicClient: dynamic.NewForConfigOrDie(config),
			Timeout:       time.Second * 60,
			Printer:       printers.NewDiscardingPrinter(),
			ConditionFn:   conditionFn,
		}
		err = o.RunWait()
		if err != nil {
			return err
		}
	*/

	// So, use a polling loop instead
	{
		ctx, cancel := context.WithTimeout(ctx, time.Second*60)
		defer cancel()
		for ctx.Err() == nil {
			result, err = volumesnapshotclient.VolumeSnapshots(namespace).Get(ctx, backupName, metav1.GetOptions{})
			if err != nil {
				return err
			}

			if result.Status != nil && result.Status.ReadyToUse != nil && *result.Status.ReadyToUse == true {
				break
			}

			time.Sleep(time.Second)
		}
	}

	slog.InfoContext(ctx, "volumesnapshot is ready!",
		slog.String("name", result.Name),
		slog.String("namespace", result.Namespace),
		slog.String("size", result.Status.RestoreSize.String()),
	)

	var volumeClass *string
	if *volumeClassFlag != "" {
		volumeClass = volumeClassFlag
	}
	backup := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      backupName,
			Annotations: map[string]string{
				NoBackupAnnotation: "true",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: volumeClass,
			DataSource: &corev1.TypedLocalObjectReference{
				APIGroup: lo.ToPtr("snapshot.storage.k8s.io"),
				Kind:     "VolumeSnapshot",
				Name:     backupName,
			},
			// TODO: would prefer to use ReadOnlyMany but not all providers support it
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceStorage: *result.Status.RestoreSize,
				},
			},
		},
	}

	// TODO: create with retry
	if err := kclient.Create(ctx, backup); err != nil {
		return err
	}

	slog.InfoContext(ctx, "created backup PVC",
		slog.String("name", backup.Name),
		slog.String("namespace", backup.Namespace),
	)

	var imagePullSecrets []corev1.LocalObjectReference
	if *imagePullSecret != "" {
		imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{
			Name: *imagePullSecret,
		})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: lo.ToPtr(int32(4)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ImagePullSecrets: imagePullSecrets,
					RestartPolicy:    "OnFailure",
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: backupName,
								ReadOnly:  true,
							},
						},
					}},
					Containers: []corev1.Container{{
						Name:            "default",
						Image:           *image,
						ImagePullPolicy: "Always",
						Env: []corev1.EnvVar{{
							Name:  "BACKUP_NAMESPACE",
							Value: namespace,
						}, {
							Name:  "BACKUP_VOLUME",
							Value: backupName,
						}, {
							Name:  "RESTIC_REPOSITORY_BASE",
							Value: "s3:" + *s3Host + "/" + *s3Bucket,
						}, {
							Name:  "RESTIC_PASSWORD",
							Value: *resticPassword,
						}, {
							Name:  "AWS_ACCESS_KEY_ID",
							Value: *s3AccessKeyId,
						}, {
							Name:  "AWS_SECRET_ACCESS_KEY",
							Value: *s3SecretAccessKey,
						}, {
							Name:  "RESTIC_HOSTNAME",
							Value: "$(BACKUP_NAMESPACE)",
						}, {
							Name:  "RESTIC_REPOSITORY",
							Value: "$(RESTIC_REPOSITORY_BASE)/$(BACKUP_NAMESPACE)/$(BACKUP_VOLUME)",
						}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "data",
							ReadOnly:  true,
							MountPath: "/data",
						}},
					}},
				},
			},
		},
	}

	// TODO: create with retry
	if err := kclient.Create(ctx, job); err != nil {
		return err
	}

	slog.InfoContext(ctx, "created backup job",
		slog.String("name", job.Name),
		slog.String("namespace", job.Namespace),
	)

	// tail the logs for each backup job Pod
	{
		ctx, cancel := context.WithTimeout(ctx, time.Hour*4)
		defer cancel()

		for ctx.Err() == nil {
			if err := kclient.Get(ctx, client.ObjectKeyFromObject(job), job); err != nil {
				return err
			}
			if condition, ok := lo.Find(job.Status.Conditions, func(c batchv1.JobCondition) bool { return c.Reason == "BackoffLimitExceeded" }); ok {
				if condition.Status == corev1.ConditionTrue {
					return fmt.Errorf(condition.Message)
				}
			}

			jobPods := &corev1.PodList{}
			labelSelector, err := labels.Parse("job-name=" + job.Name)
			if err != nil {
				return err
			}
			if err := kclient.List(ctx, jobPods, &client.ListOptions{LabelSelector: labelSelector}); err != nil {
				return err
			}

			if len(jobPods.Items) == 0 {
				// no pods yet, wait for a bit
				time.Sleep(time.Second * 5)
				continue
			}

			// find the Pod that was created last
			sort.Slice(jobPods.Items, func(i, j int) bool {
				return jobPods.Items[j].CreationTimestamp.Before(&jobPods.Items[i].CreationTimestamp)
			})
			newest := &jobPods.Items[0]

			if newest.Status.Phase == corev1.PodPending {
				// pod still pending, wait for a bit
				time.Sleep(time.Second * 5)
				continue
			}

			req := clientset.CoreV1().Pods(newest.Namespace).GetLogs(newest.Name, &corev1.PodLogOptions{
				Container: "default",
				Follow:    true,
			})
			logs, err := req.Stream(ctx)
			if statusErr, ok := err.(*errors.StatusError); ok && strings.HasSuffix(statusErr.ErrStatus.Message, "ContainerCreating") {
				// Container is still waiting to start
				time.Sleep(time.Second * 5)
				continue
			} else if err != nil {
				return err
			}
			defer logs.Close()
			scanner := bufio.NewScanner(logs)

			for scanner.Scan() {
				// TODO: ideally the Pod should print all-JSON status updates (one per line), so that
				// we could read current backup progress
				line := scanner.Text()
				slog.InfoContext(ctx, "restic",
					slog.String("name", job.Name),
					slog.String("namespace", job.Namespace),
					slog.String("output", line),
				)
			}

			// If the scanner is done, the Pod might be done, but not marked as such on the API
			// yet. Give it a few seconds to settle.
			time.Sleep(time.Second * 5)

			if err := kclient.Get(ctx, client.ObjectKeyFromObject(newest), newest); err != nil {
				return err
			}
			if newest.Status.Phase == corev1.PodSucceeded {
				break
			}
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	slog.InfoContext(ctx, "backup job succeeded",
		slog.String("name", job.Name),
		slog.String("namespace", job.Namespace),
	)

	// Clean up
	err = volumesnapshotclient.VolumeSnapshots(namespace).Delete(ctx, backupName, metav1.DeleteOptions{
		PropagationPolicy: lo.ToPtr(metav1.DeletePropagationBackground),
	})
	if err != nil {
		return err
	}

	if err := kclient.Delete(ctx, job, &client.DeleteOptions{
		PropagationPolicy: lo.ToPtr(metav1.DeletePropagationBackground),
	}); err != nil {
		return err
	}

	if err := kclient.Delete(ctx, backup, &client.DeleteOptions{
		PropagationPolicy: lo.ToPtr(metav1.DeletePropagationBackground),
	}); err != nil {
		return err
	}

	if err := waitForDeletion(namespace, "job", backupName, config); err != nil {
		return err
	}
	if err := waitForDeletion(namespace, "pvc", backupName, config); err != nil {
		return err
	}
	if err := waitForDeletion(namespace, "volumesnapshot", backupName, config); err != nil {
		return err
	}

	// TODO:
	// log backup history for this PVC, snapshot moment, duration, bytes copied, bytes to restore, total bytes

	return nil
}

func waitForDeletion(namespace, objectType, objectName string, config *rest.Config) error {
	cliConfigFlag := genericclioptions.NewConfigFlags(true)
	cliConfigFlag.Namespace = &namespace

	o := wait.WaitOptions{
		ResourceFinder: genericclioptions.NewResourceBuilderFlags().ToBuilder(
			cliConfigFlag,
			[]string{objectType + "/" + objectName},
		),
		DynamicClient: dynamic.NewForConfigOrDie(config),
		Timeout:       time.Second * 120,
		Printer:       printers.NewDiscardingPrinter(),
		ConditionFn:   wait.IsDeleted,
	}
	err := o.RunWait()
	if errors.IsNotFound(err) {
		// already deleted, ignore
	} else if err != nil {
		return err
	}
	return nil
}
