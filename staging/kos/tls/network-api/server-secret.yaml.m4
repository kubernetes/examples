apiVersion: v1
kind: Secret
metadata:
  name: network-api
  namespace: example-com
type: Opaque
data:
  server-and-ca.crt: SERVER_AND_CA_CRTS
  server.key: SERVER_KEY
