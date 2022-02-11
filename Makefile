all: vendor build
.PHONY: all

SHELL := /bin/bash

# go-get-tool will 'go get' any package $2 and install it to $1.
# backing up and recovering the go.mod/go.sum as go install doesnt work
# with project that use replacement directives, no time for a nicer solution.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
echo "Downloading $(2)" ;\
mkdir -p $(PROJECT_DIR)/tmp ;\
cp $(PROJECT_DIR)/{go.mod,go.sum} $(PROJECT_DIR)/tmp ;\
GOBIN=$(PROJECT_DIR)/bin go get $(2) ;\
cp $(PROJECT_DIR)/tmp/{go.mod,go.sum} $(PROJECT_DIR)/ ;\
}
endef

build:
	go build -o bin ./cmd/...
.PHONY: build

vendor:
	go mod tidy
	go mod vendor
.PHONY: vendor

KIND = $(shell pwd)/bin/kind
kind:
	$(call go-get-tool,$(KIND),sigs.k8s.io/kind@v0.11.1)

# Not ideal, fix when possible.
KCP = $(shell pwd)/bin/kcp
kcp:
	rm -rf ./tmp/kcp
	git clone --depth=1 https://github.com/kcp-dev/kcp ./tmp/kcp
	cd ./tmp/kcp && make
	cp ./tmp/kcp/bin/cluster-controller $(shell pwd)/bin
	cp ./tmp/kcp/bin/compat $(shell pwd)/bin
	cp ./tmp/kcp/bin/crd-puller $(shell pwd)/bin
	cp ./tmp/kcp/bin/deployment-splitter $(shell pwd)/bin
	cp ./tmp/kcp/bin/kcp $(shell pwd)/bin
	cp ./tmp/kcp/bin/kubectl-kcp $(shell pwd)/bin
	cp ./tmp/kcp/bin/shard-proxy $(shell pwd)/bin
	cp ./tmp/kcp/bin/syncer $(shell pwd)/bin
	cp ./tmp/kcp/bin/virtual-workspaces $(shell pwd)/bin
	rm -rf ./tmp/kcp

.PHONY: local-setup
local-setup: clean build kind kcp
	./utils/local-setup.sh

.PHONY: aws-setup
aws-setup: clean kcp
	./utils/aws-setup.sh --deploy

.PHONY: aws-setup-clean
aws-setup-clean:
	./utils/aws-setup.sh --clean

codegen:
	./hack/update-codegen.sh
.PHONY: codegen

.PHONY: clean
clean:
	-rm -rf ./.kcp
	-rm -f ./bin/*
	-rm -rf ./tmp

generate: generate-deepcopy generate-crd generate-client

generate-deepcopy: controller-gen
	cd pkg/apis/kuadrant && $(CONTROLLER_GEN) paths="./..." object

generate-crd: controller-gen
	cd pkg/apis/kuadrant && $(CONTROLLER_GEN) crd paths=./... output:crd:artifacts:config=../../../config/crd output:crd:dir=../../../config/crd crd:crdVersions=v1 && rm -rf ./config

generate-client:
	./scripts/gen_client.sh

controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go get sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION) ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif
