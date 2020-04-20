package args

import (
	goflag "flag"
	"fmt"
	"os"
	"strings"

	"github.com/gardener/gardener/hack/flow-reference/flow-viz-gen/generator"
	"github.com/gardener/gardener/hack/flow-reference/flow-viz-gen/parser"

	"github.com/spf13/pflag"
)

func Default() *GeneratorArgs {
	cwd, err := os.Getwd()
	if err != nil {
		panic(fmt.Errorf("failed to get current working directory: %v", err))
	}

	return &GeneratorArgs{
		InputDirs:    []string{cwd},
		OutputSuffix: "_flow",
	}
}

type GeneratorArgs struct {
	InputDirs    []string
	OutputBase   string
	OutputSuffix string
}

func (g *GeneratorArgs) AddFlags(fs *pflag.FlagSet) {
	fs.StringSliceVarP(&g.InputDirs, "input-dirs", "i", g.InputDirs, "Comma-separated list of import paths to get input types from (defaults to the current working directory)")
	fs.StringVarP(&g.OutputBase, "output-base", "o", g.OutputBase, "Output base; defaults to $GOPATH/src/ or ./ if $GOPATH is not set.")
	fs.StringVarP(&g.OutputSuffix, "output-suffix", "s", g.OutputSuffix, "Suffix for generated files (defaults to _flow)")
}

func (g *GeneratorArgs) Execute(pkgs func(*generator.Context, *GeneratorArgs) generator.Packages) error {
	g.AddFlags(pflag.CommandLine)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)
	pflag.Parse()

	b, err := g.NewBuilder()
	if err != nil {
		return fmt.Errorf("Failed making a parser: %v", err)
	}

	c, err := generator.NewContext(b)
	if err != nil {
		return fmt.Errorf("Failed making a context: %v", err)
	}

	packages := pkgs(c, g)
	if err := c.ExecutePackages(g.OutputBase, packages); err != nil {
		return fmt.Errorf("Failed executing generator: %v", err)
	}

	return nil
}

// NewBuilder makes a new parser.Builder and populates it with the input
// directories.
func (g *GeneratorArgs) NewBuilder() (*parser.Builder, error) {
	b := parser.New()

	for _, d := range g.InputDirs {
		var err error
		if strings.HasSuffix(d, "/...") {
			err = b.AddDirRecursive(strings.TrimSuffix(d, "/..."))
		} else {
			err = b.AddDir(d)
		}
		if err != nil {
			return nil, fmt.Errorf("unable to add directory %q: %v", d, err)
		}
	}
	return b, nil
}
