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

package main

import "flag"

const (
	defaultQPS              = 100
	defaultBurst            = 200
	defaultIndirectRequests = false
	defaultWorkers          = 2
)

// KOSControllerManagerOptions stores the options for all the managed
// KOS controllers.
type KOSControllerManagerOptions struct {
	KubeconfigFilename string
	QPS                int
	Burst              int
	IndirectRequests   bool

	IPAMControllerWorkers             int
	SubnetValidationControllerWorkers int
}

// AddFlags binds o's fields to the corresponding CLI flags for the managed
// controllers.
func (o *KOSControllerManagerOptions) AddFlags() {
	flag.StringVar(&o.KubeconfigFilename, "kubeconfig", "", "kubeconfig filename")
	flag.IntVar(&o.QPS, "qps", defaultQPS, "QPS to use while talking to the api-servers")
	flag.IntVar(&o.Burst, "burst", defaultBurst, "Burst to use while talking to the api-servers")
	flag.BoolVar(&o.IndirectRequests, "indirect-requests", defaultIndirectRequests, "requests go through main api-servers instead of directly to network api-servers")
	flag.IntVar(&o.IPAMControllerWorkers, "ipam-workers", defaultWorkers, "number of worker threads for the IPAM controller")
	flag.IntVar(&o.SubnetValidationControllerWorkers, "subnet-validator-workers", defaultWorkers, "number of worker threads for the subnet validator")
}
