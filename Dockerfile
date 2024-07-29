FROM ubuntu:latest AS base
ARG k8s_upstream="./upstreams/kubernetes"
ARG RELEASE_VERSION="v0.0.0"
ARG ARCH=amd64
ENV K8S_UPSTREAM=${k8s_upstream}
ENV RELEASE_VERSION=RELEASE_VERSION
ENV ARCH=${ARCH}

# This originally was under sig-scheduler-plugins/build/scheduler
# but since we are adding custom kube-scheduler, and we don't need the controller
# I moved the build logic up here instead of using hack/build-images.sh

RUN apt-get update && apt-get install -y wget git vim build-essential iputils-ping postgresql-client curl

# Install Go
ENV GO_VERSION=1.22.5
RUN wget https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz  && tar -xvf go${GO_VERSION}.linux-amd64.tar.gz && \
         mv go /usr/local && rm go${GO_VERSION}.linux-amd64.tar.gz

# This previously built kube-scheduler from the sig
ENV PATH=$PATH:/usr/local/go/bin

WORKDIR /go/src/k8s.io/kubernetes
COPY ${K8S_UPSTREAM} .

# We are adding coscheduling modules here instead of customizing
# Hopefully Kubernetes core will eventually support a group abstraction
RUN go get github.com/patrickmn/go-cache && \
    go get sigs.k8s.io/controller-runtime/pkg/client && \
    go get sigs.k8s.io/scheduler-plugins/apis/scheduling/v1alpha1 && \
    go get github.com/riverqueue/river && \
    go get github.com/riverqueue/river/riverdriver/riverpgxv5 && \
    go work vendor && \
    make WHAT=cmd/kube-scheduler && \
    cp /go/src/k8s.io/kubernetes/_output/local/go/bin/kube-scheduler /bin/kube-scheduler

# Commented out - was caching. We can uncomment when there is a more solid build
# https://kubernetes.io/docs/tasks/extend-kubernetes/configure-multiple-schedulers/
# FROM busybox
# COPY --from=base /go/src/k8s.io/kubernetes/_output/local/go/bin/kube-scheduler /bin/kube-scheduler
WORKDIR /bin
CMD ["kube-scheduler"]
