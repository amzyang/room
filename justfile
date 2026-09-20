VERSION := env("VERSION", "dev")
LDFLAGS := "-X main.version=" + VERSION

# 列出全部命令
default:
    @just --list

# 构建二进制（本地默认版本号 dev；发布版本与 Sentry DSN 由 goreleaser 注入）
build:
    go build -ldflags "{{LDFLAGS}}" -o room .

# 构建后透传参数运行：just run list --json
run *ARGS: build
    ./room {{ARGS}}

# 全量测试（与 CI 一致）
test:
    go test ./...

# 全量测试（详细输出）
test-v:
    go test -v ./...

# 测试覆盖率报告
cover:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out | tail -1

# 覆盖率明细（浏览器打开逐行标注）
cover-html: cover
    go tool cover -html=coverage.out

# 格式检查（有待修文件即失败并列出，防 check 假绿）
fmt:
    @files="$(gofmt -l .)"; if [ -n "$files" ]; then echo "$files"; echo "gofmt 未通过：just fmt-fix"; exit 1; fi

# 格式修复
fmt-fix:
    gofmt -w .

vet:
    go vet ./...

# 整理依赖
tidy:
    go mod tidy

# 提交前检查：格式 + vet + 测试
check: fmt vet test

# 真实环境 e2e：只读命令验证凭证链与飞书 API 连通（不创建/取消任何日程）
e2e: build
    ./room --version
    ./room config list --json | jq -e .ok
    ./room whoami --json | jq -e .ok
    ./room list --json | jq .meta.count
    ./room list --mine --json | jq .meta.count

# 安装到 GOPATH/bin
install:
    go build -ldflags "{{LDFLAGS}}" -o "$(go env GOPATH)/bin/room" .

# 校验 goreleaser 配置
release-check:
    goreleaser check

# 本地发布演练：产出 dist/ 全平台产物，不推 tag、不更新 homebrew-tap
release:
    goreleaser release --snapshot --clean

clean:
    rm -rf room dist coverage.out
