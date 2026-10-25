module lom-dx2metal/cmd/dx2metal-gui

go 1.27.1

tool github.com/egoist/mygo/cmd/mygo

require (
	github.com/egoist/mygo v0.6.3
	lom-dx2metal v0.0.0
)

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/pierrec/lz4/v4 v4.1.33 // indirect
	github.com/ulikunitz/xz v0.5.17 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

// mygo v0.6.3 要求 go >= 1.27.1，根 module 是 go 1.26，所以 GUI 单独成 module，
// 通过 replace 使用同仓库的 convert 包；这样根 module 的 `go build ./...` 不会碰 GUI。
replace lom-dx2metal => ../..
