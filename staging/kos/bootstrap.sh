#!/bin/bash

# This script was used to get a working template from k8s.io/sample-apiserver.
# Changes have been made since then.
# Do not invoke this script ever again.

echo 'No!' >&2
exit 1

SRC=$GOPATH/src/k8s.io/sample-apiserver
rm -rf hack pkg
cp -r $SRC/main.go $SRC/hack $SRC/pkg .
find main.go hack pkg -type f -not -name ".DS_Store" -exec sed -e 's|k8s.io/sample-apiserver|k8s.io/examples/staging/kos|g' -e 's/wardle.k8s.io/network.example.com/g' -e 's/wardle/network/g' -e 's/Wardle/Network/g' -e 's/Flunder/NetworkAttachment/g' -e 's/flunder/networkattachment/g' -e 's/Fischer/IPLock/g' -e 's/fischer/iplock/g' -i.bak \{\} \;
for d in $(find pkg -name wardle); do mv $d ${d/wardle/network/}; done
for d in $(find pkg -name wardleinitializer); do mv $d ${d/wardle/network}; done
for d in $(find pkg -name '*wardle*' -not -name '*.bak'); do mv $d ${d/wardle/network}; done
for d in $(find pkg -name '*flunder*' -not -name '*.bak'); do mv $d ${d/flunder/networkattachment}; done
for d in $(find pkg -name '*fischer*' -not -name '*.bak'); do mv $d ${d/fischer/iplock}; done
find main* hack pkg -name '*.bak' -exec rm \{\} \;
sed -e 's|$(dirname ${BASH_SOURCE})/../../..|$(dirname ${BASH_SOURCE})/../../../../..|' -i.bak hack/update-codegen.sh
mkdir -p cmd/network-apiserver
mv -f main.go cmd/network-apiserver/main.go
