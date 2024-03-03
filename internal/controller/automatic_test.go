package controller

import (
	"context"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"github.com/skybitsnl/backsnap/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Automatic controller", func() {
	var namespace string
	ctx, cancel := context.WithCancel(context.Background())

	BeforeEach(func() {
		By("creating a namespace")
		namespace = uuid.NewString()
		err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
			Annotations: map[string]string{
				BackupScheduleAnnotation: "* * * * *",
			},
		}})
		Expect(err).ToNot(HaveOccurred())

		k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: scheme.Scheme,
		})
		Expect(err).ToNot(HaveOccurred())

		err = (&AutomaticPVCBackupCreator{
			Client:          k8sManager.GetClient(),
			Scheme:          k8sManager.GetScheme(),
			Namespaces:      []string{namespace},
			DefaultSchedule: "",
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

	When("a PVC exists", func() {
		It("should get a PVCBackup if it has existed for long enough", func() {
			By("creating a new PVC")
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-data",
					Namespace: namespace,
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: map[corev1.ResourceName]resource.Quantity{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pvc)).Should(Succeed())

			Eventually(func() bool {
				var list v1alpha1.PVCBackupList
				Expect(k8sClient.List(ctx, &list, &client.ListOptions{
					Namespace: namespace,
				})).Should(Succeed())

				return len(list.Items) == 1 && list.Items[0].Spec.PVCName == "my-data"
			}, time.Minute, time.Second).Should(BeTrue())

			// Then, just over a minute later, the list length should be 2
			time.Sleep(time.Second * 10)

			Eventually(func() bool {
				var list v1alpha1.PVCBackupList
				Expect(k8sClient.List(ctx, &list, &client.ListOptions{
					Namespace: namespace,
				})).Should(Succeed())

				return len(list.Items) == 2 && lo.EveryBy(list.Items, func(b v1alpha1.PVCBackup) bool { return b.Spec.PVCName == "my-data" })
			}, time.Minute, time.Second).Should(BeTrue())
		})
	})
})
