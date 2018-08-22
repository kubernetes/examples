apiVersion: apps/v1
kind: Deployment
metadata:
  name: network-apiserver
  namespace: example-com
spec:
  replicas: 2
  selector:
    matchLabels:
      app: network-apiserver
  template:
    metadata:
      labels:
        app: network-apiserver
    spec:
      serviceAccountName: network-apiserver
      nodeSelector:
        role.kos.example.com/control: "true"
      containers:
      - name: apiserver
        image: DOCKER_PREFIX/kos-network-apiserver:latest
        imagePullPolicy: Always
        volumeMounts:
        - name: etcd-certs
          mountPath: /etcd-certs
          readOnly: true
        command:
        - /network-apiserver
        - --etcd-servers=https://the-etcd-cluster-client:2379
        - --etcd-certfile=/etcd-certs/etcd-client.crt
        - --etcd-keyfile=/etcd-certs/etcd-client.key
        - --etcd-cafile=/etcd-certs/etcd-client-ca.crt
        - -v=5
      volumes:
      - name: etcd-certs
        secret:
          secretName: etcd-client
