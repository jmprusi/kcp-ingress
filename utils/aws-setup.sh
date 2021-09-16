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



function deploy {

set -e pipefail

trap killkcp EXIT 1 2 3 6 15

killkcp() {
  echo "Killing KCP"
  kill "$KCP_PID"
}

GOROOT=$(go env GOROOT)
export GOROOT
export KCP_BIN="./bin/kcp"
TEMP_DIR="./tmp"
KCP_LOG_FILE="${TEMP_DIR}"/kcp.log

mkdir -p ${TEMP_DIR}


# Ok, this is far from ideal, but the module for deploying the k8s clusters looks for the
# route53 to exist at the planning time... so we will have to do that in two steps.
# rm -f utils/terraform/minikube.tf
terraform -chdir=utils/terraform/ init
terraform -chdir=utils/terraform/ apply -auto-approve 
# cp utils/terraform/minikube.tf.disable utils/terraform/minikube.tf
# terraform -chdir=utils/terraform/ init
# terraform -chdir=utils/terraform/ apply -auto-approve 

echo "Waiting 180 seconds for the remote clusters to be ready..."
sleep 180

echo "Getting remote clusters kubeconfigs"
terraform -chdir=utils/terraform output -raw cluster-1_kubeconfig > ${TEMP_DIR}/cluster1_kubeconfig
terraform -chdir=utils/terraform output -raw cluster-2_kubeconfig > ${TEMP_DIR}/cluster2_kubeconfig

echo "Creating Cluster objects for each of the k8s cluster."

cat ${TEMP_DIR}/cluster1_kubeconfig | sed -e 's/^/    /' | cat utils/kcp-contrib/cluster.yaml - | sed -e "s/name: local/name: "cluster-1"/" > ${TEMP_DIR}/cluster-1.yaml
cat ${TEMP_DIR}/cluster2_kubeconfig | sed -e 's/^/    /' | cat utils/kcp-contrib/cluster.yaml - | sed -e "s/name: local/name: "cluster-2"/" > ${TEMP_DIR}/cluster-2.yaml


echo "Starting KCP, sending logs to ${KCP_LOG_FILE}"
${KCP_BIN} start --push_mode --install_cluster_controller --resources_to_sync=ingresses.networking.k8s.io --auto_publish_apis > ${KCP_LOG_FILE} 2>&1 &
KCP_PID=$!

echo "Waiting 30 seconds..."
sleep 30

echo "Exporting KUBECONFIG=.kcp/data/admin.kubeconfig"
export KUBECONFIG=.kcp/data/admin.kubeconfig

echo "Registering kind k8s clusters into KCP"
kubectl apply -f ./tmp/

echo "" 
echo "The kind k8s clusters have been registered, and KCP is running, now you should run the kcp-ingress"
echo "example: "
echo ""
echo "       ./bin/ingress-controller -kubeconfig .kcp/data/admin.kubeconfig"
echo ""
echo "Dont't forget to export the proper KUBECONFIG to create objects against KCP:"
echo "export KUBECONFIG=${PWD}/.kcp/data/admin.kubeconfig"
echo ""
echo "IMPORTANT IMPORTANT IMPORTANT IMPORTANT IMPORTANT IMPORTANT IMPORTANT IMPORTANT "
echo "DON'T FORGE TO RUN \"make aws-setup-clean\" TO SHUTDOWN THE AWS RESOURCES CREATED"
echo "IMPORTANT IMPORTANT IMPORTANT IMPORTANT IMPORTANT IMPORTANT IMPORTANT IMPORTANT "
echo ""
read -p "Press enter to exit -> It will kill the KCP process running in background"

}

function clean {
  echo "Cleaning up the terraform environment..."
  terraform -chdir=utils/terraform/ destroy -auto-approve

  echo "Done!"
}

case "$1" in
        "--deploy") deploy
            ;;
        "--clean") clean
            ;;
        *)
        echo "Specify either --deploy or --clean"
        exit 1
        ;;
esac