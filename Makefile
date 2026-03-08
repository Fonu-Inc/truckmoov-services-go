SHELL := /bin/bash

IMAGE_REPO ?= truckmoov
IMAGE_TAG ?= local
LOCAL_PLATFORM ?= linux/amd64
PUSH_PLATFORMS ?= linux/amd64,linux/arm64

.PHONY: run-notification run-tracking run-dispatch fmt vet proto \
	docker-build-local docker-build-dispatch docker-build-notification docker-build-tracking \
	docker-buildx-push docker-buildx-push-dispatch docker-buildx-push-notification docker-buildx-push-tracking

run-notification:
	go run ./cmd/notification

run-tracking:
	go run ./cmd/tracking

run-dispatch:
	go run ./cmd/dispatch

fmt:
	gofmt -w ./cmd ./pkg

vet:
	go vet ./...

proto:
	@echo "Proto generation placeholder. Use buf or protoc when toolchain is installed."

docker-build-local: docker-build-dispatch docker-build-notification docker-build-tracking

docker-build-dispatch:
	docker build --platform $(LOCAL_PLATFORM) -f Dockerfile.dispatch -t $(IMAGE_REPO)/dispatch:$(IMAGE_TAG) .

docker-build-notification:
	docker build --platform $(LOCAL_PLATFORM) -f Dockerfile.notification -t $(IMAGE_REPO)/notification:$(IMAGE_TAG) .

docker-build-tracking:
	docker build --platform $(LOCAL_PLATFORM) -f Dockerfile.tracking -t $(IMAGE_REPO)/tracking:$(IMAGE_TAG) .

docker-buildx-push: docker-buildx-push-dispatch docker-buildx-push-notification docker-buildx-push-tracking

docker-buildx-push-dispatch:
	docker buildx build --platform $(PUSH_PLATFORMS) -f Dockerfile.dispatch -t $(IMAGE_REPO)/dispatch:$(IMAGE_TAG) --push .

docker-buildx-push-notification:
	docker buildx build --platform $(PUSH_PLATFORMS) -f Dockerfile.notification -t $(IMAGE_REPO)/notification:$(IMAGE_TAG) --push .

docker-buildx-push-tracking:
	docker buildx build --platform $(PUSH_PLATFORMS) -f Dockerfile.tracking -t $(IMAGE_REPO)/tracking:$(IMAGE_TAG) --push .
