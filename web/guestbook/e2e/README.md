# Guestbook end-to-end smoke test

`test` is a self-contained smoke test that verifies the guestbook example
actually works.

It assumes a kind cluster has already been created and is called `e2e`; this can be changed with the `CLUSTER_NAME` environment variable.

It:

1. builds the images from source,
2. loads those images into the cluster,
3. deploys the guestbook manifests,
4. waits for everything to become ready, and
5. POSTs a message through the frontend and reads it back.

Because the read is served by the redis *replica*, a successful round trip
also proves redis replication is working.

## Running it locally

You should create a kind cluster first with `kind create cluster --name e2e`, then run

```console
$ web/guestbook/e2e/test-kind
```

## CI

Prow runs this test via the main `tests/e2e/e2e-kind` entry point.

## Known issue: the redis-master image

The manifests reference `registry.k8s.io/redis:e2e` for the master, but that
image is no longer pullable by modern container runtimes (it ships an ancient
Docker v1 schema manifest). The test overrides it with `redis:3.2.9` — the same
version the redis-replica image is built from — so the example runs. Updating
the manifest itself is a separate follow-up.
