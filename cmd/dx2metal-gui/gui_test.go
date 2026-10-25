package main

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"lom-dx2metal/convert"
)

// fakeData 造一个合格的 *_Data 目录。
func fakeData(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Mortal_Data")
	must(t, os.MkdirAll(filepath.Join(dir, "Resources"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "globalgamemanagers"), make([]byte, 4096), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "Resources", "unity_builtin_extra"), make([]byte, 8192), 0o644))
	return dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func withLocale(t *testing.T, tag string) {
	i18n.SetLocaleFunc(func() string { return tag })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
}

func TestCheckInput(t *testing.T) {
	withLocale(t, "zh-Hans")
	data := fakeData(t)
	for _, dropped := range []string{data, filepath.Dir(data)} { // 游戏根目录会自动进入 *_Data
		if d := checkInput(dropped); d.Problem != nil || d.Path != data {
			t.Errorf("%s: %+v", dropped, d)
		}
	}
	noBuiltin := fakeData(t)
	must(t, os.Remove(filepath.Join(noBuiltin, "Resources", "unity_builtin_extra")))
	if d := checkInput(noBuiltin); d.Problem == nil || !strings.Contains(d.Problem(), "unity_builtin_extra") {
		t.Errorf("缺 unity_builtin_extra 应被拒绝：%+v", d)
	}
	noGGM := t.TempDir()
	if d := checkInput(noGGM); d.Problem == nil || !strings.Contains(d.Problem(), "globalgamemanagers") {
		t.Errorf("空目录应被拒绝：%+v", d)
	}
	f := filepath.Join(noGGM, "a.txt")
	must(t, os.WriteFile(f, nil, 0o644))
	if d := checkInput(f); d.Problem == nil {
		t.Error("文件应被拒绝")
	}
	if d := checkInput(filepath.Join(noGGM, "nope")); d.Problem == nil {
		t.Error("不存在的路径应被拒绝")
	}
}

func TestOutput(t *testing.T) {
	if got := defaultOutput("/g/Mortal_Data/"); got != "/g/Mortal_Data.metal" {
		t.Errorf("默认输出 %q", got)
	}
	if outputProblem("/g/Mortal_Data", "/g/Mortal_Data.metal") != nil {
		t.Error("旁边的输出目录不应报错")
	}
	if outputProblem("/g/Mortal_Data", "/g/Mortal_Data") == nil || outputProblem("/g/Mortal_Data", "/g/Mortal_Data/out") == nil {
		t.Error("输出与输入相同或在其内应报错")
	}
	if outputProblem("/g/Mortal_Data", "/g/Mortal_Data2") != nil {
		t.Error("名字前缀相同的兄弟目录不应误判")
	}
	if got := formatBytes(12_345_678_901); got != "12.3 GB" {
		t.Errorf("formatBytes %q", got)
	}
	if n := dirSize(fakeData(t), func() bool { return false }); n != 4096+8192 {
		t.Errorf("dirSize %d", n)
	}
}

func step(step, state string) convert.Event {
	return convert.Event{Kind: "step", Step: step, State: state}
}

func progress(cur, total int, detail string) convert.Event {
	return convert.Event{Kind: "progress", Step: "convert", Current: cur, Total: total, Unit: "programs", Detail: detail}
}

// 回放一段事件，覆盖步骤、进度、计数。
func TestReplay(t *testing.T) {
	withLocale(t, "zh-Hans")
	r := newRun()
	for _, ev := range []convert.Event{
		step("scan", "start"), step("scan", "done"), step("convert", "start"),
		progress(50, 200, "Hidden/Foo"),
		{Kind: "error", Step: "convert", Detail: "Bar", Message: "boom"},
	} {
		r.apply(ev)
	}
	r.logLine("a log line")
	if r.state[0] != stepDone || r.state[1] != stepRunning || r.state[2] != stepPending {
		t.Errorf("步骤状态 %v", r.state)
	}
	if r.shader != "Hidden/Foo" || r.done != 50 || r.total != 200 || r.fraction != 0.25 {
		t.Errorf("进度 %+v", r)
	}
	if len(r.log) != 1 || r.failedCount() != 1 {
		t.Errorf("日志 %q 失败 %d", r.log, r.failedCount())
	}
	if got := r.overall(); got < 0.29 || got > 0.31 { // (10 + 80*0.25)/100
		t.Errorf("总进度 %v", got)
	}
	r.apply(convert.Event{Kind: "progress", Step: "write", Current: 1, Total: 2}) // 非当前步骤
	if r.fraction != 0.25 {
		t.Error("非当前步骤的进度不应生效")
	}
	r.apply(step("write", "start")) // 跳过 convert 的 done：convert 也算完成
	if r.state[1] != stepDone || r.state[2] != stepRunning {
		t.Errorf("跳步后状态 %v", r.state)
	}
}

