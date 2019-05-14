/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NetworkAttachmentList is a list of NetworkAttachment objects.
type NetworkAttachmentList struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []NetworkAttachment `json:"items" protobuf:"bytes,2,rep,name=items"`
}

type NetworkAttachmentSpec struct {
	// Node is the name of the node where the attachment should appear
	Node string `json:"node" protobuf:"bytes,1,name=node"`

	// Subnet is the object name of the subnet of this attachment
	Subnet string `json:"subnet" protobuf:"bytes,2,name=subnet"`

	// PostCreateExec is a command to exec inside the attachment
	// host's connection agent container after the Linux network
	// interface is created.  Precisely: if a local NetworkAttachment
	// is in the network fabric, has a non-empty PostCreateExec, and
	// that command has not yet been launched then the command is
	// launched and, upon completion, the results reported through the
	// NetworkAttachmentStatus PostCreateExecReport field.  The
	// connection agent is configured with a set of allowed programs
	// to invoke.  If a non-allowed program is requested then the
	// result will report an error.  Each argument is subjected to a
	// very restricted form of variable expansion.  The only allowed
	// syntax is `${variableName}` and the only variables are
	// `ifname`, `ipv4`, and `mac`.
	// +optional
	// +patchStrategy=replace
	PostCreateExec []string `json:"postCreateExec,omitempty" protobuf:"bytes,3,opt,name=postCreateExec" patchStrategy:"replace"`

	// PostDeleteExec is a command to exec inside the attachment
	// host's connection agent container after the attachment's Linux
	// network interface is deleted.  Precisely: if a local
	// NetworkAttachment is not in the network fabric, has a
	// PostCreateExec that has been started, has a non-empty
	// PostDeleteExec, and the PostCreateExec has not yet been
	// launched then that command will be launched.  The result is not
	// reported in the status of the NetworkAttachment (it may be
	// deleted by then).  The same restrictions and variable
	// expansions as for PostCreateExec are applied.
	// +optional
	// +patchStrategy=replace
	PostDeleteExec []string `json:"postDeleteExec,omitempty" protobuf:"bytes,4,opt,name=postDeleteExec" patchStrategy:"replace"`
}

type NetworkAttachmentStatus struct {
	// +optional
	Errors NetworkAttachmentErrors `json:"errors,omitempty" protobuf:"bytes,1,opt,name=errors"`

	// LockUID is the UID of the IPLock object holding this attachment's
	// IP address, or the empty string when there is no address.
	// This field is a private detail of the implementation, not really
	// part of the public API.
	// +optional
	LockUID string `json:"lockUID,omitempty" protobuf:"bytes,2,opt,name=lockUID"`
	// AddressVNI is the VNI associated with this attachment's
	// IP address assignment, or the empty string when there is no address.
	// +optional
	AddressVNI uint32 `json:"addressVNI,omitempty" protobuf:"bytes,3,opt,name=addressVNI"`

	// IPv4 is non-empty when an address has been assigned.
	// +optional
	IPv4 string `json:"ipv4,omitempty" protobuf:"bytes,4,opt,name=ipv4"`

	// MACAddress is non-empty while there is a corresponding Linux
	// network interface on the host.
	// +optional
	MACAddress string `json:"macAddress,omitempty" protobuf:"bytes,5,opt,name=macAddress"`

	// IfcName is the name of the network interface that implements this
	// attachment on its node, or the empty string to indicate no
	// implementation.
	// +optional
	IfcName string `json:"ifcName,omitempty" protobuf:"bytes,6,opt,name=ifcname"`
	// HostIP is the IP address of the node the attachment is bound to.
	// +optional
	HostIP string `json:"hostIP,omitempty" protobuf:"bytes,7,opt,name=hostIP"`

	// PostCreateExecReport, if non-nil, reports on the run of the
	// PostCreateExec.
	// +optional
	PostCreateExecReport *ExecReport `json:"postCreateExecReport,omitempty" protobuf:"bytes,8,opt,name=postCreateExecReport"`
}

type NetworkAttachmentErrors struct {
	// IPAM holds errors about the IP Address Management for this attachment.
	// +optional
	// +patchStrategy=replace
	IPAM []string `json:"ipam,omitempty" protobuf:"bytes,1,opt,name=ipam" patchStrategy:"replace"`

	// Host holds errors from the node where this attachment is placed.
	// +optional
	// +patchStrategy=replace
	Host []string `json:"host,omitempty" protobuf:"bytes,2,opt,name=host" patchStrategy:"replace"`
}

