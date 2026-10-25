// Package shaderblob 读写 Unity 2020.3 Shader 对象里的子程序 blob：
// 按平台的 LZ4 block 段、程序表、单个 GPU 程序（格式 201806140）以及程序末尾的 legacy 参数 tail。
//
// 移植自 lom_arm64/shaders/shader_blob.py。
//
// 根因一（黑屏）：Unity 2020.3 的 Metal 程序把常量缓冲区/纹理/采样器的反射写在 blob 末尾的 legacy tail 里，
// 而不是 ParsedForm 的 m_Parameters。转换时必须从 Metal 驱动反射写出**完整** tail、ParsedForm 参数留空，
// 否则引擎不给常量缓冲区上传数据，画面全黑。本包的 ParseTail/Tail.Bytes 是这一格式的严格互逆实现。
package shaderblob

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/pierrec/lz4/v4"

	"github.com/li-yifei/lom-dx2metal/internal/serialized"
)

// 程序格式版本（Unity 2020.3）。
const ProgramFormat = 201806140

const (
	maxBlob     = 256 * 1024 * 1024
	maxPrograms = 1_000_000
)

var le = binary.LittleEndian

func align4(v int) int { return (v + 3) &^ 3 }

type reader struct {
	b   []byte
	pos int
}

var errTrunc = errors.New("shader blob 截断")

func (r *reader) read(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.b) {
		return nil, errTrunc
	}
	v := r.b[r.pos : r.pos+n]
	r.pos += n
	return v, nil
}

func (r *reader) uint() (uint32, error) {
	b, err := r.read(4)
	if err != nil {
		return 0, err
	}
	return le.Uint32(b), nil
}

func (r *reader) alignedBytes() ([]byte, error) {
	n, err := r.uint()
	if err != nil {
		return nil, err
	}
	v, err := r.read(int(n))
	if err != nil {
		return nil, err
	}
	if _, err := r.read(align4(r.pos) - r.pos); err != nil {
		return nil, err
	}
	return v, nil
}

