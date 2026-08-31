# Every check CI runs is a target here, and CI calls these targets rather than
# re-implementing them in YAML. That is the only way `make verify` passing
# locally actually means the pipeline will pass.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

SCORER    := services/scorer
VENV      := $(SCORER)/.venv
PY        := $(VENV)/bin/python
CHARTS    := gateway catalog scorer
ENVS      := dev staging prod
RENDER_DIR := .render
K8S_VERSION ?= 1.31.0
CRD_SCHEMAS := https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'

# --- Aggregate ---------------------------------------------------------------

.PHONY: verify
verify: lint test render validate policy tf-check ## Run every check CI runs
	@echo ""
	@echo "All checks passed."

# --- Go ----------------------------------------------------------------------

.PHONY: go-lint
go-lint: ## Vet and format-check the Go services
	@gofmt -l . | tee /dev/stderr | ( ! read )
	go vet ./...

.PHONY: go-test
go-test: ## Run Go tests under the race detector
	go test -race -count=1 ./...

# Kept separate from go-test because writing a cross-package coverage profile
# needs the `covdata` tool, which some minimal Go installations omit. CI runs
# this; `make verify` does not depend on it so a missing tool cannot block the
# checks that matter.
.PHONY: go-cover
go-cover: ## Go tests with a coverage profile (needs a complete Go toolchain)
	go test -race -count=1 -covermode=atomic -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

.PHONY: go-build
go-build: ## Build both Go binaries
	CGO_ENABLED=0 go build -trimpath -o bin/gateway ./cmd/gateway
	CGO_ENABLED=0 go build -trimpath -o bin/catalog ./cmd/catalog

# --- Python ------------------------------------------------------------------

$(VENV):
	python3 -m venv $(VENV)
	$(VENV)/bin/pip install -q --upgrade pip
	cd $(SCORER) && .venv/bin/pip install -q -e '.[dev]'

.PHONY: py-lint
py-lint: $(VENV) ## Lint and format-check the scorer
	cd $(SCORER) && .venv/bin/python -m ruff check .
	cd $(SCORER) && .venv/bin/python -m ruff format --check .

.PHONY: py-test
py-test: $(VENV) ## Run the scorer test suite with coverage
	cd $(SCORER) && .venv/bin/python -m pytest -q --cov=app --cov-report=term-missing

.PHONY: lint
lint: go-lint py-lint ## Lint everything

.PHONY: test
test: go-test py-test ## Test everything

# --- Kubernetes manifests ----------------------------------------------------

.PHONY: helm-deps
helm-deps: ## Resolve the library chart dependency for each service
	@for c in $(CHARTS); do helm dependency update deploy/charts/$$c >/dev/null; done

.PHONY: helm-lint
helm-lint: helm-deps ## Lint the Helm charts
	@for c in $(CHARTS); do helm lint deploy/charts/$$c; done

# Rendered with a pinned tag because the chart deliberately refuses to render a
# prod release without one. See platform.validateImage.
.PHONY: render
render: helm-deps ## Render every chart against every environment
	@rm -rf $(RENDER_DIR) && mkdir -p $(RENDER_DIR)
	@for e in $(ENVS); do \
		for c in $(CHARTS); do \
			helm template $$c deploy/charts/$$c \
				-f deploy/env/$$e.yaml \
				--set image.tag=v0.0.0-verify \
				> $(RENDER_DIR)/$$e-$$c.yaml; \
		done; \
	done
	@echo "Rendered $$(ls $(RENDER_DIR) | wc -l) manifest sets."

.PHONY: validate
validate: render ## Validate manifests against real Kubernetes and CRD schemas
	kubeconform -strict -summary \
		-kubernetes-version $(K8S_VERSION) \
		-schema-location default \
		-schema-location '$(CRD_SCHEMAS)' \
		$(RENDER_DIR)/*.yaml deploy/argocd/*.yaml deploy/observability/*.yaml

.PHONY: policy
policy: render policy-negative ## Enforce the security policies on rendered manifests
	conftest test --policy policy --all-namespaces $(RENDER_DIR)/*.yaml

# A policy suite that only ever sees compliant input proves nothing: a rule with
# a typo in its path expression passes silently forever. This asserts the rules
# still reject a deliberately insecure manifest.
.PHONY: policy-negative
policy-negative: ## Prove the policies can fail, not just pass
	@if conftest test --policy policy --all-namespaces policy/testdata/insecure-deployment.yaml >/dev/null 2>&1; then \
		echo "FAIL: the policies accepted policy/testdata/insecure-deployment.yaml."; \
		echo "      They are not actually enforcing anything. Check the rule paths."; \
		exit 1; \
	else \
		echo "Policies correctly rejected the insecure fixture."; \
	fi

.PHONY: promtool
promtool: ## Validate the PromQL in the SLO rules
	@python3 -c "import yaml,sys; d=yaml.safe_load(open('deploy/observability/slo-rules.yaml')); yaml.safe_dump({'groups': d['spec']['groups']}, open('$(RENDER_DIR)/rules.yaml','w'))"
	promtool check rules $(RENDER_DIR)/rules.yaml

# --- Terraform ---------------------------------------------------------------

.PHONY: tf-check
tf-check: ## Format-check the Terraform (validate needs provider download)
	terraform -chdir=infra/terraform fmt -check -recursive

.PHONY: tf-validate
tf-validate: ## Initialise providers and validate the Terraform
	terraform -chdir=infra/terraform init -backend=false -input=false
	terraform -chdir=infra/terraform validate

.PHONY: tf-plan
tf-plan: ## Plan against an environment: make tf-plan ENV=dev
	@test -n "$(ENV)" || { echo "Set ENV, e.g. make tf-plan ENV=dev"; exit 1; }
	terraform -chdir=infra/terraform plan -var-file=envs/$(ENV).tfvars

# --- Local development -------------------------------------------------------

.PHONY: dev
dev: ## Run the whole platform locally with docker compose
	docker compose -f local/docker-compose.yaml up --build

.PHONY: dev-down
dev-down: ## Tear down the local stack
	docker compose -f local/docker-compose.yaml down -v

.PHONY: kind-up
kind-up: ## Create a local kind cluster and deploy the platform to it
	./local/kind-up.sh

.PHONY: kind-down
kind-down: ## Delete the local kind cluster
	kind delete cluster --name fleet-platform

.PHONY: load-test
load-test: ## Drive enough load to trigger the scorer's HPA
	k6 run test/load/hpa-trigger.js

# --- Cleanup -----------------------------------------------------------------

.PHONY: clean
clean: ## Remove build output and rendered manifests
	rm -rf bin $(RENDER_DIR) coverage.out
	rm -rf deploy/charts/*/charts deploy/charts/*/Chart.lock
	find . -name '__pycache__' -type d -prune -exec rm -rf {} + 2>/dev/null || true