func TestOutcomes(t *testing.T) {
	withLocale(t, "zh-Hans")
	ok := &convert.Result{Shaders: 10, Programs: 100, Converted: 100}
	partial := &convert.Result{Shaders: 10, Programs: 100, Converted: 97, Removed: 3, FailedShaders: 2, Failed: []string{"a:1 A", "b:2 B"}}
	cases := []struct {
		name    string
		res     *convert.Result
		err     error
		cancel  bool
		want    outcome
		failing bool // 是否有步骤标为失败
	}{
		{"成功", ok, nil, false, outcomeOK, false},
		{"部分成功", partial, nil, false, outcomePartial, false},
		{"出错", nil, errors.New("读取失败"), false, outcomeFailed, true},
		{"取消", nil, context.Canceled, true, outcomeCancelled, true},
	}
	for _, c := range cases {
		r := newRun()
		r.apply(step("convert", "start"))
		r.cancel = c.cancel
		r.end(c.res, c.err)
		if r.outcome != c.want {
			t.Errorf("%s：结局 %v，想要 %v", c.name, r.outcome, c.want)
		}
		if failing := r.failedStep() != ""; failing != c.failing {
			t.Errorf("%s：失败步骤 %q", c.name, r.failedStep())
		}
	}
	r := newRun()
	r.end(nil, errors.New("读取失败"))
	if r.errorText() != "读取失败" || r.failedStep() == "" {
		t.Errorf("错误文本 %q 失败步骤 %q", r.errorText(), r.failedStep())
	}
	r = newRun()
	r.end(partial, nil)
	if got := r.failedList(); len(got) != 2 || got[0].Name != "a:1 A" {
		t.Errorf("失败列表 %+v", got)
	}
	r = newRun()
	r.apply(convert.Event{Kind: "error", Step: "convert", Detail: "E", Message: "m"})
	r.end(partial, nil)
	r.res.Failed = nil // Result 没给列表时退回 error 事件
	if got := r.failedList(); len(got) != 1 || got[0].Name != "E" {
		t.Errorf("事件回退：%+v", got)
	}
}

func TestETA(t *testing.T) {
	r := newRun()
	r.apply(step("convert", "start"))
	r.stepStart[1] = time.Now().Add(-30 * time.Second)
	r.apply(progress(25, 100, ""))
	if d, ok := r.eta(); !ok || d < 85*time.Second || d > 95*time.Second {
		t.Errorf("eta %v %v，应约 90 秒", d, ok)
	}
}

// runFake 经 execute 用假 runner 跑完一次，事件与完成都经 post 串行回主线程（这里用互斥量代替）。
func runFake(t *testing.T, rn runner) (*run, convert.Options) {
	t.Helper()
	var mu sync.Mutex
	post := func(f func()) { mu.Lock(); defer mu.Unlock(); f() }
	r := newRun()
	done := make(chan struct{})
	opt := convert.Options{In: fakeData(t), Out: filepath.Join(t.TempDir(), "out")}
	execute(context.Background(), rn, opt, r, post, func() { close(done) })
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("假 runner 没有在 20 秒内结束")
	}
	mu.Lock()
	defer mu.Unlock()
	return r, opt
}

