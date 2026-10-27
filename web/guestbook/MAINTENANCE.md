## Building the Docker images

```console
$ docker build -t gcr.io/google-samples/gb-frontend:v5 php-redis

$ docker build -t gcr.io/google-samples/gb-redis-follower:v2 redis-follower
```

Building Multi-architecture docker images

```console
$ make -C php-redis

$ make -C redis-follower
```

Push:

```console
$ make -C php-redis all-push

$ make -C redis-follower all-push
```
