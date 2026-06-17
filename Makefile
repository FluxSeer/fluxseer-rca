APP=fluxagent
GO=GOWORK=off go

.PHONY: fmt test run run-operator run-manager demo-up demo-down install-demo inject-fault recover-demo demo-status build-images build-demo-images

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
	kubectl get deployment,pod,risksignal -n fluxagent-demo
	kubectl run curl-status -n fluxagent-demo --restart=Never --rm -i --image=curlimages/curl:8.8.0 -- curl -s http://fluxagent-observability:8080/demo/state

build-images:
	docker build -t fluxagent/operator:latest .

build-demo-images:
	docker build -t fluxagent/demo-observability:latest -f examples/fake-observability/Dockerfile .
