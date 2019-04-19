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

	"github.com/golang/glog"

	k8scorev1api "k8s.io/api/core/v1"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	k8scorev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"

	kosclientset "k8s.io/examples/staging/kos/pkg/client/clientset/versioned"
	cactlr "k8s.io/examples/staging/kos/pkg/controllers/connectionagent"
	_ "k8s.io/examples/staging/kos/pkg/controllers/workqueue_prometheus"
	netfactory "k8s.io/examples/staging/kos/pkg/networkfabric/factory"

	_ "k8s.io/examples/staging/kos/pkg/networkfabric/logger"
	_ "k8s.io/examples/staging/kos/pkg/networkfabric/ovs"
)

const (
	defaultNumWorkers  = 2
	defaultClientQPS   = 100
	defaultClientBurst = 200

	queueName = "kos_agent_queue"
)

func main() {
	var (
		nodeName               string
		hostIP                 string
		netFabricName          string
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
	flag.StringVar(&allowedPrograms, "allowed-programs", "", "comma-separated list of allowed pathnames for post-create and post-delete execs")
	flag.StringVar(&kubeconfigFilename, "kubeconfig", "", "kubeconfig filename")
	flag.IntVar(&workers, "workers", defaultNumWorkers, "number of worker threads")
	flag.IntVar(&clientQPS, "qps", defaultClientQPS, "limit on rate of calls to api-server")
	flag.IntVar(&clientBurst, "burst", defaultClientBurst, "allowance for transient burst of calls to api-server")
	flag.IntVar(&blockProfileRate, "block-profile-rate", 0, "value given to `runtime.SetBlockProfileRate()`")
	flag.IntVar(&mutexProfileFraction, "mutex-profile-fraction", 0, "value given to `runtime.SetMutexProfileFraction()`")
	flag.Set("logtostderr", "true")
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
			glog.Errorf("-nodename flag value was not provided and default value could not be retrieved: %s\n", err.Error())
			os.Exit(2)
		}
	}
	if hostIP == "" {
		glog.Errorf("-hostip flag MUST have a value\n")
		os.Exit(3)
	}

	netFabric, err := netfactory.NewNetFabricForName(netFabricName)
	if err != nil {
		glog.Errorf("network fabric not found: %s\n", err.Error())
		os.Exit(4)
	}

	clientCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigFilename)
	if err != nil {
		glog.Errorf("Failed to build client config for kubeconfig=%q: %s\n", kubeconfigFilename, err.Error())
		os.Exit(5)
	}
	clientCfg.QPS = float32(clientQPS)
	clientCfg.Burst = clientBurst
	clientCfg = rest.AddUserAgent(clientCfg, nodeName)

	var eventIfc k8scorev1client.EventInterface
	pause := time.Second
	for {
		k8sclientset, err := k8sclient.NewForConfig(clientCfg)
		var svc *k8scorev1api.Service
		if err != nil {
			glog.Errorf("Failed to create Kubernetes clientset: %s\n", err.Error())
			goto TryAgain
		}
		eventIfc = k8sclientset.CoreV1().Events(k8scorev1api.NamespaceAll)
		svc, err = k8sclientset.CoreV1().Services("example-com").Get("network-api", k8smetav1.GetOptions{})
		if err != nil {
			glog.Errorf("Failed to fetch network-api service: %s\n", err.Error())
			goto TryAgain
		}
		if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == "None" {
			glog.Errorf("The network-api service has a useless Cluster IP (%q).\n", svc.Spec.ClusterIP)
			goto TryAgain
		}
		glog.Infof("Found Cluster IP address %q for the network-api service.\n", svc.Spec.ClusterIP)
		clientCfg.Host = svc.Spec.ClusterIP + ":443"
		break
	TryAgain:
		time.Sleep(pause)
		pause = 2 * pause
		if pause > time.Minute {
			pause = time.Minute
		}
	}

	// TODO: give our apiservers verifiable identities
	clientCfg.TLSClientConfig = rest.TLSClientConfig{Insecure: true}

	allowedProgramsSlice := strings.Split(allowedPrograms, ",")
	allowedProgramsSet := make(map[string]struct{})
	for _, ap := range allowedProgramsSlice {
		allowedProgramsSet[ap] = struct{}{}
	}

	kcs, err := kosclientset.NewForConfig(clientCfg)
	if err != nil {
		glog.Errorf("Failed to build KOS clientset for kubeconfig=%q: %s\n", kubeconfigFilename, err.Error())
		os.Exit(6)
	}

	// TODO think whether the rate limiter parameters make sense
	queue := workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(200*time.Millisecond, 8*time.Hour), queueName)

	ca := cactlr.NewConnectionAgent(nodeName, gonet.ParseIP(hostIP), kcs, eventIfc, queue, workers, netFabric, allowedProgramsSet)

	glog.Infof("Connection Agent start, nodeName=%s, hostIP=%s, netFabric=%s, allowedProgramsSlice=%v, kubeconfig=%q, workers=%d, QPS=%d, burst=%d, blockProfileRate=%d, mutexProfileFraction=%d\n",
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
		glog.Info(err)
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
