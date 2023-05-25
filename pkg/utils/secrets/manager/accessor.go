// Copyright (c) 2023 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package manager

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
)

type SecretAccessor interface {
	client.Object

	GetImmutable() *bool
	SetImmutable(*bool)
	GetData() map[string][]byte
	SetData(map[string][]byte)
	GetStringData() map[string]string
	SetStringData(map[string]string)
	GetType() corev1.SecretType
	SetType(corev1.SecretType)
}

func Accessor[T secret](obj T) SecretAccessor {
	if obj == nil {
		return nil
	}

	switch s := any(obj).(type) {
	case *corev1.Secret:
		return secretImpl{s}
	case *gardencorev1beta1.InternalSecret:
		return internalSecretImpl{s}
	}

	panic(fmt.Errorf("type %T is not supported, must be either %T or %T", obj, &corev1.Secret{}, &gardencorev1beta1.InternalSecret{}))
}

func newObject[T secret]() T {
	var obj T
	switch any(obj).(type) {
	case *corev1.Secret:
		return T(&corev1.Secret{})
	case *gardencorev1beta1.InternalSecret:
		return T(&gardencorev1beta1.InternalSecret{})
	}

	panic(fmt.Errorf("type %T is not supported, must be either %T or %T", obj, &corev1.Secret{}, &gardencorev1beta1.InternalSecret{}))
}

func newList[T secret]() client.ObjectList {
	var obj T
	switch any(obj).(type) {
	case *corev1.Secret:
		return &corev1.SecretList{}
	case *gardencorev1beta1.InternalSecret:
		return &gardencorev1beta1.InternalSecretList{}
	}

	panic(fmt.Errorf("type %T is not supported, must be either %T or %T", obj, &corev1.Secret{}, &gardencorev1beta1.InternalSecret{}))
}

type secretImpl struct {
	*corev1.Secret
}

func (i secretImpl) GetImmutable() *bool {
	return i.Immutable
}

func (i secretImpl) SetImmutable(b *bool) {
	i.Immutable = b
}

func (i secretImpl) GetData() map[string][]byte {
	return i.Data
}

func (i secretImpl) SetData(m map[string][]byte) {
	i.Data = m
}

func (i secretImpl) GetStringData() map[string]string {
	return i.StringData
}

func (i secretImpl) SetStringData(m map[string]string) {
	i.StringData = m
}

func (i secretImpl) GetType() corev1.SecretType {
	return i.Type
}

func (i secretImpl) SetType(s corev1.SecretType) {
	i.Type = s
}

type internalSecretImpl struct {
	*gardencorev1beta1.InternalSecret
}

func (i internalSecretImpl) GetImmutable() *bool {
	return i.Immutable
}

func (i internalSecretImpl) SetImmutable(b *bool) {
	i.Immutable = b
}

func (i internalSecretImpl) GetData() map[string][]byte {
	return i.Data
}

func (i internalSecretImpl) SetData(m map[string][]byte) {
	i.Data = m
}

func (i internalSecretImpl) GetStringData() map[string]string {
	return i.StringData
}

func (i internalSecretImpl) SetStringData(m map[string]string) {
	i.StringData = m
}

func (i internalSecretImpl) GetType() corev1.SecretType {
	return i.Type
}

func (i internalSecretImpl) SetType(s corev1.SecretType) {
	i.Type = s
}
