apiVersion: v1
kind: Secret
metadata:
  name: etcd-server
  namespace: example-com
type: Opaque
data:
  server.crt: SERVER_CRT
  server.key: SERVER_KEY
  server-ca.crt: CA_CRT
