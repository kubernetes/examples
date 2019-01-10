## PSP RBAC Example

This example demonstrates the usage of *PodSecurityPolicy* to control access to privileged containers
based on role and groups.

### Prerequisites

The server must be started to enable the appropriate APIs and flags

1.  allow privileged containers
1.  allow security contexts
1.  enable RBAC
1.  enable PodSecurityPolicies
1.  use the PodSecurityPolicy admission controller

If you are using the `local-up-cluster.sh` script you may enable these settings with the following syntax

```
PSP_ADMISSION=true ALLOW_PRIVILEGED=true ALLOW_SECURITY_CONTEXT=true hack/local-up-cluster.sh
```

The `kubectl` commands in this document assume that the current directory is the root directory of the cloned repository:

```console
$ git clone https://github.com/kubernetes/examples
$ cd examples
```

### Using the protected port

It is important to note that this example uses the following syntax to test with RBAC

1.  `--server=https://127.0.0.1:6443`: when performing requests this ensures that the protected port is used so
that RBAC will be enforced
1.  `--token=<token>`: this allows to make requests from a different users during testing.

## Creating the policies, roles, and bindings

NOTE: If you are using `local-up-cluster.sh` you don't need to create these
policies, roles, and bindings as they already have been created by the script,
provided the Kubernetes repository contains `examples` symlink pointing to
`staging` subdirectory of this repository checkout. For example, if you've
checked out Kubernetes as

```console
$ git clone https://github.com/kubernetes/kubernetes
```
and this examples repository next to it as
```console
$ git clone https://github.com/kubernetes/examples
```
create symlink
```console
$ ( cd kubernetes && ln -s ../examples/staging examples )
```


### Policies

The first step to enforcing cluster constraints via PSP is to create your policies.  In this
example we will use two policies, `restricted` and `privileged`. The `privileged` policy allows any type of pod.
The `restricted` policy only allows limited users, groups, volume types, and does not allow host access or privileged containers.

```yaml
apiVersion: policy/v1beta1
kind: PodSecurityPolicy
metadata:
  name: privileged
spec:
  fsGroup:
    rule: RunAsAny
  privileged: true
  runAsUser:
    rule: RunAsAny
  seLinux:
    rule: RunAsAny
  supplementalGroups:
    rule: RunAsAny
  volumes:
  - '*'
  allowedCapabilities:
  - '*'
  hostPID: true
  hostIPC: true
  hostNetwork: true
  hostPorts:
  - min: 1
    max: 65536
---
apiVersion: policy/v1beta1
kind: PodSecurityPolicy
metadata:
  name: restricted
spec:
  privileged: false
  fsGroup:
    rule: RunAsAny
  runAsUser:
    rule: MustRunAsNonRoot
  seLinux:
    rule: RunAsAny
  supplementalGroups:
    rule: RunAsAny
  volumes:
  - 'emptyDir'
  - 'secret'
  - 'downwardAPI'
  - 'configMap'
  - 'persistentVolumeClaim'
  - 'projected'
  hostPID: false
  hostIPC: false
  hostNetwork: false
```

To create these policies run

```
$ kubectl --server=https://127.0.0.1:6443 --token=foo/system:masters create -f staging/podsecuritypolicy/rbac/policies.yaml
podsecuritypolicy "privileged" created
podsecuritypolicy "restricted" created
```

### Roles and bindings

In order to create a pod, either the creating user or the service account
specified by the pod must be authorized to use a `PodSecurityPolicy` object
that allows the pod, within the pod's namespace.

That authorization is determined by the ability to perform the `use` verb
on a particular `podsecuritypolicies` resource, at the scope of the pod's namespace.
The `use` verb is a special verb that grants access to use a policy while not permitting any
other access.

Note that a user with superuser permissions within a namespace (access to `*` verbs on `*` resources)
would be allowed to use any PodSecurityPolicy within that namespace.

For this example, we'll first create RBAC `ClusterRoles` that enable access to `use` specific policies.

1. `restricted-psp-user`: this role allows the `use` verb on the `restricted` policy only
2. `privileged-psp-user`: this role allows the `use` verb on the `privileged` policy only

We can then create role bindings to grant those permissions.

* A `RoleBinding` would grant those permissions within a particular namespace
* A `ClusterRoleBinding` would grant those permissions across all namespaces

In this example, we will create `ClusterRoleBindings` to grant the roles to groups cluster-wide.

1. `privileged-psp-user`: this group is bound to the `privileged-psp-user` role and `restricted-psp-user` role which gives users
in this group access to both policies.
1. `restricted-psp-user`: this group is bound to the `restricted-psp-user` role.
1. `system:authenticated`: this is a system group for any authenticated user.  It is bound to the `edit`
role which is already provided by the cluster.

To create these roles and bindings run

```
$ kubectl --server=https://127.0.0.1:6443 --token=foo/system:masters create -f staging/podsecuritypolicy/rbac/roles.yaml
clusterrole "restricted-psp-user" created
clusterrole "privileged-psp-user" created

$ kubectl --server=https://127.0.0.1:6443 --token=foo/system:masters create -f staging/podsecuritypolicy/rbac/bindings.yaml
clusterrolebinding "privileged-psp-users" created
clusterrolebinding "restricted-psp-users" created
clusterrolebinding "edit" created
```

## Testing access

### Restricted user can create non-privileged pods

Create the pod

```
$ kubectl --server=https://127.0.0.1:6443 --token=foo/restricted-psp-users create -f staging/podsecuritypolicy/rbac/pod.yaml
pod "nginx" created
```

Check the PSP that allowed the pod

```
$ kubectl get pod nginx -o yaml | grep psp
    kubernetes.io/psp: restricted
```

### Restricted user cannot create privileged pods

Delete the existing pod

```
$ kubectl delete pod nginx
pod "nginx" deleted
```

Create the privileged pod

```
$ kubectl --server=https://127.0.0.1:6443 --token=foo/restricted-psp-users create -f staging/podsecuritypolicy/rbac/pod_priv.yaml
Error from server (Forbidden): error when creating "staging/podsecuritypolicy/rbac/pod_priv.yaml": pods "nginx" is forbidden: unable to validate against any pod security policy: [spec.containers[0].securityContext.privileged: Invalid value: true: Privileged containers are not allowed]
```

### Privileged user can create non-privileged pods

```
$ kubectl --server=https://127.0.0.1:6443 --token=foo/privileged-psp-users create -f staging/podsecuritypolicy/rbac/pod.yaml
pod "nginx" created
```

Check the PSP that allowed the pod.

```
$ kubectl get pod nginx -o yaml | egrep "psp|privileged"
    kubernetes.io/psp: privileged
```

In the versions prior 1.9 this could be the `restricted` or `privileged` PSP
since both allow for the creation of non-privileged pods. Starting from 1.9
release, the `privileged` PSP will always be used as it accepts the pod as-is
(without defaulting/mutating).

### Privileged user can create privileged pods

Delete the existing pod

```
$ kubectl delete pod nginx
pod "nginx" deleted
```

Create the privileged pod

```
$ kubectl --server=https://127.0.0.1:6443 --token=foo/privileged-psp-users create -f staging/podsecuritypolicy/rbac/pod_priv.yaml
pod "nginx" created
```

Check the PSP that allowed the pod.

```
$ kubectl get pod nginx -o yaml | egrep "psp|privileged"
    kubernetes.io/psp: privileged
      privileged: true
```

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/examples/podsecuritypolicy/rbac/README.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
