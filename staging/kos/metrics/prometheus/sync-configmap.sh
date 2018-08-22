#!/bin/bash

SCRIPTDIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
CONFIGDIR=$SCRIPTDIR/config
CONFIGMAP=$SCRIPTDIR/manifests/configmap.yaml

# Clear ConfigMap file
> $CONFIGMAP

# Write ConfigMap header
cat << EOF >> $CONFIGMAP
apiVersion: v1
kind: ConfigMap
metadata:
  name: prometheus
data:
EOF

for file in $CONFIGDIR/*.yaml; do
	echo "  $(basename $file): |" >> $CONFIGMAP
	sed 's/^/    /' $file >> $CONFIGMAP
done
