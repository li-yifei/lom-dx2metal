package core

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/li-yifei/lom-dx2metal/internal/metal"
	"github.com/li-yifei/lom-dx2metal/internal/serialized"
	"github.com/li-yifei/lom-dx2metal/internal/shaderblob"
)

// InputError 表示输入有问题（CLI 退出码 2）。
type InputError struct{ msg string }

func (e *InputError) Error() string { return e.msg }

func inputErr(format string, a ...any) error { return &InputError{fmt.Sprintf(format, a...)} }

// 步骤标题（与 progress.py 的 STEPS 对应）。
var stepTitles = map[string]string{
	"scan":    "扫描资产文件",
	"convert": "转换着色器",
	"write":   "写出资产文件",
}

// emitter 包装事件回调：step 计时与 progress 节流（与 progress.py 相同：0.25 秒内只发一次，完成时必发）。
type emitter struct {
	on       func(Event)
	mu       sync.Mutex
	current  string
	started  time.Time
	lastProg time.Time
}

func (e *emitter) emit(ev Event) {
	if e.on != nil {
		e.on(ev)
	}
}

func (e *emitter) step(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.current != "" {
		e.emit(Event{Kind: "step", Step: e.current, State: "done", Seconds: math.Round(time.Since(e.started).Seconds()*10) / 10})
	}
	e.current, e.started = name, time.Now()
	if name != "" {
		e.emit(Event{Kind: "step", Step: name, State: "start", Title: stepTitles[name]})
	}
}

func (e *emitter) progress(current, total int, unit, detail string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if now.Sub(e.lastProg) < 250*time.Millisecond && current < total {
		return
	}
	e.lastProg = now
	e.emit(Event{Kind: "progress", Step: e.current, Current: current, Total: total, Unit: unit, Detail: detail})
}

func (e *emitter) error(msg string) {
	e.mu.Lock()
	step := e.current
	e.mu.Unlock()
	e.emit(Event{Kind: "error", Step: step, Message: msg})
}

type auditFile struct {
	File    string `json:"file"`
	Written bool   `json:"written,omitempty"`
	Shaders int    `json:"shaders,omitempty"`
	Error   string `json:"error,omitempty"`
}

type audit struct {
	Data    string                   `json:"data"`
	Out     string                   `json:"out"`
	Files   []auditFile              `json:"files"`
	Shaders map[string]*shaderReport `json:"shaders"`
	Totals  *Result                  `json:"totals"`
	// globalgamemanagers 里 BuildSettings.m_GraphicsAPIs 的改动记录（--keep-graphics-api 时为空）。
	GraphicsAPIs *graphicsAudit `json:"graphics_apis,omitempty"`
}

type graphicsAudit struct {
	File    string `json:"file"`
	Before  []int  `json:"before"`
	After   []int  `json:"after"`
	Written bool   `json:"written"`
	Error   string `json:"error,omitempty"`
}

// target 是扫描出的一个待转换文件。
type target struct {
	path, rel string
	programs  int
	shaders   int
}

