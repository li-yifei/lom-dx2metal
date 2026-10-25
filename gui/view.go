package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/li-yifei/lom-dx2metal/core"

	"github.com/li-yifei/lom-dx2metal/gui/locales"
)

type phase int

const (
	phaseIdle phase = iota
	phaseRunning
	phaseDone // 成功或部分成功，看 run.outcome
	phaseFailed
)

// maxFailedShown 是结果页最多列出的失败 shader 数。
const maxFailedShown = 30

// gui 是窗口的状态；视图和所有改动都在主线程上。
type gui struct {
	win       *mygo.Window
	rn        runner
	cancelRun context.CancelFunc

	input   *inputDir
	size    int64 // 输入目录大小，-1 表示正在计算
	sizeGen atomic.Int64
	outPath string
	outAuto bool          // outPath 是自动给出的默认值，换输入时跟着换
	notice  func() string // 拖放区下面的一行提示（取消、错误等）

	phase   phase
	run     *run
	logOpen bool
	logView ui.ScrollState
	copied  time.Time
}

func icon(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`))
}

var (
	iconCheck  = icon(`<path d="M20 6 9 17l-5-5"/>`)
	iconX      = icon(`<path d="M18 6 6 18M6 6l12 12"/>`)
	iconCircle = icon(`<circle cx="12" cy="12" r="9"/>`)
	iconFolder = icon(`<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`)
	iconWarn   = icon(`<path d="M12 3 2 20h20L12 3z"/><path d="M12 10v4M12 17h.01"/>`)
)

func (g *gui) view(c *ui.Context) {
	ui.Column(c).Fill().Padding(24, 24, 14, 24).Gap(14).Children(func() {
		g.header(c)
		if g.phase == phaseIdle {
			g.idle(c) // 占满窗口：拖放区会长高
		} else {
			// 其他页面在日志把页面撑高时可以滚动。
			ui.Scroll(c).Grow(1).Gap(16).Children(func() {
				switch g.phase {
				case phaseRunning:
					g.running(c)
				case phaseDone:
					g.done(c)
				case phaseFailed:
					g.failed(c)
				}
			})
		}
		g.footer(c)
	})
}

// languages 是语言切换里的选项；应用启动时用系统偏好的那个。
var languages = []struct{ Tag, Label string }{{"zh-Hans", "简体"}, {"zh-Hant", "繁體"}, {"ja", "日本語"}}

func (g *gui) header(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(12).Children(func() {
		ui.Column(c).Gap(4).Grow(1).Children(func() {
			ui.Text(c, locales.HeaderTitle()).FontSize(21).Bold()
			ui.Text(c, locales.HeaderSubtitle()).TextColor(t.TextMuted).FontSize(12)
		})
		ui.Row(c).Gap(2).AlignSelf(ui.Start).Children(func() {
			for _, l := range languages {
				b := ui.Button(c.Key(l.Tag), l.Label).FontSize(12)
				if c.Locale() == l.Tag {
					b.Background(t.SurfacePressed).Bold()
				}
				tag := l.Tag
				b.OnClick(func() { mygo.App.SetLocale(tag) })
			}
		})
	})
}

// footer 把“非官方”与“只能自己玩、别传给别人”固定在窗口底部。
func (g *gui) footer(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(2).Padding(10, 0, 0, 0).Border(0, t.Border).Children(func() {
		ui.Text(c, locales.DisclaimerUnofficial()).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, locales.DisclaimerNoShare()).FontSize(11).TextColor(t.Warning)
	})
}

func chip(c *ui.Context, svg *ui.SVG, color ui.Color, label string) {
	t := c.Theme()
	ui.Row(c).Gap(5).AlignItems(ui.Center).Padding(4, 8).Radius(6).Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Icon(c, svg).TextColor(color).FontSize(12)
		ui.Text(c, label).FontSize(12)
	})
}

func (g *gui) idle(c *ui.Context) {
	t := c.Theme()
	zone := ui.Column(c).Grow(1).MinHeight(170).Center().Gap(8).Padding(20).Radius(12).Border(2, t.Border)
	if files := zone.DroppedFiles(); len(files) > 0 {
		g.setInput(files[0])
	}
	if zone.FileDragOver() {
		zone.Border(2, t.Accent).Background(t.Surface)
	}
	zone.Children(func() {
		switch {
		case g.input == nil:
			ui.Icon(c, iconFolder).FontSize(40).TextColor(t.TextMuted)
			ui.Text(c, locales.DropTitle()).FontSize(16).Bold()
			ui.Text(c, locales.DropHint()).FontSize(12).TextColor(t.TextMuted)
		case g.input.Problem != nil:
			ui.Icon(c, iconWarn).FontSize(34).TextColor(t.Danger)
			ui.Text(c, locales.DropInvalid()).FontSize(16).Bold().TextColor(t.Danger)
			ui.Text(c, g.input.Problem()).TextAlign(ui.Center).MaxWidth(520)
			ui.Text(c, g.input.Path).FontSize(11).TextColor(t.TextMuted).TextAlign(ui.Center).MaxLines(2).MaxWidth(600).Selectable()
		default:
			ui.Icon(c, iconCheck).FontSize(34).TextColor(t.Success)
			ui.Text(c, locales.DropValid()).FontSize(16).Bold()
			ui.Text(c, g.input.Path).FontSize(11).TextColor(t.TextMuted).TextAlign(ui.Center).MaxLines(2).MaxWidth(600).Selectable()
			if g.size < 0 {
				ui.Text(c, locales.DropSizing()).FontSize(12).TextColor(t.TextMuted)
			} else {
				ui.Text(c, locales.DropSize(formatBytes(g.size))).FontSize(12).TextColor(t.TextMuted)
			}
		}
		ui.Button(c, locales.ChooseFolder()).OnClick(g.chooseInput)
	})
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, locales.Output()).FontSize(12).Bold()
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &g.outPath).Label(locales.Output()).Grow(1)
			ui.Button(c, locales.Change()).OnClick(g.chooseOutput)
		})
		if msg := g.outputWarning(); msg != "" {
			ui.Text(c, msg).FontSize(12).TextColor(t.Warning)
		}
	})
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		if g.notice != nil {
			ui.Text(c, g.notice()).FontSize(12).TextColor(t.TextMuted).Grow(1)
		} else {
			ui.Spacer(c)
		}
		ui.PrimaryButton(c, locales.Start()).Disabled(!g.ready()).OnClick(g.confirmStart)
	})
}

// outputProblemNow 是输出位置当前的错误（挡住开始按钮）。
func (g *gui) outputProblemNow() func() string {
	if g.input == nil || g.input.Problem != nil || strings.TrimSpace(g.outPath) == "" {
		return nil
	}
	return outputProblem(g.input.Path, expandHome(strings.TrimSpace(g.outPath)))
}

// outputWarning 是输出区下面的一行：位置错误优先，其次是磁盘空间提示。
func (g *gui) outputWarning() string {
	if p := g.outputProblemNow(); p != nil {
		return p()
	}
	if g.input != nil && g.input.Problem == nil && g.size > 0 {
		if free := freeBytes(expandHome(strings.TrimSpace(g.outPath))); free >= 0 && free < g.size {
			return locales.DiskLow(formatBytes(free), formatBytes(g.size))
		}
	}
	return ""
}

func (g *gui) ready() bool {
	return g.input != nil && g.input.Problem == nil &&
		strings.TrimSpace(g.outPath) != "" && g.outputProblemNow() == nil
}

func (g *gui) steps(c *ui.Context) {
	t := c.Theme()
	r := g.run
	ui.Column(c).Gap(6).Children(func() {
		for i, s := range steps {
			ui.Row(c.Key(s.ID)).Gap(10).AlignItems(ui.Center).MinHeight(24).Children(func() {
				switch r.state[i] {
				case stepPending:
					ui.Icon(c, iconCircle).TextColor(t.Border)
				case stepRunning:
					ui.Spinner(c).Size(16, 16)
				case stepDone:
					ui.Icon(c, iconCheck).TextColor(t.Success)
				case stepFailed:
					ui.Icon(c, iconX).TextColor(t.Danger)
				}
				label := ui.Text(c, s.Title()).MinWidth(110).NoWrap()
				if r.state[i] == stepPending {
					label.TextColor(t.TextMuted)
				}
			})
		}
	})
}

func (g *gui) running(c *ui.Context) {
	t := c.Theme()
	r := g.run
	c.After(time.Second) // 用时与预计时间
	ui.Text(c, locales.Converting(filepath.Base(g.outPath))).FontSize(13).TextColor(t.TextMuted).SingleLine()
	g.steps(c)
	ui.Progress(c, r.overall()).Label(locales.Overall())
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		status := fmt.Sprintf("%d%% · ", int(r.overall()*100)) + locales.Elapsed(formatDuration(time.Since(r.started)))
		if d, ok := r.eta(); ok {
			status += " · " + locales.Eta(formatDuration(d))
		}
		ui.Text(c, status).FontSize(12).TextColor(t.TextMuted).Grow(1)
		ui.Button(c, locales.Cancel()).Disabled(r.cancel).OnClick(g.cancel)
	})
	if r.total > 0 && r.unit == "programs" { // 扫描步骤的单位是文件，不显示程序计数
		ui.Text(c, locales.Counts(int(r.done), int(r.total), r.failedCount())).FontSize(12)
	}
	if r.shader != "" {
		ui.Text(c, locales.CurrentShader(r.shader)).FontSize(12).TextColor(t.TextMuted).SingleLine()
	}
	g.liveLog(c)
}

func (g *gui) liveLog(c *ui.Context) {
	ui.Collapsible(c, locales.LiveLog(len(g.run.log)), &g.logOpen, func() {
		g.logBox(c, g.run.logTail(400), 220, true)
	})
}

// logBox 在可滚动的等宽框里显示日志；follow 时随日志增长滚到末尾，除非用户往上滚了。
func (g *gui) logBox(c *ui.Context, text string, height float32, follow bool) {
	t := c.Theme()
	if follow && g.logView.Y >= g.logView.MaxY-4 {
		g.logView.Y = math.MaxFloat32
	}
	ui.Scroll(c).TrackScroll(&g.logView).Height(height).Padding(10).Radius(8).Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Text(c, text).Font("monospace").FontSize(11).NoWrap().Selectable()
	})
}

func (g *gui) done(c *ui.Context) {
	t := c.Theme()
	r := g.run
	partial := r.outcome == outcomePartial
	g.steps(c)
	accent := t.Success
	if partial {
		accent = t.Warning
	}
	ui.Column(c).Gap(10).Padding(16).Radius(12).Background(t.Surface).Border(1, accent).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if partial {
				ui.Icon(c, iconWarn).FontSize(22).TextColor(accent)
				ui.Text(c, locales.PartialTitle()).FontSize(18).Bold()
			} else {
				ui.Icon(c, iconCheck).FontSize(22).TextColor(accent)
				ui.Text(c, locales.DoneTitle()).FontSize(18).Bold()
			}
		})
		ui.Text(c, g.outDir()).Selectable().Font("monospace").FontSize(12)
		s := r.res
		ui.Row(c).Gap(8).Wrap().Children(func() {
			chip(c, iconCheck, t.Success, locales.StatConverted(s.Converted))
			chip(c, iconCircle, t.TextMuted, locales.StatRemoved(s.Removed))
			failedColor := t.TextMuted
			if s.FailedShaders > 0 {
				failedColor = t.Danger
			}
			chip(c, iconX, failedColor, locales.StatFailed(s.FailedShaders))
			chip(c, iconCircle, t.TextMuted, locales.StatShaders(s.Shaders))
			chip(c, iconCircle, t.TextMuted, locales.StatTime(formatDuration(r.finished.Sub(r.started))))
		})
		if partial {
			ui.Text(c, locales.PartialHint()).FontSize(12).TextColor(t.TextMuted)
		}
	})
	if list := sortedFailures(r.failedList()); partial && len(list) > 0 {
		g.failedTable(c, list)
	}
	ui.Row(c).Gap(10).Children(func() {
		ui.PrimaryButton(c, locales.OpenOutput()).OnClick(func() { mygo.Shell.OpenPath(g.outDir()) })
		audit := g.auditPath()
		ui.Button(c, locales.OpenAudit()).Disabled(!exists(audit)).OnClick(func() { mygo.Shell.OpenPath(audit) })
		ui.Spacer(c)
		ui.Button(c, locales.ConvertAnother()).OnClick(g.reset)
	})
	g.liveLog(c)
}

func (g *gui) failedTable(c *ui.Context, list []failure) {
	t := c.Theme()
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, locales.FailedList(len(list))).FontSize(13).Bold()
		ui.Column(c).Gap(2).Padding(10).Radius(8).Background(t.Surface).Border(1, t.Border).Children(func() {
			for i, f := range list {
				if i == maxFailedShown {
					ui.Text(c, locales.FailedMore(len(list)-maxFailedShown)).FontSize(11).TextColor(t.TextMuted)
					break
				}
				line := f.Name
				if line == "" {
					line = f.Reason
				} else if f.Reason != "" {
					line += " — " + f.Reason
				}
				ui.Text(c.Key(fmt.Sprint(i)), line).Font("monospace").FontSize(11).SingleLine().Selectable()
			}
		})
	})
}

// outDir 是结果里的输出目录：优先 CLI 报告的，否则是用户选的。
func (g *gui) outDir() string {
	if g.run != nil && g.run.res.Out != "" {
		return g.run.res.Out
	}
	return g.outPath
}

func (g *gui) auditPath() string {
	if g.run != nil && g.run.res.Audit != "" {
		return g.run.res.Audit
	}
	return filepath.Join(g.outDir(), "dx2metal-audit.json")
}

func (g *gui) failed(c *ui.Context) {
	t := c.Theme()
	r := g.run
	g.steps(c)
	ui.Column(c).Gap(8).Padding(14).Radius(12).Border(1, t.Danger).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconX).FontSize(20).TextColor(t.Danger)
			title := locales.FailedTitle()
			if s := r.failedStep(); s != "" {
				title = locales.FailedAt(s)
			}
			ui.Text(c, title).FontSize(17).Bold()
		})
		ui.Text(c, r.errorText()).Selectable()
	})
	ui.Text(c, locales.LogTail()).FontSize(12).Bold()
	g.logBox(c, r.logTail(40), 200, false)
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		label := locales.CopyLog()
		if time.Since(g.copied) < 2*time.Second {
			label = locales.Copied()
			c.After(2 * time.Second)
		}
		ui.Button(c, label).OnClick(func() {
			mygo.Clipboard.WriteText(r.logText())
			g.copied = time.Now()
		})
		ui.Spacer(c)
		ui.Button(c, locales.Back()).OnClick(g.reset)
		ui.PrimaryButton(c, locales.Retry()).OnClick(func() { g.start() })
	})
}

// setInput 检查拖入或选择的文件夹，并在后台算它的大小。
func (g *gui) setInput(path string) {
	d := checkInput(path)
	g.input, g.notice, g.size = &d, nil, -1
	gen := g.sizeGen.Add(1)
	if d.Problem != nil {
		return
	}
	// 输出默认放在输入旁边；用户改过就不覆盖。
	if g.outPath == "" || g.outAuto {
		g.outPath, g.outAuto = defaultOutput(d.Path), true
	}
	compute := func() {
		n := dirSize(d.Path, func() bool { return g.sizeGen.Load() != gen })
		if g.sizeGen.Load() != gen {
			return
		}
		if g.win == nil { // 无窗口测试：同步完成
			g.size = n
			return
		}
		g.win.Update(func() {
			if g.sizeGen.Load() == gen {
				g.size = n
			}
		})
	}
	if g.win == nil {
		compute()
	} else {
		go compute()
	}
}

func (g *gui) chooseInput() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: g.win, Title: locales.ChooseFolderTitle(), Directory: true})
		if err != nil || len(paths) == 0 {
			return
		}
		g.win.Update(func() { g.setInput(paths[0]) })
	}()
}

func (g *gui) chooseOutput() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: g.win, Title: locales.OutputTitle(), Directory: true, CreateDirectories: true})
		if err != nil || len(paths) == 0 {
			return
		}
		g.win.Update(func() { g.outPath, g.outAuto = paths[0], false })
	}()
}

// confirmStart 在输出目录已有内容时先确认，再开始。
func (g *gui) confirmStart() {
	g.outPath = expandHome(strings.TrimSpace(g.outPath))
	if entries, err := os.ReadDir(g.outPath); err != nil || len(entries) == 0 {
		g.start()
		return
	}
	go func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: g.win, Type: mygo.MessageWarning,
			Message: locales.ReplaceTitle(filepath.Base(g.outPath)), Detail: locales.ReplaceDetail(),
			Buttons: []string{locales.Replace(), locales.Cancel()}})
		if err == nil && res.Button == 0 {
			g.win.Update(g.start)
		}
	}()
}

func (g *gui) start() {
	if err := os.MkdirAll(g.outPath, 0o755); err != nil {
		g.phase, g.notice = phaseIdle, func() string { return locales.MkdirFailed(err.Error()) }
		return
	}
	r := newRun()
	g.run, g.phase, g.notice, g.logOpen = r, phaseRunning, nil, false
	ctx, cancel := context.WithCancel(context.Background())
	g.cancelRun = cancel
	execute(ctx, g.rn, core.Options{In: g.input.Path, Out: g.outPath}, r, g.post, func() {
		cancel()
		switch r.outcome {
		case outcomeOK, outcomePartial:
			g.phase = phaseDone
		case outcomeCancelled:
			g.phase, g.notice = phaseIdle, locales.Cancelled
		default:
			g.phase = phaseFailed
		}
	})
}

// post 把 f 放到主线程执行；无窗口（测试）时直接执行。
func (g *gui) post(f func()) {
	if g.win == nil {
		f()
		return
	}
	g.win.Update(f)
}

func (g *gui) cancel() {
	if g.run != nil && !g.run.ended {
		g.run.cancel = true
		g.cancelRun()
	}
}

func (g *gui) reset() { g.phase, g.run, g.notice = phaseIdle, nil, nil }
