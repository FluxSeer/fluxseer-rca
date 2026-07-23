APP=fluxagent
GO=GOWORK=off go
DEMO_PAUSE_SECONDS ?= 4
VERSION ?= dev
GIT_COMMIT := $(shell git rev-parse HEAD)
GIT_DIRTY := $(shell test -z "$$(git status --porcelain)" && echo false || echo true)
SOURCE_DATE_EPOCH := $(shell git show -s --format=%ct HEAD)
BUILD_DATE := $(shell ./hack/source-date.sh "$(SOURCE_DATE_EPOCH)")
VERSION_PACKAGE := fluxagent/internal/version
GO_LDFLAGS := -X $(VERSION_PACKAGE).Version=$(VERSION) -X $(VERSION_PACKAGE).GitCommit=$(GIT_COMMIT) -X $(VERSION_PACKAGE).GitDirty=$(GIT_DIRTY) -X $(VERSION_PACKAGE).BuildDate=$(BUILD_DATE)
OPERATOR_IMAGE ?= fluxagent/operator
DEMO_IMAGE ?= fluxagent/demo-observability
IMAGE_TAG ?= latest
OPERATOR_IMAGE_REF := $(OPERATOR_IMAGE):$(IMAGE_TAG)
DEMO_IMAGE_REF := $(DEMO_IMAGE):$(IMAGE_TAG)

.PHONY: fmt test run run-operator run-manager demo-up demo-down install-demo apply-riskrule inject-fault recover-demo demo-status demo-degrade-missing-datasource demo-degrade-capability-mismatch demo-degrade-provider-auth-failed demo-reset-riskrule demo-degrade-all verify-e2e-kind verify-investigation-kind verify-v0.2-alpha verify-artifact-identity build-images build-demo-images

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

run:
	$(GO) run -trimpath -ldflags "$(GO_LDFLAGS)" ./cmd/$(APP)

run-operator:
	$(GO) run -trimpath -ldflags "$(GO_LDFLAGS)" ./cmd/operator

run-manager:
	$(GO) run -trimpath -ldflags "$(GO_LDFLAGS)" ./cmd/manager

install-demo:
	kubectl create namespace fluxagent-system || true
	kubectl create namespace fluxagent-demo || true
	kubectl apply -k config/default
	kubectl wait --for=condition=Established --timeout=120s crd/agentactions.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/datasources.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/investigationrequests.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/modelproviders.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/remediationplans.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/riskrules.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/risksignals.aiops.platform
	kubectl apply -k examples/kind

apply-riskrule:
	kubectl apply -k examples/riskrules -n fluxagent-demo

demo-up:
	kind create cluster --name fluxagent-demo --config examples/kind/kind-config.yaml
	$(MAKE) build-images
	$(MAKE) build-demo-images
	kind load docker-image $(OPERATOR_IMAGE_REF) --name fluxagent-demo
	kind load docker-image $(DEMO_IMAGE_REF) --name fluxagent-demo
	$(MAKE) install-demo

demo-down:
	kind delete cluster --name fluxagent-demo

inject-fault:
	kubectl patch deployment fluxagent-sample -n fluxagent-demo --type merge -p '{"spec":{"template":{"spec":{"containers":[{"name":"app","image":"busybox:1.36","command":["sh","-c","echo crashloop; exit 1"]}]}}}}'
	kubectl run curl-fault -n fluxagent-demo --restart=Never --rm -i --image=curlimages/curl:8.8.0 -- curl -s -XPOST http://fluxagent-observability:8080/demo/fault/fluxagent-sample

recover-demo:
	kubectl apply -k examples/sample-app
	kubectl run curl-recover -n fluxagent-demo --restart=Never --rm -i --image=curlimages/curl:8.8.0 -- curl -s -XPOST http://fluxagent-observability:8080/demo/recover/fluxagent-sample

