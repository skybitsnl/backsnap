package main_test

import (
	"context"
	"testing"

	"github.com/go-test/deep"
	"github.com/samber/lo"
	backsnap "github.com/skybitsnl/backsnap/cmd/backsnap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func removeIgnoreFields(pvcs []corev1.PersistentVolumeClaim) {
	for i := range pvcs {
		pvcs[i].ObjectMeta.ResourceVersion = ""
	}
}

func TestSelectPVCsForBackup_NoPVCs(t *testing.T) {
	ctx := context.Background()
	kclient := fake.NewFakeClient()

	pvcs, err := backsnap.SelectPVCsForBackup(ctx, kclient, []string{""}, map[string]struct{}{})
	if err != nil {
		t.Fatal(err)
	}

	var expectedPvcs []corev1.PersistentVolumeClaim

	removeIgnoreFields(pvcs)
	if diff := deep.Equal(pvcs, expectedPvcs); diff != nil {
		t.Fatal(diff)
	}
}

func TestSelectPVCsForBackup_OnePVC(t *testing.T) {
	ctx := context.Background()
	kclient := fake.NewFakeClient(
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "application",
				Name:      "my-storage",
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: lo.ToPtr("default-sc"),
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceStorage: resource.MustParse("16Gi"),
					},
				},
			},
		},
	)

	pvcs, err := backsnap.SelectPVCsForBackup(ctx, kclient, []string{""}, map[string]struct{}{})
	if err != nil {
		t.Fatal(err)
	}

	expectedPvcs := []corev1.PersistentVolumeClaim{{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "application",
			Name:      "my-storage",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: lo.ToPtr("default-sc"),
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceStorage: resource.MustParse("16Gi"),
				},
			},
		},
	}}

	removeIgnoreFields(pvcs)
	if diff := deep.Equal(pvcs, expectedPvcs); diff != nil {
		t.Fatal(diff)
	}
}

func TestSelectPVCsForBackup_IgnoredPVCs(t *testing.T) {
	ctx := context.Background()
	kclient := fake.NewFakeClient(
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "app1",
				Name:      "my-storage1",
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: lo.ToPtr("default-sc"),
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceStorage: resource.MustParse("16Gi"),
					},
				},
			},
		},
		// Ignore this PVC because it is in an excluded namespace
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "app2",
				Name:      "my-storage",
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: lo.ToPtr("default-sc"),
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceStorage: resource.MustParse("16Gi"),
					},
				},
			},
		},
		// Ignore this PVC because it is in a non-included namespace
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "app3",
				Name:      "my-storage",
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: lo.ToPtr("default-sc"),
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceStorage: resource.MustParse("16Gi"),
					},
				},
			},
		},
		// Ignore this PVC because it has a no-backup annotation
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "app1",
				Name:      "my-storage2",
				Annotations: map[string]string{
					backsnap.NoBackupAnnotation: "true",
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: lo.ToPtr("default-sc"),
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceStorage: resource.MustParse("16Gi"),
					},
				},
			},
		},
		// Ignore this PVC because it is being deleted
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:         "app1",
				Name:              "my-storage3",
				DeletionTimestamp: lo.ToPtr(metav1.Now()),
				Finalizers:        []string{"fake-finalizer"},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: lo.ToPtr("default-sc"),
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: map[corev1.ResourceName]resource.Quantity{
						corev1.ResourceStorage: resource.MustParse("16Gi"),
					},
				},
			},
		},
	)

	pvcs, err := backsnap.SelectPVCsForBackup(ctx, kclient, []string{"app1", "app2"}, map[string]struct{}{"app2": {}})
	if err != nil {
		t.Fatal(err)
	}

	expectedPvcs := []corev1.PersistentVolumeClaim{{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "app1",
			Name:      "my-storage1",
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: lo.ToPtr("default-sc"),
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceStorage: resource.MustParse("16Gi"),
				},
			},
		},
	}}

	removeIgnoreFields(pvcs)
	if diff := deep.Equal(pvcs, expectedPvcs); diff != nil {
		t.Fatal(diff)
	}
}
