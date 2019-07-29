apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: connection-agent
  namespace: example-com
spec:
  selector:
    matchLabels:
      app: connection-agent
  template:
    metadata:
      labels:
        app: connection-agent
      annotations:
        prometheus.io/sample: "true"
        prometheus.io/port: "9294"
    spec:
      serviceAccountName: connection-agent
      nodeSelector:
        role.kos.example.com/workload: "true"
      hostNetwork: true
      dnsPolicy: "ClusterFirstWithHostNet"
      containers:
      - name: connection-agent
        image: DOCKER_PREFIX/kos-connection-agent:latest
        imagePullPolicy: Always
        volumeMounts:
        - name: ovs-socks-dir
          mountPath: /var/run/openvswitch
        - name: netns-dir
          mountPath: /var/run/netns
          mountPropagation: Bidirectional
        - name: network-api-ca
          mountPath: /network-api
          readOnly: true
        securityContext:
          privileged: true
        env:
        - name: NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: HOST_IP
          valueFrom:
            fieldRef:
              fieldPath: status.hostIP
        command:
        - /connection-agent
        - -v=5
        - -nodename=$(NODE_NAME)
        - -hostip=$(HOST_IP)
        - -allowed-programs=/usr/local/kos/bin/TestByPing,/usr/local/kos/bin/RemoveNetNS
        - -network-api-ca=/network-api/ca.crt
        - -netfabric=ovs
      volumes:
      - name: ovs-socks-dir
        hostPath:
          path: /var/run/openvswitch
          type: Directory
      - name: netns-dir
        hostPath:
          path: /var/run/netns
          type: DirectoryOrCreate
      - name: network-api-ca
        secret:
          secretName: network-api-ca
