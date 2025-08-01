include ./.bingo/Variables.mk
include ./.bingo/Symlinks.mk
SHELL = /bin/bash
PATH := $(GOBIN):$(PATH)

# This build tag is currently leveraged by tooling/image-sync
# https://github.com/containers/image?tab=readme-ov-file#building
GOTAGS?='containers_image_openpgp'
LINT_GOTAGS?='${GOTAGS},E2Etests'
TOOLS_BIN_DIR := tooling/bin
DEPLOY_ENV ?= pers

.DEFAULT_GOAL := all

all: test lint
.PHONY: all

# There is currently no convenient way to run tests against a whole Go workspace
# https://github.com/golang/go/issues/50745
test:
	go list -f '{{.Dir}}/...' -m |RUN_TEMPLATIZE_E2E=true xargs go test -timeout 1200s -tags=$(GOTAGS) -cover
.PHONY: test

test-compile:
	go list -f '{{.Dir}}/...' -m |xargs go test -c -o /dev/null
.PHONY: test-compile

mocks: $(MOCKGEN) $(GOIMPORTS)
	MOCKGEN=${MOCKGEN} go generate ./internal/mocks
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP ./internal/mocks
.PHONY: mocks

install-tools: $(BINGO)
	$(BINGO) get
.PHONY: install-tools

licenses: $(ADDLICENSE)
	$(ADDLICENSE) -c 'Microsoft Corporation' -l apache $(shell find . -type f -name '*.go')

# There is currently no convenient way to run golangci-lint against a whole Go workspace
# https://github.com/golang/go/issues/50745
MODULES := $(shell go list -f '{{.Dir}}/...' -m | xargs)
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run -v --build-tags=$(LINT_GOTAGS) $(MODULES)
.PHONY: lint

lint-fix: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run -v --build-tags=$(LINT_GOTAGS) $(MODULES) --fix
.PHONY: lint-fix

fmt: $(GOIMPORTS)
	$(GOIMPORTS) -w -local github.com/Azure/ARO-HCP $(shell go list -f '{{.Dir}}' -m | xargs)
.PHONY: fmt

yamlfmt: $(YAMLFMT)
	# first, wrap all templated values in quotes, so they are correct YAML
	./yamlfmt.wrap.sh
	# run the formatter
	$(YAMLFMT) -dstar -exclude './api/**' '**/*.{yaml,yml}'
	# "fix" any non-string fields we cast to strings for the formatting
	./yamlfmt.unwrap.sh
.PHONY: yamlfmt

tidy: $(MODULES:/...=.tidy)

%.tidy:
	cd $(basename $@) && go mod tidy

all-tidy: tidy fmt licenses
	go work sync

mega-lint:
	docker run --rm \
		-e FILTER_REGEX_EXCLUDE='hypershiftoperator/deploy/crds/|maestro/server/deploy/templates/allow-cluster-service.authorizationpolicy.yaml|acm/deploy/helm/multicluster-engine-config/charts/policy/charts' \
		-e REPORT_OUTPUT_FOLDER=/tmp/report \
		-v $${PWD}:/tmp/lint:Z \
		oxsecurity/megalinter:v8
.PHONY: mega-lint

#
# Infra
#

infra.region:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make region
.PHONY: infra.region

infra.svc:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make svc.init
.PHONY: infra.svc

infra.svc.aks.kubeconfig:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make -s svc.aks.kubeconfig
.PHONY: infra.svc.aks.kubeconfig

infra.svc.aks.kubeconfigfile:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make -s svc.aks.kubeconfigfile
.PHONY: infra.svc.aks.kubeconfigfile

infra.mgmt:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make mgmt.init
.PHONY: infra.mgmt

infra.mgmt.solo:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make mgmt.solo.init
.PHONY: infra.mgmt.solo

infra.mgmt.aks.kubeconfig:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make -s mgmt.aks.kubeconfig
.PHONY: infra.mgmt.aks.kubeconfig

infra.mgmt.aks.kubeconfigfile:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make -s mgmt.aks.kubeconfigfile
.PHONY: infra.mgmt.aks.kubeconfigfile

infra.monitoring:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make monitoring
.PHONY: infra.monitoring

infra.all:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make infra
.PHONY: infra.all

infra.svc.clean:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make svc.clean
.PHONY: infra.svc.clean

