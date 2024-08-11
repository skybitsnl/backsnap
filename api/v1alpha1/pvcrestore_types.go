package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PVCRestoreSpec defines the desired state of PVCRestore
type PVCRestoreSpec struct {
	// The name of the source PVC that will be restored. The source PVC does not
	// need to exist anymore, this is just for finding its data.
	SourcePVC string `json:"sourcePvc,omitempty"`
	// The namespace that the source PVC used to exist in. If empty, assume that
	// the source namespace is the same as the namespace where this PVCRestore
	// object exists.
	SourceNamespace string `json:"sourceNamespace,omitempty"`
	// The snapshot to restore, or empty to restore the latest snapshot.
	SourceSnapshot string `json:"sourceSnapshot,omitempty"`

	// The name of the new PVC where the source contents will be restored into.
	// The PVC must not exist, and will be created. If empty, assume that the
	// target PVC is the same name as the source PVC.
	TargetPVC string `json:"targetPvc,omitempty"`

	// The size of the target PVC. Must be large enough to contain the backup's
	// contents.
	TargetPVCSize resource.Quantity `json:"targetPvcSize,omitempty"`

	// NodeSelector is a selector which must be true for the restore Pod to fit
	// on a node. This can be used e.g. to select which type of node, or which
	// Availability Zone, performs a restore. This, in turn, may also determine
	// in which Availability Zone the restored volume is created.
	// More info: https://kubernetes.io/docs/concepts/configuration/assign-pod-node/
	// +optional
	// +mapType=atomic
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// If specified, the restore Pod's tolerations.
	// +optional
	// +listType=atomic
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// If specified, indicates the restore Pod's priority.
	// +optional
	PriorityClassName string `json:"priorityClassName,omitempty"`

	// If specified, indicates the labels to be put on the restored PVC, restore
	// Job and restore Pod.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/labels
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// If specified, indicates the annotations to be put on the restored PVC,
	// restore Job and restore Pod. This SHOULD NOT include any backsnap.skyb.it
	// annotations.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// PVCRestoreStatus defines the observed state of PVCRestore
type PVCRestoreStatus struct {
	StartedAt  *metav1.Time     `json:"startedAt,omitempty"`
	FinishedAt *metav1.Time     `json:"finishedAt,omitempty"`
	Duration   *metav1.Duration `json:"duration,omitempty"`
	Result     *Result          `json:"result,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Time the restore was requested"
//+kubebuilder:printcolumn:name="Started at",type="string",JSONPath=".status.startedAt",description="Time the restore job started running"
//+kubebuilder:printcolumn:name="Duration",type="string",JSONPath=".status.duration",description="Time the restore job took to finish running"
//+kubebuilder:printcolumn:name="Result",type="string",JSONPath=".status.result",description="Shows whether the restore succeeded or not"

// PVCRestore is the Schema for the pvcrestores API
type PVCRestore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PVCRestoreSpec   `json:"spec,omitempty"`
	Status PVCRestoreStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PVCRestoreList contains a list of PVCRestore
type PVCRestoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PVCRestore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PVCRestore{}, &PVCRestoreList{})
}
