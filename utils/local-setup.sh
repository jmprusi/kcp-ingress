#!/bin/bash
usage() { echo "usage: ./local-setup.sh -c <number of clusters>" 1>&2; exit 1; }
while getopts ":c:" arg; do
  case "${arg}" in
    c)
      NUM_CLUSTERS=${OPTARG}
      ;;
    *)
      usage
      ;;
  esac
done
shift $((OPTIND-1))


if [ -z "${NUM_CLUSTERS}" ]; then
    usage
fi
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
  kill "$WORKSPACE_PID"
  kill "$KCP_PID"
}

GOROOT=$(go env GOROOT)
export GOROOT
TEMP_DIR="./tmp"
KCP_LOG_FILE="${TEMP_DIR}"/kcp.log
KCP_BRANCH=release-prototype-2

KIND_CLUSTER_PREFIX="kcp-cluster-"
for ((i=1;i<=$NUM_CLUSTERS;i++))
do
	CLUSTERS="${CLUSTERS}${KIND_CLUSTER_PREFIX}${i} "
done

mkdir -p ${TEMP_DIR}

# Clone an build KCP if not yet built.
if [[ ! -f ./tmp/kcp/bin/kcp ]]; then
  echo "Building KCP"
  rm -rf ./tmp/kcp
  git clone --branch ${KCP_BRANCH} --depth=1 https://github.com/kcp-dev/kcp ./tmp/kcp
  cd ./tmp/kcp
  make
  cd -
fi
export PATH=./tmp/kcp/bin:$PATH

echo "Starting KCP, sending logs to ${KCP_LOG_FILE}"
rm -rf .kcp 2> /dev/null || true
kcp start --push-mode --run-controllers --resources-to-sync=deployments --resources-to-sync=services --resources-to-sync=ingresses.networking.k8s.io --auto-publish-apis > ${KCP_LOG_FILE} 2>&1 &
KCP_PID=$!

createCluster() {
  cluster=$1;
  port80=$2;
  port443=$3;
  cat <<EOF | kind create cluster --name ${cluster} --config=-
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
    hostPort: ${port80}
    protocol: TCP
  - containerPort: 443
    hostPort: ${port443}
    protocol: TCP
EOF
  echo "Deploying Ingress controller to kind cluster"
  {
  kubectl config use-context kind-${cluster}

  VERSION=$(curl https://raw.githubusercontent.com/kubernetes/ingress-nginx/master/stable.txt)
  curl https://raw.githubusercontent.com/kubernetes/ingress-nginx/"${VERSION}"/deploy/static/provider/kind/deploy.yaml | sed "s/--publish-status-address=localhost/--report-node-internal-ip-address/g" | kubectl apply -f -
  kubectl annotate ingressclass nginx "ingressclass.kubernetes.io/is-default-class=true"

  } &>/dev/null
}

if [ "${SKIP_KIND_SETUP:-false}" == "false" ] ; then
  clusterCount=$(kind get clusters | grep ${KIND_CLUSTER_PREFIX} | wc -l)
  if ! [[ $clusterCount =~ "0" ]] ; then
    echo "Deleting previous kind clusters."
    kind get clusters | grep ${KIND_CLUSTER_PREFIX} | xargs kind delete clusters
  fi

  echo "Deploying $NUM_CLUSTERS kind k8s clusters locally."

  port80=8080
  port443=8443
  for cluster in $CLUSTERS
  do
    createCluster "$cluster" $port80 $port443
    port80=$((port80+1))
    port443=$((port443+1))
  #move to next cluster
  done
fi

if ! ps -p ${KCP_PID}; then
  echo "####"
  echo "---> KCP failed to start, see ${KCP_LOG_FILE} for info."
  echo "####"
  exit 1 #this will trigger cleanup function
fi

echo "Exporting KUBECONFIG=.kcp/admin.kubeconfig"
export KUBECONFIG=.kcp/admin.kubeconfig

# Wait for KCP to startup...
set +e
for i in {1..15}; do
  if [ -f .kcp/admin.kubeconfig ] ; then
    kubectl api-resources > /dev/null 2>&1
    if [ $? == 0 ]; then
      break
    fi
  fi
  echo "Waiting for KCP to startup..."
  sleep 1
done
set -e pipefail

echo "Starting Workspace Server"
virtual-workspaces workspaces \
  --workspaces:kubeconfig .kcp/admin.kubeconfig \
  --authentication-kubeconfig .kcp/admin.kubeconfig \
  --tls-cert-file .kcp/apiserver.crt \
  --tls-private-key-file .kcp/apiserver.key \
    > ./tmp/virtual-workspaces.log 2>&1 &
WORKSPACE_PID=$!

echo "Setting up a demo workspace"

kubectl create namespace default
kubectl create secret generic kubeconfig --from-file=kubeconfig=${KUBECONFIG}
cat <<EOF | kubectl apply -f -
apiVersion: tenancy.kcp.dev/v1alpha1
kind: WorkspaceShard
metadata:
  name: boston
spec:
  credentials:
    namespace: default
    name: kubeconfig
EOF

sleep 2

kubectl kcp workspace --token user-1-token --workspace-directory-insecure-skip-tls-verify create demo --use

# Import some apis into the KCP instance from the kind cluster.
APIS="deployments.apps services ingresses.networking.k8s.io"
crd-puller --kubeconfig ~/.kube/config $APIS
for api in $APIS ; do
  kubectl apply -f $api.yaml
done

echo "Registering HCG APIs"
kubectl apply -f ./config/crd

echo "Adding kind k8s clusters to the workspace"

for cluster in $CLUSTERS; do
  echo "Creating cluster objects for the kind cluster: $cluster"
  cat <<EOF | kubectl apply -f -
apiVersion: cluster.example.dev/v1alpha1
kind: Cluster
metadata:
  name: ${cluster}
spec:
  kubeconfig: |
$(kubectl --kubeconfig ~/.kube/config config view --flatten --minify --context kind-${cluster}|sed 's,^,    ,')
EOF
done

#./bin/deployment-splitter --kubeconfig=.kcp/admin.kubeconfig >> ${KCP_LOG_FILE} 2>&1 &
#CONTROLLER_2=$!

echo ""
echo "The kind k8s clusters have been registered, and KCP is running, now you should run the ingress-controller"
echo "example: "
echo ""
echo "       ./bin/ingress-controller -kubeconfig .kcp/admin.kubeconfig"
echo ""
echo "kubectl is connected to the demo workspace."
echo ""
echo "KCP will be stopped when you exit this shell."

bash