func (r *reader) keywords() ([][]byte, error) {
	n, err := r.uint()
	if err != nil {
		return nil, err
	}
	if n > maxPrograms {
		return nil, errors.New("关键字数量非法")
	}
	out := make([][]byte, n)
	for i := range out {
		if out[i], err = r.alignedBytes(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func appendKeywords(out []byte, kws [][]byte) []byte {
	out = le.AppendUint32(out, uint32(len(kws)))
	for _, k := range kws {
		out = appendPadded(out, k)
	}
	return out
}

// appendPadded 写 u32 长度 + 数据 + 补齐到 4 字节（与 shader_blob.write_array 相同）。
func appendPadded(out, data []byte) []byte {
	out = le.AppendUint32(out, uint32(len(data)))
	out = append(out, data...)
	for i := 0; i < (4-len(data)%4)%4; i++ {
		out = append(out, 0)
	}
	return out
}

// Program 是一个 GPU 子程序。
type Program struct {
	Header        [6]uint32 // [0]=格式版本，[1]=程序类型（Metal：23 顶点 / 24 片元）
	Keywords      [][]byte
	LocalKeywords [][]byte
	Code          []byte // D3D11：前缀 + 去掉反射的 DXBC；Metal：metal_code 头 + MSL 源码
	Parameters    []byte // legacy 参数 tail 原始字节
}

// ParseProgram 解析单个程序。
func ParseProgram(data []byte) (*Program, error) {
	if len(data) > maxBlob {
		return nil, errors.New("shader blob 超过检查上限")
	}
	r := &reader{b: data}
	hb, err := r.read(24)
	if err != nil {
		return nil, err
	}
	p := &Program{}
	for i := range p.Header {
		p.Header[i] = le.Uint32(hb[i*4:])
	}
	if p.Header[0] != ProgramFormat {
		return nil, fmt.Errorf("不支持的 GPU 程序格式 %d", p.Header[0])
	}
	if p.Keywords, err = r.keywords(); err != nil {
		return nil, err
	}
	if p.LocalKeywords, err = r.keywords(); err != nil {
		return nil, err
	}
	if p.Code, err = r.alignedBytes(); err != nil {
		return nil, err
	}
	p.Parameters = data[r.pos:]
	return p, nil
}

// Bytes 编码程序。
func (p *Program) Bytes() []byte {
	var out []byte
	for _, h := range p.Header {
		out = le.AppendUint32(out, h)
	}
	out = appendKeywords(out, p.Keywords)
	out = appendKeywords(out, p.LocalKeywords)
	out = appendPadded(out, p.Code)
	return append(out, p.Parameters...)
}

// Platform 是 Shader 对象里一个平台的压缩段表。
type Platform struct {
	Offsets, CompressedLengths, DecompressedLengths []uint32
}

// Unpack 解压一个平台的全部段并切出程序。compressed 为 Shader.compressedBlob。
func Unpack(compressed []byte, pl Platform) ([]*Program, error) {
	if len(pl.Offsets) != len(pl.CompressedLengths) || len(pl.Offsets) != len(pl.DecompressedLengths) {
		return nil, errors.New("压缩段表长度不一致")
	}
	var segments [][]byte
	total := 0
	for i := range pl.Offsets {
		off, n, dn := int(pl.Offsets[i]), int(pl.CompressedLengths[i]), int(pl.DecompressedLengths[i])
		total += dn
		if dn > maxBlob || total > maxBlob {
			return nil, errors.New("平台数据超过解压上限")
		}
		if off < 0 || n < 0 || off+n > len(compressed) {
			return nil, errors.New("压缩段越界")
		}
		dec := make([]byte, dn)
		got, err := lz4.UncompressBlock(compressed[off:off+n], dec)
		if err != nil {
			return nil, fmt.Errorf("LZ4 解压失败：%w", err)
		}
		if got != dn {
			return nil, errors.New("解压长度不符")
		}
		segments = append(segments, dec)
	}
	if len(segments) == 0 {
		return nil, errors.New("缺少程序表")
	}
	r := &reader{b: segments[0]}
	count, err := r.uint()
	if err != nil {
		return nil, err
	}
	if count > maxPrograms || 4+int(count)*12 > len(segments[0]) {
		return nil, errors.New("程序表非法")
	}
	programs := make([]*Program, count)
	for i := range programs {
		e, _ := r.read(12)
		off, n, seg := int(le.Uint32(e)), int(le.Uint32(e[4:])), int(le.Uint32(e[8:]))
		if seg >= len(segments) {
			return nil, errors.New("程序段号非法")
		}
		payload := segments[seg]
		if off+n > len(payload) {
			return nil, errors.New("程序超出段范围")
		}
		if programs[i], err = ParseProgram(payload[off : off+n]); err != nil {
			return nil, fmt.Errorf("程序 %d：%w", i, err)
		}
	}
	return programs, nil
}

// PackPrograms 把程序编码成单段：程序表 + 依次排列（每个程序补齐到 4 字节）。
func PackPrograms(programs []*Program) ([]byte, error) {
	if len(programs) > maxPrograms {
		return nil, errors.New("程序过多")
	}
	table := le.AppendUint32(nil, uint32(len(programs)))
	cursor := 4 + 12*len(programs)
	var payload []byte
	for _, p := range programs {
		data := p.Bytes()
		table = le.AppendUint32(table, uint32(cursor))
		table = le.AppendUint32(table, uint32(len(data)))
		table = le.AppendUint32(table, 0)
		payload = append(payload, data...)
		pad := (4 - len(data)%4) % 4
		payload = append(payload, make([]byte, pad)...)
		cursor += len(data) + pad
	}
	out := append(table, payload...)
	if len(out) > maxBlob {
		return nil, errors.New("平台数据超过写出上限")
	}
	return out, nil
}

// CompressBlock 以 LZ4 block 格式（不带长度前缀）压缩。
func CompressBlock(src []byte) ([]byte, error) {
	dst := make([]byte, lz4.CompressBlockBound(len(src)))
	var c lz4.Compressor
	n, err := c.CompressBlock(src, dst)
	if err != nil {
		return nil, err
	}
	if n == 0 && len(src) > 0 {
		return nil, errors.New("LZ4 压缩失败（不可压缩）")
	}
	return dst[:n], nil
}

// MetalCode 生成 Unity 版本 4 的 Metal 源码程序头（与 Mac 版 Unity 产出的资产一致）+ 入口名 + 源码 + NUL。
func MetalCode(source string) []byte {
	return metalCode(source, "xlatMtlMain", 5, 15)
}

func metalCode(source, entry string, flags, writeMask uint32) []byte {
	entryBytes := append([]byte(entry), 0)
	var out []byte
	for _, v := range []uint32{0xF00DCAFE, 44, 4, uint32(44 + len(entryBytes)), 0, 0, 0, 0, 0, flags, writeMask} {
		out = le.AppendUint32(out, v)
	}
	out = append(out, entryBytes...)
	out = append(out, source...)
	return append(out, 0)
}

// ShaderPlatforms 从 Shader 对象取平台列表。
func ShaderPlatforms(shader *serialized.Struct) []uint32 {
	var out []uint32
	for _, v := range shader.A("platforms") {
		i, _ := v.(int64)
		out = append(out, uint32(i))
	}
	return out
}

func uintMatrix(v []any, i int) []uint32 {
	if i >= len(v) {
		return nil
	}
	row, _ := v[i].([]any)
	out := make([]uint32, len(row))
	for j, x := range row {
		n, _ := x.(int64)
		out[j] = uint32(n)
	}
	return out
}

// UnpackShader 解出 Shader 对象某平台（4 = D3D11，14 = Metal）的全部程序。
func UnpackShader(shader *serialized.Struct, platform uint32) ([]*Program, error) {
	idx := -1
	for i, p := range ShaderPlatforms(shader) {
		if p == platform {
			idx = i
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("Shader 没有平台 %d", platform)
	}
	blob, _ := shader.Get("compressedBlob").([]byte)
	return Unpack(blob, Platform{
		Offsets:             uintMatrix(shader.A("offsets"), idx),
		CompressedLengths:   uintMatrix(shader.A("compressedLengths"), idx),
		DecompressedLengths: uintMatrix(shader.A("decompressedLengths"), idx),
	})
}

// ReplacePlatform 用单个平台、单段 LZ4 blob 替换 Shader 的全部平台数据（shader_blob.replace_platform）。
func ReplacePlatform(shader *serialized.Struct, platform uint32, programs []*Program) error {
	decoded, err := PackPrograms(programs)
	if err != nil {
		return err
	}
	compressed, err := CompressBlock(decoded)
	if err != nil {
		return err
	}
	one := func(v uint32) []any { return []any{[]any{int64(v)}} }
	shader.Set("platforms", []any{int64(platform)})
	shader.Set("offsets", one(0))
	shader.Set("compressedLengths", one(uint32(len(compressed))))
	shader.Set("decompressedLengths", one(uint32(len(decoded))))
	shader.Set("compressedBlob", compressed)
	return nil
}
