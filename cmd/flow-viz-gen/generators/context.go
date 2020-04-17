package generators

import (
	"github.com/gardener/gardener/cmd/flow-viz-gen/parser"
)

// Context is global context for individual generators to consume.
type Context struct {
	// All the user-specified packages. This is after recursive expansion.
	Inputs []string

	// Allows generators to add packages at runtime.
	Builder *parser.Builder
}

// NewContext generates a context from the given builder, naming systems, and
// the naming system you wish to construct the canonical ordering from.
func NewContext(b *parser.Builder) (*Context, error) {
	c := &Context{
		Inputs:  b.FindPackages(),
		Builder: b,
	}

	return c, nil
}
