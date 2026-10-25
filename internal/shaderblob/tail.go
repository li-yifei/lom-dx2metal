package shaderblob

import (
	"errors"
	"fmt"
)

// Legacy tail 资源绑定种类。
const (
	KindTexture       = 0 // 纹理：index = 槽位，aux = 采样器槽位或 -1，texture_flags = dim<<1 | 多重采样
	KindConstBuffer   = 1 // 常量缓冲区：index = 槽位，aux = 0
	KindStructured    = 2 // StructuredBuffer：(name, kind, index, aux)，aux 原样取自 D3D tail
	KindInlineSampler = 4 // 内联采样器：name 为空，index = 槽位，aux = 采样器状态（见根因三）
)

// Channel 是顶点输入通道映射。
type Channel struct {
	Source, Target uint32
}

// Channels 是顶点通道表。
type Channels struct {
	SourceMap uint32
	Channels  []Channel
}

// Member 是常量缓冲区成员。
type Member struct {
	Name                                        string
	Type, Rows, Cols, Matrix, ArraySize, Offset uint32
}

// Buffer 是一个常量缓冲区。
type Buffer struct {
	Name   string
	Size   uint32
	Params []Member
}

// Binding 是一个资源绑定。
type Binding struct {
	Name         string
	Kind         uint32
	Index        uint32
	Aux          uint32
	TextureFlags uint32 // 只有 Kind == KindTexture 时存在
}

// Tail 是解析后的 legacy 参数 tail。
type Tail struct {
	Channels Channels
	Buffers  []Buffer
	Bindings []Binding
}

// ParseTail 读取 legacy tail，规则与 shader_blob.legacy_parameters 一致（含全部校验）。
//
// 布局取自原版 Blit/CopyTexture 程序与 Unity 原生加载函数 LoadGpuProgramFromData（0x6536e0；资源分派 0x65442c）。
func ParseTail(data []byte) (*Tail, error) {
	r := &reader{b: data}
	count := func() (uint32, error) {
		v, err := r.uint()
		if err == nil && v > 65536 {
			err = errors.New("legacy 参数数量超过检查上限")
		}
		return v, err
	}
	text := func() (string, error) {
		b, err := r.alignedBytes()
		return string(b), err
	}
	t := &Tail{}
	var err error
	if t.Channels.SourceMap, err = r.uint(); err != nil {
		return nil, err
	}
	n, err := count()
	if err != nil {
		return nil, err
	}
	t.Channels.Channels = make([]Channel, 0, n)
	for range n {
		s, err1 := r.uint()
		d, err2 := r.uint()
		if err := errors.Join(err1, err2); err != nil {
			return nil, err
		}
		t.Channels.Channels = append(t.Channels.Channels, Channel{s, d})
	}
	nb, err := count()
	if err != nil {
		return nil, err
	}
	for range nb {
		var b Buffer
		if b.Name, err = text(); err != nil {
			return nil, err
		}
		if b.Size, err = r.uint(); err != nil {
			return nil, err
		}
		np, err := count()
		if err != nil {
			return nil, err
		}
		for range np {
			var m Member
			if m.Name, err = text(); err != nil {
				return nil, err
			}
			fields := []*uint32{&m.Type, &m.Rows, &m.Cols, &m.Matrix, &m.ArraySize, &m.Offset}
			for _, f := range fields {
				if *f, err = r.uint(); err != nil {
					return nil, err
				}
			}
			if m.Matrix > 1 {
				return nil, errors.New("legacy 矩阵标志非法")
			}
			if m.Rows < 1 || m.Rows > 4 || m.Cols < 1 || m.Cols > 4 {
				return nil, errors.New("legacy 参数维度未验证")
			}
			b.Params = append(b.Params, m)
		}
		ns, err := count()
		if err != nil {
			return nil, err
		}
		if ns != 0 {
			return nil, errors.New("legacy 结构体常量需要进一步解码")
		}
		t.Buffers = append(t.Buffers, b)
	}
	nr, err := count()
	if err != nil {
		return nil, err
	}
	for range nr {
		var b Binding
		if b.Name, err = text(); err != nil {
			return nil, err
		}
		if b.Kind, err = r.uint(); err != nil {
			return nil, err
		}
		// kind 2 = StructuredBuffer（name, kind, index, aux），例如 URP Particles/Unlit 实例化变体的 unity_ParticleInstanceData。
		switch b.Kind {
		case KindTexture, KindConstBuffer, KindStructured, KindInlineSampler:
		default:
			return nil, fmt.Errorf("未验证的 legacy 资源种类 %d（%s）", b.Kind, b.Name)
		}
		if b.Index, err = r.uint(); err != nil {
			return nil, err
		}
		if b.Aux, err = r.uint(); err != nil {
			return nil, err
		}
		if b.Kind == KindTexture {
			// 版本 201806140 单独存放打包的纹理维度：bit0 多重采样，bit1..7 TextureDimension（见 0x6545e4）。
			if b.TextureFlags, err = r.uint(); err != nil {
				return nil, err
			}
		}
		t.Bindings = append(t.Bindings, b)
	}
	if r.pos != len(data) {
		return nil, fmt.Errorf("legacy 参数 tail 有 %d 字节未解析", len(data)-r.pos)
	}
	return t, nil
}

// Bytes 是 ParseTail 的严格逆运算。
func (t *Tail) Bytes() []byte {
	var out []byte
	u := func(v uint32) { out = le.AppendUint32(out, v) }
	text := func(s string) { out = appendPadded(out, []byte(s)) }
	u(t.Channels.SourceMap)
	u(uint32(len(t.Channels.Channels)))
	for _, c := range t.Channels.Channels {
		u(c.Source)
		u(c.Target)
	}
	u(uint32(len(t.Buffers)))
	for _, b := range t.Buffers {
		text(b.Name)
		u(b.Size)
		u(uint32(len(b.Params)))
		for _, m := range b.Params {
			text(m.Name)
			for _, v := range []uint32{m.Type, m.Rows, m.Cols, m.Matrix, m.ArraySize, m.Offset} {
				u(v)
			}
		}
		u(0) // 结构体常量
	}
	u(uint32(len(t.Bindings)))
	for _, b := range t.Bindings {
		text(b.Name)
		u(b.Kind)
		u(b.Index)
		u(b.Aux)
		if b.Kind == KindTexture {
			u(b.TextureFlags)
		}
	}
	return out
}

// Equal 比较两个 tail 是否完全相同。
func (t *Tail) Equal(o *Tail) bool {
	return string(t.Bytes()) == string(o.Bytes())
}

// EmptyTail 是未引用占位程序的 tail：无通道、一个空的无名缓冲区、无绑定。
func EmptyTail() *Tail {
	return &Tail{Buffers: []Buffer{{}}}
}