func run(ctx context.Context, opt Options, onEvent func(Event)) (res *Result, err error) {
	em := &emitter{on: onEvent}
	logf := func(format string, a ...any) {
		if opt.Log != nil {
			opt.Log(fmt.Sprintf(format, a...))
		}
	}
	defer func() {
		if err != nil {
			em.error(err.Error())
			em.step("")
			em.emit(Event{Kind: "result", OK: false, Result: res})
		}
	}()
	if opt.In == "" {
		return nil, inputErr("缺少 --in")
	}
	if opt.Out == "" {
		return nil, inputErr("缺少 --out（不做原地改写）")
	}
	in, err := filepath.Abs(opt.In)
	if err != nil {
		return nil, inputErr("%v", err)
	}
	out, err := filepath.Abs(opt.Out)
	if err != nil {
		return nil, inputErr("%v", err)
	}
	st, err := os.Stat(in)
	if err != nil {
		return nil, inputErr("输入不存在：%s", in)
	}
	if out == in || strings.HasPrefix(out+string(filepath.Separator), in+string(filepath.Separator)) && st.IsDir() {
		return nil, inputErr("--out 不能在 --in 里面")
	}
	auditPath := opt.AuditPath
	if auditPath == "" {
		auditPath = filepath.Join(out, "dx2metal-audit.json")
	}
	jobs := opt.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	only := map[string]bool{}
	for _, n := range opt.Only {
		only[n] = true
	}
	wanted := func(tree *serialized.Struct) bool {
		p := shaderblob.ShaderPlatforms(tree)
		if len(p) != 1 || p[0] != platformD3D11 {
			return false
		}
		return len(only) == 0 || only[tree.S("m_ParsedForm").Str("m_Name")]
	}

	// 1. 扫描。
	em.step("scan")
	var candidates []string
	base := in
	if st.IsDir() {
		candidates = shaderFiles(in)
	} else {
		candidates = []string{in}
		base = filepath.Dir(in)
	}
	var targets []target
	totalPrograms := 0
	result := &Result{Out: out, Audit: auditPath}
	au := &audit{Data: in, Out: out, Shaders: map[string]*shaderReport{}, Totals: result}
	for i, path := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(base, path)
		em.progress(i, len(candidates), "files", rel)
		b, err := os.ReadFile(path)
		if err != nil {
			au.Files = append(au.Files, auditFile{File: rel, Error: err.Error()})
			continue
		}
		a, err := loadAsset(b)
		if err != nil {
			if !st.IsDir() {
				return nil, inputErr("%s：%v", rel, err)
			}
			au.Files = append(au.Files, auditFile{File: rel, Error: truncate(err.Error(), 300)})
			continue
		}
		tg := target{path: path, rel: rel}
		for _, f := range a.files {
			for _, o := range f.Shaders() {
				tree, err := f.ReadObject(o)
				if err != nil || !wanted(tree) {
					continue
				}
				programs, err := shaderblob.UnpackShader(tree, platformD3D11)
				if err == nil {
					tg.programs += len(programs)
				}
				tg.shaders++
			}
		}
		if tg.shaders > 0 {
			targets = append(targets, tg)
			totalPrograms += tg.programs
		}
	}
	em.progress(len(candidates), len(candidates), "files", "")
	gfxData, err := patchGraphicsAPIs(in, st.IsDir(), opt.KeepGraphicsAPI, au, result)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 && gfxData == nil { // 既没有 shader 要转、也没有图形 API 要改
		return nil, inputErr("%s 下没有只带 D3D11 程序的 Shader", in)
	}
	logf("找到 %d 个含 D3D11 Shader 的文件，共 %d 个程序", len(targets), totalPrograms)

	var dev *metal.Device
	if len(targets) > 0 {
		if dev, err = metal.NewDevice(); err != nil {
			return nil, err
		}
	}

	// 2. 转换并写出（逐文件；文件内按 shader 并行）。
	em.step("convert")
	done := 0
	var doneMu sync.Mutex
	for _, tg := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(tg.path)
		if err != nil {
			au.Files = append(au.Files, auditFile{File: tg.rel, Error: err.Error()})
			continue
		}
		a, err := loadAsset(b)
		if err != nil {
			au.Files = append(au.Files, auditFile{File: tg.rel, Error: err.Error()})
			continue
		}
		type job struct {
			file *serialized.File
			obj  *serialized.Object
			tree *serialized.Struct
		}
		var list []job
		for _, f := range a.files {
			for _, o := range f.Shaders() {
				tree, err := f.ReadObject(o)
				if err != nil || !wanted(tree) {
					continue
				}
				list = append(list, job{f, o, tree})
			}
		}
		reports := make([]*shaderReport, len(list))
		tails := make([]map[int]*shaderblob.Tail, len(list))
		sem := make(chan struct{}, jobs)
		var wg sync.WaitGroup
		for i, j := range list {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				start := time.Now()
				name := j.tree.S("m_ParsedForm").Str("m_Name")
				programs := 0
				if p, err := shaderblob.UnpackShader(j.tree, platformD3D11); err == nil {
					programs = len(p)
				}
				rep, tl, err := safeConvert(ctx, dev, j.tree)
				if err != nil {
					rep = &shaderReport{Shader: name, Error: truncate(err.Error(), 500), Failures: map[string]string{}}
					tl = nil
				}
				rep.Asset, rep.PathID = filepath.ToSlash(tg.rel), j.obj.PathID
				rep.Seconds = math.Round(time.Since(start).Seconds()*10) / 10
				reports[i], tails[i] = rep, tl
				doneMu.Lock()
				done += programs
				d := done
				doneMu.Unlock()
				em.progress(d, totalPrograms, "programs", name)
			}()
		}
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		changed := 0
		for i, j := range list {
			rep := reports[i]
			if len(tails[i]) > 0 {
				if err := j.file.WriteObject(j.obj, j.tree); err != nil {
					rep.Error = "write: " + truncate(err.Error(), 300)
					tails[i] = nil
				} else {
					rep.Platforms = []int{platformMetal}
					changed++
				}
			}
			if len(tails[i]) == 0 {
				result.FailedShaders++
				result.Failed = append(result.Failed, fmt.Sprintf("%s:%d %s", rep.Asset, rep.PathID, rep.Shader))
			}
			result.Shaders++
			result.Programs += rep.Programs
			result.Converted += rep.Converted
			result.Removed += rep.RemovedSubprograms
			result.PadReads += rep.PadReads
			if len(rep.Failures) > 0 && len(tails[i]) > 0 {
				result.Failed = append(result.Failed, fmt.Sprintf("%s:%d %s（%d 个变体失败）", rep.Asset, rep.PathID, rep.Shader, len(rep.Failures)))
			}
			msg := fmt.Sprintf("  %s %s: %d/%d 个程序", rep.Asset, rep.Shader, rep.Converted, rep.Programs)
			if rep.RemovedSubprograms > 0 {
				msg += fmt.Sprintf("，移除 %d 个变体", rep.RemovedSubprograms)
			}
			if rep.Error != "" {
				msg += "，错误：" + rep.Error
			}
			logf("%s", msg)
			au.Shaders[fmt.Sprintf("%s:%d", rep.Asset, rep.PathID)] = rep
		}
		if changed == 0 {
			au.Files = append(au.Files, auditFile{File: tg.rel, Shaders: 0})
			continue
		}
		data, err := a.bytes()
		if err != nil {
			au.Files = append(au.Files, auditFile{File: tg.rel, Error: "write: " + err.Error()})
			continue
		}
		// 读回写出的字节，确认每个 tail 都能逐项往返（与 Python 版的 tail_readback_mismatch 相同）。
		if err := readback(data, list2(list, tails, reports)); err != nil {
			au.Files = append(au.Files, auditFile{File: tg.rel, Error: "readback: " + err.Error()})
			continue
		}
		dst := filepath.Join(out, tg.rel)
		if err := writeAtomic(dst, data); err != nil {
			return nil, err
		}
		au.Files = append(au.Files, auditFile{File: filepath.ToSlash(tg.rel), Written: true, Shaders: changed})
	}
	em.progress(totalPrograms, totalPrograms, "programs", "")

	// 3. audit。
	em.step("write")
	if gfxData != nil {
		if err := writeAtomic(filepath.Join(out, "globalgamemanagers"), gfxData); err != nil {
			return nil, err
		}
		au.GraphicsAPIs.Written = true
		logf("globalgamemanagers：图形 API %v → %v", au.GraphicsAPIs.Before, au.GraphicsAPIs.After)
	}
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
		return nil, err
	}
	ab, _ := json.MarshalIndent(au, "", " ")
	if err := os.WriteFile(auditPath, append(ab, '\n'), 0o644); err != nil {
		return nil, err
	}
	em.step("")
	ok := result.FailedShaders == 0
	for _, f := range au.Files {
		if f.Error != "" {
			ok = false
		}
	}
	em.emit(Event{Kind: "result", OK: ok, Result: result})
	return result, nil
}

