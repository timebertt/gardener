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

type graphFinder struct {
	fset *token.FileSet

	found       bool
	graphName   string
	graphObject *ast.Object
}

func (g *graphFinder) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.ValueSpec:
		indexOfNewGraphExpr := -1
		for i, expr := range n.Values {
			ast.Walk(g, expr)
			if g.found {
				indexOfNewGraphExpr = i
				break
			}
		}

		if indexOfNewGraphExpr != -1 {
			g.graphObject = n.Names[indexOfNewGraphExpr].Obj
			return nil
		}

	case *ast.CallExpr:
		if g.visitPotentialNewGraphCall(n) {
			g.found = true
			return nil
		}
	}

	return g
}

func (g *graphFinder) visitPotentialNewGraphCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := selector.X.(*ast.Ident)
	// TODO check import alias of github.com/gardener/gardener/pkg/utils/flow
	if !ok || pkgIdent.Name != "flow" {
		return false
	}
	if selector.Sel.Name != "NewGraph" {
		return false
	}

	if len(call.Args) == 0 {
		klog.V(2).Infof("could not determine name of flow graph definition, no arguments for flow.NewGraph: %s", getFilePos(g.fset, call.Lparen))
		return false
	}

	if nameArg, ok := call.Args[0].(*ast.BasicLit); ok && nameArg.Kind == token.STRING {
		if name, err := strconv.Unquote(nameArg.Value); err == nil {
			g.graphName = name
			return true
		} else {
			klog.V(2).Infof("error unquoting string literal to determine flow name: %s: %v", getFilePos(g.fset, call.Lparen), err)
		}
	} else {
		klog.V(2).Infof("could not find name of flow graph definition, "+
			"first argument for flow.NewGraph is not a string literal: %s", getFilePos(g.fset, call.Lparen))
	}

	return false
}
