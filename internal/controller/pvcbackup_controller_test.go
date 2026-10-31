package controller

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	mclient "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"github.com/skybitsnl/backsnap/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	cruntimeconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
)

var _ = Describe("PVCBackup and PVCRestore controller", func() {
	var storageClassName string
	if sc, ok := os.LookupEnv("STORAGE_CLASS_NAME"); ok && len(sc) > 0 {
		storageClassName = sc
	} else {
		// This is a Minikube assumption
		storageClassName = "csi-hostpath-sc"
	}

	// Restored PVCs may use a different StorageClass than backup PVCs. The
	// Minikube standard one ignores the VolumeSnapshot when provisioning a
	// volume, so if backups accidentally used it, they would back up an empty
	// volume. The tests check the backup PVC's StorageClass directly.
	var restoreStorageClassName string
	if sc, ok := os.LookupEnv("RESTORE_STORAGE_CLASS_NAME"); ok && len(sc) > 0 {
		restoreStorageClassName = sc
	} else {
		// This is a Minikube assumption
		restoreStorageClassName = "standard"
	}

	var snapshotClassName string
	if vc, ok := os.LookupEnv("SNAPSHOT_CLASS_NAME"); ok && len(vc) > 0 {
		snapshotClassName = vc
	} else {
		// This is a Minikube assumption
		snapshotClassName = "csi-hostpath-snapclass"
	}

	var ctx context.Context
	var cancel context.CancelFunc

	var mc *mclient.Client
	var namespace string

	var mbucket string
	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())

		By("creating a namespace")
		namespace = uuid.NewString()
		err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		Expect(err).ToNot(HaveOccurred())

		By("deploying RustFS in the namespace")
		rustfs := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "rustfs",
				Labels:    map[string]string{"app": "rustfs"},
			},
			Spec: corev1.PodSpec{
				Volumes: []corev1.Volume{{
					Name: "local",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{
							SizeLimit: lo.ToPtr(resource.MustParse("1Gi")),
						},
					},
				}},
				Containers: []corev1.Container{{
					Name:  "rustfs",
					Image: "rustfs/rustfs:1.0.4",
					Args:  []string{"/data"},
					Env: []corev1.EnvVar{{
						Name:  "RUSTFS_ACCESS_KEY",
						Value: "rustfsadmin",
					}, {
						Name:  "RUSTFS_SECRET_KEY",
						Value: "rustfsadmin",
					}},
					VolumeMounts: []corev1.VolumeMount{{
						Name:      "local",
						ReadOnly:  false,
						MountPath: "/data",
					}},
					// The port-forward below fails if RustFS is not listening yet
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9000)},
						},
						PeriodSeconds: 1,
					},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, rustfs)).To(BeNil())
		mservice := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "rustfs"},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{
					Port: 9000,
				}},
				Selector: map[string]string{
					"app": "rustfs",
				},
			},
		}
		Expect(k8sClient.Create(ctx, mservice)).To(BeNil())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rustfs), rustfs)).To(BeNil())
			return lo.ContainsBy(rustfs.Status.Conditions, func(c corev1.PodCondition) bool {
				return c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue
			})
		}, time.Minute, time.Second).Should(BeTrue())

		By("connecting to RustFS in the namespace")
		l, err := net.Listen("tcp", "localhost:0")
		Expect(err).ToNot(HaveOccurred())
		maddr := l.Addr().String()
		Expect(l.Close()).ToNot(HaveOccurred())
		go func() {
			defer GinkgoRecover()
			// ensure we keep the ctx we started with, and not the next node's ctx
			ctx := ctx
			cmd := exec.CommandContext(ctx, "kubectl", "port-forward", "-n", namespace, "pod/rustfs", portFromAddr(maddr)+":9000")
			cmd.Stdout = GinkgoWriter
			cmd.Stderr = GinkgoWriter
			Expect(cmd.Start()).To(BeNil())
			err := cmd.Wait()
			if ctx.Err() == nil {
				Expect(err).ToNot(HaveOccurred())
			}
		}()
		mc, err = mclient.New(maddr, &mclient.Options{
			Creds:  credentials.NewStaticV4("rustfsadmin", "rustfsadmin", ""),
			Secure: false,
		})
		Expect(err).ToNot(HaveOccurred())

		_, err = mc.HealthCheck(10 * time.Second)
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() bool {
			return mc.IsOnline()
		}, time.Minute, time.Second).Should(BeTrue())

		mbucket = uuid.NewString()
		err = mc.MakeBucket(ctx, mbucket, mclient.MakeBucketOptions{})
		Expect(err).ToNot(HaveOccurred())

		k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: scheme.Scheme,
		})
		Expect(err).ToNot(HaveOccurred())

		backupSettings := BackupSettings{
			SnapshotClass:       snapshotClassName,
			StorageClass:        storageClassName,
			RestoreStorageClass: restoreStorageClassName,
			ImagePullSecret:     "",
			Image:               resticImage,
			// RustFS we just set up
			S3Host:            "http://rustfs:9000",
			S3Bucket:          mbucket,
			S3AccessKeyId:     "rustfsadmin",
			S3SecretAccessKey: "rustfsadmin",
			// any restic password
			ResticPassword: "resticpass",
		}

		err = (&PVCBackupReconciler{
			Client:              k8sManager.GetClient(),
			Scheme:              k8sManager.GetScheme(),
			Namespaces:          []string{namespace},
			BackupSettings:      backupSettings,
			MaxRunningBackups:   1,
			SleepBetweenBackups: 1,
		}).SetupWithManager(ctx, k8sManager)
		Expect(err).ToNot(HaveOccurred())

		err = (&PVCRestoreReconciler{
			Client:         k8sManager.GetClient(),
			Scheme:         k8sManager.GetScheme(),
			Namespaces:     []string{namespace},
			BackupSettings: backupSettings,
		}).SetupWithManager(k8sManager)
		Expect(err).ToNot(HaveOccurred())

		go func() {
			defer GinkgoRecover()
			err = k8sManager.Start(ctx)
			Expect(err).ToNot(HaveOccurred(), "failed to run manager")
		}()
	})
	AfterEach(func() {
		err := k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		Expect(err).ToNot(HaveOccurred())

		cancel()
		ctx = nil
	})

	When("a PVCBackup is created", func() {
		It("should create a backup", func() {
			By("creating a PVC")
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-data",
					Namespace: namespace,
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					StorageClassName: &storageClassName,
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: map[corev1.ResourceName]resource.Quantity{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pvc)).Should(Succeed())

			By("filling it with some data")
			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "data-filler",
					Namespace: namespace,
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyOnFailure,
							Volumes: []corev1.Volume{{
								Name: "data",
								VolumeSource: corev1.VolumeSource{
									PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
										ClaimName: "my-data",
										ReadOnly:  false,
									},
								},
							}},
							Containers: []corev1.Container{{
								Name:  "default",
								Image: "ubuntu:jammy",
								Command: []string{
									"/bin/bash", "-c", "echo 'Hello World!' >/data/foo.txt",
								},
								Env: []corev1.EnvVar{},
								VolumeMounts: []corev1.VolumeMount{{
									Name:      "data",
									ReadOnly:  false,
									MountPath: "/data",
								}},
							}},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, job)).Should(Succeed())

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).Should(Succeed())

				return job.Status.CompletionTime != nil && !job.Status.CompletionTime.IsZero()
			}, time.Minute, time.Second).Should(BeTrue())

			By("creating a PVCBackup")
			backup := &v1alpha1.PVCBackup{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "my-backup"},
				Spec: v1alpha1.PVCBackupSpec{
					PVCName: "my-data",
					TTL:     metav1.Duration{Duration: time.Second * 5},
				},
			}
			Expect(k8sClient.Create(ctx, backup)).Should(Succeed())

			// The backup PVC is deleted once the backup finishes, so check it
			// while the backup is running.
			By("the backup PVC, named after the PVCBackup, uses the backup StorageClass")
			expectPVCStorageClass(ctx, namespace, "my-backup", storageClassName)

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(backup), backup)).Should(Succeed())

				return backup.Status.FinishedAt != nil && !backup.Status.FinishedAt.IsZero()
			}, time.Minute, time.Second).Should(BeTrue())

			Expect(*backup.Status.Result).To(BeEquivalentTo("Succeeded"))

			By("the PVCBackup should not be deleted after 10 seconds, since it's the last one")
			time.Sleep(time.Second * 10)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(backup), backup)).Should(Succeed())

			By("creating a second PVCBackup")
			backup2 := &v1alpha1.PVCBackup{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "my-backup2"},
				Spec: v1alpha1.PVCBackupSpec{
					PVCName: "my-data",
					TTL:     metav1.Duration{Duration: time.Second * 5},
				},
			}
			Expect(k8sClient.Create(ctx, backup2)).Should(Succeed())
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(backup2), backup2)).Should(Succeed())

				return backup2.Status.FinishedAt != nil && !backup2.Status.FinishedAt.IsZero()
			}, time.Minute, time.Second).Should(BeTrue())

			Expect(*backup2.Status.Result).To(BeEquivalentTo("Succeeded"))

			By("eventually the first PVCBackup should be deleted")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(backup), backup)
				return apierrors.IsNotFound(err)
			}, time.Minute, time.Second).Should(BeTrue())

			By("the S3 volume should contain two snapshots")
			prefix := namespace + "/my-data/snapshots"
			var snapshots []string
			for object := range mc.ListObjects(ctx, mbucket, mclient.ListObjectsOptions{
				Prefix:    prefix,
				Recursive: true,
			}) {
				Expect(object.Err).Should(BeNil())
				snapshots = append(snapshots, object.Key[len(prefix)+1:])
			}
			Expect(snapshots).To(HaveLen(2))
			Expect(snapshots[0]).ToNot(HaveLen(0))
			Expect(snapshots[1]).ToNot(HaveLen(0))

			By("deleting the second backup object, PVC and job")
			Expect(k8sClient.Delete(ctx, backup2)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, pvc, &client.DeleteOptions{
				PropagationPolicy: lo.ToPtr(metav1.DeletePropagationForeground),
			})).Should(Succeed())
			Expect(k8sClient.Delete(ctx, job, &client.DeleteOptions{
				PropagationPolicy: lo.ToPtr(metav1.DeletePropagationForeground),
			})).Should(Succeed())

			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(pvc), pvc))
			}, time.Minute, time.Second).Should(BeTrue())

			By("creating a PVCRestore object")
			restore := &v1alpha1.PVCRestore{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "my-restore"},
				Spec: v1alpha1.PVCRestoreSpec{
					SourcePVC:       "my-data",
					SourceNamespace: namespace,
					SourceSnapshot:  "latest",
					TargetPVC:       "",
					TargetPVCSize:   resource.MustParse("1Gi"),
				},
			}
			Expect(k8sClient.Create(ctx, restore)).Should(Succeed())

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(restore), restore)).Should(Succeed())

				return restore.Status.FinishedAt != nil && !restore.Status.FinishedAt.IsZero()
			}, time.Minute, time.Second).Should(BeTrue())

			Expect(*restore.Status.Result).To(BeEquivalentTo("Succeeded"))

			By("the restored PVC uses the restore StorageClass")
			expectPVCStorageClass(ctx, namespace, "my-data", restoreStorageClassName)

			By("dumping the contents")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "data-printer",
					Namespace: namespace,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: "my-data",
								ReadOnly:  true,
							},
						},
					}},
					Containers: []corev1.Container{{
						Name:  "default",
						Image: "ubuntu:jammy",
						Command: []string{
							"/bin/bash", "-c", "cat /data/foo.txt",
						},
						Env: []corev1.EnvVar{},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "data",
							ReadOnly:  true,
							MountPath: "/data",
						}},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).Should(Succeed())

				return pod.Status.Phase == corev1.PodSucceeded
			}, time.Minute, time.Second).Should(BeTrue())

			// TODO: retrieve container logs using controller-runtime
			// https://github.com/kubernetes-sigs/controller-runtime/issues/452
			config := cruntimeconfig.GetConfigOrDie()
			clientset := kubernetes.NewForConfigOrDie(config)
			req := clientset.CoreV1().Pods(namespace).GetLogs(pod.ObjectMeta.Name, &corev1.PodLogOptions{
				Container: "default",
				Follow:    true,
			})
			logs, err := req.Stream(ctx)
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = logs.Close() }()
			bytes, err := io.ReadAll(logs)
			Expect(err).ToNot(HaveOccurred())

			Expect(string(bytes)).To(Equal("Hello World!\n"))
		})
	})

	When("multiple PVCBackups are created", func() {
		It("should run only one of them at a time", func() {
			By("creating three PVCs")
			var pvcs []*corev1.PersistentVolumeClaim
			for i := 0; i < 3; i += 1 {
				pvc := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("my-data-%d", i),
						Namespace: namespace,
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						StorageClassName: &storageClassName,
						AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.VolumeResourceRequirements{
							Requests: map[corev1.ResourceName]resource.Quantity{
								corev1.ResourceStorage: resource.MustParse("1Gi"),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, pvc)).Should(Succeed())
				pvcs = append(pvcs, pvc)
			}

			By("filling them with some data")
			var jobs []*batchv1.Job
			for i, pvc := range pvcs {
				job := &batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("data-filler-%d", i),
						Namespace: namespace,
					},
					Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								RestartPolicy: corev1.RestartPolicyOnFailure,
								Volumes: []corev1.Volume{{
									Name: "data",
									VolumeSource: corev1.VolumeSource{
										PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
											ClaimName: pvc.ObjectMeta.Name,
											ReadOnly:  false,
										},
									},
								}},
								Containers: []corev1.Container{{
									Name:  "default",
									Image: "ubuntu:jammy",
									Command: []string{
										"/bin/bash", "-c", "dd if=/dev/urandom of=/data/foo.txt bs=1M count=100",
									},
									Env: []corev1.EnvVar{},
									VolumeMounts: []corev1.VolumeMount{{
										Name:      "data",
										ReadOnly:  false,
										MountPath: "/data",
									}},
								}},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, job)).Should(Succeed())
				jobs = append(jobs, job)
			}

			Eventually(func() bool {
				for _, job := range jobs {
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).Should(Succeed())

					if job.Status.CompletionTime == nil || job.Status.CompletionTime.IsZero() {
						return false
					}
				}
				return true
			}, time.Minute, time.Second).Should(BeTrue())

			By("creating PVCBackups for all of them")
			var backups []*v1alpha1.PVCBackup
			for i, pvc := range pvcs {
				backup := &v1alpha1.PVCBackup{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: fmt.Sprintf("my-backup-%d", i)},
					Spec: v1alpha1.PVCBackupSpec{
						PVCName: pvc.Name,
						TTL:     metav1.Duration{Duration: time.Minute * 5},
					},
				}
				Expect(k8sClient.Create(ctx, backup)).Should(Succeed())

				backups = append(backups, backup)
			}

			By("waiting until they complete")
			// three backups, we give each a minute, plus there's a 30 second period worst-case period after
			// which the controller tries to start a backup again, so 3 * 1.5 = 4.5 minutes.
			Eventually(func() bool {
				for _, backup := range backups {
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(backup), backup)).Should(Succeed())

					if backup.Status.FinishedAt == nil || backup.Status.FinishedAt.IsZero() {
						return false
					}
				}
				return true
			}, 5*time.Minute, time.Second).Should(BeTrue())

			for _, backup := range backups {
				Expect(*backup.Status.Result).To(BeEquivalentTo("Succeeded"))
			}

			By("checking that none of them ran simultaneously")
			for i, backup := range backups {
				for j, backup2 := range backups {
					if i == j {
						continue
					}
					st1 := backup.Status.StartedAt
					st2 := backup2.Status.StartedAt
					startedBeforeEqual := st1.Equal(st2) || st1.Before(st2)
					finishedBefore := backup.Status.FinishedAt.Before(st2)
					if startedBeforeEqual && !finishedBefore {
						Fail(fmt.Sprintf("backup %s (%s-%s) overlapped with backup %s (%s-%s)",
							backup.Name, backup.Status.StartedAt.Format("15:04:05"), backup.Status.FinishedAt.Format("15:04:05"),
							backup2.Name, backup2.Status.StartedAt.Format("15:04:05"), backup2.Status.FinishedAt.Format("15:04:05"),
						))
					}
				}
			}
		})
	})

	When("a StorageClass is set on the object", func() {
		// This StorageClass does not exist, so PVCs using it stay Pending. That is
		// enough to check which StorageClass they were created with. The Pending
		// PVCs are cleaned up along with the namespace.
		nonexistentStorageClassName := "backsnap-test-nonexistent"

		It("should create the backup PVC with that StorageClass", func() {
			By("creating a PVC to back up")
			createPVC(ctx, namespace, "my-data", storageClassName)

			By("creating a PVCBackup that overrides the StorageClass")
			backup := &v1alpha1.PVCBackup{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "my-backup"},
				Spec: v1alpha1.PVCBackupSpec{
					PVCName:          "my-data",
					StorageClassName: nonexistentStorageClassName,
				},
			}
			Expect(k8sClient.Create(ctx, backup)).Should(Succeed())

			By("the backup PVC, named after the PVCBackup, uses the overridden StorageClass")
			expectPVCStorageClass(ctx, namespace, "my-backup", nonexistentStorageClassName)
		})

		It("should create the restored PVC with that StorageClass", func() {
			By("creating a PVCRestore that overrides the StorageClass")
			restore := &v1alpha1.PVCRestore{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "my-restore"},
				Spec: v1alpha1.PVCRestoreSpec{
					SourcePVC:        "my-data",
					TargetPVCSize:    resource.MustParse("1Gi"),
					StorageClassName: nonexistentStorageClassName,
				},
			}
			Expect(k8sClient.Create(ctx, restore)).Should(Succeed())

			By("the restored PVC uses the overridden StorageClass")
			expectPVCStorageClass(ctx, namespace, "my-data", nonexistentStorageClassName)
		})
	})

	When("a backup or restore is interrupted", func() {
		// A lock left behind by an interrupted restic process blocks other
		// backups and restores of the same PVC, until it becomes stale after
		// 30 minutes.

		It("should not leave a lock behind when a backup is interrupted", func() {
			// With this much data, the backup runs for several seconds, so it is
			// still running when the Pod is stopped.
			By("creating a PVC with some data")
			createPVC(ctx, namespace, "my-data", storageClassName)
			fillPVC(ctx, namespace, "my-data", "dd if=/dev/urandom of=/data/random bs=1M count=900")

			By("creating a PVCBackup")
			backup := createBackup(ctx, namespace, "my-backup", "my-data")

			By("waiting until restic uploads data into the new repository, while holding a lock")
			Eventually(func() []string {
				return listObjects(ctx, mc, mbucket, namespace+"/my-data/data/")
			}, 2*time.Minute, 100*time.Millisecond).ShouldNot(BeEmpty())
			Expect(listObjects(ctx, mc, mbucket, namespace+"/my-data/config")).To(HaveLen(1))
			Expect(listObjects(ctx, mc, mbucket, namespace+"/my-data/locks/")).ToNot(BeEmpty())

			By("deleting the PVCBackup, and stopping its Pod")
			Expect(k8sClient.Delete(ctx, backup)).Should(Succeed())
			stopJobPods(ctx, namespace, "my-backup")

			By("the backup was interrupted, so no snapshot was saved")
			Expect(listObjects(ctx, mc, mbucket, namespace+"/my-data/snapshots/")).To(BeEmpty())

			By("restic has removed its lock")
			Expect(listObjects(ctx, mc, mbucket, namespace+"/my-data/locks/")).To(BeEmpty())
		})

		It("should not leave a lock behind when a restore is interrupted", func() {
			// With this much data, the restore runs for a few seconds, so it is
			// still running when the Pod is stopped. More data would make the
			// backup below slow.
			By("backing up a PVC with some data")
			createPVC(ctx, namespace, "my-data", storageClassName)
			fillPVC(ctx, namespace, "my-data", "dd if=/dev/urandom of=/data/random bs=1M count=300")
			expectBackupSucceeds(ctx, createBackup(ctx, namespace, "my-backup", "my-data"))
			Expect(listObjects(ctx, mc, mbucket, namespace+"/my-data/locks/")).To(BeEmpty())

			By("creating a PVCRestore")
			restore := &v1alpha1.PVCRestore{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "my-restore"},
				Spec: v1alpha1.PVCRestoreSpec{
					SourcePVC:     "my-data",
					TargetPVC:     "my-restored-data",
					TargetPVCSize: resource.MustParse("1Gi"),
				},
			}
			Expect(k8sClient.Create(ctx, restore)).Should(Succeed())

			By("waiting until restic restores, while holding a lock")
			Eventually(func() []string {
				return listObjects(ctx, mc, mbucket, namespace+"/my-data/locks/")
			}, 2*time.Minute, 100*time.Millisecond).ShouldNot(BeEmpty())

			By("deleting the PVCRestore, and stopping its Pod")
			Expect(k8sClient.Delete(ctx, restore)).Should(Succeed())
			stopJobPods(ctx, namespace, "my-restore")

			By("restic has removed its lock")
			Expect(listObjects(ctx, mc, mbucket, namespace+"/my-data/locks/")).To(BeEmpty())
		})
	})

	When("the repository has a stale lock", func() {
		It("should remove the lock and back up", func() {
			// The lock in this repository was left by a restic backup that
			// was killed, and is older than 30 minutes, so restic considers
			// it stale. See testdata/generate-stale-lock-repo.sh.
			By("uploading a repository with a stale lock")
			uploadDir(ctx, mc, mbucket, "testdata/stale-lock-repo", namespace+"/my-data")
			Expect(listObjects(ctx, mc, mbucket, namespace+"/my-data/locks/")).To(HaveLen(1))

			By("backing up a PVC into it")
			createPVC(ctx, namespace, "my-data", storageClassName)
			fillPVC(ctx, namespace, "my-data", "echo 'Hello World!' >/data/foo.txt")
			expectBackupSucceeds(ctx, createBackup(ctx, namespace, "my-backup", "my-data"))

			By("the stale lock has been removed")
			Expect(listObjects(ctx, mc, mbucket, namespace+"/my-data/locks/")).To(BeEmpty())
		})
	})
})

