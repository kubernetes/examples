apiVersion: v1
kind: Secret
metadata:
  name: etcd-peer
  namespace: example-com
type: Opaque
data:
  peer.crt: PEER_CRT
  peer.key: PEER_KEY
  peer-ca.crt: CA_CRT