infra.mgmt.clean:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make mgmt.clean
.PHONY: infra.mgmt.clean

infra.region.clean:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make region.clean
.PHONY: infra.region.clean

infra.clean:
	@cd dev-infrastructure && DEPLOY_ENV=$(DEPLOY_ENV) make clean
.PHONY: infra.clean

infra.tracing:
	cd observability/tracing && KUBECONFIG="$$(cd ../../dev-infrastructure && make -s svc.aks.kubeconfigfile)" make
.PHONY: infra.tracing

#
# Services
#

# Service Deployment Conventions:
#
# - Services are deployed in aks clusters (either svc or mgmt), which are
#   provisioned via infra section above
# - Makefile targets to deploy services ends with ".deploy" suffix
# - To deploy all services on svc or mgmt cluster, we have special targets
#   `svc.deployall` and `mgmt.deployall`, and `deployall` deploys everithing.
# - Placement of a service is controlled via services_svc and services_mgmt
#   variables
# - If the name of the service contains a dot, it's interpreted as directory
#   separator "/" (used for maestro only).

# Services deployed on "svc" aks cluster
services_svc =
# Services deployed on "mgmt" aks cluster(s)
services_mgmt =
# List of all services
services_all = $(join services_svc,services_mgmt)

.PHONY: $(addsuffix .deploy, $(services_all)) deployall svc.deployall mgmt.deployall listall list clean

# Service deployment on either svc or mgmt aks cluster, a service name
# needs to be listed either in services_svc or services_mgmt variable (wich
# defines where it will be deployed).
%.deploy:
	$(eval export dirname=$(subst .,/,$(basename $@)))
	@if [ $(words $(filter $(basename $@), $(services_svc))) = 1 ]; then\
	    ./svc-deploy.sh $(DEPLOY_ENV) $(dirname) svc;\
	elif [ $(words $(filter $(basename $@), $(services_mgmt))) = 1 ]; then\
	    ./svc-deploy.sh $(DEPLOY_ENV) $(dirname) mgmt;\
	else\
	    echo "'$(basename $@)' is not to be deployed on neither svc nor mgmt cluster";\
	    exit 1;\
	fi


# Pipelines section
# This sections is used to reference pipeline runs and should replace
# the usage of `svc-deploy.sh` script in the future.
services_svc_pipelines = backend frontend cluster-service maestro.server observability.tracing
services_mgmt_pipelines = secret-sync-controller acm hypershiftoperator maestro.agent observability.tracing route-monitor-operator
%.deploy_pipeline: $(ORAS_LINK)
	$(eval export dirname=$(subst .,/,$(basename $@)))
	./templatize.sh $(DEPLOY_ENV) -p ./$(dirname)/pipeline.yaml -P run

%.dry_run: $(ORAS_LINK)
	$(eval export dirname=$(subst .,/,$(basename $@)))
	./templatize.sh $(DEPLOY_ENV) -p ./$(dirname)/pipeline.yaml -P run -d

svc.deployall: $(ORAS_LINK) $(addsuffix .deploy_pipeline, $(services_svc_pipelines)) $(addsuffix .deploy, $(services_svc))
mgmt.deployall: $(ORAS_LINK) $(addsuffix .deploy, $(services_mgmt)) $(addsuffix .deploy_pipeline, $(services_mgmt_pipelines))
deployall: $(ORAS_LINK) svc.deployall mgmt.deployall

listall:
	@echo svc: ${services_svc}
	@echo mgmt: ${services_mgmt}

list:
	@grep '^[^#[:space:]].*:' Makefile

rebase:
	hack/rebase-n-materialize.sh
.PHONY: rebase

validate-config-pipelines:
	$(MAKE) -C tooling/templatize templatize
	tooling/templatize/templatize pipeline validate --topology-config-file topology.yaml --service-config-file config/config.yaml --dev-mode --dev-region $(shell yq '.environments[] | select(.name == "dev") | .defaults.region' <tooling/templatize/settings.yaml) $(ONLY_CHANGED)

validate-changed-config-pipelines:
	$(MAKE) validate-config-pipelines DEV_MODE="--dev-mode --dev-region uksouth" ONLY_CHANGED="--only-changed"

validate-config:
	$(MAKE) -C config/ validate
