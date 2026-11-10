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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuild generates the page from the checked-in suite data and requires every feature to be
// mapped to an area, so a new feature ID prefix gets an explicit areaRules entry.
func TestBuild(t *testing.T) {
	pd, err := build(filepath.Join("..", "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pd.Roles) == 0 || len(pd.Surface) == 0 {
		t.Fatalf("no data: %d roles, %d surface entries", len(pd.Roles), len(pd.Surface))
	}
	for _, r := range pd.Roles {
		for _, f := range r.Features {
			if f.Area == areaOther {
				t.Errorf("feature %s has no area: add its prefix to areaRules", f.ID)
			}
		}
	}
	out := filepath.Join(t.TempDir(), "conformance.html")
	if err := render(pd, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// H4sI is the base64-encoded gzip header.
	if !strings.Contains(string(b), `<script id="data" type="application/json">"H4sI`) {
		t.Errorf("rendered page does not embed the compressed data")
	}
}
