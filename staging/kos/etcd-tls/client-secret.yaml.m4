apiVersion: v1
kind: Secret
metadata:
  name: etcd-client
  namespace: example-com
type: Opaque
data:
  etcd-client.crt: CLIENT_CRT
  etcd-client.key: CLIENT_KEY
  etcd-client-ca.crt: CA_CRT
