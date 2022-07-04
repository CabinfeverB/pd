// Copyright 2022 TiKV Project Authors.
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

package plan

import "github.com/tikv/pd/server/schedule/diagnosis"

// Plan is the basic unit for both scheduling and diagnosis.
// TODO: for each scheduler/checker, we can have an individual definition but need to implement the common interfaces.
type Plan interface{}

type DiagnosePlan interface {
	GetSourceStore() uint64
	GetRegion() uint64
	GetTargetStore() uint64
	GetStep() diagnosis.ScheduleStep
	GetReason() string
	IsSchedulable() bool
	GetFailObject() uint64
}