// safeConvert 转换一个 shader；panic 转成错误（单个 shader 出问题不拖垮整个 Run）。
func safeConvert(ctx context.Context, dev *metal.Device, tree *serialized.Struct) (rep *shaderReport, tails map[int]*shaderblob.Tail, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("内部错误：%v", r)
		}
	}()
	return convertShader(ctx, dev, tree)
}

type readbackItem struct {
	pathID int64
	tails  map[int]*shaderblob.Tail
	report *shaderReport
}

func list2[J any](list []J, tails []map[int]*shaderblob.Tail, reports []*shaderReport) []readbackItem {
	var out []readbackItem
	for i := range list {
		if len(tails[i]) > 0 {
			out = append(out, readbackItem{reports[i].PathID, tails[i], reports[i]})
		}
	}
	return out
}

func readback(data []byte, items []readbackItem) error {
	a, err := loadAsset(data)
	if err != nil {
		return err
	}
	byID := map[int64]*serialized.Struct{}
	for _, f := range a.files {
		for _, o := range f.Shaders() {
			for _, it := range items {
				if it.pathID == o.PathID {
					tree, err := f.ReadObject(o)
					if err != nil {
						return err
					}
					byID[o.PathID] = tree
				}
			}
		}
	}
	for _, it := range items {
		tree := byID[it.pathID]
		if tree == nil {
			return fmt.Errorf("读回时找不到 path_id %d", it.pathID)
		}
		programs, err := shaderblob.UnpackShader(tree, platformMetal)
		if err != nil {
			return err
		}
		bad := []int{}
		keys := make([]int, 0, len(it.tails))
		for k := range it.tails {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		for _, i := range keys {
			got, err := shaderblob.ParseTail(programs[i].Parameters)
			if err != nil || !got.Equal(it.tails[i]) {
				bad = append(bad, i)
			}
		}
		it.report.TailReadbackMismatch = bad
		if len(bad) > 0 {
			return fmt.Errorf("path_id %d 有 %d 个 tail 读回不一致", it.pathID, len(bad))
		}
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".dx2metal-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// patchGraphicsAPIs 把 globalgamemanagers 里 BuildSettings.m_GraphicsAPIs 改成只含 Metal（16），省得玩家拼 .app 时手工改
// （Python 版 assemble.patch_settings 的同一步）。只有列表不是 [16] 时才返回要写出的字节；结果记入 audit。
// in 为目录时处理 <in>/globalgamemanagers；in 为单个文件且名为 globalgamemanagers 时处理它本身。
func patchGraphicsAPIs(in string, isDir, keep bool, au *audit, result *Result) ([]byte, error) {
	path := in
	if isDir {
		path = filepath.Join(in, "globalgamemanagers")
	} else if filepath.Base(in) != "globalgamemanagers" {
		return nil, nil
	}
	if keep {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil // 没有 globalgamemanagers：不是错误（例如只给了 bundle 目录）
	}
	rec := &graphicsAudit{File: "globalgamemanagers", After: []int{serialized.GraphicsMetal}}
	au.GraphicsAPIs = rec
	f, err := serialized.Parse(b)
	if err == nil {
		var changed bool
		rec.Before, changed, err = f.SetGraphicsAPIs(rec.After)
		if err == nil {
			result.GraphicsBefore = rec.Before
			if !changed {
				return nil, nil
			}
			result.GraphicsPatched = true
			return f.Bytes(), nil
		}
	}
	rec.Error = truncate(err.Error(), 300)
	if !isDir {
		return nil, inputErr("globalgamemanagers：%v", err)
	}
	return nil, nil
}
