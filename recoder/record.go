package recoder

import (
	"errors"
	"strings"

	"github.com/go-delve/delve/pkg/terminal"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/n0trace/testgen/testcase"
)

type Recorder struct {
	client     service.Client
	breakpoint string
	scope      api.EvalScope
}

func NewRecorder(client service.Client, breakpoint string) (recoder *Recorder, err error) {
	loadConfig := &api.LoadConfig{
		FollowPointers:     true,
		MaxVariableRecurse: 1,
		MaxStringLen:       64,
		MaxArrayValues:     64,
		MaxStructFields:    -1,
	}
	recoder = &Recorder{
		client:     client,
		scope:      api.EvalScope{GoroutineID: -1},
		breakpoint: breakpoint,
	}
	client.SetReturnValuesLoadConfig(loadConfig)
	err = recoder.init()
	return
}

func (r *Recorder) init() (err error) {
	var locations []api.Location

	if locations, err = r.client.FindLocation(r.scope, r.breakpoint, true, nil); err != nil {
		return
	}

	if len(locations) == 0 {
		err = errors.New("not found breakpoint")
		return
	}

	if _, err = r.client.CreateBreakpoint(&api.Breakpoint{
		File: locations[0].File, Line: locations[0].Line,
	}); err != nil {
		err = nil
	}

	stateChan := r.client.Continue()
	var state *api.DebuggerState
	for state = range stateChan {
		if state.Err != nil {
			err = state.Err
			return
		}
	}

	return
}

func (r *Recorder) R() (cases []*testcase.TestCase, err error) {
	current, next := &Step{}, &Step{}

	if current.DebuggerState, err = r.client.GetState(); err != nil {
		return
	}

	if current.stackers, err = r.client.Stacktrace(
		-1, 5, api.StacktraceSimple, &terminal.ShortLoadConfig); err != nil {
		return
	}

	if next.DebuggerState, err = r.client.Step(); err != nil {
		return
	}

	if next.stackers, err = r.client.Stacktrace(
		-1, 5, api.StacktraceSimple, &terminal.ShortLoadConfig); err != nil {
		return
	}

	c := &testcase.TestCase{Function: current.CurrentThread.Function, File: current.CurrentThread.File}
	if c.Args, err = r.client.ListFunctionArgs(r.scope, terminal.ShortLoadConfig); err != nil {
		return
	}
	if err = r.cursor(current, next, c); err != nil {
		return
	}
	return []*testcase.TestCase{c}, err
}

type Step struct {
	*api.DebuggerState
	stackers []api.Stackframe
}

func min(ints ...int) int {
	switch len(ints) {
	case 0:
	case 1:
		return ints[0]
	case 2:
		if ints[0] < ints[1] {
			return ints[0]
		}
		return ints[1]
	}
	return min(ints[0], min(ints[1:]...))
}

type Relation int

const (
	Same    Relation = 0
	Parent  Relation = 1
	Child   Relation = -1
	Nothing Relation = -2
)

func Compare(a, b Step) (r Relation) {
	if len(a.stackers) == 0 || len(b.stackers) == 0 {
		r = Nothing
		return
	}
	for i := 0; i < min(len(a.stackers), len(b.stackers), 2); i++ {
		if a.stackers[i].PC == b.stackers[i].PC {
			r = Same
			return
		}
	}
	a0 := a.stackers[1]
	for i := 1; i < len(b.stackers); i++ {
		if a0.PC == b.stackers[i].PC {
			r = Child
			return r
		}
	}
	b0 := b.stackers[1]
	for i := 1; i < len(a.stackers); i++ {
		if b0.PC == a.stackers[i].PC {
			r = Parent
			return r
		}
	}
	r = Nothing
	return
}

func (r *Recorder) cursor(prev, current *Step, c *testcase.TestCase) (err error) {
	if prev == nil {
		return
	}

	next := &Step{}
	if current.stackers, err = r.client.Stacktrace(
		-1, 5, api.StacktraceSimple, &terminal.ShortLoadConfig); err != nil {
		return
	}

	switch Compare(*prev, *current) {
	case Same:
		if next.DebuggerState, err = r.client.Step(); err != nil {
			return
		}
		err = r.cursor(current, next, c)
		return
	case Parent:
		c.Returns = current.CurrentThread.ReturnValues
		if c.Parent == nil {
			return
		}
		if next.DebuggerState, err = r.client.Step(); err != nil {
			return
		}
		err = r.cursor(current, next, c.Parent)
		return
	case Child:
		var args []api.Variable
		if args, err = r.client.ListFunctionArgs(r.scope, terminal.ShortLoadConfig); err != nil {
			return
		}
		nc := &testcase.TestCase{
			Args:     args,
			Function: current.CurrentThread.Function,
			File:     current.CurrentThread.File,
			Parent:   c,
		}
		c.SubCases = append(c.SubCases, nc)
		if !strings.Contains(current.CurrentThread.Function.Name(), "main") {
			if next.DebuggerState, err = r.client.StepOut(); err != nil {
				return
			}
			return r.cursor(current, next, nc)
		}

		if next.DebuggerState, err = r.client.Step(); err != nil {
			return
		}
		err = r.cursor(current, next, nc)
		return
	case Nothing:
		return nil
	}
	return nil
}
