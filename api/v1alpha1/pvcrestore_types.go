package v1alpha1

import (
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
