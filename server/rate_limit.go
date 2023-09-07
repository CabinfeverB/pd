// Copyright 2023 TiKV Project Authors.
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

package server

import "github.com/tikv/pd/pkg/utils/profileutil"

type RateLimitManager struct {
	recorders map[string]profileutil.ResourceUsageRecorder
	goroutine *profileutil.ProfileCollector
	memory    *profileutil.ProfileCollector
}

func NewRateLimitManager() *RateLimitManager {
	return &RateLimitManager{
		recorders: make(map[string]profileutil.ResourceUsageRecorder),
		goroutine: profileutil.NewGoroutineProfileCollector(),
		memory:    profileutil.NewMemoryProfileCollector(),
	}
}

func (m *RateLimitManager) AddAPI(name string) {
	m.recorders[name] = &profileutil.GoroutineDCRecorder{
		Name:      name,
		HeapDelta: profileutil.NewMovingDC(5),
	}
}

func (m *RateLimitManager) Allow(name string) bool {
	if r, ok := m.recorders[name]; ok {
		return r.Allow()
	}
	return true
}

func (m *RateLimitManager) Collect() {
	m.goroutine.GetSampleValues(m.recorders)
	//m.memory.GetSampleValues(m.recorders)
}
