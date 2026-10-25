package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/li-yifei/lom-dx2metal/convert"

	"github.com/li-yifei/lom-dx2metal/cmd/dx2metal-gui/locales"
)

// runner 隔离对转换包的调用：正式用 convert.Run，测试与截图用假的。
type runner interface {
	Run(ctx context.Context, opt convert.Options, onEvent func(convert.Event)) (*convert.Result, error)
}

// realRunner 进程内调用 convert.Run，不起子进程、不经过 JSON。
type realRunner struct{}

func (realRunner) Run(ctx context.Context, opt convert.Options, onEvent func(convert.Event)) (*convert.Result, error) {
	return convert.Run(ctx, opt, onEvent)
}

// execute 在新 goroutine 里运行一次转换。onEvent 与 opt.Log 在工作 goroutine 调用，
// 这里统一经 post 切回 UI 线程再改 r；Run 返回后用 post 调 onDone。post 必须按调用顺序执行。
func execute(ctx context.Context, rn runner, opt convert.Options, r *run, post func(func()), onDone func()) {
	opt.Log = func(line string) { post(func() { r.logLine(line) }) }
	go func() {
		res, err := rn.Run(ctx, opt, func(ev convert.Event) { post(func() { r.apply(ev) }) })
		post(func() {
			r.end(res, err)
			onDone()
		})
	}()
}

// fakeRunner 吐一串逼真的事件序列，可注入失败；用于测试与截图（LOM_GUI_FAKE）。
type fakeRunner struct {
	Programs int           // 总程序数，默认 120
	Delay    time.Duration // 每个事件的间隔
	Failed   []string      // 这些 shader 记为失败（部分成功）
	Err      error         // 非 nil：中途以该错误结束
	Removed  int
}

var fakeShaders = []string{
	"Legacy Shaders/Diffuse", "Legacy Shaders/VertexLit", "Hidden/Internal-StencilWrite",
	"Universal Render Pipeline/Lit", "Universal Render Pipeline/Unlit", "Universal Render Pipeline/Particles/Unlit",
	"Hidden/Universal Render Pipeline/Bloom", "Hidden/Universal Render Pipeline/LutBuilderLdr",
	"Hidden/Universal Render Pipeline/FinalPost", "FlatKit/Stylized Surface", "FlatKit/OutlineFilter",
	"LOM/Character/Skin", "LOM/Character/Hair", "LOM/Scene/Water", "Spine/Skeleton",
}

func (f fakeRunner) Run(ctx context.Context, opt convert.Options, onEvent func(convert.Event)) (*convert.Result, error) {
	total := f.Programs
	if total == 0 {
		total = 120
	}
	nap := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(f.Delay):
			return nil
		}
	}
	logf := func(format string, a ...any) {
		if opt.Log != nil {
			opt.Log(fmt.Sprintf(format, a...))
		}
	}
	onEvent(convert.Event{Kind: "step", Step: "scan", State: "start", Title: "扫描资产"})
	logf("扫描 %s", opt.In)
	if err := nap(); err != nil {
		return nil, err
	}
	onEvent(convert.Event{Kind: "step", Step: "scan", State: "done", Seconds: 0.4})
	onEvent(convert.Event{Kind: "step", Step: "convert", State: "start", Title: "转换着色器"})
	for i := 0; i <= total; i++ {
		name := fakeShaders[i%len(fakeShaders)]
		onEvent(convert.Event{Kind: "progress", Step: "convert", Current: i, Total: total, Unit: "programs", Detail: name})
		if i > 0 && i%7 == 0 {
			logf("  %s: vs/fs 配对 %d 完成", name, i)
		}
		if f.Err != nil && i == total/2 {
			onEvent(convert.Event{Kind: "error", Step: "convert", Message: f.Err.Error()})
			logf("panic: %v", f.Err)
			return nil, f.Err
		}
		for k, failed := range f.Failed {
			if i == (k+1)*total/(len(f.Failed)+1) {
				onEvent(convert.Event{Kind: "error", Step: "convert", Detail: failed, Message: "Metal 编译失败，变体已删除"})
				logf("  WARN %s: Metal 编译失败，变体已删除", failed)
			}
		}
		if err := nap(); err != nil {
			return nil, err
		}
	}
	onEvent(convert.Event{Kind: "step", Step: "convert", State: "done", Seconds: 5.2})
	onEvent(convert.Event{Kind: "step", Step: "write", State: "start", Title: "写出资产"})
	if err := nap(); err != nil {
		return nil, err
	}
	onEvent(convert.Event{Kind: "step", Step: "write", State: "done", Seconds: 0.3})
	res := &convert.Result{Shaders: len(fakeShaders), Programs: total, Converted: total - f.Removed, Removed: f.Removed,
		FailedShaders: len(f.Failed), Out: opt.Out, Audit: opt.AuditPath, Failed: f.Failed}
	if res.Audit == "" {
		res.Audit = opt.Out + "/dx2metal-audit.json"
	}
	onEvent(convert.Event{Kind: "result", OK: len(f.Failed) == 0, Result: res})
	return res, nil
}

// fakeFromEnv 按 LOM_GUI_FAKE=ok|partial|fail|real 选 runner（脚本化截图用），未设置用真的。
func fakeFromEnv() runner {
	mode := os.Getenv("LOM_GUI_FAKE")
	if mode == "" || mode == "real" {
		return realRunner{}
	}
	f := fakeRunner{Delay: 20 * time.Millisecond}
	if v, err := strconv.Atoi(os.Getenv("FAKE_PROGRAMS")); err == nil {
		f.Programs = v
	}
	if d, err := time.ParseDuration(os.Getenv("FAKE_DELAY")); err == nil {
		f.Delay = d
	}
	switch mode {
	case "partial":
		f.Failed, f.Removed = []string{"Hidden/VideoComposite", "FlatKit/OutlineFilter"}, 6
	case "fail":
		f.Err = errors.New("HLSLcc 崩溃：假装的内部错误")
	}
	return f
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return locales.Seconds(strconv.Itoa(int(d.Seconds())))
	}
	return locales.Minutes(int(d.Minutes()), fmt.Sprintf("%02d", int(d.Seconds())%60))
}
