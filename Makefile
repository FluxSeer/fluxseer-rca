APP=fluxseer
GO=GOWORK=off go
DEMO_PAUSE_SECONDS ?= 4
VERSION ?= dev
RELEASE_VERSION ?= $(if $(filter dev,$(VERSION)),v0.4.0-beta.3,$(VERSION))
V0_3_RELEASE_VERSION ?= v0.3.0-beta.3
V0_3_PREVIOUS_RELEASE_VERSION ?= v0.3.0-beta.2
V0_3_PUBLISHED_CHART_OCI ?= oci://ghcr.io/fluxseer/fluxseer-rca/charts/fluxseer-rca
V0_3_PUBLISHED_IMAGE_REPOSITORY ?= ghcr.io/fluxseer/fluxseer-rca/operator
V0_4_RELEASE_VERSION ?= v0.4.0-beta.3
V0_4_PREVIOUS_RELEASE_VERSION ?= v0.4.0-beta.2
V0_4_PUBLISHED_CHART_OCI ?= oci://ghcr.io/fluxseer/fluxseer-rca/charts/fluxseer-rca
V0_4_PUBLISHED_IMAGE_REPOSITORY ?= ghcr.io/fluxseer/fluxseer-rca/operator
GIT_COMMIT := $(shell git rev-parse HEAD)
GIT_DIRTY := $(shell test -z "$$(git status --porcelain)" && echo false || echo true)
SOURCE_DATE_EPOCH := $(shell git show -s --format=%ct HEAD)
BUILD_DATE := $(shell ./hack/source-date.sh "$(SOURCE_DATE_EPOCH)")
VERSION_PACKAGE := github.com/FluxSeer/fluxseer-rca/internal/version
GO_LDFLAGS := -X $(VERSION_PACKAGE).Version=$(VERSION) -X $(VERSION_PACKAGE).GitCommit=$(GIT_COMMIT) -X $(VERSION_PACKAGE).GitDirty=$(GIT_DIRTY) -X $(VERSION_PACKAGE).BuildDate=$(BUILD_DATE)
GO_BUILD_FLAGS := -trimpath -buildvcs=false
OPERATOR_IMAGE ?= fluxseer/fluxseer-rca/operator
IMAGE_REPOSITORY ?= $(OPERATOR_IMAGE)
DEMO_IMAGE ?= fluxseer/fluxseer-rca/demo-observability
DEMO_IMAGE_REPOSITORY ?= $(DEMO_IMAGE)
IMAGE_TAG ?= $(VERSION)
TARGET_PLATFORM ?= linux/amd64
CHART_VERSION := $(patsubst v%,%,$(VERSION))
OPERATOR_IMAGE_REF := $(IMAGE_REPOSITORY):$(IMAGE_TAG)
DEMO_IMAGE_REF := $(DEMO_IMAGE_REPOSITORY):$(IMAGE_TAG)

.PHONY: fmt lint test run run-operator run-manager demo-up demo-down install-demo apply-riskrule inject-fault recover-demo demo-status demo-degrade-missing-datasource demo-degrade-capability-mismatch demo-degrade-provider-auth-failed demo-reset-riskrule demo-degrade-all verify-e2e-kind verify-investigation-kind verify-lifecycle-kind verify-v0.5-alpha1-live-harness verify-v0.5-alpha1-kind-rca verify-v0.3-beta-upgrade-kind verify-v0.2-alpha verify-v0.2-beta verify-v0.3-schema-freeze verify-v0.3-beta-hardening verify-v0.4-approval-lifecycle verify-runtime-provider-policy-cluster verify-runtime-matrix-cluster verify-runtime-canonical-workloads-cluster verify-runtime-riskrule-incidents-cluster verify-runtime-traffic-pattern-conformance-cluster verify-runtime-prometheus-pattern-conformance-cluster verify-runtime-public-report-catalog export-runtime-public-reports verify-rbac-profiles verify-rule-packs verify-detection-pattern-catalog verify-traffic-pattern-promql verify-prometheus-pattern-promql verify-docs-contract verify-rule-packs-kind verify-artifact-identity verify-packaging-consistency verify-build-reproducibility verify-release-inputs verify-release-cleanup verify-release-pretag verify-release-v0.2-beta verify-release-v0.3-beta verify-release-v0.3-rc build-images build-demo-images

