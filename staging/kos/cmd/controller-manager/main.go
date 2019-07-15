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
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/component-base/logs"
	"k8s.io/klog"

	kosclientset "k8s.io/examples/staging/kos/pkg/client/clientset/versioned"
	kosinformers "k8s.io/examples/staging/kos/pkg/client/informers/externalversions"
)

func main() {
	ctlrOpts := &KOSControllerManagerOptions{}
	ctlrOpts.AddFlags()
	flag.Set("logtostderr", "true")
	logs.InitLogs()
	defer logs.FlushLogs()
	flag.Parse()

	if len(os.Getenv("GOMAXPROCS")) == 0 {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}
	rand.Seed(time.Now().UnixNano())
	rand.Uint64()
	rand.Uint64()
	rand.Uint64()

	k8sClientCfg, kosClientCfg, err := buildClientConfigs(ctlrOpts)
	if err != nil {
		klog.Errorf("Failed to build client configs: %s.", err.Error())
		os.Exit(2)
	}

	kosInformersClientset, err := kosclientset.NewForConfig(addUserAgent(kosClientCfg, "kos-controller-manager"))
	if err != nil {
		klog.Errorf("Failed to configure kos clientset for informers: %s.", err.Error())
		os.Exit(3)
	}

	ctx := controllerContext{
		options:         ctlrOpts,
		sharedInformers: kosinformers.NewSharedInformerFactory(kosInformersClientset, 0),
		stop:            stopOnSignals(),
	}
	for controller, startController := range managedControllers {
		k8sCC := addUserAgent(k8sClientCfg, controller)
		kosCC := addUserAgent(kosClientCfg, controller)
		if err := startController(ctx, k8sCC, kosCC); err != nil {
			klog.Errorf("Failed to start %s: %s", controller, err.Error())
			os.Exit(4)
		}
	}
	klog.Info("All controllers started.")

	ctx.sharedInformers.Start(ctx.stop)
	klog.V(2).Info("Informers started.")

	<-ctx.stop
}

func addUserAgent(cfg *rest.Config, agent string) *rest.Config {
	newCfg := *cfg
	rest.AddUserAgent(&newCfg, agent)
	return &newCfg
}

func buildClientConfigs(opts *KOSControllerManagerOptions) (k8sCfg, kosCfg *rest.Config, err error) {
	k8sCfg, err = clientcmd.BuildConfigFromFlags("", opts.KubeconfigFilename)
	if err != nil {
		err = fmt.Errorf("Failed to build client config for kubeconfig=%q: %s", opts.KubeconfigFilename, err.Error())
		return
	}
	k8sCfg.QPS = float32(opts.QPS)
	k8sCfg.Burst = opts.Burst
	kosCfgStruct := *k8sCfg
	kosCfg = &kosCfgStruct
	// TODO: Give our API servers verifiable identities.
	kosCfg.TLSClientConfig = rest.TLSClientConfig{Insecure: true}
	if !opts.IndirectRequests {
		kosCfg.Host = "network-api:443"
	}
	return
}

// stopOnSignals makes a "stop channel" that is closed upon receipt of certain
// OS signals commonly used to request termination of a process.  On the second
// such signal, Exit(1) immediately.
func stopOnSignals() <-chan struct{} {
	stopCh := make(chan struct{})
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		close(stopCh)
		<-c
		os.Exit(1)
	}()
	return stopCh
}
