// Package hlslcc 用 cgo 静态链接 third_party/HLSLcc（Unity 的 DXBC→MSL 交叉编译器，含 null-guard 补丁），
// 进程内把 DXBC 翻译成 Metal 源码：无子进程、无临时文件。
//
// null-guard 补丁（third_party/HLSLcc/0001-null-guard-optimized-unity-dxbc.patch）必须保留：
// HLSLcc 在 cgo 里崩溃会拖垮整个进程（包括 GUI）。
package hlslcc

/*
#cgo CFLAGS: -O2 -w -I${SRCDIR}/../../third_party/HLSLcc/src/cbstring
#cgo CXXFLAGS: -std=c++11 -O2 -w -I${SRCDIR}/../../third_party/HLSLcc -I${SRCDIR}/../../third_party/HLSLcc/include -I${SRCDIR}/../../third_party/HLSLcc/src -I${SRCDIR}/../../third_party/HLSLcc/src/cbstring -I${SRCDIR}/../../third_party/HLSLcc/src/internal_includes
#include <stdlib.h>
#include "shim.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

// Unity 的 HLSLcc 标志：TRANSLATE_MATRICES | REMOVE_UNUSED_GLOBALS | METAL_SHADOW_SAMPLER_LINEAR。
const UnityFlags = 0x20000 | 0x10000 | 0x80000

// HLSLcc 内部是否完全可重入没有保证，串行调用（翻译本身很快，并行收益在 Metal 编译）。
var mu sync.Mutex

// ToMetal 把完整 DXBC 容器翻译成 MSL。
func ToMetal(dxbc []byte, flags uint32) (string, error) {
	if len(dxbc) < 32 || string(dxbc[:4]) != "DXBC" {
		return "", errors.New("不是 DXBC 容器")
	}
	mu.Lock()
	defer mu.Unlock()
	src := C.CBytes(dxbc)
	defer C.free(src)
	var out *C.char
	var n C.size_t
	// 锁定 OS 线程：C 侧按线程识别崩溃。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch rc := C.dx2metal_hlslcc_to_metal((*C.char)(src), C.size_t(len(dxbc)), C.uint(flags), &out, &n); {
	case rc == 0:
		return "", errors.New("HLSLcc 翻译失败")
	case rc < 0:
		return "", fmt.Errorf("HLSLcc 崩溃（信号 %d），已隔离", -rc)
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoStringN(out, C.int(n)), nil
}
