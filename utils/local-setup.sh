#!/bin/bash
#
# Copyright 2021 Red Hat, Inc.
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
#
set -e pipefail

trap cleanup EXIT 1 2 3 6 15

cleanup() {
  echo "Killing KCP"
  kill "$KCP_PID"
}

GOROOT=$(go env GOROOT)
export GOROOT
export KIND_BIN="./bin/kind"
export KCP_BIN="./bin/kcp"
TEMP_DIR="./tmp"
KCP_LOG_FILE="${TEMP_DIR}"/kcp.log

# TODO(jmprusi): Hardcoded, improve.
KIND_CLUSTER_A="kcp-cluster-a"
KIND_CLUSTER_B="kcp-cluster-b"

mkdir -p ${TEMP_DIR}


# TODO(jmprusi): Split this setup into up/clean actions.
echo "Deleting any previous kind clusters."
{
  ${KIND_BIN} delete cluster --name ${KIND_CLUSTER_A}
  ${KIND_BIN} delete cluster --name ${KIND_CLUSTER_B}
} &> /dev/null

echo "Deploying two kind k8s clusters locally."


cat <<EOF | ${KIND_BIN} create cluster --name ${KIND_CLUSTER_A} --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: InitConfiguration
    nodeRegistration:
      kubeletExtraArgs:
        node-labels: "ingress-ready=true"
  extraPortMappings:
  - containerPort: 80
    hostPort: 8080
    protocol: TCP
  - containerPort: 443
    hostPort: 8443
    protocol: TCP
EOF

cat <<EOF | ${KIND_BIN} create cluster --name ${KIND_CLUSTER_B} --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: InitConfiguration
    nodeRegistration:
      kubeletExtraArgs:
        node-labels: "ingress-ready=true"
  extraPortMappings:
  - containerPort: 80
    hostPort: 8081
    protocol: TCP
  - containerPort: 443
    hostPort: 8444
    protocol: TCP
EOF


echo "Creating Cluster objects for each of the k8s cluster."

${KIND_BIN} get kubeconfig --name=${KIND_CLUSTER_A} | sed -e 's/^/    /' | cat utils/kcp-contrib/cluster.yaml - | sed -e "s/name: local/name: ${KIND_CLUSTER_A}/" > ${TEMP_DIR}/${KIND_CLUSTER_A}.yaml
${KIND_BIN} get kubeconfig --name=${KIND_CLUSTER_B} | sed -e 's/^/    /' | cat utils/kcp-contrib/cluster.yaml - | sed -e "s/name: local/name: ${KIND_CLUSTER_B}/" > ${TEMP_DIR}/${KIND_CLUSTER_B}.yaml

echo "Deploying Ingress controller to kind k8s clusters"

{
kubectl config use-context kind-${KIND_CLUSTER_A}

VERSION=$(curl https://raw.githubusercontent.com/kubernetes/ingress-nginx/master/stable.txt)
curl https://raw.githubusercontent.com/kubernetes/ingress-nginx/"${VERSION}"/deploy/static/provider/kind/deploy.yaml | sed "s/--publish-status-address=localhost/--report-node-internal-ip-address/g" | kubectl apply -f -
kubectl annotate ingressclass nginx "ingressclass.kubernetes.io/is-default-class=true" 

kubectl config use-context kind-${KIND_CLUSTER_B}
curl https://raw.githubusercontent.com/kubernetes/ingress-nginx/"${VERSION}"/deploy/static/provider/kind/deploy.yaml | sed "s/--publish-status-address=localhost/--report-node-internal-ip-address/g" | kubectl apply -f -
kubectl annotate ingressclass nginx "ingressclass.kubernetes.io/is-default-class=true" 

} &>/dev/null

echo "Starting KCP, sending logs to ${KCP_LOG_FILE}"
${KCP_BIN} start --push-mode --install-cluster-controller --resources-to-sync=deployments --resources-to-sync=services --resources-to-sync=ingresses.networking.k8s.io --auto-publish-apis > ${KCP_LOG_FILE} 2>&1 &
KCP_PID=$!

echo "Waiting 15 seconds..."
sleep 15

echo "Exporting KUBECONFIG=.kcp/admin.kubeconfig"
export KUBECONFIG=.kcp/admin.kubeconfig

echo "Registering kind k8s clusters into KCP"
kubectl apply -f ./tmp/

echo "" 
echo "The kind k8s clusters have been registered, and KCP is running, now you should run the kcp-ingress"
echo "example: "
echo ""
echo "       ./bin/ingress-controller -kubeconfig .kcp/admin.kubeconfig"
echo ""
echo "Dont't forget to export the proper KUBECONFIG to create objects against KCP:"
echo "export KUBECONFIG=${PWD}/.kcp/admin.kubeconfig"
echo ""
read -p "Press enter to exit -> It will kill the KCP process running in background"
