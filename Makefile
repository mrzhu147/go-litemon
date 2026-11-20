APP := go-litemon
.PHONY: all build run dump shot tidy clean
all: build
build: ## 原生 Qt5 面板（需要 qtbase5-dev / qt5-qtbase-devel + g++）
	CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o $(APP) .
run: build
	./$(APP)
dump: build
	./$(APP) --dump
shot: build
	QT_QPA_PLATFORM=offscreen ./$(APP) --screenshot screenshot.png
tidy:
	go mod tidy
clean:
	rm -f $(APP)
