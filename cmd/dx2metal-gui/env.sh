# 把 Go 模块缓存与构建缓存放到仓库之外的用户缓存目录（不碰系统目录，也不会被 git/gofmt 扫到）。
export GOPATH="$HOME/Library/Caches/dx2metal-gui/gopath" GOMODCACHE="$HOME/Library/Caches/dx2metal-gui/gopath/pkg/mod" GOCACHE="$HOME/Library/Caches/dx2metal-gui/gocache"
export GOFLAGS=-modcacherw GOTOOLCHAIN=go1.27.1 GOTELEMETRY=off
# convert 包用 cgo 链接 HLSLcc 与 Metal；mygo build 默认会把 CGO_ENABLED 设为 0，这里显式打开。
export CGO_ENABLED=1
