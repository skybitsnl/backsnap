package controller

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
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

	var snapshotClassName string
	if vc, ok := os.LookupEnv("SNAPSHOT_CLASS_NAME"); ok && len(vc) > 0 {
		snapshotClassName = vc
	} else {
		// This is a Minikube assumption
		snapshotClassName = "csi-hostpath-snapclass"
	}

	ctx, cancel := context.WithCancel(context.Background())

	var mc *mclient.Client
	var namespace string

	var mbucket string
	BeforeEach(func() {
		By("creating a namespace")
		namespace = uuid.NewString()
		err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		Expect(err).ToNot(HaveOccurred())

		By("deploying MinIO in the namespace")
		minio := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "minio",
				Labels:    map[string]string{"app": "minio"},
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
					Name:    "minio",
					Image:   "quay.io/minio/minio:RELEASE.2024-03-03T17-50-39Z",
					Command: []string{"minio", "server", "/data", "--console-address", ":9001"},
					VolumeMounts: []corev1.VolumeMount{{
						Name:      "local",
						ReadOnly:  false,
						MountPath: "/data",
					}},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, minio)).To(BeNil())
		mservice := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "minio"},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{
					Port: 9000,
				}},
				Selector: map[string]string{
					"app": "minio",
				},
			},
		}
		Expect(k8sClient.Create(ctx, mservice)).To(BeNil())
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(minio), minio)).To(BeNil())
			return minio.Status.Phase == corev1.PodRunning
		}, time.Minute, time.Second).Should(BeTrue())

		By("connecting to MinIO in the namespace")
		l, err := net.Listen("tcp", "localhost:0")
		Expect(err).ToNot(HaveOccurred())
		maddr := l.Addr().String()
		Expect(l.Close()).ToNot(HaveOccurred())
		go func() {
			defer GinkgoRecover()
			cmd := exec.CommandContext(ctx, "kubectl", "port-forward", "-n", namespace, "pod/minio", portFromAddr(maddr)+":9000")
			Expect(cmd.Start()).To(BeNil())
			err := cmd.Wait()
			if ctx.Err() == nil {
				Expect(err).ToNot(HaveOccurred())
			}
		}()
		mc, err = mclient.New(maddr, &mclient.Options{
			Creds:  credentials.NewStaticV4("minioadmin", "minioadmin", ""),
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
			SnapshotClass: snapshotClassName,
			VolumeClass:   storageClassName,
			// default restic image
			ImagePullSecret: "",
			Image:           "sjorsgielen/backsnap-restic:latest-main",
			// MinIO we just set up
			S3Host:            "http://minio:9000",
			S3Bucket:          mbucket,
			S3AccessKeyId:     "minioadmin",
			S3SecretAccessKey: "minioadmin",
			// any restic password
			ResticPassword: "resticpass",
		}

		err = (&PVCBackupReconciler{
			Client:         k8sManager.GetClient(),
			Scheme:         k8sManager.GetScheme(),
			Namespaces:     []string{namespace},
			BackupSettings: backupSettings,
		}).SetupWithManager(k8sManager)
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

			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(backup), backup)).Should(Succeed())

				return backup.Status.FinishedAt != nil && !backup.Status.FinishedAt.IsZero()
			}, time.Minute, time.Second).Should(BeTrue())

			Expect(*backup.Status.Result).To(BeEquivalentTo("Succeeded"))

			By("the S3 volume should contain a single snapshot")
			prefix := namespace + "/my-data/snapshots"
			var snapshots []string
			for object := range mc.ListObjects(ctx, mbucket, mclient.ListObjectsOptions{
				Prefix:    prefix,
				Recursive: true,
			}) {
				Expect(object.Err).Should(BeNil())
				snapshots = append(snapshots, object.Key[len(prefix)+1:])
			}
			Expect(snapshots).To(HaveLen(1))
			Expect(snapshots[0]).ToNot(HaveLen(0))

			By("deleting the original backup object, PVC and job")
			Expect(k8sClient.Delete(ctx, backup)).Should(Succeed())
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
			defer logs.Close()
			bytes, err := io.ReadAll(logs)
			Expect(err).ToNot(HaveOccurred())

			Expect(string(bytes)).To(Equal("Hello World!\n"))
		})
	})
})

func portFromAddr(addr string) string {
	idx := strings.Index(addr, ":")
	return addr[idx+1:]
}
