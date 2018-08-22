#!/bin/bash

SCRIPTDIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
DATASRCDIR=$SCRIPTDIR/datasources
DASHPRVDIR=$SCRIPTDIR/dashboard-providers
DASHDEFDIR=$SCRIPTDIR/dashboard-defs
DATASRCCONFIGMAP=$SCRIPTDIR/manifests/configmap-datasources.yaml
DASHPRVCONFIGMAP=$SCRIPTDIR/manifests/configmap-dashboard-providers.yaml
DASHDEFCONFIGMAP=$SCRIPTDIR/manifests/configmap-dashboard-defs.yaml

# Clear datasource ConfigMap file
> $DATASRCCONFIGMAP

# Write ConfigMap header
cat << EOF >> $DATASRCCONFIGMAP
apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-datasources
data:
EOF

# Write file contents
for file in $DATASRCDIR/*; do
	echo "  $(basename $file): |" >> $DATASRCCONFIGMAP
	sed 's/^/    /' $file >> $DATASRCCONFIGMAP
done


# Clear datasource ConfigMap file
> $DASHPRVCONFIGMAP

# Write ConfigMap header
cat << EOF >> $DASHPRVCONFIGMAP
apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-dashboard-providers
data:
EOF

# Write file contents
for file in $DASHPRVDIR/*; do
	echo "  $(basename $file): |" >> $DASHPRVCONFIGMAP
	sed 's/^/    /' $file >> $DASHPRVCONFIGMAP
done


# Clear datasource ConfigMap file
> $DASHDEFCONFIGMAP

# Write ConfigMap header
cat << EOF >> $DASHDEFCONFIGMAP
apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-dashboard-defs
data:
EOF

# Write file contents
for file in $DASHDEFDIR/*; do
	echo "  $(basename $file): |" >> $DASHDEFCONFIGMAP
	sed 's/^/    /' $file >> $DASHDEFCONFIGMAP
done
