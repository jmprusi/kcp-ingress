#!/bin/bash

#
# Copyright 2022 Red Hat, Inc.
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

# Adds a CRC cluster to your local setup. You must run local-setup before running this script.
#
# Requires crc
# wget https://developers.redhat.com/content-gateway/file/pub/openshift-v4/clients/crc/1.40.0/crc-linux-amd64.tar.xz
# $ crc version
# CodeReady Containers version: 1.40.0+5966df09
# OpenShift version: 4.9.18 (embedded in executable)
#
# Get pull secret from here https://cloud.redhat.com/openshift/create/local, and save it to ~/pull-secret

# Note: Kubernetes versions of all clusters added to kcp need to be the same minor version for resources to sync correctly.
#
# $ kubectl --context crc-admin version -o json | jq .serverVersion.gitVersion
# "v1.22.3+e790d7f"
# $ kubectl --context kind-kcp-cluster-1 version -o json | jq .serverVersion.gitVersion
# "v1.22.7"
# $ kubectl --context kind-kcp-cluster-2 version -o json | jq .serverVersion.gitVersion
# "v1.22.7"
# $ kubectl get clusters -o wide
# NAME              LOCATION          READY   SYNCED API RESOURCES
# kcp-cluster-1     kcp-cluster-1     True    ["deployments.apps","ingresses.networking.k8s.io","secrets","services"]
# kcp-cluster-2     kcp-cluster-2     True    ["deployments.apps","ingresses.networking.k8s.io","secrets","services"]
# kcp-cluster-crc   kcp-cluster-crc   True    ["deployments.apps","ingresses.networking.k8s.io","secrets","services"]

set -e pipefail

TEMP_DIR="./tmp"
CRC_KUBECONFIG="crc.kubeconfig"
CRC_CLUSTER_NAME=kcp-cluster-crc
PULL_SECRET=~/pull-secret

crc start -p $PULL_SECRET

cp ~/.crc/machines/crc/kubeconfig ${TEMP_DIR}/${CRC_KUBECONFIG}

cat ${TEMP_DIR}/${CRC_KUBECONFIG} | sed -e 's/^/    /' | cat utils/kcp-contrib/cluster.yaml - | sed -e "s/name: local/name: ${CRC_CLUSTER_NAME}/" >${TEMP_DIR}/${CRC_CLUSTER_NAME}.yaml

echo "Registering crc cluster into KCP"
KUBECONFIG=.kcp/admin.kubeconfig kubectl apply -f ${TEMP_DIR}/${CRC_CLUSTER_NAME}.yaml
