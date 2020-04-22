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
)

type syncPointFinder struct {
	fset        *token.FileSet
	funcVisitor *funcVisitor

	found           bool
	syncPointObject *ast.Object
	taskIDsObjects  []*ast.Object
}

func (f *syncPointFinder) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.ValueSpec:
		indexOfTaskIDsExpr := -1
		for i, expr := range n.Values {
			ast.Walk(f, expr)
			if f.found {
				indexOfTaskIDsExpr = i
				break
			}
		}

		if indexOfTaskIDsExpr != -1 {
			f.syncPointObject = n.Names[indexOfTaskIDsExpr].Obj
			return nil
		}

	case *ast.CallExpr:
		if f.visitPotentialTaskIDs(n) {
			return nil
		}
	}

	return f
}

func (f *syncPointFinder) visitPotentialTaskIDs(call *ast.CallExpr) bool {
	finder := &taskIDsFinder{fset: f.fset}
	ast.Walk(finder, call)
	if finder.found {
		f.taskIDsObjects = finder.taskIDsObjects
		f.found = true
		return true
	}

	return false
}
