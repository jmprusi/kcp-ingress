#!/bin/bash
cd "$(dirname "$0")/.." # cd to the project dir.
export PATH=${PWD}/bin:$PATH

# build the ingress-controller
echo in $(pwd)
make build

# clean up on script exit
set -e pipefail
trap cleanup EXIT 1 2 3 6 15
cleanup_commands=()
cleanup() {
  set +e
  for c in "${cleanup_commands[@]}"; do
    echo $c
    /bin/bash -c "$c" > /dev/null 2>&1
  done
}

# Clone an build KCP if not yet built.
make build
if [[ ! -f ./tmp/kcp/bin/kcp ]]; then
  echo building kcp
  rm -rf ./tmp/kcp
  git clone --depth=1 https://github.com/kcp-dev/kcp ./tmp/kcp
  cd ./tmp/kcp
  make
  cd -
fi

cd ./tmp/kcp
echo in $(pwd)
export PATH=${PWD}/bin:$PATH

# Start kcp itself
rm -rf .kcp
./bin/kcp start --auto-publish-apis --resources-to-sync="ingresses.networking.k8s.io,deployments.apps,services" --push-mode --token-auth-file contrib/demo/workspaceKubectlPlugin-script/kcp-tokens \
    > /tmp/kcp.log 2>&1 &
cleanup_commands+=( "kill $!" )

# Create 2 kind clusters (kind slow, gives kcp time to startup)
function create_kind_cluster {
  cat <<EOF | kind create cluster --name $1 --config=-
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
    hostPort: $2
    protocol: TCP
  - containerPort: 443
    hostPort: $3
    protocol: TCP
EOF
}

if [ "${DELETE_KIND_CLUSTERS:-true}" == "true" ] ; then
  kind delete clusters east west
  create_kind_cluster east 8080 8433
  create_kind_cluster west 8081 8444
else
  sleep 5 # to give kcp some time to startup..
fi

# Apply the nginx-ingress controller manifest into each cluster.
kubectl --context kind-east apply -f ./contrib/demo/ingress-script/nginx-ingress.yaml
kubectl --context kind-west apply -f ./contrib/demo/ingress-script/nginx-ingress.yaml

## Start the virtual workspaces server
./bin/virtual-workspaces workspaces --workspaces:kubeconfig .kcp/admin.kubeconfig --authentication-kubeconfig .kcp/admin.kubeconfig --secure-port 6444 --authentication-skip-lookup \
    > /tmp/virtual-workspaces.log 2>&1 &
cleanup_commands+=( "kill $!" )

export KUBECONFIG=.kcp/admin.kubeconfig

kubectl create namespace default
kubectl create secret generic kubeconfig --from-file=kubeconfig=${KUBECONFIG}
kubectl apply -f contrib/demo/workspaceKubectlPlugin-script/workspace-shard.yaml

# Note, we are still working through certificate configurations, so for
# the time being, we have to skip TLS verification with the kubectl
# kcp workspace plugin.
kubectl kcp workspace --token user-1-token --workspace-directory-insecure-skip-tls-verify create workspace1 --use

# Import some apis into the KCP instance from the kind cluster.
APIS="deployments.apps services ingresses.networking.k8s.io"
go run ./cmd/crd-puller/pull-crds.go --kubeconfig ~/.kube/config $APIS
for api in $APIS ; do
  kubectl apply -f $api.yaml
done

# Now we can create a deployment
kubectl create namespace default
#kubectl create deployment --image=gcr.io/kuar-demo/kuard-amd64:blue --port=8080 kuard
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app: kuard
  name: kuard
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kuard
  template:
    metadata:
      labels:
        app: kuard
    spec:
      containers:
        - image: gcr.io/kuar-demo/kuard-amd64:blue
          name: kuard-amd64
          ports:
            - containerPort: 8080
              protocol: TCP
EOF
kubectl expose deployment/kuard

cat <<EOF | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: kuard
spec:
  rules:
    - host: kuard.kcp-apps.127.0.0.1.nip.io
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: kuard
                port:
                  number: 8080
EOF

# 2.1.5 Register kind-east with kcp
kubectl apply -f config/cluster.example.dev_clusters.yaml
cat <<EOF | kubectl apply -f -
apiVersion: cluster.example.dev/v1alpha1
kind: Cluster
metadata:
  name: kind-east
spec:
  kubeconfig: |
$(kubectl --kubeconfig ~/.kube/config config view --flatten --minify --context kind-east|sed 's,^,    ,')
EOF

# # ----------------------------------------------------------------------
# # 2.1.7 Add a second Cluster
cat <<EOF | kubectl apply -f -
apiVersion: cluster.example.dev/v1alpha1
kind: Cluster
metadata:
  name: kind-west
spec:
  kubeconfig: |
$(kubectl --kubeconfig ~/.kube/config config view --flatten --minify --context kind-west|sed 's,^,    ,')
EOF
# # 2.1.8 Stop the first kind cluster
# kind delete clusters east
# echo ===================================================================
# echo 2.1.9 Show the deployment moved
# echo
# echo $ kubectl get namespace default -o yaml
# echo
# echo ===================================================================
# bash

# Start the ingress controller and envoy
ingress-controller -kubeconfig=.kcp/admin.kubeconfig -envoyxds -envoy-listener-port=8181 \
    > /tmp/kcp-ingress.log 2>&1 &
cleanup_commands+=( "kill $!" )

envoy --config-path ./build/kcp-ingress/utils/envoy/bootstrap.yaml \
    > /tmp/envoy.log 2>&1 &
cleanup_commands+=( "kill $!" )

echo ===================================================================
echo "2.1.10 Add ingress to a single deployment with a single hostname, show traffic flowing to whichever kind cluster has the deployment"
echo
echo $ curl -v -H "Host: kuard.kcp-apps.127.0.0.1.nip.io" http://localhost:8080
echo
echo ===================================================================
bash

