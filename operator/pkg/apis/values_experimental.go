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

// ValuesExperimental specifies experimental helm fields that could be removed or changed in the future.
type ValuesExperimental struct {
	// Controls whether the experimental feature is enabled.
	StableValidationPolicy *bool `json:"stableValidationPolicy,omitempty"`
}

// ValuesTelemetry controls whether telemetry is exported for Pilot.
type ValuesTelemetry struct {
	// Controls whether telemetry is exported for Pilot.
	Enabled *bool `json:"enabled,omitempty"`

	// Configuration for Telemetry v2.
	V2 *ValuesTelemetryV2 `json:"v2,omitempty"`
}

// ValuesTelemetryV2 controls whether pilot will configure telemetry v2.
type ValuesTelemetryV2 struct {
	// Controls whether pilot will configure telemetry v2.
	Enabled *bool `json:"enabled,omitempty"`

	// Telemetry v2 settings for prometheus.
	Prometheus *ValuesTelemetryV2Prometheus `json:"prometheus,omitempty"`

	// Telemetry v2 settings for stackdriver.
	Stackdriver *ValuesTelemetryV2Stackdriver `json:"stackdriver,omitempty"`
}

// ValuesTelemetryV2Prometheus controls telemetry v2 prometheus settings.
type ValuesTelemetryV2Prometheus struct {
	// Controls whether stats envoyfilter would be enabled or not.
	Enabled *bool `json:"enabled,omitempty"`
}

// ValuesTelemetryV2Stackdriver controls telemetry v2 stackdriver settings.
type ValuesTelemetryV2Stackdriver struct {
	// Controls whether stackdriver telemetry is enabled.
	Enabled *bool `json:"enabled,omitempty"`
}
