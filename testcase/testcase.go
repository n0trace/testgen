package testcase

import "github.com/go-delve/delve/service/api"

type TestCase struct {
	Args     []api.Variable
	Returns  []api.Variable
	Function *api.Function
	File     string
	SubCases []*TestCase
	Parent   *TestCase
}
