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
	"math/rand"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/golang/glog"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/examples/staging/kos/cmd/controller-manager/options"
	kosclientset "k8s.io/examples/staging/kos/pkg/client/clientset/versioned"
	kosinformers "k8s.io/examples/staging/kos/pkg/client/informers/externalversions"
)

func main() {
	controllersOptions := &options.KOSControllerManagerOptions{
		SubnetValidationController: &options.SubnetValidationControllerOptions{},
		IPAMController:             &options.IPAMControllerOptions{},
	}
	controllersOptions.AddFlags()
	flag.Set("logtostderr", "true")
	flag.Parse()

	if len(os.Getenv("GOMAXPROCS")) == 0 {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}
	rand.Seed(time.Now().UnixNano())
	rand.Uint64()
	rand.Uint64()
	rand.Uint64()

	baseClientCfg, err := clientcmd.BuildConfigFromFlags("", controllersOptions.KubeconfigFilename)
	if err != nil {
		glog.Errorf("Failed to build client config for kubeconfig=%q: %s", controllersOptions.KubeconfigFilename, err.Error())
		os.Exit(2)
	}

	kosInformersClientCfg := *baseClientCfg
	// TODO we might want to set QPS and burst for the Informers' clientset as
	// well. If that's the case we need new flags.
	kosInformersClientCfg.TLSClientConfig = rest.TLSClientConfig{Insecure: true}
	kosInformersClientset, err := kosclientset.NewForConfig(&kosInformersClientCfg)
	if err != nil {
		glog.Errorf("Failed to configure kos clientset for informers: %s", err.Error())
		os.Exit(3)
	}

	stop := stopOnSignals()
	ctx := controllerContext{
		clientCfg:       baseClientCfg,
		options:         controllersOptions,
		sharedInformers: kosinformers.NewSharedInformerFactory(kosInformersClientset, 0),
		stop:            stop,
	}
	for controller, startController := range managedControllers {
		if err := startController(ctx); err != nil {
			glog.Errorf("Failed to start %s: %s", controller, err.Error())
			os.Exit(4)
		}
	}
	glog.Info("All controllers started.")

	ctx.sharedInformers.Start(stop)
	glog.V(2).Info("Informers started.")

	<-stop
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
