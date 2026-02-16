// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package apis

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/util/intstr"
)

// IntOrString is a type that can hold an int32 or a string.
type IntOrString struct {
	IntVal *int32
	StrVal *string
}

// UnmarshalJSON implements the json.Unmarshaler interface.
func (i *IntOrString) UnmarshalJSON(value []byte) error {
	if value[0] == '"' {
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return err
		}
		i.StrVal = &s
		return nil
	}
	var n int32
	if err := json.Unmarshal(value, &n); err != nil {
		return err
	}
	i.IntVal = &n
	return nil
}

// MarshalJSON implements the json.Marshaler interface.
func (i *IntOrString) MarshalJSON() ([]byte, error) {
	if i.IntVal != nil {
		return json.Marshal(*i.IntVal)
	}
	if i.StrVal != nil {
		return json.Marshal(*i.StrVal)
	}
	return []byte("null"), nil
}

// ToKubernetes converts to the Kubernetes IntOrString type.
func (i *IntOrString) ToKubernetes() intstr.IntOrString {
	if i.IntVal != nil {
		return intstr.FromInt32(*i.IntVal)
	}
	if i.StrVal != nil {
		return intstr.FromString(*i.StrVal)
	}
	return intstr.IntOrString{}
}