fmt:
	$(GO) fmt ./...

lint:
	GOWORK=off golangci-lint run ./...

test:
	$(GO) test ./...

run:
	$(GO) run $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" ./cmd/$(APP)

run-operator:
	$(GO) run $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" ./cmd/operator

run-manager:
	$(GO) run $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" ./cmd/manager

install-demo:
	kubectl create namespace fluxseer-rca-system || true
	kubectl create namespace fluxseer-rca-demo || true
	IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY) IMAGE_TAG=$(IMAGE_TAG) bash hack/render-release-kustomize.sh config/default | kubectl apply -f -
	kubectl wait --for=condition=Established --timeout=120s crd/agentactions.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/datasources.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/investigationrequests.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/modelproviders.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/remediationplans.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/riskrules.aiops.platform
	kubectl wait --for=condition=Established --timeout=120s crd/risksignals.aiops.platform
	IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY) IMAGE_TAG=$(IMAGE_TAG) bash hack/render-release-kustomize.sh examples/kind | kubectl apply -f -

apply-riskrule:
	kubectl apply -k examples/riskrules -n fluxseer-rca-demo

demo-up:
	kind create cluster --name fluxseer-rca-demo --config examples/kind/kind-config.yaml
	$(MAKE) build-images
	$(MAKE) build-demo-images
	kind load docker-image $(OPERATOR_IMAGE_REF) --name fluxseer-rca-demo
	kind load docker-image $(DEMO_IMAGE_REF) --name fluxseer-rca-demo
	$(MAKE) install-demo

demo-down:
	kind delete cluster --name fluxseer-rca-demo

inject-fault:
	kubectl patch deployment fluxseer-rca-sample -n fluxseer-rca-demo --type merge -p '{"spec":{"template":{"spec":{"containers":[{"name":"app","image":"busybox:1.36","command":["sh","-c","echo crashloop; exit 1"]}]}}}}'
	kubectl run curl-fault -n fluxseer-rca-demo --restart=Never --rm -i --image=curlimages/curl:8.8.0 -- curl -s -XPOST http://fluxseer-rca-observability:8080/demo/fault/fluxseer-rca-sample

recover-demo:
	kubectl apply -k examples/sample-app
	kubectl run curl-recover -n fluxseer-rca-demo --restart=Never --rm -i --image=curlimages/curl:8.8.0 -- curl -s -XPOST http://fluxseer-rca-observability:8080/demo/recover/fluxseer-rca-sample

demo-status:
	kubectl get deployment,pod,datasource,riskrule,risksignal -n fluxseer-rca-demo
	kubectl describe datasource prometheus -n fluxseer-rca-demo
	kubectl describe riskrule fluxseer-rca-sample-latency -n fluxseer-rca-demo
	signal_name="$$(kubectl get risksignal -n fluxseer-rca-demo -l fluxseer-rca.aiops.platform/risk-rule=fluxseer-rca-sample-latency --sort-by=.metadata.creationTimestamp -o 'jsonpath={range .items[?(@.spec.target.name=="fluxseer-rca-sample")]}{.metadata.name}{"\n"}{end}' 2>/dev/null | tail -n1)"; if [ -n "$$signal_name" ]; then kubectl describe risksignal "$$signal_name" -n fluxseer-rca-demo; fi
	kubectl run curl-status -n fluxseer-rca-demo --restart=Never --rm -i --image=curlimages/curl:8.8.0 -- curl -s http://fluxseer-rca-observability:8080/demo/state

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

verify-lifecycle-kind:
	VERSION=$(VERSION) IMAGE_TAG=$(IMAGE_TAG) TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY) bash test/e2e/kind/verify_lifecycle_kind.sh

