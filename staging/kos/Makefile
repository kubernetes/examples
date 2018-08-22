DOCKER_PREFIX=${LOGNAME}
KOS_PERSIST=${HOME}/.kos

publish:: publish-network-apiserver
publish:: publish-ipam-controller
publish:: publish-connection-agent

build:: build-network-apiserver
build:: build-ipam-controller
build:: build-connection-agent
build:: build-attachment-tput-driver

clean:
	rm -f images/*/*r
	rm -f images/*/*/*[rt]
	rm -rf local-binaries
	rm -f deploy/main/50-*
	rm -rf "${KOS_PERSIST}/etcd-tls"

build-network-apiserver:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o images/network-apiserver/network-apiserver k8s.io/examples/staging/kos/cmd/network-apiserver

build-ipam-controller:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o images/controllers/ipam/ipam-controller k8s.io/examples/staging/kos/cmd/controllers/ipam

build-connection-agent:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o images/controllers/connectionagent/connection-agent k8s.io/examples/staging/kos/cmd/controllers/connectionagent

build-attachment-tput-driver:
	go build -a -o local-binaries/attachment-tput-driver k8s.io/examples/staging/kos/cmd/attachment-tput-driver

publish-network-apiserver:
	cd images/network-apiserver && docker build -t ${DOCKER_PREFIX}/kos-network-apiserver:latest . && docker push ${DOCKER_PREFIX}/kos-network-apiserver:latest

publish-ipam-controller:
	cd images/controllers/ipam && docker build -t ${DOCKER_PREFIX}/kos-ipam-controller:latest . && docker push ${DOCKER_PREFIX}/kos-ipam-controller:latest

publish-connection-agent:
	cp -R cmd/attachment-tput-driver/test-scripts images/controllers/connectionagent
	cd images/controllers/connectionagent && docker build -t ${DOCKER_PREFIX}/kos-connection-agent:latest . && docker push ${DOCKER_PREFIX}/kos-connection-agent:latest

deploy/main/50-rs-xs.yaml: deploy.m4/main/50-rs-xs.yaml.m4
	m4 -DDOCKER_PREFIX=${DOCKER_PREFIX} deploy.m4/main/50-rs-xs.yaml.m4 > deploy/main/50-rs-xs.yaml

deploy/main/50-rs-ipam.yaml: deploy.m4/main/50-rs-ipam.yaml.m4
	m4 -DDOCKER_PREFIX=${DOCKER_PREFIX} deploy.m4/main/50-rs-ipam.yaml.m4 > deploy/main/50-rs-ipam.yaml

deploy/main/50-ds-ca.yaml: deploy.m4/main/50-ds-ca.yaml.m4
	m4 -DDOCKER_PREFIX=${DOCKER_PREFIX} deploy.m4/main/50-ds-ca.yaml.m4 > deploy/main/50-ds-ca.yaml

%.key:
	mkdir -p $$(dirname $@)
	openssl genrsa -out $@ 4096

${KOS_PERSIST}/etcd-tls/ca.pem: ${KOS_PERSIST}/etcd-tls/ca.key
	cd "${KOS_PERSIST}/etcd-tls" && \
	echo 01 > ca.srl && \
	openssl req -x509 -new -nodes -key ca.key -days 10000 \
		-out ca.pem -subj "/CN=kos-etcd-ca"

${KOS_PERSIST}/etcd-tls/peer.crt: ${KOS_PERSIST}/etcd-tls/ca.key ${KOS_PERSIST}/etcd-tls/ca.pem ${KOS_PERSIST}/etcd-tls/peer.key etcd-tls/cnf/peer.cnf
	openssl req -new -key "${KOS_PERSIST}/etcd-tls/peer.key" \
		-out "${KOS_PERSIST}/etcd-tls/peer.csr" -subj "/CN=kos-etcd-peer" \
		-config etcd-tls/cnf/peer.cnf && \
	openssl x509 -req -in "${KOS_PERSIST}/etcd-tls/peer.csr" \
		-CA "${KOS_PERSIST}/etcd-tls/ca.pem" \
		-CAserial "${KOS_PERSIST}/etcd-tls/ca.srl" \
		-CAkey "${KOS_PERSIST}/etcd-tls/ca.key" \
		-out "${KOS_PERSIST}/etcd-tls/peer.crt" \
		-days 1500 -extensions v3_req \
		-extfile etcd-tls/cnf/peer.cnf && \
	rm "${KOS_PERSIST}/etcd-tls/peer.csr"

${KOS_PERSIST}/etcd-tls/server.crt: ${KOS_PERSIST}/etcd-tls/ca.key ${KOS_PERSIST}/etcd-tls/ca.pem ${KOS_PERSIST}/etcd-tls/server.key etcd-tls/cnf/server.cnf
	openssl req -new -key "${KOS_PERSIST}/etcd-tls/server.key" \
		-out "${KOS_PERSIST}/etcd-tls/server.csr" \
		-subj "/CN=kos-etcd-server" \
		-config etcd-tls/cnf/server.cnf && \
	openssl x509 -req -in "${KOS_PERSIST}/etcd-tls/server.csr" \
		-CA "${KOS_PERSIST}/etcd-tls/ca.pem" \
		-CAserial "${KOS_PERSIST}/etcd-tls/ca.srl" \
		-CAkey "${KOS_PERSIST}/etcd-tls/ca.key" \
		-out "${KOS_PERSIST}/etcd-tls/server.crt" \
		-days 1500 -extensions v3_req \
		-extfile etcd-tls/cnf/server.cnf && \
	rm "${KOS_PERSIST}/etcd-tls/server.csr"

${KOS_PERSIST}/etcd-tls/client.crt: ${KOS_PERSIST}/etcd-tls/ca.key ${KOS_PERSIST}/etcd-tls/ca.pem ${KOS_PERSIST}/etcd-tls/client.key etcd-tls/cnf/client.cnf
	openssl req -new -key "${KOS_PERSIST}/etcd-tls/client.key" \
		-out "${KOS_PERSIST}/etcd-tls/client.csr" \
		-subj "/CN=kos-etcd-client" \
		-config etcd-tls/cnf/client.cnf && \
	openssl x509 -req -in "${KOS_PERSIST}/etcd-tls/client.csr" \
		-CA "${KOS_PERSIST}/etcd-tls/ca.pem" \
		-CAserial "${KOS_PERSIST}/etcd-tls/ca.srl" \
		-CAkey "${KOS_PERSIST}/etcd-tls/ca.key" \
		-out "${KOS_PERSIST}/etcd-tls/client.crt" \
		-days 1500 -extensions v3_req \
		-extfile etcd-tls/cnf/client.cnf && \
	rm "${KOS_PERSIST}/etcd-tls/client.csr"

${KOS_PERSIST}/etcd-tls/peer-secret.yaml:  ${KOS_PERSIST}/etcd-tls/ca.pem ${KOS_PERSIST}/etcd-tls/peer.key ${KOS_PERSIST}/etcd-tls/peer.crt etcd-tls/peer-secret.yaml.m4
	m4	-DPEER_CRT=$$(base64 -i ${KOS_PERSIST}/etcd-tls/peer.crt) \
		-DPEER_KEY=$$(base64 -i ${KOS_PERSIST}/etcd-tls/peer.key) \
		-DCA_CRT=$$(base64 -i ${KOS_PERSIST}/etcd-tls/ca.pem) \
		etcd-tls/peer-secret.yaml.m4 > ${KOS_PERSIST}/etcd-tls/peer-secret.yaml

${KOS_PERSIST}/etcd-tls/server-secret.yaml:  ${KOS_PERSIST}/etcd-tls/ca.pem ${KOS_PERSIST}/etcd-tls/server.key ${KOS_PERSIST}/etcd-tls/server.crt etcd-tls/server-secret.yaml.m4
	m4	-DSERVER_CRT=$$(base64 -i ${KOS_PERSIST}/etcd-tls/server.crt) \
		-DSERVER_KEY=$$(base64 -i ${KOS_PERSIST}/etcd-tls/server.key) \
		-DCA_CRT=$$(base64 -i ${KOS_PERSIST}/etcd-tls/ca.pem) \
		etcd-tls/server-secret.yaml.m4 > ${KOS_PERSIST}/etcd-tls/server-secret.yaml

${KOS_PERSIST}/etcd-tls/client-secret.yaml:  ${KOS_PERSIST}/etcd-tls/ca.pem ${KOS_PERSIST}/etcd-tls/client.key ${KOS_PERSIST}/etcd-tls/client.crt etcd-tls/client-secret.yaml.m4
	m4	-DCLIENT_CRT=$$(base64 -i ${KOS_PERSIST}/etcd-tls/client.crt) \
		-DCLIENT_KEY=$$(base64 -i ${KOS_PERSIST}/etcd-tls/client.key) \
		-DCA_CRT=$$(base64 -i ${KOS_PERSIST}/etcd-tls/ca.pem) \
		etcd-tls/client-secret.yaml.m4 > ${KOS_PERSIST}/etcd-tls/client-secret.yaml

.PHONY: deploy
deploy: deploy/main/50-rs-xs.yaml deploy/main/50-rs-ipam.yaml deploy/main/50-ds-ca.yaml ${KOS_PERSIST}/etcd-tls/peer-secret.yaml ${KOS_PERSIST}/etcd-tls/server-secret.yaml ${KOS_PERSIST}/etcd-tls/client-secret.yaml
	kubectl apply -f deploy/ns && \
	kubectl apply -f deploy/etcd-operator-rbac && \
	kubectl apply -f ${KOS_PERSIST}/etcd-tls/peer-secret.yaml && \
	kubectl apply -f ${KOS_PERSIST}/etcd-tls/server-secret.yaml && \
	kubectl apply -f ${KOS_PERSIST}/etcd-tls/client-secret.yaml && \
	kubectl apply -f deploy/etcd-operator && \
	while ! kubectl get EtcdCluster ; do sleep 5 ; done && \
	kubectl apply -f deploy/etcd-cluster && \
	kubectl apply -f deploy/main

.PHONY: undeploy
undeploy: deploy/main/50-rs-xs.yaml deploy/main/50-rs-ipam.yaml deploy/main/50-ds-ca.yaml ${KOS_PERSIST}/etcd-tls/peer-secret.yaml ${KOS_PERSIST}/etcd-tls/server-secret.yaml ${KOS_PERSIST}/etcd-tls/client-secret.yaml
	kubectl delete --ignore-not-found -f deploy/main && \
	! kubectl get EtcdCluster || kubectl delete --ignore-not-found -f deploy/etcd-cluster && \
	while kubectl get EtcdCluster -n example-com the-etcd-cluster ; do sleep 15; done && \
	kubectl delete --ignore-not-found -f deploy/etcd-operator && \
	kubectl delete --ignore-not-found Endpoints etcd-operator && \
	kubectl delete --ignore-not-found crd etcdclusters.etcd.database.coreos.com && \
	kubectl delete --ignore-not-found -f ${KOS_PERSIST}/etcd-tls/peer-secret.yaml && \
	kubectl delete --ignore-not-found -f ${KOS_PERSIST}/etcd-tls/server-secret.yaml && \
	kubectl delete --ignore-not-found -f ${KOS_PERSIST}/etcd-tls/client-secret.yaml && \
	kubectl delete --ignore-not-found -f deploy/etcd-operator-rbac && \
	kubectl delete --ignore-not-found -f deploy/ns

# The following just document how these files were originally made.
# FYI, this what when master pointed to commit aeb3e3e0835ec5135cfe50340f59853b5b6fc407

deploy/etcd-operator-rbac/47-eo-role.yaml:
	curl https://raw.githubusercontent.com/coreos/etcd-operator/master/example/rbac/cluster-role-template.yaml | sed -e "s/<ROLE_NAME>/kos-etcd-operator/g" > deploy/etcd-operator-rbac/47-eo-role.yaml

deploy/etcd-operator-rbac/47-eo-rolebind.yaml:
	curl https://raw.githubusercontent.com/coreos/etcd-operator/master/example/rbac/cluster-role-binding-template.yaml | sed -e "s/<ROLE_NAME>/kos-etcd-operator/g" -e "s/<ROLE_BINDING_NAME>/kos-etcd-operator/g" -e "s/<NAMESPACE>/example-com/g" > deploy/etcd-operator-rbac/47-eo-rolebind.yaml

