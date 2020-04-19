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

package parser

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/klog"
)

// This clarifies when a pkg path has been canonicalized.
type importPathString string

// Builder lets you add all the go files in all the packages that you care
// about, then constructs the type source data.
type Builder struct {
	context *build.Context

	// If true, include *_test.go
	IncludeTestFiles bool

	// Map of package names to more canonical information about the package.
	// This might hold the same value for multiple names, e.g. if someone
	// referenced ./pkg/name or in the case of vendoring, which canonicalizes
	// differently that what humans would type.
	buildPackages map[string]*build.Package

	fset *token.FileSet
	// map of package path to list of parsed files
	parsed map[importPathString][]parsedFile
	// map of package path to absolute path (to prevent overlap)
	absPaths map[importPathString]string

	// All comments from everywhere in every parsed file.
	endLineToCommentGroup map[fileLine]*ast.CommentGroup
}

// parsedFile is for tracking files with name
type parsedFile struct {
	name string
	file *ast.File
}

// key type for finding comments.
type fileLine struct {
	file string
	line int
}

type (
	PackageFuncs map[string]Funcs
	Funcs        map[string]*ast.FuncDecl
)

// New constructs a new builder.
func New() *Builder {
	c := build.Default
	if c.GOROOT == "" {
		if p, err := exec.Command("which", "go").CombinedOutput(); err == nil {
			// The returned string will have some/path/bin/go, so remove the last two elements.
			c.GOROOT = filepath.Dir(filepath.Dir(strings.Trim(string(p), "\n")))
		} else {
			klog.Warningf("Warning: $GOROOT not set, and unable to run `which go` to find it: %v\n", err)
		}
	}
	// Force this to off, since we don't properly parse CGo.  All symbols must
	// have non-CGo equivalents.
	c.CgoEnabled = false
	return &Builder{
		context:               &c,
		buildPackages:         map[string]*build.Package{},
		fset:                  token.NewFileSet(),
		parsed:                map[importPathString][]parsedFile{},
		absPaths:              map[importPathString]string{},
		endLineToCommentGroup: map[fileLine]*ast.CommentGroup{},
	}
}

// Get package information from the go/build package. Automatically excludes
// e.g. test files and files for other platforms-- there is quite a bit of
// logic of that nature in the build package.
func (b *Builder) importBuildPackage(dir string) (*build.Package, error) {
	if buildPkg, ok := b.buildPackages[dir]; ok {
		return buildPkg, nil
	}
	// This validates the `package foo // github.com/bar/foo` comments.
	buildPkg, err := b.importWithMode(dir, build.ImportComment)
	if err != nil {
		if _, ok := err.(*build.NoGoError); !ok {
			return nil, fmt.Errorf("unable to import %q: %v", dir, err)
		}
	}
	if buildPkg == nil {
		// Might be an empty directory. Try to just find the dir.
		buildPkg, err = b.importWithMode(dir, build.FindOnly)
		if err != nil {
			return nil, err
		}
	}

	// Remember it under the user-provided name.
	klog.V(5).Infof("saving buildPackage %s", dir)
	b.buildPackages[dir] = buildPkg
	canonicalPackage := canonicalizeImportPath(buildPkg.ImportPath)
	if dir != string(canonicalPackage) {
		// Since `dir` is not the canonical name, see if we knew it under another name.
		if buildPkg, ok := b.buildPackages[string(canonicalPackage)]; ok {
			return buildPkg, nil
		}
		// Must be new, save it under the canonical name, too.
		klog.V(5).Infof("saving buildPackage %s", canonicalPackage)
		b.buildPackages[string(canonicalPackage)] = buildPkg
	}

	return buildPkg, nil
}

// addFile adds a file to the set. The pkgPath must be of the form
// "canonical/pkg/path" and the path must be the absolute path to the file. A
// flag indicates whether this file was user-requested or just from following
// the import graph.
func (b *Builder) addFile(pkgPath importPathString, path string, src []byte, userRequested bool) error {
	for _, p := range b.parsed[pkgPath] {
		if path == p.name {
			klog.V(5).Infof("addFile %s %s already parsed, skipping", pkgPath, path)
			return nil
		}
	}
	klog.V(6).Infof("addFile %s %s", pkgPath, path)
	p, err := parser.ParseFile(b.fset, path, src, parser.DeclarationErrors|parser.ParseComments)
	if err != nil {
		return err
	}

	b.parsed[pkgPath] = append(b.parsed[pkgPath], parsedFile{path, p})
	for _, c := range p.Comments {
		position := b.fset.Position(c.End())
		b.endLineToCommentGroup[fileLine{position.Filename, position.Line}] = c
	}

	return nil
}

// AddDir adds an entire directory, scanning it for go files. 'dir' should have
// a single go package in it. GOPATH, GOROOT, and the location of your go
// binary (`which go`) will all be searched if dir doesn't literally resolve.
func (b *Builder) AddDir(dir string) error {
	_, err := b.importPackage(dir, true)
	return err
}

// AddDirRecursive is just like AddDir, but it also recursively adds
// subdirectories; it returns an error only if the path couldn't be resolved;
// any directories recursed into without go source are ignored.
func (b *Builder) AddDirRecursive(dir string) error {
	// Add the root.
	if _, err := b.importPackage(dir, true); err != nil {
		klog.Warningf("Ignoring directory %v: %v", dir, err)
	}

	// filepath.Walk includes the root dir, but we already did that, so we'll
	// remove that prefix and rebuild a package import path.
	prefix := b.buildPackages[dir].Dir
	fn := func(filePath string, info os.FileInfo, err error) error {
		if info != nil && info.IsDir() {
			rel := filepath.ToSlash(strings.TrimPrefix(filePath, prefix))
			if rel != "" {
				// Make a pkg path.
				pkg := path.Join(string(canonicalizeImportPath(b.buildPackages[dir].ImportPath)), rel)

				// Add it.
				if _, err := b.importPackage(pkg, true); err != nil {
					klog.Warningf("Ignoring child directory %v: %v", pkg, err)
				}
			}
		}
		return nil
	}
	if err := filepath.Walk(b.buildPackages[dir].Dir, fn); err != nil {
		return err
	}
	return nil
}

