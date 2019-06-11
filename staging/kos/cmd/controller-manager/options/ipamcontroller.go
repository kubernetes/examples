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

package options

import "flag"

// IPAMControllerOptions stores the parameters for the IPAM controller alone
// (as opposed to parameters that affect other controllers as well).
type IPAMControllerOptions struct {
	Workers          int
	QPS              int
	Burst            int
	IndirectRequests bool
}

// addFlags binds o's fields to the corresponding CLI flags for the IPAM
// controller.
func (o *IPAMControllerOptions) addFlags() {
	flag.IntVar(&o.Workers, "ipam-workers", 2, "number of worker threads for the IPAM controller")
	flag.IntVar(&o.QPS, "ipam-qps", 100, "limit on rate of calls from IPAM controller to api-server")
	flag.IntVar(&o.Burst, "ipam-burst", 200, "allowance for transient burst of calls from IPAM controller to api-server")
	flag.BoolVar(&o.IndirectRequests, "ipam-indirect-requests", false, "send requests from IPAM controller through the main apiserver(s) rather than directly to the network apiservers")
}
