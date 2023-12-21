package main_test

import (
	"context"
	"testing"

	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	backsnap "github.com/skybitsnl/backsnap/cmd/backsnap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	oldfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBackupPVC_DoesntExist(t *testing.T) {
	ctx := context.Background()

	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	batchv1.AddToScheme(scheme)
	volumesnapshotv1.AddToScheme(scheme)

	clientset := oldfake.NewSimpleClientset()
	kclient := fake.NewClientBuilder().WithScheme(scheme).Build()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)

	err := backsnap.BackupPvc(ctx, clientset, kclient, dynamicClient, "app1", "my-storage", backsnap.BackupSettings{})
	if !errors.IsNotFound(err) {
		t.Fatalf("expected Not Found error, but got: %+v", err)
	}
}