// The implementation of AddDir. A flag indicates whether this directory was
// user-requested or just from following the import graph.
func (b *Builder) addDir(dir string, userRequested bool) error {
	klog.V(5).Infof("addDir %s", dir)
	buildPkg, err := b.importBuildPackage(dir)
	if err != nil {
		return err
	}
	canonicalPackage := canonicalizeImportPath(buildPkg.ImportPath)
	pkgPath := canonicalPackage
	if dir != string(canonicalPackage) {
		klog.V(5).Infof("addDir %s, canonical path is %s", dir, pkgPath)
	}

	// Sanity check the pkg dir has not changed.
	if prev, found := b.absPaths[pkgPath]; found {
		if buildPkg.Dir != prev {
			return fmt.Errorf("package %q (%s) previously resolved to %s", pkgPath, buildPkg.Dir, prev)
		}
	} else {
		b.absPaths[pkgPath] = buildPkg.Dir
	}

	var files []string
	files = append(files, buildPkg.GoFiles...)
	if b.IncludeTestFiles {
		files = append(files, buildPkg.TestGoFiles...)
	}

	for _, file := range files {
		if !strings.HasSuffix(file, ".go") {
			continue
		}
		absPath := filepath.Join(buildPkg.Dir, file)
		data, err := ioutil.ReadFile(absPath)
		if err != nil {
			return fmt.Errorf("while loading %q: %v", absPath, err)
		}
		err = b.addFile(pkgPath, absPath, data, userRequested)
		if err != nil {
			return fmt.Errorf("while parsing %q: %v", absPath, err)
		}
	}
	return nil
}

// importPackage is a function that will be called by the type check package when it
// needs to import a go package. 'path' is the import path.
func (b *Builder) importPackage(dir string, userRequested bool) (*build.Package, error) {
	klog.V(5).Infof("importPackage %s", dir)
	var pkgPath = importPathString(dir)

	buildPkg := b.buildPackages[dir]
	// Get the canonical path if we can.
	if buildPkg != nil {
		canonicalPackage := canonicalizeImportPath(buildPkg.ImportPath)
		klog.V(5).Infof("importPackage %s, canonical path is %s", dir, canonicalPackage)
		pkgPath = canonicalPackage
	}

	// If we have not seen this before, process it now.
	if _, found := b.parsed[pkgPath]; !found {
		// Add it.
		if err := b.addDir(dir, userRequested); err != nil {
			return nil, err
		}

		// Get the canonical path now that it has been added.
		if buildPkg = b.buildPackages[dir]; buildPkg != nil {
			canonicalPackage := canonicalizeImportPath(buildPkg.ImportPath)
			klog.V(5).Infof("importPackage %s, canonical path is %s", dir, canonicalPackage)
			pkgPath = canonicalPackage
		}
	}

	return buildPkg, nil
}

// FindPackages fetches a list of the user-imported packages.
func (b *Builder) FindPackages() []string {
	// Iterate packages in a predictable order.
	var pkgPaths []string
	for k := range b.buildPackages {
		pkgPaths = append(pkgPaths, k)
	}
	sort.Strings(pkgPaths)

	return pkgPaths
}

func (b *Builder) GetBuildPackage(dir string) *build.Package {
	return b.buildPackages[dir]
}

func (b *Builder) GetPackageParsedFiles(dir string) []parsedFile {
	return b.parsed[canonicalizeImportPath(dir)]
}

func (b *Builder) importWithMode(dir string, mode build.ImportMode) (*build.Package, error) {
	// This is a bit of a hack.  The srcDir argument to Import() should
	// properly be the dir of the file which depends on the package to be
	// imported, so that vendoring can work properly and local paths can
	// resolve.  We assume that there is only one level of vendoring, and that
	// the CWD is inside the GOPATH, so this should be safe. Nobody should be
	// using local (relative) paths except on the CLI, so CWD is also
	// sufficient.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("unable to get current directory: %v", err)
	}
	buildPkg, err := b.context.Import(dir, cwd, mode)
	if err != nil {
		return nil, err
	}
	return buildPkg, nil
}

// if there's a comment on the line `lines` before pos, return its text, otherwise "".
func (b *Builder) priorCommentLines(pos token.Pos, lines int) *ast.CommentGroup {
	position := b.fset.Position(pos)
	key := fileLine{position.Filename, position.Line - lines}
	return b.endLineToCommentGroup[key]
}

func (b *Builder) FindFuncs() PackageFuncs {
	funcs := map[string]Funcs{}

	for pkg, parsedFiles := range b.parsed {
		pkgString := string(pkg)
		funcs[pkgString] = Funcs{}

		for _, file := range parsedFiles {
			for _, decls := range file.file.Decls {
				funcDecl, ok := decls.(*ast.FuncDecl)
				if !ok {
					continue
				}
				funcs[pkgString][funcDecl.Name.Name] = funcDecl
			}
		}
	}

	return funcs
}

// canonicalizeImportPath takes an import path and returns the actual package.
// It doesn't support nested vendoring.
func canonicalizeImportPath(importPath string) importPathString {
	if !strings.Contains(importPath, "/vendor/") {
		return importPathString(importPath)
	}

	return importPathString(importPath[strings.Index(importPath, "/vendor/")+len("/vendor/"):])
}
