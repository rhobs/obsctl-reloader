include .bingo/Variables.mk

GO111MODULE       ?= on
export GO111MODULE

FILES_TO_FMT      	 ?= $(shell find . -path ./vendor -prune -o -name '*.go' -print)
JSONNET_SRC = $(shell find . -type f -not -path './*vendor_jsonnet/*' \( -name '*.libsonnet' -o -name '*.jsonnet' \))

GOBIN ?= $(firstword $(subst :, ,${GOPATH}))/bin

# Tools.
GIT ?= $(shell which git)

# Support gsed on OSX (installed via brew), falling back to sed. On Linux
# systems gsed won't be installed, so will use sed as expected.
SED ?= $(shell which gsed 2>/dev/null || which sed)

define require_clean_work_tree
    @git update-index -q --ignore-submodules --refresh

    @if ! git diff-files --quiet --ignore-submodules --; then \
        echo >&2 "$1: you have unstaged changes."; \
        git diff-files --name-status -r --ignore-submodules -- >&2; \
        echo >&2 "Please commit or stash them."; \
        exit 1; \
    fi

    @if ! git diff-index --cached --quiet HEAD --ignore-submodules --; then \
        echo >&2 "$1: your index contains uncommitted changes."; \
        git diff-index --cached --name-status -r --ignore-submodules HEAD -- >&2; \
        echo >&2 "Please commit or stash them."; \
        exit 1; \
    fi

endef

.PHONY: login build push format

CONTAINER_ENGINE ?= $(shell which podman >/dev/null 2>&1 && echo podman || echo docker)
AUTH_FLAG ?= $(shell which podman >/dev/null 2>&1 && echo --authfile || echo --config)

IMAGE_NAME := quay.io/app-sre/obsctl-reloader
IMAGE_TAG := $(shell git rev-parse --short=7 HEAD)
DOCKER_CONF := $(PWD)/.docker


login:
	mkdir -p $(DOCKER_CONF)
	@$(CONTAINER_ENGINE) $(AUTH_FLAG)=$(DOCKER_CONF)/auth.json login -u="${QUAY_USER}" -p="${QUAY_TOKEN}" quay.io

build:
	@$(CONTAINER_ENGINE) build -t $(IMAGE_NAME):latest .
	@$(CONTAINER_ENGINE) tag $(IMAGE_NAME):latest $(IMAGE_NAME):$(IMAGE_TAG)

push:
	@$(CONTAINER_ENGINE) $(AUTH_FLAG)=$(DOCKER_CONF)/auth.json push $(IMAGE_NAME):latest
	@$(CONTAINER_ENGINE) $(AUTH_FLAG)=$(DOCKER_CONF)/auth.json push $(IMAGE_NAME):$(IMAGE_TAG)

deploy:
	oc apply -f examples/prometheusrule.yaml
	oc process -p IMAGE_TAG=$(IMAGE_TAG) -f openshift/template.yaml | oc apply -f -


.PHONY: deps
deps: ## Ensures fresh go.mod and go.sum.
	@go mod tidy
	@go mod verify

.PHONY: format
format: ## Formats Go and jsonnet.
format: $(GOIMPORTS) $(JSONNET_SRC) $(JSONNETFMT)
	@echo ">> formatting Go code"
	@$(GOIMPORTS) -w $(FILES_TO_FMT)
	@echo ">>>>> formatting jsonnet"
	$(JSONNETFMT) -n 2 --max-blank-lines 2 --string-style s --comment-style s -i $(JSONNET_SRC)

.PHONY: test
test: ## Runs all Go unit tests.
export GOCACHE=/tmp/cache
test:
	@echo ">> running unit tests (without cache)"
	@rm -rf $(GOCACHE)
	@go test -v -timeout=30m $(shell go list ./... | grep -v e2e);

.PHONY: check-git
check-git:
ifneq ($(GIT),)
	@test -x $(GIT) || (echo >&2 "No git executable binary found at $(GIT)."; exit 1)
else
	@echo >&2 "No git binary found."; exit 1
endif

# PROTIP:
# Add
#      --cpu-profile-path string   Path to CPU profile output file
#      --mem-profile-path string   Path to memory profile output file
# to debug big allocations during linting.
lint: ## Runs various static analysis against our code.
lint: $(FAILLINT) $(GOLANGCI_LINT) $(MISSPELL) format check-git deps
	$(call require_clean_work_tree,"detected not clean master before running lint")
	@echo ">> verifying modules being imported"
	@$(FAILLINT) -paths "fmt.{Print,Printf,Println}" -ignore-tests ./...
	@echo ">> examining all of the Go files"
	@go vet -stdmethods=false ./...
	@echo ">> linting all of the Go files GOGC=${GOGC}"
	@$(GOLANGCI_LINT) run
	@echo ">> detecting misspells"
	@find . -type f | grep -v vendor/ | grep -vE '\./\..*' | xargs $(MISSPELL) -error

.PHONY: manifests
manifests: openshift/template.yaml

openshift/template.yaml: openshift/template.jsonnet $(JSONNET) $(GOJSONTOYAML)
	-rm -rf openshift/template.yaml
	$(JSONNET) openshift/template.jsonnet | $(GOJSONTOYAML) > $@
