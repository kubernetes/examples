apiVersion: apps/v1
kind: Deployment
metadata:
  name: kos-controller-manager
  namespace: example-com
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kos-controller-manager
  template:
    metadata:
      labels:
        app: kos-controller-manager
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9295"
    spec:
      serviceAccountName: kos-controller-manager
      nodeSelector:
        role.kos.example.com/control: "true"
      containers:
      - name: kos-controller-manager
        image: DOCKER_PREFIX/kos-controller-manager:latest
        imagePullPolicy: Always
        command:
        - /controller-manager
        - -v=5
        - --ipam-qps=200
