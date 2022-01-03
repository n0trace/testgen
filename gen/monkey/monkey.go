package monkey

import (
	"bytes"
	"fmt"
)

type generator struct {
	buf                       bytes.Buffer
	indent                    string
	mockNames                 map[string]string // may be empty
	filename                  string            // may be empty
	destination               string            // may be empty
	srcPackage, srcInterfaces string            // may be empty
	copyrightHeader           string

	packageMap map[string]string // map from import path to package name
}

func (g *generator) p(format string, args ...interface{}) {
	fmt.Fprintf(&g.buf, g.indent+format+"\n", args...)
}
