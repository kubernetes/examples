#!/usr/bin/env bash

# Copyright 2014 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

function checkmaster() {
  while true; do
    master=$(redis-cli -h ${REDIS_SENTINEL_SERVICE_HOST} -p ${REDIS_SENTINEL_SERVICE_PORT} --csv SENTINEL get-master-addr-by-name mymaster | tr ',' ' ' | cut -d' ' -f1)
    if [[ -n ${master} ]]; then
      master="${master//\"}"
    else
      if [[ "${1:-slave}" == "slave" ]]; then
        echo "Failed to find master."
        sleep 60
        exit 1
      else
        master=$(hostname -i)
      fi
    fi

    redis-cli -h ${master} INFO
    if [[ "$?" == "0" ]]; then
      break
    fi
    echo "Connecting to master failed.  Waiting..."
    sleep 10
  done
}

function launchsentinel() {
  checkmaster "sentinel"

  sentinel_conf=sentinel.conf

  echo "sentinel monitor mymaster ${master} 6379 2" > ${sentinel_conf}
  echo "sentinel down-after-milliseconds mymaster 60000" >> ${sentinel_conf}
  echo "sentinel failover-timeout mymaster 180000" >> ${sentinel_conf}
  echo "sentinel parallel-syncs mymaster 1" >> ${sentinel_conf}
  echo "bind 0.0.0.0" >> ${sentinel_conf}

  redis-sentinel ${sentinel_conf} --protected-mode no
}

function launchmaster() {
  # After unexpected restarting of the master pod, one of slaves will be a new master and this pod will be one of slaves.
  master=$(redis-cli -h ${REDIS_SENTINEL_SERVICE_HOST} -p ${REDIS_SENTINEL_SERVICE_PORT} --csv SENTINEL get-master-addr-by-name mymaster | tr ',' ' ' | cut -d' ' -f1)
  if [[ -n ${master} ]]; then
    master="${master//\"}"
    sed -i "s/%master-ip%/${master}/" /redis.conf
    sed -i "s/%master-port%/6379/" /redis.conf
  else
    sed -e "/slaveof/ s/^/#/" -i /redis.conf
  fi
  redis-server /redis.conf --protected-mode no
}

function launchslave() {
  checkmaster "slave"

  sed -i "s/%master-ip%/${master}/" /redis.conf
  sed -i "s/%master-port%/6379/" /redis.conf
  redis-server /redis.conf --protected-mode no
}

# AOF and SAVE features can have performace issues with non-persistent storage.
REDIS_DATA="${REDIS_DATA:-/redis-master-data}"
if [[ ! -e "${REDIS_DATA}" ]];then
  echo "${REDIS_DATA} doesn't exist, data won't be persistent!"
  mkdir -p "${REDIS_DATA}"

  ENABLE_AOF=${ENABLE_AOF:-no}
  ENABLE_SAVE=${ENABLE_SAVE:-no}
else
  echo "${REDIS_DATA} seems to be a persistent storage."

  ENABLE_AOF=${ENABLE_AOF:-yes}
  ENABLE_SAVE=${ENABLE_SAVE:-yes}
fi

sed -i "s#%redis-data%#${REDIS_DATA}#" /redis.conf

# https://redis.io/topics/persistence
[[ "${ENABLE_AOF}" == "no" ]] && sed -e '/appendonly/ s/^#*/#/' -i /redis.conf || echo "AOF is enabled."
[[ "${ENABLE_SAVE}" == "no" ]] && sed -e '/save/ s/^#*/#/' -i /redis.conf || echo "SAVE is enabled."

if [[ "${SENTINEL}" == "true" ]]; then
  launchsentinel
  exit 0
fi

# If hostname is redis-0, the pod can be the master of statefulset.
if [[ "${MASTER}" == "true" ]] || [[ "$(hostname)" == "redis-0" ]]; then
  launchmaster
  exit 0
fi

launchslave
