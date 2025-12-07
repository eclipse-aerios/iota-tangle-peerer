# output dir
OUT_DIR := ./_out
# dir for tools: e.g., golangci-lint
TOOL_DIR := $(OUT_DIR)/tool
# use golangci-lint for static code check
GOLANGCI_VERSION := v1.61.0
GOLANGCI_BIN := $(TOOL_DIR)/golangci-lint

# multi-arch platforms
PLATFORMS ?= linux/amd64,linux/arm64

# default target
all: fmt lint test

lint: golangci

.PHONY: golangci
golangci: $(GOLANGCI_BIN)
	@echo === running golangci-lint
	@$(TOOL_DIR)/golangci-lint --config=golangci.yml run ./...

$(GOLANGCI_BIN):
	@echo === installing golangci-lint
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | bash -s -- -b $(TOOL_DIR) $(GOLANGCI_VERSION)

# tests
test: mod-check unit-test

.PHONY: mod-check
mod-check:
	@echo === running go mod verify
	@go mod verify

.PHONY: unit-test
unit-test:
	@echo === running unit test
	@go test --timeout 10s -v ./...

.PHONY: fmt
fmt: $(GOLANGCI_BIN)
	@echo === running golangci-lint fix
	@$(TOOL_DIR)/golangci-lint --fix --config=golangci.yml run ./...


IMG_VERSION ?= latest
IMG ?= iota-peerer:${IMG_VERSION}


.PHONY: docker-build
docker-build: test
	docker build -t ${IMG} .

.PHONY: docker-build-multiarch
docker-build-multiarch: test
	docker buildx build --load --provenance=false --platform=${PLATFORMS} -t ${IMG} .

.PHONY: docker-build-push-multiarch
docker-build-push-multiarch: test
	docker buildx build --push --provenance=false --platform=${PLATFORMS} -t ${IMG} .

.PHONY: docker-push
docker-push: test
	docker push ${IMG}
