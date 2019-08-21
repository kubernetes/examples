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
	gonet "net"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	k8scorev1api "k8s.io/api/core/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	k8scorev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/component-base/logs"
	"k8s.io/klog"

	kosclientset "k8s.io/examples/staging/kos/pkg/client/clientset/versioned"
	"k8s.io/examples/staging/kos/pkg/controllers/connectionagent"
	_ "k8s.io/examples/staging/kos/pkg/controllers/workqueue_prometheus"
	netfactory "k8s.io/examples/staging/kos/pkg/networkfabric/factory"

	_ "k8s.io/examples/staging/kos/pkg/networkfabric/logger"
	_ "k8s.io/examples/staging/kos/pkg/networkfabric/ovs"
)

const (
	defaultNumWorkers  = 2
	defaultClientQPS   = 100
	defaultClientBurst = 200

	queueName = "ca"

	networkAPIHost = "network-api"
	networkAPIPort = "443"
)

func main() {
	var (
		nodeName               string
		hostIP                 string
		netFabricName          string
		caFile                 string
		allowedPrograms        string
		kubeconfigFilename     string
		workers                int
		clientQPS, clientBurst int
		blockProfileRate       int
		mutexProfileFraction   int
	)
	flag.StringVar(&nodeName, "nodename", "", "node name")
	flag.StringVar(&hostIP, "hostip", "", "host IP")
	flag.StringVar(&netFabricName, "netfabric", "", "network fabric name")
	flag.StringVar(&caFile, "network-api-ca", "", "file path to the CA's certificate for the Network API")
	flag.StringVar(&allowedPrograms, "allowed-programs", "", "comma-separated list of allowed pathnames for post-create and post-delete execs")
	flag.StringVar(&kubeconfigFilename, "kubeconfig", "", "kubeconfig filename")
	flag.IntVar(&workers, "workers", defaultNumWorkers, "number of worker threads")
	flag.IntVar(&clientQPS, "qps", defaultClientQPS, "limit on rate of calls to api-server")
	flag.IntVar(&clientBurst, "burst", defaultClientBurst, "allowance for transient burst of calls to api-server")
	flag.IntVar(&blockProfileRate, "block-profile-rate", 0, "value given to `runtime.SetBlockProfileRate()`")
	flag.IntVar(&mutexProfileFraction, "mutex-profile-fraction", 0, "value given to `runtime.SetMutexProfileFraction()`")
	flag.Set("logtostderr", "true")
	logs.InitLogs()
	defer logs.FlushLogs()
	flag.Parse()

	if len(os.Getenv("GOMAXPROCS")) == 0 {
		runtime.GOMAXPROCS(runtime.NumCPU())
	}
	if blockProfileRate > 0 {
		runtime.SetBlockProfileRate(blockProfileRate)
	}
	if mutexProfileFraction > 0 {
		runtime.SetMutexProfileFraction(mutexProfileFraction)
	}

	var err error
	if nodeName == "" {
		// fall back to default node name
		nodeName, err = os.Hostname()
		if err != nil {
			klog.Errorf("-nodename flag value was not provided and default value could not be retrieved: %s", err.Error())
			os.Exit(2)
		}
	}
	if hostIP == "" {
		klog.Errorf("-hostip flag MUST have a value")
		os.Exit(3)
	}

	netFabric, err := netfactory.NewNetFabricForName(netFabricName)
	if err != nil {
		klog.Errorf("network fabric not found: %s", err.Error())
		os.Exit(4)
	}

	clientCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigFilename)
	if err != nil {
		klog.Errorf("Failed to build client config for kubeconfig=%q: %s", kubeconfigFilename, err.Error())
		os.Exit(5)
	}
	clientCfg.QPS = float32(clientQPS)
	clientCfg.Burst = clientBurst
	clientCfg = rest.AddUserAgent(clientCfg, nodeName)

	var eventIfc k8scorev1client.EventInterface
	pause := time.Second
	for {
		k8sclientset, err := k8sclient.NewForConfig(clientCfg)
		if err == nil {
			eventIfc = k8sclientset.CoreV1().Events(k8scorev1api.NamespaceAll)
			break
		}
		klog.Errorf("Failed to create Kubernetes clientset: %s", err.Error())
		time.Sleep(pause)
		pause = 2 * pause
		if pause > time.Minute {
			pause = time.Minute
		}
	}

	clientCfg.Host = networkAPIHost + ":" + networkAPIPort
	if caFile != "" {
		clientCfg.TLSClientConfig = rest.TLSClientConfig{CAFile: caFile}
	}

	allowedProgramsSlice := strings.Split(allowedPrograms, ",")
	allowedProgramsSet := make(map[string]struct{})
	for _, ap := range allowedProgramsSlice {
		allowedProgramsSet[ap] = struct{}{}
	}

	kcs, err := kosclientset.NewForConfig(clientCfg)
	if err != nil {
		klog.Errorf("Failed to build KOS clientset for kubeconfig=%q: %s", kubeconfigFilename, err.Error())
		os.Exit(6)
	}

	// TODO think whether the rate limiter parameters make sense
	queue := workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(200*time.Millisecond, 8*time.Hour), queueName)

	ca := connectionagent.New(nodeName, gonet.ParseIP(hostIP), kcs, eventIfc, queue, workers, netFabric, allowedProgramsSet)

	klog.Infof("Connection Agent start, nodeName=%s, hostIP=%s, netFabric=%s, allowedProgramsSlice=%v, kubeconfig=%q, workers=%d, QPS=%d, burst=%d, blockProfileRate=%d, mutexProfileFraction=%d",
		nodeName,
		hostIP,
		netFabric.Name(),
		allowedProgramsSlice,
		kubeconfigFilename,
		workers,
		clientQPS,
		clientBurst,
		blockProfileRate,
		mutexProfileFraction)

	stopCh := StopOnSignals()
	err = ca.Run(stopCh)
	if err != nil {
		klog.Info(err)
	}
}

// StopOnSignals makes a "stop channel" that is closed upon receipt of certain
// OS signals commonly used to request termination of a process.  On the second
// such signal, Exit(1) immediately.
func StopOnSignals() <-chan struct{} {
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
