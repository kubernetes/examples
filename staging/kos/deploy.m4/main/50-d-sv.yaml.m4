apiVersion: apps/v1
kind: Deployment
metadata:
  name: subnets-validator
  namespace: example-com
spec:
  replicas: 1
  selector:
    matchLabels:
      app: subnets-validator
  template:
    metadata:
      labels:
        app: subnets-validator
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9296"
    spec:
      serviceAccountName: subnets-validator
      nodeSelector:
        role.kos.example.com/control: "true"
      containers:
      - name: subnets-validator
        image: DOCKER_PREFIX/kos-subnets-validator:latest
        imagePullPolicy: Always
        command:
        - /subnets-validator
        - -v=5
