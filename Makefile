APP_NAME := ohmyssh
GO_MODULE := github.com/maogou/ohmyssh/cmd/ohmyssh

# 版本号注入到 internal/constant,不传时与常量里的默认值保持一致
VERSION ?= 0.1.0
LDFLAGS := -s -w -X github.com/maogou/ohmyssh/internal/constant.version=$(VERSION)

# golangci-lint 钉死版本:它的依赖图很大会污染主模块的依赖版本,所以不放进 go.mod,
# 而是装到本地 .bin 里。CI 的 lint job 用的是同一个版本,改这里要一起改。
GOLANGCI_VERSION := v2.13.2
BIN := $(CURDIR)/.bin
GOLANGCI := $(BIN)/golangci-lint

# 覆盖率报告用浏览器打开,按操作系统选择命令
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
OPEN_CMD := open
endif
ifeq ($(UNAME_S),Linux)
OPEN_CMD := xdg-open
endif

.PHONY: build run lint test coverage clean

build:
	@echo "编译二进制文件..."
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o ${APP_NAME} ${GO_MODULE}

run:
	@echo "运行 ohmyssh..."
	go run ./cmd/${APP_NAME}/ $(ARGS)

lint: ${GOLANGCI}
	@echo "代码检查..."
	${GOLANGCI} run ./...

# 已经装过就不重复装,删掉 .bin 或改 GOLANGCI_VERSION 会重新装
${GOLANGCI}:
	@echo "安装 golangci-lint ${GOLANGCI_VERSION} 到 .bin ..."
	@mkdir -p ${BIN}
	GOBIN=${BIN} go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_VERSION}

# -race 在本地是零成本,而项目里并发不少:传输进度、SIGWINCH 监听、bubbletea 的 Cmd
test:
	@echo "运行测试..."
	go test -race ./...

coverage:
	@echo "运行测试并统计覆盖率..."
	go test -race -coverprofile=./coverage.out ./...
	@echo "生成覆盖率报告..."
	go tool cover -html=coverage.out -o coverage.html
	@if [ -n "$(OPEN_CMD)" ]; then echo "打开 coverage.html ..."; $(OPEN_CMD) coverage.html; else echo "请手动打开 coverage.html"; fi

clean:
	@echo "清理构建产物..."
	rm -f ${APP_NAME}
	@echo "清理覆盖率报告..."
	rm -f coverage.out coverage.html
