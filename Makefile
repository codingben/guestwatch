BINARY_NAME    ?= guestwatch
CMD_PATH       := ./cmd/guestwatch
CONTAINER_TOOL ?= docker
IMAGE_REGISTRY ?= ghcr.io/codingben
IMAGE_NAME     ?= guestwatch
IMAGE_TAG      ?= latest
IMAGE          := $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: build
build:
	go build -o $(BINARY_NAME) $(CMD_PATH)

.PHONY: ui-build
ui-build:
	cd ui && npm ci && npm run build

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
