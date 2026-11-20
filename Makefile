APP := go-litemon
.PHONY: all build run test clean tidy
all: build
build: ## 静态单二进制（无 cgo 依赖）
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(APP) .
run:
	go run . --listen 0.0.0.0:9527
tidy:
	go mod tidy
clean:
	rm -f $(APP)
