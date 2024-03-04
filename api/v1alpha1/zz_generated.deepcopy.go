//go:build !ignore_autogenerated

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PVCBackup) DeepCopyInto(out *PVCBackup) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PVCBackup.
func (in *PVCBackup) DeepCopy() *PVCBackup {
	if in == nil {
		return nil
	}
	out := new(PVCBackup)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PVCBackup) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PVCBackupList) DeepCopyInto(out *PVCBackupList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]PVCBackup, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PVCBackupList.
func (in *PVCBackupList) DeepCopy() *PVCBackupList {
	if in == nil {
		return nil
	}
	out := new(PVCBackupList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PVCBackupList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PVCBackupSpec) DeepCopyInto(out *PVCBackupSpec) {
	*out = *in
	out.TTL = in.TTL
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PVCBackupSpec.
func (in *PVCBackupSpec) DeepCopy() *PVCBackupSpec {
	if in == nil {
		return nil
	}
	out := new(PVCBackupSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PVCBackupStatus) DeepCopyInto(out *PVCBackupStatus) {
	*out = *in
	if in.FinishedAt != nil {
		in, out := &in.FinishedAt, &out.FinishedAt
		*out = (*in).DeepCopy()
	}
	if in.Result != nil {
		in, out := &in.Result, &out.Result
		*out = new(Result)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PVCBackupStatus.
func (in *PVCBackupStatus) DeepCopy() *PVCBackupStatus {
	if in == nil {
		return nil
	}
	out := new(PVCBackupStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PVCRestore) DeepCopyInto(out *PVCRestore) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PVCRestore.
func (in *PVCRestore) DeepCopy() *PVCRestore {
	if in == nil {
		return nil
	}
	out := new(PVCRestore)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PVCRestore) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PVCRestoreList) DeepCopyInto(out *PVCRestoreList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]PVCRestore, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PVCRestoreList.
func (in *PVCRestoreList) DeepCopy() *PVCRestoreList {
	if in == nil {
		return nil
	}
	out := new(PVCRestoreList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *PVCRestoreList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PVCRestoreSpec) DeepCopyInto(out *PVCRestoreSpec) {
	*out = *in
	out.TargetPVCSize = in.TargetPVCSize.DeepCopy()
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PVCRestoreSpec.
func (in *PVCRestoreSpec) DeepCopy() *PVCRestoreSpec {
	if in == nil {
		return nil
	}
	out := new(PVCRestoreSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PVCRestoreStatus) DeepCopyInto(out *PVCRestoreStatus) {
	*out = *in
	if in.FinishedAt != nil {
		in, out := &in.FinishedAt, &out.FinishedAt
		*out = (*in).DeepCopy()
	}
	if in.Result != nil {
		in, out := &in.Result, &out.Result
		*out = new(Result)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PVCRestoreStatus.
func (in *PVCRestoreStatus) DeepCopy() *PVCRestoreStatus {
	if in == nil {
		return nil
	}
	out := new(PVCRestoreStatus)
	in.DeepCopyInto(out)
	return out
}
