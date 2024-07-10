#!/bin/bash

REGISTRY="${1:-ghcr.io/vsoch}"
HERE=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )
ROOT=$(dirname ${HERE})

# Go to the script directory
cd ${ROOT}

# These build each of the images. The sidecar is separate from the other two in src/
make REGISTRY=${REGISTRY} SCHEDULER_IMAGE=fluxnetes SIDECAR_IMAGE=fluxnetes-sidecar

# This is what it might look like to push
# docker push ghcr.io/vsoch/fluxnetes-sidecar && docker push ghcr.io/vsoch/fluxnetes:latest

# We load into kind so we don't need to push/pull and use up internet data ;)
kind load docker-image ${REGISTRY}/fluxnetes-sidecar:latest
kind load docker-image ${REGISTRY}/fluxnetes:latest

# And then install using the charts. The pull policy ensures we use the loaded ones
helm uninstall fluxnetes || true
helm install \
  --set scheduler.image=${REGISTRY}/fluxnetes:latest \
  --set scheduler.pullPolicy=Never \
  --set sidecar.pullPolicy=Never \
  --set sidecar.image=${REGISTRY}/fluxnetes-sidecar:latest \
        fluxnetes chart/
