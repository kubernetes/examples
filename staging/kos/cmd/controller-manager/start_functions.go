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
	"time"

	k8scorev1api "k8s.io/api/core/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	kosclientset "k8s.io/examples/staging/kos/pkg/client/clientset/versioned"
	kosinformers "k8s.io/examples/staging/kos/pkg/client/informers/externalversions"
	"k8s.io/examples/staging/kos/pkg/controllers/ipam"
	"k8s.io/examples/staging/kos/pkg/controllers/subnet"
	_ "k8s.io/examples/staging/kos/pkg/controllers/workqueue_prometheus"
)

var managedControllers map[string]startFunction

func init() {
	managedControllers = map[string]startFunction{
		"kos-subnet-validation-controller": startSubnetValidationController,
		"kos-ipam-controller":              startIPAMController,
	}
}

type controllerContext struct {
	options         *KOSControllerManagerOptions
	sharedInformers kosinformers.SharedInformerFactory
	stop            <-chan struct{}
}

type startFunction func(ctx controllerContext, k8sClientCfg, kosClientCfg *rest.Config) error

func startIPAMController(ctx controllerContext, k8sClientCfg, kosClientCfg *rest.Config) error {
	klog.Infof("IPAM controller config: kubeconfig=%q, workers=%d, QPS=%d, burst=%d, indirect-requests=%t", ctx.options.KubeconfigFilename, ctx.options.IPAMControllerWorkers, ctx.options.QPS, ctx.options.Burst, ctx.options.IndirectRequests)

	k8sClientset, err := k8sclient.NewForConfig(k8sClientCfg)
	if err != nil {
		return fmt.Errorf("failed to configure k8s clientset for kubeconfig=%q: %s", ctx.options.KubeconfigFilename, err.Error())
	}
	eventIfc := k8sClientset.CoreV1().Events(k8scorev1api.NamespaceAll)

	kosClientset, err := kosclientset.NewForConfig(kosClientCfg)
	if err != nil {
		return fmt.Errorf("failed to configure KOS clientset for kubeconfig=%q: %s", ctx.options.KubeconfigFilename, err.Error())
	}

	kosInformers := ctx.sharedInformers.Network().V1alpha1()
	ctlr := ipam.NewController(kosClientset.NetworkV1alpha1(),
		kosInformers.Subnets().Informer(),
		kosInformers.Subnets().Lister(),
		kosInformers.NetworkAttachments().Informer(),
		kosInformers.NetworkAttachments().Lister(),
		kosInformers.IPLocks().Informer(),
		kosInformers.IPLocks().Lister(),
		eventIfc,
		workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(200*time.Millisecond, 8*time.Hour), "kos_ipam_controller_queue"),
		ctx.options.IPAMControllerWorkers,
		ctx.options.Hostname,
	)

	// Start the controller.
	go func() {
		if err := ctlr.Run(ctx.stop); err != nil {
			panic(err.Error())
		}
	}()

	return nil
}

func startSubnetValidationController(ctx controllerContext, k8sClientCfg, kosClientCfg *rest.Config) error {
	klog.Infof("Subnet validation controller config: kubeconfig=%q, workers=%d, QPS=%d, burst=%d, indirect-requests=%t", ctx.options.KubeconfigFilename, ctx.options.SubnetValidationControllerWorkers, ctx.options.QPS, ctx.options.Burst, ctx.options.IndirectRequests)

	k8sClientset, err := k8sclient.NewForConfig(k8sClientCfg)
	if err != nil {
		return fmt.Errorf("failed to configure k8s clientset for kubeconfig=%q: %s", ctx.options.KubeconfigFilename, err.Error())
	}
	eventIfc := k8sClientset.CoreV1().Events(k8scorev1api.NamespaceAll)

	kosClientset, err := kosclientset.NewForConfig(kosClientCfg)
	if err != nil {
		return fmt.Errorf("failed to configure KOS clientset for kubeconfig=%q: %s", ctx.options.KubeconfigFilename, err.Error())
	}

	subnets := ctx.sharedInformers.Network().V1alpha1().Subnets()
	ctlr := subnet.NewValidationController(kosClientset.NetworkV1alpha1(),
		subnets.Informer(),
		subnets.Lister(),
		eventIfc,
		workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(200*time.Millisecond, 8*time.Hour), "kos_subnet_validator_queue"),
		ctx.options.SubnetValidationControllerWorkers,
		ctx.options.Hostname,
	)

	// Start the controller.
	go func() {
		if err := ctlr.Run(ctx.stop); err != nil {
			panic(err.Error())
		}
	}()

	return nil
}