// fillPVC runs command in a Job with the PVC mounted at /data, and waits for
// the Job to complete.
func fillPVC(ctx context.Context, namespace, pvcName, command string) {
	GinkgoHelper()
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "fill-" + pvcName},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyOnFailure,
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName},
						},
					}},
					Containers: []corev1.Container{{
						Name:         "default",
						Image:        "ubuntu:jammy",
						Command:      []string{"/bin/bash", "-c", command},
						VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data"}},
					}},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, job)).Should(Succeed())
	Eventually(func() *metav1.Time {
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).Should(Succeed())
		return job.Status.CompletionTime
	}, 2*time.Minute, time.Second).ShouldNot(BeNil())
}

func createBackup(ctx context.Context, namespace, name, pvcName string) *v1alpha1.PVCBackup {
	GinkgoHelper()
	backup := &v1alpha1.PVCBackup{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       v1alpha1.PVCBackupSpec{PVCName: pvcName},
	}
	Expect(k8sClient.Create(ctx, backup)).Should(Succeed())
	return backup
}

func expectBackupSucceeds(ctx context.Context, backup *v1alpha1.PVCBackup) {
	GinkgoHelper()
	Eventually(func() *metav1.Time {
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(backup), backup)).Should(Succeed())
		return backup.Status.FinishedAt
	}, 3*time.Minute, time.Second).ShouldNot(BeNil())
	Expect(*backup.Status.Result).To(BeEquivalentTo("Succeeded"))
}

