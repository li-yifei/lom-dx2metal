package core

import (
	"errors"

	"github.com/li-yifei/lom-dx2metal/internal/serialized"
)

// asset 是一个输入文件：普通 SerializedFile，或 UnityFS bundle 里的若干 SerializedFile。
type asset struct {
	bundle *serialized.Bundle
	files  []*serialized.File
	owners []*serialized.BundleEntry // bundle 时与 files 一一对应
}

func loadAsset(b []byte) (*asset, error) {
	switch {
	case serialized.IsBundle(b):
		bd, err := serialized.ParseBundle(b)
		if err != nil {
			return nil, err
		}
		a := &asset{bundle: bd}
		for _, e := range bd.Entries {
			if !serialized.IsSerializedFile(e.Data) {
				continue
			}
			f, err := serialized.Parse(e.Data)
			if err != nil {
				return nil, err
			}
			a.files = append(a.files, f)
			a.owners = append(a.owners, e)
		}
		return a, nil
	case serialized.IsSerializedFile(b):
		f, err := serialized.Parse(b)
		if err != nil {
			return nil, err
		}
		return &asset{files: []*serialized.File{f}}, nil
	}
	return nil, errors.New("不是 SerializedFile v22 或 UnityFS bundle")
}

// bytes 把改动后的资产重新编码；bundle 以 LZ4 重新打包。
func (a *asset) bytes() ([]byte, error) {
	if a.bundle == nil {
		return a.files[0].Bytes(), nil
	}
	for i, f := range a.files {
		if f.Changed() {
			a.owners[i].Data = f.Bytes()
		}
	}
	return a.bundle.Bytes()
}
