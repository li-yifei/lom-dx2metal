package params

import "testing"

// 根因三：内联采样器状态必须解出 HLSLcc 认识的名字，才会生成 constexpr sampler。
func TestInlineSamplerName(t *testing.T) {
	cases := map[uint32]string{
		85:    "sampler_LinearClamp", // 本游戏的调色 LUT
		84:    "sampler_PointClamp",
		0:     "sampler_PointRepeat",
		1:     "sampler_LinearRepeat",
		0x19:  "sampler_LinearMirrorUClampVRepeatW", // U=mirror(2) V=clamp(1) W=repeat(0)
		3:     "",                                   // 过滤值 3 无法命名
		0x100: "",
	}
	for state, want := range cases {
		if got := InlineSamplerName(state); got != want {
			t.Errorf("InlineSamplerName(%d) = %q，期望 %q", state, got, want)
		}
	}
}

// 根因二：同名 CB 跨 common/variant 按成员合并，变体自己的成员不能丢。
func TestMergeSameNameCB(t *testing.T) {
	common := &Params{CBs: []ConstantBuffer{{NameIndex: 7, Size: 32, Vector: []VectorParam{{NameIndex: 1, Index: 0, Dim: 4}}, IsPartialCB: true}}}
	variant := &Params{CBs: []ConstantBuffer{{NameIndex: 7, Size: 48, Vector: []VectorParam{
		{NameIndex: 1, Index: 0, Dim: 4}, {NameIndex: 2, Index: 32, Dim: 4}}}}}
	m := Merge(common, variant)
	if len(m.CBs) != 1 {
		t.Fatalf("应合并成 1 个 CB，得到 %d", len(m.CBs))
	}
	cb := m.CBs[0]
	if cb.Size != 48 || len(cb.Vector) != 2 || cb.Vector[1].NameIndex != 2 {
		t.Fatalf("合并结果错误：%+v", cb)
	}
	if len(common.CBs[0].Vector) != 1 {
		t.Fatal("Merge 不应修改输入")
	}
}
