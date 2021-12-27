package record

import (
	"errors"
	"strings"

	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
)

type Record struct {
	client     service.Client
	breakpoint string
	scope      api.EvalScope
}

type Case struct {
	Args     []api.Variable
	Returns  []api.Variable
	Function *api.Function
	Cases    []*Case
}

func NewRecord(client service.Client, breakpoint string) *Record {
	return &Record{
		client:     client,
		breakpoint: breakpoint,
		scope:      api.EvalScope{GoroutineID: -1},
	}
}

func (r *Record) Init() (err error) {
	var (
		locations  []api.Location
		breakpoint *api.Breakpoint
	)

	if locations, err = r.client.FindLocation(api.EvalScope{GoroutineID: -1}, r.breakpoint, true, nil); err != nil {
		return
	}

	if len(locations) == 0 {
		err = errors.New("not found breakpoint")
		return
	}

	if breakpoint, err = r.client.CreateBreakpoint(&api.Breakpoint{File: locations[0].File, Line: locations[0].Line}); err != nil {
		err = nil
	}
	_ = breakpoint
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

func (r *Record) R() (cases []*Case, err error) {
	var current, next *api.DebuggerState
	if current, err = r.client.GetState(); err != nil {
		return
	}
	if next, err = r.client.Step(); err != nil {
		return
	}
	_, cases, err = r.loop(nil, current, next)
	return
}

func (r *Record) loop(parent, prev, current *api.DebuggerState) (returns []api.Variable, cases []*Case, err error) {
	var next *api.DebuggerState
	if !strings.Contains(current.CurrentThread.Function.Name(), "main") {
		if next, err = r.client.StepOut(); err != nil {
			return
		}
		return r.loop(parent, current, next)
	}

	if current.CurrentThread.Function.Name() == prev.CurrentThread.Function.Name() {
		if next, err = r.client.Step(); err != nil {
			return
		}
		return r.loop(parent, current, next)
	}

	if parent != nil && current.CurrentThread.Function.Name() == parent.CurrentThread.Function.Name() {
		returns = current.CurrentThread.ReturnValues
		return
	}

	var args []api.Variable
	if args, err = r.client.ListFunctionArgs(r.scope, api.LoadConfig{}); err != nil {
		return
	}

	c := &Case{
		Args:     args,
		Function: current.CurrentThread.Function,
	}

	if next, err = r.client.Step(); err != nil {
		return
	}

	if c.Returns, c.Cases, err = r.loop(parent, current, next); err != nil {
		return
	}

	cases = append(cases, c)
	return
}
