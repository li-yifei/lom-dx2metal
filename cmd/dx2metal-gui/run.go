package main

import (
	"errors"
	"strings"
	"time"

	"lom-dx2metal/convert"

	"lom-dx2metal/cmd/dx2metal-gui/locales"
)

// stepDef 是 convert 命令的一个步骤，weight 是它在总进度条里的占比。
type stepDef struct {
	ID     string
	Title  func() string
	Weight float64
}

// 步骤与 DESIGN.md「CLI 契约」里的 step 字段一一对应；convert 是绝对大头。
var steps = []stepDef{
	{"scan", locales.StepScan, 10},
	{"convert", locales.StepConvert, 80},
	{"write", locales.StepWrite, 10},
}

type stepState int

const (
	stepPending stepState = iota
	stepRunning
	stepDone
	stepFailed
)

// failure 是一个转换失败的 shader。
type failure struct {
	Name   string
	Reason string
}

// outcome 是一次转换的结局。
type outcome int

const (
	outcomeNone outcome = iota
	outcomeOK
	outcomePartial // 退出码 3：部分 shader 失败，其余已输出
	outcomeFailed
	outcomeCancelled
)

// run 是一次转换的状态，只在主线程上修改。
type run struct {
	state     []stepState
	stepStart []time.Time
	current   int     // 正在运行的步骤下标，开始前为 -1
	fraction  float64 // 本步进度，未知为 -1
	unit      string
	done      float64
	total     float64
	shader    string // 当前 shader 名
	started   time.Time
	finished  time.Time

	log     []string // convert.Options.Log 的日志行
	errors  []failure
	input   bool           // Run 返回了 *convert.InputError
	errMsg  string         // 最后一条 error 事件的消息，或 Run 返回的错误
	res     convert.Result // Run 返回的汇总（出错时为零值）
	outcome outcome
	cancel  bool // 用户请求取消
	ended   bool
}

func newRun() *run {
	return &run{state: make([]stepState, len(steps)), stepStart: make([]time.Time, len(steps)),
		current: -1, fraction: -1, started: time.Now()}
}

func stepIndex(id string) int {
	for i, s := range steps {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// logLine 记一行日志。
func (r *run) logLine(text string) { r.log = append(r.log, text) }

// apply 处理一个进度事件。
func (r *run) apply(ev convert.Event) {
	i := stepIndex(ev.Step)
	switch ev.Kind {
	case "step":
		if i < 0 {
			return
		}
		switch ev.State {
		case "start":
			// 前面的步骤即使没看到 done 也算完成。
			for j := 0; j < i; j++ {
				if r.state[j] != stepFailed {
					r.state[j] = stepDone
				}
			}
			if r.state[i] != stepRunning {
				r.stepStart[i] = time.Now()
			}
			r.state[i], r.current = stepRunning, i
			r.fraction, r.unit, r.done, r.total = -1, "", 0, 0
		case "done":
			r.state[i] = stepDone
		}
	case "progress":
		if i < 0 || i != r.current {
			return
		}
		r.unit, r.done, r.total = ev.Unit, float64(ev.Current), float64(ev.Total)
		if ev.Total > 0 {
			r.fraction = min(1, float64(ev.Current)/float64(ev.Total))
		}
		if ev.Detail != "" {
			r.shader = ev.Detail
		}
	case "error":
		// 事件不区分“单个 shader 失败”和“致命错误”：都记下来，
		// 最终是否失败由 Run 的返回值决定。
		r.errMsg = ev.Message
		r.errors = append(r.errors, failure{Name: ev.Detail, Reason: ev.Message})
		if i >= 0 {
			r.current = i
		}
	}
}

// end 记录 Run 的返回并定结局：取消优先；有错误 = 失败；有 shader 失败 = 部分成功。
func (r *run) end(res *convert.Result, err error) {
	r.ended, r.finished = true, time.Now()
	if res != nil {
		r.res = *res
	}
	switch {
	case r.cancel:
		r.outcome = outcomeCancelled
	case err != nil:
		r.outcome = outcomeFailed
		r.errMsg = err.Error()
		var ie *convert.InputError
		r.input = errors.As(err, &ie)
	case r.res.FailedShaders > 0: // 与 CLI 的退出码 3 一致；Failed 里还含只删了变体的 shader，不算失败
		r.outcome = outcomePartial
	default:
		r.outcome = outcomeOK
	}
	switch r.outcome {
	case outcomeOK, outcomePartial:
		for j := range r.state {
			r.state[j] = stepDone
		}
	default:
		if r.current >= 0 && r.state[r.current] == stepRunning {
			r.state[r.current] = stepFailed
		} else if r.current < 0 {
			r.state[0], r.current = stepFailed, 0
		}
	}
}

// failedList 是失败 shader 列表：优先用 Result.Failed，其次用 error 事件。
func (r *run) failedList() []failure {
	if len(r.res.Failed) > 0 {
		list := make([]failure, len(r.res.Failed))
		for i, name := range r.res.Failed {
			list[i] = failure{Name: name}
		}
		return list
	}
	var list []failure
	for _, f := range r.errors {
		if f.Name != "" || f.Reason != "" {
			list = append(list, f)
		}
	}
	return list
}

// failedCount 是进行中显示的失败数。
func (r *run) failedCount() int { return len(r.errors) }

// errorText 是失败原因：Run 的错误或最后一条 error 事件。
func (r *run) errorText() string {
	if r.input {
		return locales.InputProblem(r.errMsg)
	}
	if r.errMsg != "" {
		return r.errMsg
	}
	return locales.FailedUnknown()
}

// failedStep 是出错步骤的标题，没有则为 ""。
func (r *run) failedStep() string {
	for i, s := range r.state {
		if s == stepFailed {
			return steps[i].Title()
		}
	}
	return ""
}

// overall 是总进度，0 到 1。
func (r *run) overall() float64 {
	var total, done float64
	for i, s := range steps {
		total += s.Weight
		switch r.state[i] {
		case stepDone:
			done += s.Weight
		case stepRunning:
			if r.fraction > 0 {
				done += s.Weight * r.fraction
			}
		}
	}
	return done / total
}

// eta 按本步已有速度估计剩余时间，进度太少时不估。
func (r *run) eta() (time.Duration, bool) {
	if r.current < 0 || r.state[r.current] != stepRunning || r.fraction < 0.05 || r.fraction >= 1 {
		return 0, false
	}
	elapsed := time.Since(r.stepStart[r.current])
	if elapsed < 3*time.Second {
		return 0, false
	}
	return time.Duration(float64(elapsed) * (1 - r.fraction) / r.fraction), true
}

func (r *run) logText() string { return strings.Join(r.log, "\n") + "\n" }

func (r *run) logTail(n int) string {
	return strings.Join(r.log[max(0, len(r.log)-n):], "\n")
}