verify-v0.5-alpha1-live-harness:
	VERSION=$(VERSION) IMAGE_TAG=$(IMAGE_TAG) TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) bash test/e2e/kind/verify_v0_5_alpha1_live_harness.sh

verify-v0.5-alpha1-kind-rca:
	VERSION=$(VERSION) IMAGE_TAG=$(IMAGE_TAG) TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) bash test/e2e/kind/verify_v0_5_alpha1_kind_rca.sh

verify-v0.3-beta-upgrade-kind:
	VERSION=$(V0_3_RELEASE_VERSION) PREVIOUS_VERSION=$(V0_3_PREVIOUS_RELEASE_VERSION) PUBLISHED_CHART_OCI=$(V0_3_PUBLISHED_CHART_OCI) PUBLISHED_IMAGE_REPOSITORY=$(V0_3_PUBLISHED_IMAGE_REPOSITORY) IMAGE_TAG=$(IMAGE_TAG) TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY) bash test/e2e/kind/verify_v0_3_beta_upgrade_kind.sh

verify-v0.2-beta:
	$(GO) test ./...
	kubectl kustomize config/default >/tmp/fluxseer-rca-config-default.yaml
	kubectl kustomize examples/kind >/tmp/fluxseer-rca-kind-example.yaml

verify-v0.2-alpha: verify-v0.2-beta

verify-v0.3-schema-freeze:
	bash hack/verify-v0.3-schema-freeze.sh

verify-v0.3-beta-hardening:
	bash hack/verify-v0.3-beta-hardening.sh

verify-v0.4-approval-lifecycle:
	bash hack/verify-v0.4-approval-lifecycle.sh

verify-runtime-provider-policy-cluster:
	bash test/e2e/runtime/verify_cluster_matrix.sh

verify-runtime-matrix-cluster:
	bash test/e2e/runtime/verify_p0_cluster_matrix.sh

verify-runtime-canonical-workloads-cluster:
	bash test/e2e/runtime/verify_canonical_workloads.sh

verify-runtime-riskrule-incidents-cluster:
	bash test/e2e/runtime/verify_riskrule_incidents.sh

verify-runtime-traffic-pattern-conformance-cluster:
	bash test/e2e/runtime/verify_traffic_pattern_conformance.sh

verify-runtime-prometheus-pattern-conformance-cluster:
	bash test/e2e/runtime/verify_prometheus_pattern_conformance.sh

verify-runtime-public-report-catalog:
	bash hack/verify-public-report-catalog.sh test/e2e/runtime/public_report_scenarios.json

export-runtime-public-reports:
	bash hack/export-public-riskrule-reports.sh

verify-rbac-profiles:
	bash hack/verify-rbac-profiles.sh

verify-rule-packs:
	bash hack/verify-rule-packs.sh

verify-detection-pattern-catalog:
	bash hack/verify-detection-pattern-catalog.sh

verify-traffic-pattern-promql:
	bash hack/verify-traffic-pattern-promql.sh

verify-prometheus-pattern-promql:
	bash hack/verify-prometheus-pattern-promql.sh

verify-docs-contract:
	bash hack/verify-docs-contract.sh

verify-rule-packs-kind:
	VERSION=$(VERSION) IMAGE_TAG=$(IMAGE_TAG) TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY) bash test/e2e/kind/verify_rule_packs_kind.sh

verify-artifact-identity: build-images build-demo-images
	VERSION=$(VERSION) GIT_COMMIT=$(GIT_COMMIT) GIT_DIRTY=$(GIT_DIRTY) BUILD_DATE=$(BUILD_DATE) TARGET_PLATFORM=$(TARGET_PLATFORM) OPERATOR_IMAGE_REF=$(OPERATOR_IMAGE_REF) DEMO_IMAGE_REF=$(DEMO_IMAGE_REF) bash hack/verify-artifact-identity.sh

verify-packaging-consistency:
	VERSION=$(VERSION) CHART_VERSION=$(CHART_VERSION) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY) IMAGE_TAG=$(IMAGE_TAG) bash hack/verify-packaging-consistency.sh

