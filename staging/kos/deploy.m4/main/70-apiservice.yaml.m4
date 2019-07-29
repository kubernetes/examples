apiVersion: apiregistration.k8s.io/v1
kind: APIService
metadata:
  name: v1alpha1.network.example.com
spec:
  group: network.example.com
  groupPriorityMinimum: 1000
  versionPriority: 15
  caBundle: CA_CRT
  service:
    name: network-api
    namespace: example-com
  version: v1alpha1
