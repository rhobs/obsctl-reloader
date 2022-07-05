.PHONY: login build push format

CONTAINER_ENGINE ?= $(shell which podman >/dev/null 2>&1 && echo podman || echo docker)
AUTH_FLAG ?= $(shell which podman >/dev/null 2>&1 && echo --authfile || echo --config)

IMAGE_NAME := quay.io/maorfr/obsctl-reloader
IMAGE_TAG := $(shell git rev-parse --short=7 HEAD)
DOCKER_CONF := $(PWD)/.docker


login:
	mkdir -p $(DOCKER_CONF)
	@$(CONTAINER_ENGINE) login $(AUTH_FLAG)=$(DOCKER_CONF)/auth.json -u="${QUAY_USER}" -p="${QUAY_TOKEN}" quay.io

build:
	@$(CONTAINER_ENGINE) build $(AUTH_FLAG)=$(DOCKER_CONF)/auth.json -t $(IMAGE_NAME):latest .
	@$(CONTAINER_ENGINE) tag $(IMAGE_NAME):latest $(IMAGE_NAME):$(IMAGE_TAG)

push:
	@$(CONTAINER_ENGINE) push $(AUTH_FLAG)=$(DOCKER_CONF)/auth.json $(IMAGE_NAME):latest
	@$(CONTAINER_ENGINE) push $(AUTH_FLAG)=$(DOCKER_CONF)/auth.json $(IMAGE_NAME):$(IMAGE_TAG)

deploy:
	oc apply -f examples/prometheusrule.yaml
	oc process -p IMAGE_TAG=$(IMAGE_TAG) -f openshift/template.yaml | oc apply -f -

format:
	@. ./venv/bin/activate && black run.py
