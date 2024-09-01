#!/bin/bash

# This will test fluence with two jobs.
# We choose jobs as they generate output and complete, and pods
# are expected to keep running (and then would error)

set -eEu -o pipefail

# Keep track of root directory to return to
here=$(pwd)

REGISTRY=ghcr.io/converged-computing

# And then install using the charts. The pull policy ensures we use the loaded ones
helm install \
  --set postgres.image=${REGISTRY}/fluxnetes-postgres:latest \
  --set scheduler.image=${REGISTRY}/fluxnetes:latest \
  --set sidecar.image=${REGISTRY}/fluxnetes-sidecar:latest \
  --set postgres.pullPolicy=Never \
  --set scheduler.pullPolicy=Never \
  --set sidecar.pullPolicy=Never \
        fluxnetes chart/

# These containers should already be loaded into minikube
echo "Sleeping 1 minute waiting for scheduler deploy"
sleep 60
kubectl get pods

# This will get the fluence image (which has scheduler and sidecar), which should be first
fluxnetes_pod=$(kubectl get pods -o json | jq -r .items[0].metadata.name)
echo "Found fluxnetes pod ${fluxnetes_pod}"

# Show logs for debugging, if needed
echo
echo "⭐️ kubectl logs ${fluxnetes_pod} -c sidecar"
kubectl logs ${fluxnetes_pod} -c sidecar
echo
echo "⭐️ kubectl logs ${fluxnetes_pod} -c scheduler"
kubectl logs ${fluxnetes_pod} -c scheduler

# We now want to apply the examples
kubectl apply -f ./examples/job.yaml

# Get them based on associated job
fluxnetes_job_pod=$(kubectl get pods --selector=job-name=job -o json | jq -r .items[0].metadata.name)
fluxnetes_scheduler=$(kubectl get pods --selector=job-name=job -o json | jq -r .items[0].spec.schedulerName)

echo
echo "Fluxnetes job pod is ${fluxnetes_job_pod}"
sleep 20

# Shared function to check output
function check_output {
  check_name="$1"
  actual="$2"
  expected="$3"
  if [[ "${expected}" != "${actual}" ]]; then
    echo "Expected output is ${expected}"
    echo "Actual output is ${actual}"
    exit 1
  fi
}

# Get output (and show)
fluxnetes_output=$(kubectl logs ${fluxnetes_job_pod})

echo
echo "Job pod output: ${fluxnetes_output}"
echo "                Scheduled by: ${fluxnetes_scheduler}"

# Check output explicitly
check_output 'check-fluxnetes-output' "${fluxnetes_output}" "potato"
check_output 'check-scheduled-by' "${fluxnetes_scheduler}" "fluxnetes"

# But events tell us actually what happened, let's parse throught them and find our pods
# This tells us the Event -> reason "Scheduled" and who it was reported by.
reported_by=$(kubectl events --for pod/${fluxnetes_job_pod} -o json  | jq -c '[ .items[] | select( .reason | contains("Scheduled")) ]' | jq -r .[0].reportingComponent)
check_output 'reported-by-fluxnetes' "${reported_by}" "fluxnetes"
