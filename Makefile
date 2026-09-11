.PHONY: build test test-race lint chart-lint chart-render

build:
	go build ./...

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	go vet ./...

chart-lint:
	helm lint charts/kubevirt-ai-agent

chart-render:
	helm template test charts/kubevirt-ai-agent --namespace test \
		--set privacy.consoleEvidenceEgressAcknowledged=true \
		--set model.classifier=test-model >/tmp/kubevirt-ai-agent-rendered.yaml
