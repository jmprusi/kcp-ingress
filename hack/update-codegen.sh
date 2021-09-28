#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

export GOFLAGS="-mod=mod"
SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CODEGEN_PKG=${CODEGEN_PKG:-$(cd "${SCRIPT_ROOT}"; go list -f '{{.Dir}}' -m k8s.io/code-generator)}

#"deepcopy,client,informer,lister"
bash "${CODEGEN_PKG}"/generate-groups.sh "all" \
  github.com/jmprusi/kcp-ingress/pkg/client github.com/jmprusi/kcp-ingress/pkg/apis \
  "globalloadbalancer:v1alpha1" \
  --go-header-file "${SCRIPT_ROOT}"/hack/boilerplate.go.txt --output-base ${GOPATH}/src

# Update generated CRD YAML
"${GOPATH}"/bin/controller-gen crd:preserveUnknownFields=false rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/
