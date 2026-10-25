package serialized

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz/lzma"
)

// UnityFS 归档标志。
const (
	fsCompressionMask = 0x3F
	fsCombined        = 0x40  // 块信息与目录信息合在一起
	fsInfoAtEnd       = 0x80  // 块信息在文件末尾
	fsPaddingAtStart  = 0x200 // 块信息后、数据前按 16 对齐（2020.3.34+）
	fsEncrypted       = 0x100 // 旧版本里表示加密；本工具不支持
)

// 压缩类型。
const (
	compNone  = 0
	compLZMA  = 1
	compLZ4   = 2
	compLZ4HC = 3
)

// BundleEntry 是 bundle 里的一个文件。
type BundleEntry struct {
	Path  string
	Flags uint32 // 4 = SerializedFile
	Data  []byte
}

// Bundle 是解析后的 UnityFS bundle。
type Bundle struct {
	Version       uint32
	PlayerVersion string
	EngineVersion string
	Flags         uint32
	Entries       []*BundleEntry
}

// IsBundle 判断是否为 UnityFS。
func IsBundle(b []byte) bool { return bytes.HasPrefix(b, []byte("UnityFS\x00")) }

func decompress(data []byte, size int, flags uint32) ([]byte, error) {
	switch flags & fsCompressionMask {
	case compNone:
		return data, nil
	case compLZ4, compLZ4HC:
		out := make([]byte, size)
		n, err := lz4.UncompressBlock(data, out)
		if err != nil {
			return nil, fmt.Errorf("LZ4 解压失败：%w", err)
		}
		if n != size {
			return nil, errors.New("LZ4 解压长度不符")
		}
		return out, nil
	case compLZMA:
		// Unity 的 LZMA 块：5 字节属性 + 原始流，没有长度字段。
		if len(data) < 5 {
			return nil, errors.New("LZMA 块截断")
		}
		hdr := make([]byte, 13)
		copy(hdr, data[:5])
		binary.LittleEndian.PutUint64(hdr[5:], uint64(size))
		r, err := lzma.NewReader(io.MultiReader(bytes.NewReader(hdr), bytes.NewReader(data[5:])))
		if err != nil {
			return nil, err
		}
		out := make([]byte, size)
		if _, err := io.ReadFull(r, out); err != nil {
			return nil, fmt.Errorf("LZMA 解压失败：%w", err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("未知压缩类型 %d", flags&fsCompressionMask)
}

// ParseBundle 解析 UnityFS（版本 6–8）。
func ParseBundle(b []byte) (*Bundle, error) {
	if !IsBundle(b) {
		return nil, errors.New("不是 UnityFS")
	}
	be := binary.BigEndian
	p := 8
	cstr := func() (string, error) {
		i := bytes.IndexByte(b[p:], 0)
		if i < 0 {
			return "", errors.New("bundle 头字符串未结束")
		}
		s := string(b[p : p+i])
		p += i + 1
		return s, nil
	}
	if len(b) < 12 {
		return nil, errors.New("bundle 截断")
	}
	bd := &Bundle{Version: be.Uint32(b[p:])}
	p += 4
	var err error
	if bd.PlayerVersion, err = cstr(); err != nil {
		return nil, err
	}
	if bd.EngineVersion, err = cstr(); err != nil {
		return nil, err
	}
	if p+20 > len(b) {
		return nil, errors.New("bundle 头截断")
	}
	compSize := int(be.Uint32(b[p+8:]))
	uncompSize := int(be.Uint32(b[p+12:]))
	bd.Flags = be.Uint32(b[p+16:])
	p += 20
	if bd.Flags&fsEncrypted != 0 && bd.Flags&fsPaddingAtStart == 0 {
		// 2020.3.34 之前 0x100 不是 padding 标志而是加密；两者不会同时出现在 2020.3.49 的 bundle 里。
		return nil, errors.New("不支持加密 bundle")
	}
	if bd.Version >= 7 {
		p = (p + 15) &^ 15
	}
	var info []byte
	if bd.Flags&fsInfoAtEnd != 0 {
		if compSize > len(b) {
			return nil, errors.New("块信息越界")
		}
		info = b[len(b)-compSize:]
	} else {
		if p+compSize > len(b) {
			return nil, errors.New("块信息越界")
		}
		info = b[p : p+compSize]
		p += compSize
	}
	info, err = decompress(info, uncompSize, bd.Flags)
	if err != nil {
		return nil, err
	}
	if len(info) < 20 {
		return nil, errors.New("块信息截断")
	}
	q := 16 // 未压缩数据哈希
	nblocks := int(be.Uint32(info[q:]))
	q += 4
	type block struct {
		usize, csize int
		flags        uint16
	}
	blocks := make([]block, nblocks)
	for i := range blocks {
		if q+10 > len(info) {
			return nil, errors.New("块表截断")
		}
		blocks[i] = block{int(be.Uint32(info[q:])), int(be.Uint32(info[q+4:])), be.Uint16(info[q+8:])}
		q += 10
	}
	if q+4 > len(info) {
		return nil, errors.New("目录截断")
	}
	nnodes := int(be.Uint32(info[q:]))
	q += 4
	type node struct {
		off, size int64
		flags     uint32
		path      string
	}
	nodes := make([]node, nnodes)
	for i := range nodes {
		if q+20 > len(info) {
			return nil, errors.New("目录截断")
		}
		n := node{off: int64(be.Uint64(info[q:])), size: int64(be.Uint64(info[q+8:])), flags: be.Uint32(info[q+16:])}
		q += 20
		end := bytes.IndexByte(info[q:], 0)
		if end < 0 {
			return nil, errors.New("目录路径未结束")
		}
		n.path = string(info[q : q+end])
		q += end + 1
		nodes[i] = n
	}
	if bd.Flags&fsPaddingAtStart != 0 {
		p = (p + 15) &^ 15
	}
	var data []byte
	for _, bl := range blocks {
		if p+bl.csize > len(b) {
			return nil, errors.New("数据块越界")
		}
		d, err := decompress(b[p:p+bl.csize], bl.usize, uint32(bl.flags))
		if err != nil {
			return nil, err
		}
		data = append(data, d...)
		p += bl.csize
	}
	for _, n := range nodes {
		if n.off < 0 || n.off+n.size > int64(len(data)) {
			return nil, fmt.Errorf("bundle 文件 %s 越界", n.path)
		}
		bd.Entries = append(bd.Entries, &BundleEntry{Path: n.path, Flags: n.flags, Data: data[n.off : n.off+n.size]})
	}
	return bd, nil
}

const bundleChunk = 0x20000 // 与 Unity 一样按 128 KiB 分块压缩

// Bytes 以 LZ4 分块压缩重新打包（块信息紧跟文件头；保留原有的 0x200 对齐标志）。
func (bd *Bundle) Bytes() ([]byte, error) {
	var data []byte
	type dir struct {
		off, size int64
		flags     uint32
		path      string
	}
	var dirs []dir
	for _, e := range bd.Entries {
		dirs = append(dirs, dir{int64(len(data)), int64(len(e.Data)), e.Flags, e.Path})
		data = append(data, e.Data...)
	}
	be := binary.BigEndian
	info := make([]byte, 16)
	var blocks []byte
	var compressed []byte
	nblocks := 0
	var c lz4.Compressor
	for off := 0; off < len(data) || (off == 0 && len(data) == 0); off += bundleChunk {
		chunk := data[off:min(off+bundleChunk, len(data))]
		dst := make([]byte, lz4.CompressBlockBound(len(chunk)))
		n, err := c.CompressBlock(chunk, dst)
		if err != nil {
			return nil, err
		}
		flags := uint16(compLZ4)
		if n == 0 || n >= len(chunk) {
			dst, n, flags = chunk, len(chunk), compNone // 不可压缩：原样存
		}
		blocks = be.AppendUint32(blocks, uint32(len(chunk)))
		blocks = be.AppendUint32(blocks, uint32(n))
		blocks = be.AppendUint16(blocks, flags)
		compressed = append(compressed, dst[:n]...)
		nblocks++
		if len(data) == 0 {
			break
		}
	}
	info = be.AppendUint32(info, uint32(nblocks))
	info = append(info, blocks...)
	info = be.AppendUint32(info, uint32(len(dirs)))
	for _, d := range dirs {
		info = be.AppendUint64(info, uint64(d.off))
		info = be.AppendUint64(info, uint64(d.size))
		info = be.AppendUint32(info, d.flags)
		info = append(info, d.path...)
		info = append(info, 0)
	}
	// 块信息本身不压缩，最稳妥。
	flags := uint32(fsCombined|compNone) | bd.Flags&fsPaddingAtStart
	var out []byte
	out = append(out, "UnityFS\x00"...)
	out = be.AppendUint32(out, bd.Version)
	out = append(out, bd.PlayerVersion...)
	out = append(out, 0)
	out = append(out, bd.EngineVersion...)
	out = append(out, 0)
	sizePos := len(out)
	out = be.AppendUint64(out, 0)
	out = be.AppendUint32(out, uint32(len(info)))
	out = be.AppendUint32(out, uint32(len(info)))
	out = be.AppendUint32(out, flags)
	if bd.Version >= 7 {
		for len(out)%16 != 0 {
			out = append(out, 0)
		}
	}
	out = append(out, info...)
	if flags&fsPaddingAtStart != 0 {
		for len(out)%16 != 0 {
			out = append(out, 0)
		}
	}
	out = append(out, compressed...)
	be.PutUint64(out[sizePos:], uint64(len(out)))
	return out, nil
}
