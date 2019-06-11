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

import (
	"fmt"
	"os"
	"time"

	"github.com/golang/glog"

	k8scorev1api "k8s.io/api/core/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"

	"k8s.io/examples/staging/kos/cmd/controller-manager/options"
	kosclientset "k8s.io/examples/staging/kos/pkg/client/clientset/versioned"
	kosinformers "k8s.io/examples/staging/kos/pkg/client/informers/externalversions"
	"k8s.io/examples/staging/kos/pkg/controllers/ipam"
	"k8s.io/examples/staging/kos/pkg/controllers/subnet"
)

var managedControllers map[string]startFunction

func init() {
	managedControllers = map[string]startFunction{
		"subnet-validation-contoller": startSubnetValidationController,
		"ipam-controller":             startIPAMController,
	}
}

type controllerContext struct {
	clientCfg       *rest.Config
	options         *options.KOSControllerManagerOptions
	sharedInformers kosinformers.SharedInformerFactory
	stop            <-chan struct{}
}

type startFunction func(c controllerContext) error

func startIPAMController(c controllerContext) error {
	options := c.options.IPAMController
	clientCfg := *c.clientCfg
	clientCfg.QPS = float32(options.QPS)
	clientCfg.Burst = options.Burst
	clientCfg.UserAgent = "ipam-controller"

	glog.Infof("Starting IPAM controller with config: kubeconfig=%q, workers=%d, QPS=%f, burst=%d, indirect-requests=%t", c.options.KubeconfigFilename, options.Workers, clientCfg.QPS, clientCfg.Burst, options.IndirectRequests)

	//? We're giving to the events client the same QPS and burst as the other
	// client. Not necessarily bad, not necessarily good.
	k8sClientset, err := k8sclient.NewForConfig(&clientCfg)
	if err != nil {
		return fmt.Errorf("failed to configure k8s clientset for kubeconfig=%q: %s", c.options.KubeconfigFilename, err.Error())
	}
	eventIfc := k8sClientset.CoreV1().Events(k8scorev1api.NamespaceAll)

	if !options.IndirectRequests {
		clientCfg.Host = "network-api:443"
	}

	// TODO: Give our API servers verifiable identities.
	clientCfg.TLSClientConfig = rest.TLSClientConfig{Insecure: true}

	kosClientset, err := kosclientset.NewForConfig(&clientCfg)
	if err != nil {
		return fmt.Errorf("failed to configure KOS clientset for kubeconfig=%q: %s", c.options.KubeconfigFilename, err.Error())
	}

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to get local hostname: %s", err.Error())
	}

	kosInformers := c.sharedInformers.Network().V1alpha1()
	queue := workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(200*time.Millisecond, 8*time.Hour), "kos_ipam_controller_queue")
	ctlr := ipam.NewController(kosClientset.NetworkV1alpha1(),
		kosInformers.Subnets().Informer(),
		kosInformers.Subnets().Lister(),
		kosInformers.NetworkAttachments().Informer(),
		kosInformers.NetworkAttachments().Lister(),
		kosInformers.IPLocks().Informer(),
		kosInformers.IPLocks().Lister(),
		eventIfc,
		queue,
		options.Workers,
		hostname)
	go ctlr.Run(c.stop)

	return nil
}

func startSubnetValidationController(c controllerContext) error {
	options := c.options.SubnetValidationController
	clientCfg := *c.clientCfg
	clientCfg.QPS = float32(options.QPS)
	clientCfg.Burst = options.Burst
	clientCfg.UserAgent = "subnet-validation-controller"

	glog.Infof("Starting subnet validation controller with config: kubeconfig=%q, workers=%d, QPS=%f, burst=%d", c.options.KubeconfigFilename, options.Workers, clientCfg.QPS, clientCfg.Burst)

	// TODO: Give our API servers verifiable identities.
	clientCfg.TLSClientConfig = rest.TLSClientConfig{Insecure: true}

	kosClientset, err := kosclientset.NewForConfig(&clientCfg)
	if err != nil {
		return fmt.Errorf("failed to configure KOS clientset for kubeconfig=%q: %s", c.options.KubeconfigFilename, err.Error())
	}

	subnets := c.sharedInformers.Network().V1alpha1().Subnets()
	queue := workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(200*time.Millisecond, 8*time.Hour), "kos_subnet_validator_queue")
	ctlr := subnet.NewValidationController(kosClientset.NetworkV1alpha1(),
		subnets.Informer(),
		subnets.Lister(),
		queue,
		options.Workers)
	go ctlr.Run(c.stop)

	return nil
}
