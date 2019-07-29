apiVersion: v1
kind: Secret
metadata:
  name: network-api-ca
  namespace: example-com
type: Opaque
data:
  ca.crt: CA_CRT