demo-status:
	kubectl get deployment,pod,datasource,riskrule,risksignal -n fluxagent-demo
	kubectl describe datasource prometheus -n fluxagent-demo
	kubectl describe riskrule fluxagent-sample-latency -n fluxagent-demo
	kubectl describe risksignal fluxagent-sample-latency-fluxagent-sample-risk -n fluxagent-demo || true
	kubectl run curl-status -n fluxagent-demo --restart=Never --rm -i --image=curlimages/curl:8.8.0 -- curl -s http://fluxagent-observability:8080/demo/state

demo-degrade-missing-datasource:
	$(MAKE) inject-fault
	bash examples/kind/degraded-demo.sh missing-datasource

demo-degrade-capability-mismatch:
	$(MAKE) inject-fault
	bash examples/kind/degraded-demo.sh capability-mismatch

demo-degrade-provider-auth-failed:
	$(MAKE) inject-fault
	bash examples/kind/degraded-demo.sh provider-auth-failed

demo-reset-riskrule:
	bash examples/kind/degraded-demo.sh reset

demo-degrade-all:
	@printf '\n%s\n' "============================================================"
	@printf '%s\n' "Section 1/4: Baseline Fault Injection"
	@printf '%s\n' "============================================================"
	$(MAKE) inject-fault
	@printf '\n%s\n' "Pause $(DEMO_PAUSE_SECONDS)s before degraded case 1 ..."
	@sleep $(DEMO_PAUSE_SECONDS)
	@printf '\n%s\n' "============================================================"
	@printf '%s\n' "Section 2/4: Degraded Case - Missing DataSource"
	@printf '%s\n' "============================================================"
	bash examples/kind/degraded-demo.sh missing-datasource
	@printf '\n%s\n' "Pause $(DEMO_PAUSE_SECONDS)s before reset ..."
	@sleep $(DEMO_PAUSE_SECONDS)
	@printf '\n%s\n' "============================================================"
	@printf '%s\n' "Section 3/4: Reset Rule To Baseline"
	@printf '%s\n' "============================================================"
	bash examples/kind/degraded-demo.sh reset
	@printf '\n%s\n' "Pause $(DEMO_PAUSE_SECONDS)s before degraded case 2 ..."
	@sleep $(DEMO_PAUSE_SECONDS)
	@printf '\n%s\n' "============================================================"
	@printf '%s\n' "Section 4/4: Degraded Case - Capability Mismatch"
	@printf '%s\n' "============================================================"
	bash examples/kind/degraded-demo.sh capability-mismatch
	@printf '\n%s\n' "Pause $(DEMO_PAUSE_SECONDS)s before final reset ..."
	@sleep $(DEMO_PAUSE_SECONDS)
	@printf '\n%s\n' "============================================================"
	@printf '%s\n' "Final Reset: Restore Baseline Rule"
	@printf '%s\n' "============================================================"
	bash examples/kind/degraded-demo.sh reset
	@printf '\n%s\n' "Recording flow complete."

verify-e2e-kind:
	bash test/e2e/kind/verify_e2e_kind.sh

verify-investigation-kind:
	bash test/e2e/kind/verify_investigation_kind.sh

verify-v0.2-alpha:
	$(GO) test ./...
	kubectl kustomize config/default >/tmp/fluxagent-config-default.yaml
	kubectl kustomize examples/kind >/tmp/fluxagent-kind-example.yaml

verify-artifact-identity: build-images build-demo-images
	VERSION=$(VERSION) GIT_COMMIT=$(GIT_COMMIT) GIT_DIRTY=$(GIT_DIRTY) BUILD_DATE=$(BUILD_DATE) OPERATOR_IMAGE_REF=$(OPERATOR_IMAGE_REF) DEMO_IMAGE_REF=$(DEMO_IMAGE_REF) bash hack/verify-artifact-identity.sh

build-images:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg GIT_DIRTY=$(GIT_DIRTY) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(OPERATOR_IMAGE_REF) .

build-demo-images:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg GIT_DIRTY=$(GIT_DIRTY) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(DEMO_IMAGE_REF) -f examples/fake-observability/Dockerfile .
