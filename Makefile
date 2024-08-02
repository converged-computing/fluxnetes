# Local Directory for upstreams
UPSTREAMS ?= ./upstreams

# Local repository directories
UPSTREAM_K8S ?= $(UPSTREAMS)/kubernetes
UPSTREAM_COMMIT ?= "20b216738a5e9671ddf4081ed97b5565e0b1ee01"

# Remote repositories
UPSTREAM_K8S_REPO ?= https://github.com/kubernetes/kubernetes

BASH ?= /bin/bash
DOCKER ?= docker
TAG ?= latest
ARCH ?= amd64

# These are passed to build the sidecar
REGISTRY ?= ghcr.io/converged-computing
SIDECAR_IMAGE ?= fluxnetes-sidecar:latest
POSTGRES_IMAGE ?= fluxnetes-postgres:latest
SCHEDULER_IMAGE ?= fluxnetes

.PHONY: all build build-sidecar clone update push push-sidecar push-fluxnetes build-postgres

all: prepare build-sidecar build build-postgres

upstreams: 
	mkdir -p $(UPSTREAMS)

clone-k8s: upstreams
	if [ -d "$(UPSTREAM_K8S)" ]; then echo "Kubernetes upstream is cloned"; else ./hack/clone-k8s.sh $(UPSTREAM_K8S_REPO) $(UPSTREAM_K8S) $(UPSTREAM_COMMIT); fi

prepare: clone clone-k8s
	# Add fluxnetes as a new in-tree plugin
	rm -rf $(UPSTREAM_K8S)/pkg/scheduler/framework/plugins/fluxnetes

	cp kubernetes/cmd/kube-scheduler/scheduler.go $(UPSTREAM_K8S)/cmd/kube-scheduler/scheduler.go
	cp kubernetes/pkg/scheduler/*.go $(UPSTREAM_K8S)/pkg/scheduler/
	cp -R kubernetes/pkg/fluxnetes $(UPSTREAM_K8S)/pkg/scheduler/framework/plugins/fluxnetes
	cp -R src/fluxnetes/pkg/fluxion-grpc $(UPSTREAM_K8S)/pkg/scheduler/framework/plugins/fluxnetes/fluxion-grpc

build: prepare
	docker build -t ${REGISTRY}/${SCHEDULER_IMAGE} --build-arg ARCH=$(ARCH) --build-arg VERSION=$(VERSION) --build-arg k8s_upstream=$(UPSTREAM_K8S) .

push-sidecar:
	$(DOCKER) push $(REGISTRY)/$(SIDECAR_IMAGE):$(TAG) --all-tags

push-fluxnetes:
	$(DOCKER) push $(REGISTRY)/$(IMAGE):$(TAG) --all-tags

build-sidecar: 
	make -C ./src LOCAL_REGISTRY=${REGISTRY} LOCAL_IMAGE=${SIDECAR_IMAGE}

build-postgres: 
	docker build -f src/build/postgres/Dockerfile -t ${REGISTRY}/${POSTGRES_IMAGE} .

push: push-sidecar push-fluxnetes
