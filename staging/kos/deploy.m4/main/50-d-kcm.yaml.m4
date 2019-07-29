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
# Uncomment the following lines if --indirect-requests is set to false
        # volumeMounts:
        # - name: network-api-ca
        #   mountPath: /network-api
        #   readOnly: true
        env:
        - name: HOSTNAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        command:
        - /controller-manager
        - -v=5
        - --hostname=$(HOSTNAME)
        - --qps=100
        - --burst=200
        - --indirect-requests=true
# Uncomment the following line if --indirect-requests is set to false
#        - --network-api-ca=/network-api/ca.crt
        - --ipam-workers=2
        - --subnet-validator-workers=2
# Uncomment the following lines if --indirect-requests is set to false
      # volumes:
      # - name: network-api-ca
      #   secret:
      #     secretName: network-api-ca
