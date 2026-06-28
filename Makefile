APP=fluxagent
GO=GOWORK=off go
DEMO_PAUSE_SECONDS ?= 4

.PHONY: fmt test run run-operator run-manager demo-up demo-down install-demo apply-riskrule inject-fault recover-demo demo-status demo-degrade-missing-datasource demo-degrade-capability-mismatch demo-reset-riskrule demo-degrade-all verify-v0.2-alpha build-images build-demo-images

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

run:
	$(GO) run ./cmd/$(APP)

run-operator:
	$(GO) run ./cmd/operator

run-manager:
	$(GO) run ./cmd/manager

install-demo:
	kubectl create namespace fluxagent-demo || true
	kubectl apply -k examples/kind

apply-riskrule:
	kubectl apply -k examples/riskrules -n fluxagent-demo

demo-up:
	kind create cluster --name fluxagent-demo --config examples/kind/kind-config.yaml
	$(MAKE) build-images
	$(MAKE) build-demo-images
	kind load docker-image fluxagent/operator:latest --name fluxagent-demo
	kind load docker-image fluxagent/demo-observability:latest --name fluxagent-demo
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

verify-v0.2-alpha:
	$(GO) test ./...
	kubectl kustomize config/default >/tmp/fluxagent-config-default.yaml
	kubectl kustomize examples/kind >/tmp/fluxagent-kind-example.yaml

build-images:
	docker build -t fluxagent/operator:latest .

build-demo-images:
	docker build -t fluxagent/demo-observability:latest -f examples/fake-observability/Dockerfile .
