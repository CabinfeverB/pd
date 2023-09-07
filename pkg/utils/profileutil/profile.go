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

package profileutil

import (
	"bytes"
	"fmt"
	"runtime/pprof"
	"sync"

	"github.com/google/pprof/profile"
	"github.com/pingcap/log"
	"github.com/tikv/pd/pkg/utils/syncutil"
	"go.uber.org/zap"
)

type ResourceUsageRecorder interface {
	Collect(float64)
	Flush()
	Allow() bool
}

type GoroutineDCRecorder struct {
	Name string
	// resourceType int
	HeapDelta *MovingDC
	buffer    float64
	mu        syncutil.Mutex
}

func (r *GoroutineDCRecorder) Collect(v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buffer += v
}

func (r *GoroutineDCRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.buffer > 0 {
		log.Info("Record Goroutine", zap.String("name", r.Name), zap.Float64("value", r.buffer), zap.Bool("allow", r.HeapDelta.add(r.buffer)))
	}
	r.buffer = 0
}

func (r *GoroutineDCRecorder) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.HeapDelta.get()
}

type ProfileCollector struct {
	name string

	lastDataSize int
	sync.Mutex
}

func NewGoroutineProfileCollector() *ProfileCollector {
	return &ProfileCollector{
		name: "goroutine",
	}
}

func NewMemoryProfileCollector() *ProfileCollector {
	return &ProfileCollector{
		name: "heap",
	}
}

func (c *ProfileCollector) GetSampleValues(targets map[string]ResourceUsageRecorder) {
	p := c.GetProfile()
	index := len(p.SampleType) - 1
	for _, s := range p.Sample {
	locationLoop:
		for _, l := range s.Location {
			for _, li := range l.Line {
				if r, ok := targets[li.Function.Name]; ok {
					r.Collect(float64(s.Value[index]))
					break locationLoop
				}
			}
		}
	}
	for _, r := range targets {
		r.Flush()
	}
}

func (c *ProfileCollector) GetProfile() *profile.Profile {
	p := pprof.Lookup(c.name)
	if p == nil {
		log.Error("")
	}
	capacity := (c.lastDataSize/4096 + 1) * 4096
	data := bytes.NewBuffer(make([]byte, 0, capacity))
	p.WriteTo(data, 0)
	pprof.StartCPUProfile(data)

	pp, err := profile.ParseData(data.Bytes())
	if err != nil {
		log.Error("")
		return nil
	}
	c.lastDataSize = data.Len()
	return pp
}

// differential coefficient
type MovingDC struct {
	records []float64
	size    uint64
	count   uint64
	last    float64

	positiveCount float64
	sum           float64
}

// NewMovingDC returns a MedianFilter.
func NewMovingDC(size int) *MovingDC {
	return &MovingDC{
		records: make([]float64, size),
		size:    uint64(size),
	}
}

// Add adds a data point.
func (r *MovingDC) add(n float64) bool {
	if r.count >= r.size {
		popValue := r.records[r.count%r.size]
		r.sum -= popValue
		if popValue > 0 {
			r.positiveCount--
		}
	}
	delta := n - r.last
	r.last = n
	r.records[r.count%r.size] = delta
	if delta > 0 {
		r.positiveCount++
	}
	r.sum += delta
	r.count++
	log.Info("MovingDC", zap.Any("records", r.records), zap.Bool("allow", float64(r.positiveCount)-float64(r.size)*0.6 >= -1e-6 && r.sum >= 1e-6))

	return float64(r.positiveCount)-float64(r.size)*0.6 >= -1e-6 && r.sum >= 1e-6
}

func (r *MovingDC) get() bool {
	return float64(r.positiveCount)-float64(r.size)*0.6 >= -1e-6 && r.sum >= 1e-6
}

// Reset cleans the data set.
func (r *MovingDC) Reset() {
	r.positiveCount = 0
	r.count = 0
	r.sum = 0
}

func GetAPIGoroutineCount(map[string]struct{}) map[string]int64 {
	cnt := make(map[string]int64)
	return cnt
}

func GetMemoryProfile() {

}

func GetGoroutineProfile1() {
	p := pprof.Lookup("goroutine")
	data := bytes.NewBuffer(make([]byte, 0, 4096))
	p.WriteTo(data, 0)
	pp, err := profile.ParseData(data.Bytes())
	log.Info("data---", zap.String("data", data.String()))
	fmt.Println(err)
	if err != nil {
		return
	}
	for _, s := range pp.Sample {
		fmt.Println("********************", s.Value)
		fmt.Println(s.Label)
		for _, l := range s.Location {
			fmt.Println("    ", l.ID, l.Address, l.IsFolded)
			for _, li := range l.Line {
				fmt.Println("        ", li.Line, li.Function.Filename, li.Function.Name, li.Function.ID, li.Function.StartLine, li.Function.SystemName)
			}
		}
	}
}
