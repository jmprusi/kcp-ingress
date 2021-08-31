all: vendor build
.PHONY: all

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
	$(call go-get-tool,$(KIND),sigs.k8s.io/kind@v0.10.0)

KCP = $(shell pwd)/bin/kcp
kcp:
	$(call go-get-tool,$(KCP),github.com/kcp-dev/kcp/cmd/kcp@latest)

.PHONY: local-setup
local-setup: clean build kind kcp
	./utils/local-setup.sh

.PHONY: aws-setup
aws-setup: clean kcp
	./utils/aws-setup.sh --deploy

.PHONY: aws-setup-clean
aws-setup-clean:
	./utils/aws-setup.sh --clean


.PHONY: clean
clean:
	-rm -rf ./.kcp
	-rm -f ./bin/*
	-rm -rf ./tmp
