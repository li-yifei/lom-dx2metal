// Package convert 是 lom-dx2metal 的公开 Go API：把 Unity 2020.3 D3D11 着色器转成 Metal 并写出资产文件。
// CLI（cmd/dx2metal）与 GUI（cmd/dx2metal-gui）都只调用 Run。
//
// Run 不依赖进程级全局状态，可在 goroutine 里运行、同一进程多次调用；ctx 取消即中止；
// 单个程序的失败只记入 audit，不 panic。
package convert

import "context"

// Options 是一次转换的参数。
type Options struct {
	In, Out   string       // 输入 Data 目录或单个资产文件；输出目录。In 永远只读
	Only      []string     // 只转这些 shader 名（空 = 全部）
	Jobs      int          // 并行 shader 数（<=0 时取 CPU 数）
	AuditPath string       // audit JSON 路径（空 = <Out>/dx2metal-audit.json）
	Log       func(string) // 普通日志（CLI 打到 stderr）；可为 nil
}

// Event 是进度事件，与 lom_arm64/progress.py 的事件一一对应。
type Event struct {
	Kind    string  // "step" | "progress" | "error" | "result"
	Step    string  // "scan" | "convert" | "write"
	State   string  // step: "start" | "done"
	Title   string  // step start 的中文标题
	Seconds float64 // step done 的耗时
	Current int     // progress
	Total   int     // progress
	Unit    string  // progress 单位（"programs"）
	Detail  string  // 当前 shader 名
	Message string  // error
	OK      bool    // result
	Result  *Result // result
}

// Result 是转换汇总。
type Result struct {
	Shaders       int      `json:"shaders"`
	Programs      int      `json:"programs"`
	Converted     int      `json:"converted"`
	Removed       int      `json:"removed"`
	FailedShaders int      `json:"failed_shaders"`
	PadReads      int      `json:"pad_reads"`
	Out           string   `json:"-"`
	Audit         string   `json:"-"`
	Failed        []string `json:"-"` // 有失败变体的 shader（"文件:path_id 名字"）
}

// Run 执行转换。
func Run(ctx context.Context, opt Options, onEvent func(Event)) (*Result, error) {
	return run(ctx, opt, onEvent)
}
