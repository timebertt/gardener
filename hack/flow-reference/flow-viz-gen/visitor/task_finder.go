// Copyright (c) 2020 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package visitor

import (
	"go/ast"
	"go/token"
	"strconv"

	"k8s.io/klog/v2"
)

type taskFinder struct {
	graphObject *ast.Object
	fset        *token.FileSet

	found               bool
	taskName            string
	taskObject          *ast.Object
	dependenciesObjects []*ast.Object
}

func (f *taskFinder) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.ValueSpec:
		indexOfGraphAddExpr := -1
		for i, expr := range n.Values {
			ast.Walk(f, expr)
			if f.found {
				indexOfGraphAddExpr = i
				break
			}
		}

		if indexOfGraphAddExpr != -1 {
			f.taskObject = n.Names[indexOfGraphAddExpr].Obj
			return nil
		}

	case *ast.CallExpr:
		if f.visitPotentialAddTaskCall(n) {
			f.found = true
			return nil
		}
	}

	return f
}

func (f *taskFinder) visitPotentialAddTaskCall(call *ast.CallExpr) bool {
	if f.graphObject == nil {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	leftIdent, ok := selector.X.(*ast.Ident)
	if !ok || leftIdent.Obj != f.graphObject {
		return false
	}
	if selector.Sel.Name != "Add" {
		return false
	}

	if len(call.Args) != 1 {
		klog.V(2).Infof("could not determine task for Graph.Add call, there should be exactly one argument: %s", getFilePos(f.fset, call.Lparen))
		return false
	}

	if taskLit, ok := call.Args[0].(*ast.CompositeLit); ok {
		if f.visitPotentialTaskLiteral(taskLit) {
			return true
		}
		klog.V(2).Infof("could not determine flow.Task literal, unsupported expression: %s", getFilePos(f.fset, taskLit.Pos()))
	} else {
		klog.V(2).Infof("could not determine flow.Task, is not a CompositeLit: %s", getFilePos(f.fset, call.Args[0].Pos()))
	}

	return false
}

func (f *taskFinder) visitPotentialTaskLiteral(lit *ast.CompositeLit) bool {
	selector, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := selector.X.(*ast.Ident)
	if !ok || pkgIdent.Name != "flow" {
		return false
	}
	if selector.Sel.Name != "Task" {
		return false
	}

	for i, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			klog.V(2).Infof("could not determine field in flow.Task literal, element %d is not a KeyValueExpr: %s", i, getFilePos(f.fset, elt.Pos()))
			return false
		}
		keyIdent, ok := kv.Key.(*ast.Ident)
		if !ok {
			klog.V(2).Infof("could not determine field in flow.Task literal, key of element %d is not an Ident: %s", i, getFilePos(f.fset, elt.Pos()))
			return false
		}

		switch keyIdent.Name {
		case "Name":
			if !f.visitPotentialTaskName(kv.Value) {
				return false
			}
		case "Dependencies":
			if !f.visitPotentialTaskDependencies(kv.Value) {
				return false
			}
		}
	}

	return f.taskName != ""
}

func (f *taskFinder) visitPotentialTaskName(expr ast.Expr) bool {
	if nameLit, ok := expr.(*ast.BasicLit); ok && nameLit.Kind == token.STRING {
		if name, err := strconv.Unquote(nameLit.Value); err == nil {
			f.taskName = name
			return true
		} else {
			klog.V(2).Infof("error unquoting string literal to determine flow task name: %s: %v", getFilePos(f.fset, nameLit.Pos()), err)
		}
	} else {
		klog.V(2).Infof("could not find name of flow task, value is not a string literal: %s", getFilePos(f.fset, expr.Pos()))
	}

	return false
}

func (f *taskFinder) visitPotentialTaskDependencies(call ast.Expr) bool {
	finder := &taskIDsFinder{fset: f.fset}
	ast.Walk(finder, call)
	if finder.found {
		f.dependenciesObjects = finder.taskIDsObjects
		return true
	}

	return false
}
