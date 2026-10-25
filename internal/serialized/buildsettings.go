package serialized

import (
	"errors"
	"slices"
)

// GraphicsMetal 是 UnityEngine.Rendering.GraphicsDeviceType.Metal 的枚举值。
const GraphicsMetal = 16

// BuildSettings 返回 globalgamemanagers 里的 BuildSettings(ClassID 141) 对象。
func (f *File) BuildSettings() (*Object, error) {
	for _, o := range f.Objects {
		if o.ClassID == ClassBuildSettings {
			return o, nil
		}
	}
	return nil, errors.New("没有 BuildSettings 对象")
}

// GraphicsAPIs 读出 BuildSettings.m_GraphicsAPIs。
func (f *File) GraphicsAPIs() ([]int, error) {
	o, err := f.BuildSettings()
	if err != nil {
		return nil, err
	}
	v, err := f.ReadObject(o)
	if err != nil {
		return nil, err
	}
	var out []int
	for _, x := range v.A("m_GraphicsAPIs") {
		i, _ := x.(int64)
		out = append(out, int(i))
	}
	return out, nil
}

// SetGraphicsAPIs 把 BuildSettings.m_GraphicsAPIs 改成 apis，其它字段不动；返回改前的列表与是否有改动。
//
// Windows 构建的列表是 [2]（Direct3D11）。官方 macOS Player 按这个列表挑图形设备，
// 不改成 [16]（Metal）就得靠启动参数 -force-metal。Python 版 assemble.patch_settings 做的是同一件事。
func (f *File) SetGraphicsAPIs(apis []int) (before []int, changed bool, err error) {
	o, err := f.BuildSettings()
	if err != nil {
		return nil, false, err
	}
	v, err := f.ReadObject(o)
	if err != nil {
		return nil, false, err
	}
	for _, x := range v.A("m_GraphicsAPIs") {
		i, _ := x.(int64)
		before = append(before, int(i))
	}
	if slices.Equal(before, apis) {
		return before, false, nil
	}
	list := make([]any, len(apis))
	for i, a := range apis {
		list[i] = int64(a)
	}
	v.Set("m_GraphicsAPIs", list)
	return before, true, f.WriteObject(o, v)
}
