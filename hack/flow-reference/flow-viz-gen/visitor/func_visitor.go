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
	"fmt"
	"go/ast"
	"go/token"

	"k8s.io/klog"
)

var _ = ast.Visitor(&funcVisitor{})

type funcVisitor struct {
	funcName string
	fset     *token.FileSet

	GraphFound      bool
	graphObject     *ast.Object
	GraphName       string
	Tasks           map[string]*Task
	objectToTaskIDs map[*ast.Object][]string
}

type Task struct {
	Name         string
	Dependencies []string
}

func NewFuncVisitor(funcName string, fset *token.FileSet) *funcVisitor {
	return &funcVisitor{
		funcName:        funcName,
		fset:            fset,
		Tasks:           map[string]*Task{},
		objectToTaskIDs: map[*ast.Object][]string{},
	}
}

func (f *funcVisitor) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.ValueSpec:
		if f.visitPotentialNewGraph(n) {
			return nil
		}
		if f.visitPotentialNewTask(n) {
			return nil
		}
		if f.visitPotentialSyncPoint(n) {
			return nil
		}
	}

	return f
}

func (f *funcVisitor) visitPotentialNewGraph(spec *ast.ValueSpec) bool {
	finder := &graphFinder{fset: f.fset}
	ast.Walk(finder, spec)
	if finder.found {
		if f.GraphFound {
			panic(fmt.Errorf("multiple flow graphs defined in func %q which is not supported", f.funcName))
		}

		f.GraphFound = true
		f.GraphName = finder.graphName
		f.graphObject = finder.graphObject

		return true
	}

	return false
}

func (f *funcVisitor) visitPotentialNewTask(spec *ast.ValueSpec) bool {
	if f.graphObject == nil {
		// flow.NewGraph call has not occurred yet
		return false
	}

	finder := &taskFinder{fset: f.fset, graphObject: f.graphObject}
	ast.Walk(finder, spec)
	if finder.found {
		taskID := finder.taskName

		var foundDependencies []string
		for _, obj := range finder.dependenciesObjects {
			if taskIDs, found := f.objectToTaskIDs[obj]; found {
				foundDependencies = append(foundDependencies, taskIDs...)
			} else {
				klog.V(2).Infof("failed to resolve dependencies of task: %s", getFilePos(f.fset, spec.Pos()))
				return false
			}
		}

		f.Tasks[taskID] = &Task{
			Name:         finder.taskName,
			Dependencies: foundDependencies,
		}
		f.objectToTaskIDs[finder.taskObject] = []string{finder.taskName}

		return true
	}

	return false
}

func (f *funcVisitor) visitPotentialSyncPoint(spec *ast.ValueSpec) bool {
	if f.graphObject == nil {
		// flow.NewGraph call has not occurred yet
		return false
	}

	finder := &syncPointFinder{fset: f.fset, funcVisitor: f}
	ast.Walk(finder, spec)
	if finder.found {
		var foundTaskIDs []string
		for _, obj := range finder.taskIDsObjects {
			if taskIDs, found := f.objectToTaskIDs[obj]; found {
				foundTaskIDs = append(foundTaskIDs, taskIDs...)
			} else {
				klog.V(2).Infof("failed to resolve dependencies of sync point: %s", getFilePos(f.fset, spec.Pos()))
				return false
			}
		}

		f.objectToTaskIDs[finder.syncPointObject] = foundTaskIDs

		return true
	}

	return false
}
