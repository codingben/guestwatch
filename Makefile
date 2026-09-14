BINARY_NAME    ?= kubevirt-ai-agent
CMD_PATH       := ./cmd/kubevirt-ai-agent
CONTAINER_TOOL ?= docker
IMAGE_REGISTRY ?= ghcr.io/codingben
IMAGE_NAME     ?= kubevirt-ai-agent
IMAGE_TAG      ?= latest
IMAGE          := $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: build
build:
	go build -o $(BINARY_NAME) $(CMD_PATH)

.PHONY: test
test:
	go test ./...

.PHONY: test-race
test-race:
	go test -race ./...

.PHONY: lint
lint:
	go vet ./...

.PHONY: image
image:
	$(CONTAINER_TOOL) build -t $(IMAGE) .

.PHONY: image-push
image-push:
	$(CONTAINER_TOOL) push $(IMAGE)

.PHONY: clean
clean:
	rm -f $(BINARY_NAME)