// ExecReport reports on what happened when a command was execd.
type ExecReport struct {
	// ExitStatus is the Linux exit status from the command, or a
	// negative number to signal a prior problem (detailed in StdErr).
	ExitStatus int32 `json:"exitStatus" protobuf:"bytes,1,name=exitStatus"`

	StartTime metav1.Time `json:"startTime,omitempty" protobuf:"bytes,2,name=startTime"`

	StopTime metav1.Time `json:"stopTime,omitempty" protobuf:"bytes,3,name=stopTime"`

	StdOut string `json:"stdOut" protobuf:"bytes,4,name=stdOut"`
	StdErr string `json:"stdErr" protobuf:"bytes,5,name=stdErr"`
}

// Equal tests whether the two referenced ExecReports say the same
// thing within the available time precision.  The apiservers only
// store time values with seconds precision.
func (x *ExecReport) Equiv(y *ExecReport) bool {
	if x == y {
		return true
	}
	if x == nil || y == nil {
		return false
	}
	return x.ExitStatus == y.ExitStatus &&
		x.StdOut == y.StdOut &&
		x.StdErr == y.StdErr &&
		x.StartTime.Time.Truncate(time.Second).Equal(y.StartTime.Time.Truncate(time.Second)) &&
		x.StopTime.Time.Truncate(time.Second).Equal(y.StopTime.Time.Truncate(time.Second))
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type NetworkAttachment struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec NetworkAttachmentSpec `json:"spec" protobuf:"bytes,2,name=spec"`

	// +optional
	Status NetworkAttachmentStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// SubnetSpec is the desired state of a subnet.
// For a given VNI, all the subnets having that VNI:
// - have disjoint IP ranges, and
// - are in the same Kubernetes API namespace.
type SubnetSpec struct {
	// IPv4 is the CIDR notation for the v4 address range of this subnet.
	IPv4 string `json:"ipv4" protobuf:"bytes,1,name=ipv4"`

	// VNI identifies the virtual network.
	// Valid values are in the range [1,2097151].
	VNI uint32 `json:"vni" protobuf:"bytes,2,name=vni"`
}

type SubnetStatus struct {
	// Validated tells users and consumers whether the subnet spec has passed
	// validation or not. The fields that undergo validation are VNI and CIDR.
	// As a consequence, if Validated is true it is guaranteed to stay true
	// until either one of VNI or CIDR is updated.
	// If Validated is false or unset, there are three possible reasons:
	// 	(1) Validation has not been performed yet.
	// 	(2) The subnet CIDR overlaps with the CIDR of another subnet with the
	//		same VNI.
	//	(3) The subnet Namespace is different than that of another subnet with
	// 		the same VNI.
	// +optional
	Validated bool `json:"validated,omitempty" protobuf:"bytes,1,opt,name=validated"`

	// +optional
	Errors SubnetErrors `json:"errors,omitempty" protobuf:"bytes,2,opt,name=errors"`
}

type SubnetErrors struct {
	// IPAM holds the complaints, if any, from the IPAM controller.
	// +optional
	// +patchStrategy=replace
	IPAM []string `json:"ipam,omitempty" protobuf:"bytes,1,opt,name=ipam" patchStrategy:"replace"`

	// Validation holds the complaints, if any, from the subnets validator. It
	// might contain an explanation on why SubnetStatus.Validated is false.
	// +optional
	// +patchStrategy=replace
	Validation []string `json:"validation,omitempty" protobuf:"bytes,2,opt,name=validation" patchStrategy:"replace"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Subnet struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec SubnetSpec `json:"spec" protobuf:"bytes,2,name=spec"`

	// +optional
	Status SubnetStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SubnetList is a list of Subnet objects.
type SubnetList struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []Subnet `json:"items" protobuf:"bytes,2,rep,name=items"`
}

type IPLockSpec struct {
	SubnetName string `json:"subnetName" protobuf:"bytes,1,name=subnetName"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type IPLock struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec IPLockSpec `json:"spec" protobuf:"bytes,2,name=spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// IPLockList is a list of IPLock objects.
type IPLockList struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []IPLock `json:"items" protobuf:"bytes,2,rep,name=items"`
}
