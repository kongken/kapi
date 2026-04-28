APP_NAME := starter-service
BUILD_DIR := bin

.PHONY: tidy proto proto-lint build run clean docker-build

tidy:
	go mod tidy

proto:
	buf generate

proto-lint:
	buf lint

build:
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/service

run:
	BUTTERFLY_CONFIG_TYPE=file \
	BUTTERFLY_CONFIG_FILE_PATH=./config.yaml \
	BUTTERFLY_TRACING_DISABLE=true \
	go run ./cmd/service

clean:
	rm -rf $(BUILD_DIR)

docker-build:
	docker build -t $(APP_NAME):latest .
