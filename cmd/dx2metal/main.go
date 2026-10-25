// dx2metal：把 Unity 2020.3 Windows 构建的 D3D11 着色器离线转成 Metal 并写出资产文件。
//
// 本命令只是公开包 convert 的薄包装；--progress-json 时把 core.Event 编成与
// lom_arm64/progress.py 相同的 JSON 行写到 stdout，普通日志走 stderr。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/li-yifei/lom-dx2metal/core"
	"github.com/li-yifei/lom-dx2metal/internal/serialized"
	"github.com/li-yifei/lom-dx2metal/internal/shaderblob"
)

// Version 是工具版本。
const Version = "0.1.0"

const usage = `用法：
  dx2metal convert --in <Data目录或单个资产文件> --out <输出目录> [--only <shader名>...] [--audit <json>] [--progress-json] [--jobs N] [--keep-graphics-api]
                   默认还会把 globalgamemanagers 的图形 API 列表改成只含 Metal（16）并写到输出目录；--keep-graphics-api 关闭
  dx2metal inspect <资产文件>     列出 Shader 对象、各平台程序数、已有 Metal 程序的 tail 摘要；globalgamemanagers 显示图形 API 列表
  dx2metal verify <资产文件>      对每个 Metal 程序做 tail parse→write 字节往返，报告不一致
  dx2metal version
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "convert":
		return cmdConvert(args[1:])
	case "inspect":
		return cmdInspect(args[1:], false)
	case "verify":
		return cmdInspect(args[1:], true)
	case "version", "--version", "-v":
		fmt.Printf("dx2metal %s\n", Version)
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "未知子命令 %q\n%s", args[0], usage)
	return 2
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func cmdConvert(args []string) int {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	var opt core.Options
	var only multi
	progress := fs.Bool("progress-json", false, "stdout 输出 JSON 进度事件")
	fs.StringVar(&opt.In, "in", "", "Data 目录或单个资产文件（只读）")
	fs.StringVar(&opt.Out, "out", "", "输出目录")
	fs.Var(&only, "only", "只转换这个 shader（可重复）")
	fs.StringVar(&opt.AuditPath, "audit", "", "audit JSON 路径（默认 <out>/dx2metal-audit.json）")
	fs.IntVar(&opt.Jobs, "jobs", 0, "并行 shader 数（默认 CPU 数）")
	fs.BoolVar(&opt.KeepGraphicsAPI, "keep-graphics-api", false, "不改 globalgamemanagers 的 BuildSettings.m_GraphicsAPIs")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opt.Only = only
	opt.Log = func(s string) { fmt.Fprintln(os.Stderr, s) }
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var on func(core.Event)
	if *progress {
		on = func(e core.Event) {
			os.Stdout.Write(encodeEvent(e, opt))
		}
	}
	res, err := core.Run(ctx, opt, on)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		var ie *core.InputError
		if errors.As(err, &ie) {
			return 2
		}
		return 1
	}
	fmt.Fprintf(os.Stderr, "完成：%d 个 shader / %d 个程序 → 转换 %d，移除 %d 个变体，失败 shader %d；输出 %s，audit %s\n",
		res.Shaders, res.Programs, res.Converted, res.Removed, res.FailedShaders, res.Out, res.Audit)
	if res.FailedShaders > 0 {
		return 3
	}
	return 0
}

// kv 是有序 JSON 对象的一项。
type kv struct {
	k string
	v any
}

// pyJSON 按 Python json.dumps(ensure_ascii=False) 的默认格式（", " 与 ": " 分隔）编码有序对象。
func pyJSON(fields []kv) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteString(", ")
		}
		k, _ := json.Marshal(f.k)
		b.Write(k)
		b.WriteString(": ")
		switch v := f.v.(type) {
		case []kv:
			b.Write(pyJSON(v))
		default:
			var vb bytes.Buffer
			enc := json.NewEncoder(&vb)
			enc.SetEscapeHTML(false)
			enc.Encode(v)
			b.Write(bytes.TrimRight(vb.Bytes(), "\n"))
		}
	}
	b.WriteByte('}')
	return b.Bytes()
}

// encodeEvent 把事件编成一行 {"lom": 1, ...}，字段顺序与 progress.py 相同。
func encodeEvent(e core.Event, opt core.Options) []byte {
	f := []kv{{"lom", 1}, {"event", e.Kind}}
	switch e.Kind {
	case "step":
		f = append(f, kv{"step", e.Step}, kv{"state", e.State})
		if e.State == "start" {
			f = append(f, kv{"title", e.Title})
		} else {
			f = append(f, kv{"seconds", e.Seconds})
		}
	case "progress":
		f = append(f, kv{"step", e.Step}, kv{"current", e.Current})
		if e.Total > 0 {
			f = append(f, kv{"total", e.Total})
		}
		if e.Unit != "" {
			f = append(f, kv{"unit", e.Unit})
		}
		if e.Detail != "" {
			f = append(f, kv{"detail", e.Detail})
		}
	case "error":
		f = append(f, kv{"step", e.Step}, kv{"message", e.Message})
	case "result":
		f = append(f, kv{"ok", e.OK})
		if r := e.Result; r != nil {
			f = append(f, kv{"out", r.Out}, kv{"audit", r.Audit}, kv{"shaders", []kv{
				{"shaders", r.Shaders}, {"programs", r.Programs}, {"converted", r.Converted},
				{"removed", r.Removed}, {"failed_shaders", r.FailedShaders}, {"pad_reads", r.PadReads},
			}})
		} else {
			f = append(f, kv{"out", opt.Out}, kv{"audit", opt.AuditPath})
		}
	}
	return append(pyJSON(f), '\n')
}

// cmdInspect 实现 inspect 与 verify。
func cmdInspect(args []string, verify bool) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		return 2
	}
	var files []*serialized.File
	switch {
	case serialized.IsBundle(b):
		bd, err := serialized.ParseBundle(b)
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 2
		}
		for _, e := range bd.Entries {
			if serialized.IsSerializedFile(e.Data) {
				f, err := serialized.Parse(e.Data)
				if err != nil {
					fmt.Fprintf(os.Stderr, "错误：%s：%v\n", e.Path, err)
					return 2
				}
				files = append(files, f)
			}
		}
	default:
		f, err := serialized.Parse(b)
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			return 2
		}
		files = append(files, f)
		if !verify {
			if _, err := f.BuildSettings(); err == nil {
				apis, err := f.GraphicsAPIs()
				if err != nil {
					fmt.Printf("BuildSettings：读取失败：%v\n", err)
				} else {
					fmt.Printf("BuildSettings.m_GraphicsAPIs = %v（%s）\n", apis, graphicsNames(apis))
				}
			}
		}
	}
	bad, checked := 0, 0
	for _, f := range files {
		for _, o := range f.Shaders() {
			tree, err := f.ReadObject(o)
			if err != nil {
				fmt.Printf("path_id %d：读取失败：%v\n", o.PathID, err)
				bad++
				continue
			}
			name := tree.S("m_ParsedForm").Str("m_Name")
			platforms := shaderblob.ShaderPlatforms(tree)
			if !verify {
				fmt.Printf("path_id %d  %s  平台 %v\n", o.PathID, name, platforms)
			}
			for _, p := range platforms {
				programs, err := shaderblob.UnpackShader(tree, p)
				if err != nil {
					fmt.Printf("  平台 %d：解包失败：%v\n", p, err)
					bad++
					continue
				}
				if !verify {
					fmt.Printf("  平台 %d：%d 个程序\n", p, len(programs))
				}
				if p != 14 {
					continue
				}
				for i, prog := range programs {
					tail, err := shaderblob.ParseTail(prog.Parameters)
					checked++
					if err != nil || !bytes.Equal(tail.Bytes(), prog.Parameters) {
						bad++
						fmt.Printf("  %s 程序 %d：tail 往返不一致（%v）\n", name, i, err)
						continue
					}
					if !verify {
						kinds := map[uint32]int{}
						for _, bd := range tail.Bindings {
							kinds[bd.Kind]++
						}
						fmt.Printf("    程序 %d 类型 %d：%d 个缓冲区，绑定 CB %d / 纹理 %d / 结构化 %d / 内联采样器 %d，%d 个顶点通道\n",
							i, prog.Header[1], len(tail.Buffers)-1, kinds[1], kinds[0], kinds[2], kinds[4], len(tail.Channels.Channels))
					}
				}
			}
		}
	}
	if verify {
		fmt.Printf("检查 %d 个 Metal 程序，不一致 %d 个\n", checked, bad)
	}
	if bad > 0 {
		return 1
	}
	return 0
}

// GraphicsDeviceType 常见值的名字。
var graphicsAPINames = map[int]string{2: "Direct3D11", 8: "Null", 11: "OpenGLES2", 12: "OpenGLES3", 16: "Metal", 17: "OpenGLCore", 18: "Direct3D12", 21: "Vulkan"}

func graphicsNames(apis []int) string {
	var names []string
	for _, a := range apis {
		if n, ok := graphicsAPINames[a]; ok {
			names = append(names, n)
		} else {
			names = append(names, fmt.Sprint(a))
		}
	}
	return strings.Join(names, ", ")
}