// stopJobPods deletes the Pods of a Job with a grace period of one second,
// instead of the default 30 seconds, and waits until they are gone. Without
// handling SIGTERM, restic would be killed before it removes its locks. The
// owner of the Job must already be deleted, so that the Job does not start new
// Pods.
func stopJobPods(ctx context.Context, namespace, jobName string) {
	GinkgoHelper()
	Expect(k8sClient.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace(namespace),
		client.MatchingLabels{"job-name": jobName}, client.GracePeriodSeconds(1))).Should(Succeed())
	Eventually(func() []corev1.Pod {
		pods := &corev1.PodList{}
		Expect(k8sClient.List(ctx, pods, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName})).Should(Succeed())
		return pods.Items
	}, 2*time.Minute, time.Second).Should(BeEmpty())
}

// listObjects returns the keys of all objects in the bucket under prefix.
func listObjects(ctx context.Context, mc *mclient.Client, bucket, prefix string) []string {
	GinkgoHelper()
	var keys []string
	for object := range mc.ListObjects(ctx, bucket, mclient.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		Expect(object.Err).ToNot(HaveOccurred())
		keys = append(keys, object.Key)
	}
	return keys
}

// uploadDir uploads all files in dir to the bucket, under prefix.
func uploadDir(ctx context.Context, mc *mclient.Client, bucket, dir, prefix string) {
	GinkgoHelper()
	Expect(filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		_, err = mc.FPutObject(ctx, bucket, prefix+"/"+filepath.ToSlash(rel), path, mclient.PutObjectOptions{})
		return err
	})).Should(Succeed())
}

func createPVC(ctx context.Context, namespace, name, storageClassName string) {
	GinkgoHelper()
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &storageClassName,
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, pvc)).Should(Succeed())
}

// expectPVCStorageClass waits until the PVC exists, then checks its StorageClass.
func expectPVCStorageClass(ctx context.Context, namespace, name, storageClassName string) {
	GinkgoHelper()
	pvc := &corev1.PersistentVolumeClaim{}
	Eventually(func() error {
		return k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, pvc)
	}, time.Minute, time.Second).Should(Succeed())
	Expect(pvc.Spec.StorageClassName).To(HaveValue(Equal(storageClassName)))
}

func portFromAddr(addr string) string {
	idx := strings.Index(addr, ":")
	return addr[idx+1:]
}
