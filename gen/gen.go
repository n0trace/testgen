package gen

import "github.com/n0trace/testgen/testcase"

type Gen interface {
	AddCase(cases []testcase.TestCase)
	Generate() error
}