func TestExecuteFake(t *testing.T) {
	withLocale(t, "zh-Hans")
	r, opt := runFake(t, fakeRunner{Programs: 30})
	if r.outcome != outcomeOK || r.res.Programs != 30 || r.res.Converted != 30 || r.res.Out != opt.Out || r.state[2] != stepDone {
		t.Errorf("成功：%v %+v", r.outcome, r.res)
	}
	if len(r.log) == 0 {
		t.Error("Options.Log 应进日志")
	}

	r, _ = runFake(t, fakeRunner{Programs: 30, Failed: []string{"A", "B"}, Removed: 6})
	if r.outcome != outcomePartial || r.res.FailedShaders != 2 || r.failedCount() != 2 || r.res.Removed != 6 {
		t.Errorf("部分成功：%v %+v", r.outcome, r.res)
	}
	if list := r.failedList(); len(list) != 2 || list[0].Name != "A" {
		t.Errorf("失败列表 %+v", list)
	}

	r, _ = runFake(t, fakeRunner{Programs: 30, Err: errors.New("HLSLcc 崩溃")})
	if r.outcome != outcomeFailed || r.errorText() != "HLSLcc 崩溃" || r.failedStep() == "" {
		t.Errorf("失败：%v %q %q", r.outcome, r.errorText(), r.failedStep())
	}
}

func TestCancel(t *testing.T) {
	var mu sync.Mutex
	post := func(f func()) { mu.Lock(); defer mu.Unlock(); f() }
	r := newRun()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	execute(ctx, fakeRunner{Programs: 1000, Delay: 50 * time.Millisecond}, convert.Options{In: fakeData(t), Out: t.TempDir()}, r, post, func() { close(done) })
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	r.cancel = true // 与 gui.cancel 一致：先标记再取消 ctx
	mu.Unlock()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("取消没有停掉 Run")
	}
	mu.Lock()
	defer mu.Unlock()
	if r.outcome != outcomeCancelled {
		t.Errorf("结局 %v", r.outcome)
	}
}

// snapshot 在 LOM_GUI_SNAPSHOTS 指向目录时保存测试渲染的画面。
func snapshot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("LOM_GUI_SNAPSHOTS")
	if dir == "" {
		return
	}
	must(t, os.MkdirAll(dir, 0o755))
	f, err := os.Create(filepath.Join(dir, name+".png"))
	must(t, err)
	defer f.Close()
	png.Encode(f, tt.Image())
}

