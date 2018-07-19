# Kubernetes Examples

This directory contains a number of examples of how to run real applications
with Kubernetes.

Refer to the [Kubernetes documentation] for how to execute the tutorials.

### Maintained Examples

Maintained Examples are expected to be updated with every Kubernetes release, to
use the latest and greatest features, current guidelines and best practices,
and to refresh command syntax, output, changed prerequisites, as needed.

|Name | Description | Notable Features Used | Complexity Level|
------------- | ------------- | ------------ | ------------ | 
|[Guestbook](guestbook/) | PHP app with Redis | Deployment, Service | Beginner |
|[WordPress](mysql-wordpress-pd/) | WordPress with MySQL | Deployment, Persistent Volume with Claim | Beginner|
|[Cassandra](cassandra/) | Cloud Native Cassandra | Daemon Set, Stateful Set, Replication Controller | Intermediate

> Note: Please add examples that are maintained to the list above.

See [Example Guidelines](guidelines.md) for a description of what goes
in this directory, and what examples should contain.

[Kubernetes documentation]: https://kubernetes.io/docs/tutorials/

### Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md) for instructions on how to contribute.
