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

package parse

import (
	"github.com/golang/glog"

	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	k8scache "k8s.io/client-go/tools/cache"

	netv1a1 "k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
)

func AttNSN(att *netv1a1.NetworkAttachment) k8stypes.NamespacedName {
	return k8stypes.NamespacedName{Namespace: att.Namespace,
		Name: att.Name}
}

// Peel removes the k8scache.DeletedFinalStateUnknown wrapper,
// if any, and returns the result as a k8sruntime.Object.
func Peel(obj interface{}) k8sruntime.Object {
	switch o := obj.(type) {
	case *k8scache.DeletedFinalStateUnknown:
		return o.Obj.(k8sruntime.Object)
	case k8scache.DeletedFinalStateUnknown:
		return o.Obj.(k8sruntime.Object)
	case k8sruntime.Object:
		return o
	default:
		glog.Errorf("Peel: object of unexpected type %T: %#+v\n", obj, obj)
		panic(obj)
	}
}