verify-build-reproducibility:
	VERSION=$(VERSION) GIT_COMMIT=$(GIT_COMMIT) GIT_DIRTY=$(GIT_DIRTY) BUILD_DATE=$(BUILD_DATE) SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY) IMAGE_TAG=$(IMAGE_TAG) TARGET_PLATFORM=$(TARGET_PLATFORM) bash hack/verify-build-reproducibility.sh

verify-release-inputs:
	VERSION=$(RELEASE_VERSION) bash hack/verify-release-inputs.sh

verify-release-cleanup:
	VERSION=$(RELEASE_VERSION) bash hack/verify-release-cleanup.sh

verify-release-pretag:
	VERSION=$(RELEASE_VERSION) bash hack/verify-release-pretag.sh

verify-release-v0.2-beta:
	$(MAKE) verify-release-inputs VERSION=$(RELEASE_VERSION)
	$(MAKE) verify-v0.2-beta
	$(MAKE) verify-rule-packs
	$(MAKE) verify-rule-packs-kind VERSION=$(RELEASE_VERSION) IMAGE_TAG=release-rulepack-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-e2e-kind VERSION=$(RELEASE_VERSION) IMAGE_TAG=release-e2e-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-investigation-kind VERSION=$(RELEASE_VERSION) IMAGE_TAG=release-investigation-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-artifact-identity VERSION=$(RELEASE_VERSION) IMAGE_TAG=release-identity-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-packaging-consistency VERSION=$(RELEASE_VERSION) IMAGE_TAG=release-packaging-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-build-reproducibility VERSION=$(RELEASE_VERSION) IMAGE_TAG=release-reproducibility-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-lifecycle-kind VERSION=$(RELEASE_VERSION) IMAGE_TAG=release-lifecycle-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-release-cleanup VERSION=$(RELEASE_VERSION)

verify-release-v0.3-beta:
	$(MAKE) verify-release-inputs VERSION=$(V0_3_RELEASE_VERSION)
	$(MAKE) verify-v0.3-schema-freeze
	$(MAKE) verify-v0.2-beta
	$(MAKE) verify-rule-packs
	$(MAKE) verify-rule-packs-kind VERSION=$(V0_3_RELEASE_VERSION) IMAGE_TAG=release-rulepack-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-e2e-kind VERSION=$(V0_3_RELEASE_VERSION) IMAGE_TAG=release-e2e-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-investigation-kind VERSION=$(V0_3_RELEASE_VERSION) IMAGE_TAG=release-investigation-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-artifact-identity VERSION=$(V0_3_RELEASE_VERSION) IMAGE_TAG=release-identity-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-packaging-consistency VERSION=$(V0_3_RELEASE_VERSION) IMAGE_TAG=release-packaging-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-build-reproducibility VERSION=$(V0_3_RELEASE_VERSION) IMAGE_TAG=release-reproducibility-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-lifecycle-kind VERSION=$(V0_3_RELEASE_VERSION) IMAGE_TAG=release-lifecycle-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-v0.3-beta-upgrade-kind V0_3_RELEASE_VERSION=$(V0_3_RELEASE_VERSION) IMAGE_TAG=release-upgrade-test TARGET_PLATFORM=$(TARGET_PLATFORM) IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) DEMO_IMAGE_REPOSITORY=$(DEMO_IMAGE_REPOSITORY)
	$(MAKE) verify-release-cleanup VERSION=$(V0_3_RELEASE_VERSION)

verify-release-v0.3-rc: verify-release-v0.3-beta

build-images:
	docker buildx build --load --platform $(TARGET_PLATFORM) --provenance=false --sbom=false \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg GIT_DIRTY=$(GIT_DIRTY) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--build-arg SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) \
		-t $(OPERATOR_IMAGE_REF) .

build-demo-images:
	docker buildx build --load --platform $(TARGET_PLATFORM) --provenance=false --sbom=false \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg GIT_DIRTY=$(GIT_DIRTY) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--build-arg SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH) \
		-t $(DEMO_IMAGE_REF) -f examples/fake-observability/Dockerfile .
