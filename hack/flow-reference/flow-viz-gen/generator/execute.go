/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package generator

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/gengo/generator"
	"k8s.io/klog/v2"

	"github.com/gardener/gardener/hack/flow-reference/flow-viz-gen/parser"
)

func errs2strings(errors []error) []string {
	strs := make([]string, len(errors))
	for i := range errors {
		strs[i] = errors[i].Error()
	}
	return strs
}

func (c *Context) filteredBy(filter func(*Context, string, string) bool) *Context {
	c2 := *c
	c2.Funcs = parser.PackageFuncs{}
	for pkgName, pkgFuncs := range c.Funcs {
		for funcName, funcDecl := range pkgFuncs {
			if filter(c, pkgName, funcName) {
				if c2.Funcs[pkgName] == nil {
					c2.Funcs[pkgName] = parser.Funcs{}
				}
				c2.Funcs[pkgName][funcName] = funcDecl
			}
		}
	}
	return &c2
}

// ExecutePackages runs the generators for every package in 'packages'. 'outDir'
// is the base directory in which to place all the generated packages; it
// should be a physical path on disk, not an import path. e.g.:
// /path/to/home/path/to/gopath/src/
// Each package has its import path already, this will be appended to 'outDir'.
func (c *Context) ExecutePackages(outDir string, packages Packages) error {
	var errors []error
	for _, p := range packages {
		if err := c.ExecutePackage(outDir, p); err != nil {
			errors = append(errors, err)
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf("some packages had errors:\n%v\n", strings.Join(errs2strings(errors), "\n"))
	}
	return nil
}

// ExecutePackage executes a single package. 'outDir' is the base directory in
// which to place the package; it should be a physical path on disk, not an
// import path. e.g.: '/path/to/home/path/to/gopath/src/' The package knows its
// import path already, this will be appended to 'outDir'.
func (c *Context) ExecutePackage(outDir string, p Package) error {
	path := filepath.Join(outDir, p.Path())
	klog.V(2).Infof("Processing package %q, disk location %q", p.Name(), path)
	// Filter out any types the *package* doesn't care about.
	packageContext := c.filteredBy(p.Filter)
	os.MkdirAll(path, 0755)
	files := map[string]*generator.File{}
	for _, g := range p.Generators(packageContext) {
		// Filter out types the *generator* doesn't care about.
		genContext := packageContext

		fileType := g.FileType()
		if len(fileType) == 0 {
			return fmt.Errorf("generator %q must specify a file type", g.Name())
		}
		f := files[g.Filename()]
		if f == nil {
			// This is the first generator to reference this file, so start it.
			f = &generator.File{
				Name:              g.Filename(),
				FileType:          fileType,
				PackageName:       p.Name(),
				PackagePath:       p.Path(),
				PackageSourcePath: p.SourcePath(),
				Header:            p.Header(g.Filename()),
				Imports:           map[string]struct{}{},
			}
			files[f.Name] = f
		} else {
			if f.FileType != g.FileType() {
				return fmt.Errorf("file %q already has type %q, but generator %q wants to use type %q", f.Name, f.FileType, g.Name(), g.FileType())
			}
		}

		if vars := g.PackageVars(genContext); len(vars) > 0 {
			for _, v := range vars {
				if _, err := fmt.Fprintf(&f.Vars, "%s\n", v); err != nil {
					return err
				}
			}
		}
		if consts := g.PackageConsts(genContext); len(consts) > 0 {
			for _, v := range consts {
				if _, err := fmt.Fprintf(&f.Consts, "%s\n", v); err != nil {
					return err
				}
			}
		}
		if err := genContext.executeBody(&f.Body, g); err != nil {
			return err
		}
		if imports := g.Imports(genContext); len(imports) > 0 {
			for _, i := range imports {
				f.Imports[i] = struct{}{}
			}
		}
	}

	var errors []error
	for _, f := range files {
		finalPath := filepath.Join(path, f.Name)
		assembler, ok := c.FileTypes[f.FileType]
		if !ok {
			return fmt.Errorf("the file type %q registered for file %q does not exist in the context", f.FileType, f.Name)
		}
		var err error
		if c.Verify {
			err = assembler.VerifyFile(f, finalPath)
		} else {
			err = assembler.AssembleFile(f, finalPath)
		}
		if err != nil {
			errors = append(errors, err)
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf("errors in package %q:\n%v\n", p.Path(), strings.Join(errs2strings(errors), "\n"))
	}
	return nil
}

func (c *Context) executeBody(w io.Writer, g Generator) error {
	et := generator.NewErrorTracker(w)
	if err := g.Init(c, et); err != nil {
		return err
	}
	for _, fs := range c.Funcs {
		for _, f := range fs {
			if err := g.GenerateFunc(c, f, et); err != nil {
				return err
			}
		}
	}
	if err := g.Finalize(c, et); err != nil {
		return err
	}
	return et.Error()
}
