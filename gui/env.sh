# GUI 构建环境。根 go.mod 已是 go 1.27.1，GOTOOLCHAIN=auto 会按需下载对应工具链，不需要手动指定。
export GOTOOLCHAIN=auto GOFLAGS=-modcacherw GOTELEMETRY=off
# core 包用 cgo 链接 HLSLcc 与 Metal；mygo build 默认会把 CGO_ENABLED 设为 0，这里显式打开。
export CGO_ENABLED=1
