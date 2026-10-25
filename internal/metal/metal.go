// Package metal 用 cgo + Metal.framework 在进程内编译 MSL、把顶点/片元程序链接成渲染管线并取驱动反射
// （移植 vendor/metal-tools/reflect_batch.m，不起子进程）。反射结果的字段与 reflect_batch 的 JSON 相同。
package metal

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -O2 -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Metal -framework Foundation
#include <stdlib.h>
#include "reflect.h"
*/
import "C"

import (
	"encoding/json"
	"errors"
	"runtime"
	"unsafe"
)

// Device 是一个 MTLDevice。并发安全（Metal 设备的编译与建管线可多线程调用）。
type Device struct {
	ptr unsafe.Pointer
}

// NewDevice 打开系统默认 Metal 设备。
func NewDevice() (*Device, error) {
	p := C.dx2metal_device_new()
	if p == nil {
		return nil, errors.New("没有可用的 Metal 设备")
	}
	d := &Device{ptr: p}
	runtime.AddCleanup(d, func(p unsafe.Pointer) { C.dx2metal_release(p) }, p)
	return d, nil
}

// Library 是编译好的 MTLLibrary。
type Library struct {
	ptr unsafe.Pointer
}

// Release 释放库。
func (l *Library) Release() {
	if l != nil && l.ptr != nil {
		C.dx2metal_release(l.ptr)
		l.ptr = nil
	}
}

// Compile 以 MSL 2.1 编译源码。
func (d *Device) Compile(source string) (*Library, error) {
	cs := C.CString(source)
	defer C.free(unsafe.Pointer(cs))
	var cerr *C.char
	p := C.dx2metal_library_new(d.ptr, cs, &cerr)
	if p == nil {
		msg := "unknown"
		if cerr != nil {
			msg = C.GoString(cerr)
			C.free(unsafe.Pointer(cerr))
		}
		return nil, errors.New(msg)
	}
	return &Library{ptr: p}, nil
}

// Member 是反射出的结构体成员。
type Member struct {
	Name        string   `json:"name"`
	Offset      int      `json:"offset"`
	ArraySize   *int     `json:"array_size"`
	ArrayStride *int     `json:"array_stride"`
	Type        string   `json:"type"`
	Rows        int      `json:"rows"`
	Cols        int      `json:"cols"`
	Members     []Member `json:"members"`
	HasMembers  bool     `json:"-"`
}

// UnmarshalJSON 记录是否带 members 字段（Python 版按 "members" in member 判断）。
func (m *Member) UnmarshalJSON(b []byte) error {
	type plain Member
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(b, (*plain)(m)); err != nil {
		return err
	}
	_, m.HasMembers = raw["members"]
	return nil
}

// Argument 是反射出的着色器参数。
type Argument struct {
	Name        string   `json:"name"`
	Index       int      `json:"index"`
	Active      bool     `json:"active"`
	Kind        string   `json:"kind"` // buffer | texture | sampler | unsupported
	Size        int      `json:"size"`
	Alignment   int      `json:"alignment"`
	Members     []Member `json:"members"`
	TextureType int      `json:"texture_type"`
}

// VertexInput 是顶点着色器的活动输入属性。
type VertexInput struct {
	Name  string `json:"name"`
	Index int    `json:"index"`
	Type  string `json:"type"`
	Rows  int    `json:"rows"`
	Cols  int    `json:"cols"`
}

// Reflection 是一对程序的链接结果。
type Reflection struct {
	Linked       bool          `json:"linked"`
	Error        string        `json:"error"`
	VertexInputs []VertexInput `json:"vertex_inputs"`
	Vertex       []Argument    `json:"vertex"`
	Fragment     []Argument    `json:"fragment"`
}

// ReflectPair 链接两份库的 xlatMtlMain 并取反射；链接失败时 Linked=false、Error 为驱动的错误信息。
func (d *Device) ReflectPair(vertex, fragment *Library) (*Reflection, error) {
	out := C.dx2metal_reflect_pair(d.ptr, vertex.ptr, fragment.ptr)
	if out == nil {
		return nil, errors.New("反射失败")
	}
	defer C.free(unsafe.Pointer(out))
	r := &Reflection{}
	if err := json.Unmarshal([]byte(C.GoString(out)), r); err != nil {
		return nil, err
	}
	return r, nil
}
