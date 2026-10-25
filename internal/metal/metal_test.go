package metal

import "testing"

const vs = `#include <metal_stdlib>
using namespace metal;
struct VGlobals_Type { float4 _Color; float4 hlslcc_mtx4x4unity_ObjectToWorld[4]; };
struct In { float4 POSITION0 [[ attribute(0) ]]; };
struct Out { float4 pos [[ position ]]; };
vertex Out xlatMtlMain(constant VGlobals_Type& VGlobals [[ buffer(0) ]], In in [[ stage_in ]]) {
    Out o; o.pos = in.POSITION0 * VGlobals._Color + VGlobals.hlslcc_mtx4x4unity_ObjectToWorld[0]; return o;
}`

const fs = `#include <metal_stdlib>
using namespace metal;
struct Out { half4 c [[ color(0) ]]; };
fragment Out xlatMtlMain(texture2d<float> _MainTex [[ texture(0) ]], sampler sampler_MainTex [[ sampler(0) ]]) {
    Out o; o.c = half4(_MainTex.sample(sampler_MainTex, float2(0.5))); return o;
}`

// 进程内编译 + 链接 + 反射。
func TestReflectPair(t *testing.T) {
	d, err := NewDevice()
	if err != nil {
		t.Skip(err)
	}
	v, err := d.Compile(vs)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	f, err := d.Compile(fs)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()
	r, err := d.ReflectPair(v, f)
	if err != nil || !r.Linked {
		t.Fatalf("链接失败：%v %s", err, r.Error)
	}
	if len(r.VertexInputs) != 1 || r.VertexInputs[0].Name != "POSITION0" {
		t.Fatalf("顶点输入：%+v", r.VertexInputs)
	}
	var buf *Argument
	for i := range r.Vertex {
		if r.Vertex[i].Name == "VGlobals" {
			buf = &r.Vertex[i]
		}
	}
	if buf == nil || len(buf.Members) != 2 || buf.Members[1].Offset != 16 || *buf.Members[1].ArraySize != 4 {
		t.Fatalf("VGlobals 反射：%+v", buf)
	}
	if len(r.Fragment) != 2 {
		t.Fatalf("片元参数：%+v", r.Fragment)
	}
	if _, err := d.Compile("garbage"); err == nil {
		t.Fatal("坏源码应编译失败")
	}
}
