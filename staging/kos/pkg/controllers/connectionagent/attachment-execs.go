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

package connectionagent

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"

	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	netv1a1 "k8s.io/examples/staging/kos/pkg/apis/network/v1alpha1"
	netfabric "k8s.io/examples/staging/kos/pkg/networkfabric"
	kosutil "k8s.io/examples/staging/kos/pkg/util"
)

const (
	FailProgramForbidden  = -1
	FailErrNotExit        = -2
	FailSysUnexpectedType = -3
)

// LaunchCommand normally forks a goroutine to exec the given command.
// If the given command is empty this function does nothing and
// returns nil.  If a problem is discovered during preparation then an
// ExecReport reflecting that problem is returned and there is no
// exec.  Otherwise the fork and exec are done and, if `saveReport`,
// the attachment's local state is updated with the ExecReport and the
// attachment is requeued so that the ExecReport gets stored into the
// attachment's status iff it still should be.  If `!saveReport` then
// the ExecReport is just logged (but probably should be emitted in an
// Event).
func (c *ConnectionAgent) LaunchCommand(attNSN k8stypes.NamespacedName, localIfc *netfabric.LocalNetIfc, cmd []string, what string, doit, saveReport bool) (statusErrs kosutil.SliceOfString) {
	if len(cmd) == 0 {
		return nil
	}
	if _, allowed := c.allowedPrograms[cmd[0]]; !allowed {
		glog.V(4).Infof("Non-allowed attachment command spec: att=%s, vni=%06x, ipv4=%s, ifcName=%s, mac=%s, what=%s, cmd=%#v\n", attNSN, localIfc.VNI, localIfc.GuestIP, localIfc.Name, localIfc.GuestMAC, what, cmd)
		return kosutil.SliceOfString{fmt.Sprintf("%s specifies non-allowed path %s", what, cmd[0])}
	}
	if !doit {
		return nil
	}
	glog.V(4).Infof("Will launch attachment command: att=%s, vni=%06x, ipv4=%s, ifcName=%s, mac=%s, what=%s, cmd=%#v\n", attNSN, localIfc.VNI, localIfc.GuestIP, localIfc.Name, localIfc.GuestMAC, what, cmd)
	go func() { c.RunCommand(attNSN, localIfc, cmd, what, saveReport) }()
	return nil
}

func (c *ConnectionAgent) RunCommand(attNSN k8stypes.NamespacedName, localIfc *netfabric.LocalNetIfc, urcmd []string, what string, saveReport bool) {
	expanded := make([]string, len(urcmd)-1)
	for i, argi := range urcmd[1:] {
		argi = strings.Replace(argi, "${ifname}", localIfc.Name, -1)
		argi = strings.Replace(argi, "${ipv4}", localIfc.GuestIP.String(), -1)
		argi = strings.Replace(argi, "${mac}", localIfc.GuestMAC.String(), -1)
		expanded[i] = argi
	}
	cmd := exec.Command(urcmd[0], expanded...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	startTime := time.Now()
	err := cmd.Run()
	stopTime := time.Now()
	cr := netv1a1.ExecReport{
		StartTime: k8smetav1.Time{startTime},
		StopTime:  k8smetav1.Time{stopTime},
		StdOut:    stdout.String(),
		StdErr:    stderr.String(),
	}
	if err == nil {
		cr.ExitStatus = 0
	} else {
		switch et := err.(type) {
		case *exec.ExitError:
			esys := et.Sys()
			switch esyst := esys.(type) {
			case syscall.WaitStatus:
				cr.ExitStatus = int32(esyst.ExitStatus())
			default:
				glog.Warningf("et.Sys has unexpected type: vni=%06x, att=%s, what=%s, type=%T, esys=%#+v\n", localIfc.VNI, attNSN, what, esys, esys)
				cr.ExitStatus = FailSysUnexpectedType
			}
		default:
			glog.Warningf("err is not a *exec.ExitError: vni=%06x, att=%s, what=%s, type=%T, err=%#+v\n", localIfc.VNI, attNSN, what, err, err)
			cr.ExitStatus = FailErrNotExit
		}
	}
	c.attachmentExecDurationHistograms.With(prometheus.Labels{"what": what}).Observe(stopTime.Sub(startTime).Seconds())
	c.attachmentExecStatusCounts.With(prometheus.Labels{"what": what, "exitStatus": strconv.FormatInt(int64(cr.ExitStatus), 10)}).Add(1)
	glog.V(4).Infof("Exec report: att=%s, vni=%06x, ipv4=%s, ifcName=%s, mac=%s, what=%s, report=%#+v\n", attNSN, localIfc.VNI, localIfc.GuestIP, localIfc.Name, localIfc.GuestMAC, what, cr)
	if !saveReport {
		return
	}
	c.setExecReport(attNSN, &cr)
	c.queue.Add(attNSN)
}