func TestViews(t *testing.T) {
	for _, lang := range []struct{ tag, drop, valid, done, partial, failed, disclaimer string }{
		{"zh-Hans", "把游戏的 *_Data 文件夹拖到这里", "输入目录有效", "转换完成", "部分成功", "转换失败：转换着色器", "请勿分发转换结果"},
		{"zh-Hant", "把遊戲的 *_Data 資料夾拖到這裡", "輸入資料夾有效", "轉換完成", "部分成功", "轉換失敗：轉換著色器", "請勿散布轉換結果"},
		{"ja", "ゲームの *_Data フォルダーをここにドロップ", "入力フォルダーは有効です", "変換が完了しました", "一部成功", "変換に失敗：シェーダーを変換", "変換結果を配布しないでください"},
	} {
		t.Run(lang.tag, func(t *testing.T) {
			withLocale(t, lang.tag)
			g := &gui{rn: fakeRunner{}, size: -1}
			tt := ui.NewTester(g.view, 760, 700)
			tt.SetLocale(lang.tag)
			tt.Frame()
			if !tt.HasText(lang.drop) || !tt.HasText(lang.disclaimer) {
				t.Fatalf("空闲页文本 %q", tt.Texts())
			}
			snapshot(t, tt, lang.tag+"-1-idle")

			data := fakeData(t)
			r, ok := tt.Find(lang.drop)
			if !ok || !tt.DropFiles(r.X+r.W/2, r.Y+r.H/2, data) {
				t.Fatal("拖放没有被接收")
			}
			tt.Frame()
			if g.input == nil || g.input.Problem != nil || !tt.HasText(lang.valid) || g.outPath != data+".metal" {
				t.Fatalf("拖入后：%+v out=%q %q", g.input, g.outPath, tt.Texts())
			}
			snapshot(t, tt, lang.tag+"-2-ready")

			// 拖入不合格目录
			bad := t.TempDir()
			r, _ = tt.Find(lang.valid)
			tt.DropFiles(r.X+r.W/2, r.Y+r.H/2, bad)
			tt.Frame()
			if g.input.Problem == nil {
				t.Fatal("不合格目录被接受")
			}
			snapshot(t, tt, lang.tag+"-2b-invalid")

			// 进行中
			g.phase, g.run, g.logOpen = phaseRunning, newRun(), true
			for _, ev := range []convert.Event{
				step("scan", "start"), step("scan", "done"), step("convert", "start"),
				progress(1204, 2550, "Hidden/Universal Render Pipeline/Bloom"),
				{Kind: "error", Step: "convert", Detail: "Hidden/VideoComposite", Message: "Metal 编译失败"},
			} {
				g.run.apply(ev)
			}
			g.run.logLine("WARN Hidden/VideoComposite: Metal 编译失败，变体已删除")
			g.run.logLine("Hidden/Universal Render Pipeline/Bloom: 12 个程序完成")
			tt.Frame()
			snapshot(t, tt, lang.tag+"-3-running")

			// 完成与部分成功
			out := filepath.Join(t.TempDir(), "Mortal_Data.metal")
			must(t, os.MkdirAll(out, 0o755))
			audit := filepath.Join(out, "dx2metal-audit.json")
			must(t, os.WriteFile(audit, []byte("{}"), 0o644))
			for _, c := range []struct {
				res  convert.Result
				want string
				name string
			}{
				{convert.Result{Shaders: 102, Programs: 2550, Converted: 2550, Out: out, Audit: audit}, lang.done, "4-done"},
				{convert.Result{Shaders: 102, Programs: 2550, Converted: 2544, Removed: 6, FailedShaders: 2, Out: out, Audit: audit,
					Failed: []string{"unity_builtin_extra:1234 Hidden/VideoComposite", "globalgamemanagers.assets:77 XR/RTAI"}}, lang.partial, "4b-partial"},
			} {
				g.run = newRun()
				res := c.res
				g.run.end(&res, nil)
				g.phase, g.logOpen = phaseDone, false
				tt.Frame()
				if !tt.HasText(c.want) {
					t.Fatalf("结果页 %q", tt.Texts())
				}
				snapshot(t, tt, lang.tag+"-"+c.name)
				if len(res.Failed) > 0 && !tt.HasText("unity_builtin_extra:1234 Hidden/VideoComposite") {
					t.Errorf("部分成功页应列出失败 shader：%q", tt.Texts())
				}
			}

			// 失败
			g.run, g.phase = newRun(), phaseFailed
			g.run.apply(step("convert", "start"))
			g.run.logLine("panic: fake internal error")
			g.run.apply(convert.Event{Kind: "error", Step: "convert", Message: "HLSLcc 崩溃"})
			g.run.end(nil, errors.New("HLSLcc 崩溃"))
			tt.Frame()
			if !tt.HasText(lang.failed) {
				t.Fatalf("失败页 %q", tt.Texts())
			}
			snapshot(t, tt, lang.tag+"-5-failed")
		})
	}
}

// 输入有问题时 convert.Run 返回 *convert.InputError，界面给友好提示而不是裸错误。
func TestInputError(t *testing.T) {
	withLocale(t, "zh-Hans")
	r, _ := runFake(t, realRunner{}) // fakeData 里没有真正的资产，Run 会拒绝它或报错
	if r.outcome != outcomeFailed {
		t.Fatalf("结局 %v", r.outcome)
	}
	r = newRun()
	_, err := convert.Run(context.Background(), convert.Options{In: filepath.Join(t.TempDir(), "不存在"), Out: t.TempDir()}, nil)
	r.end(nil, err)
	if !r.input || !strings.Contains(r.errorText(), "请确认拖入的是未经修改") {
		t.Errorf("input=%v 文本 %q（err=%v）", r.input, r.errorText(), err)
	}
}
