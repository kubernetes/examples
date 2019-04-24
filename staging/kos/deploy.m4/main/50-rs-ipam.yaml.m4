apiVersion: apps/v1
kind: Deployment
metadata:
  name: ipam-controller
  namespace: example-com
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ipam-controller
  template:
    metadata:
      labels:
        app: ipam-controller
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9295"
    spec:
      serviceAccountName: ipam-controller
      nodeSelector:
        role.kos.example.com/control: "true"
      containers:
      - name: ipam-controller
        image: DOCKER_PREFIX/kos-ipam-controller:latest
        imagePullPolicy: Always
        command:
        - /ipam-controller
        - -v=5
        - --qps=200
