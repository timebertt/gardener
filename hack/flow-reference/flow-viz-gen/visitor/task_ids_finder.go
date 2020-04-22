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

	"k8s.io/klog/v2"
)

type taskIDsFinder struct {
	fset *token.FileSet

	found          bool
	taskIDsObjects []*ast.Object
}

func (t *taskIDsFinder) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.CallExpr:
		if t.visitPotentialFlowTaskIDs(n) {
			t.found = true
			return nil
		}
	}

	return t
}

func (t *taskIDsFinder) visitPotentialFlowTaskIDs(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	leftIdent, ok := selector.X.(*ast.Ident)
	if !ok || leftIdent.Name != "flow" {
		return false
	}
	if selector.Sel.Name != "NewTaskIDs" {
		return false
	}

	if len(call.Args) == 0 {
		klog.V(2).Infof("could not determine task IDs of syncpoint, there should be at least one argument: %s", getFilePos(t.fset, call.Lparen))
		return false
	}

	var foundTaskIDObjects []*ast.Object
	for i, arg := range call.Args {
		if ident, ok := arg.(*ast.Ident); ok {
			foundTaskIDObjects = append(foundTaskIDObjects, ident.Obj)
		} else {
			klog.V(2).Infof("could not determine task ID of syncpoint, is not an Ident: %s", getFilePos(t.fset, call.Args[i].Pos()))
			return false
		}
	}

	t.taskIDsObjects = foundTaskIDObjects
	t.found = true
	return true
}
