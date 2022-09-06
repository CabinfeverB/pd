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

package cluster

import (
	"errors"
	"fmt"
	"time"

	"github.com/pingcap/log"
	"github.com/tikv/pd/pkg/cache"
	"github.com/tikv/pd/pkg/errs"
	"github.com/tikv/pd/pkg/syncutil"
	"github.com/tikv/pd/server/schedule/operator"
	"github.com/tikv/pd/server/schedule/plan"
	"github.com/tikv/pd/server/schedulers"
	"go.uber.org/zap"
)

const (
	disabled   = "disabled"
	paused     = "paused"
	scheduling = "scheduling"
	pending    = "pending"
	// TODO: find a better name
	normal = "normal"
)

const (
	maxDiagnosticResultNum = 10
)

var SummaryFuncs = map[string]plan.Summary{}

type diagnosticManager struct {
	syncutil.RWMutex
	cluster *RaftCluster
	workers map[string]*diagnosticWorker
}

func newDiagnosticManager(cluster *RaftCluster) *diagnosticManager {
	workers := make(map[string]*diagnosticWorker)
	for name := range DiagnosableSchedulers {
		workers[name] = newDiagnosticWorker(name, cluster)
	}
	return &diagnosticManager{
		cluster: cluster,
		workers: workers,
	}
}

func (d *diagnosticManager) getDiagnosticResult(name string) (*DiagnosticResult, error) {
	if isDisabled, _ := d.cluster.IsSchedulerDisabled(name); isDisabled {
		ts := uint64(time.Now().Unix())
		res := &DiagnosticResult{Name: schedulers.BalanceRegionName, Timestamp: ts, Status: disabled}
		return res, nil
	}
	worker := d.getWorker(name)
	if worker == nil {
		return nil, errs.ErrSchedulerUndiagnosable.FastGenByArgs(name)
	}
	result := worker.getLastResult()
	if result == nil {
		return nil, errs.ErrSchedulerDiagnosisNotRunning.FastGenByArgs(name)
	}
	return result, nil
}

func (d *diagnosticManager) getWorker(name string) *diagnosticWorker {
	return d.workers[name]
}

// diagnosticWorker is used to manage diagnose mechanism
type diagnosticWorker struct {
	schedulerName string
	cluster       *RaftCluster
	summaryFunc   plan.Summary
	result        *cache.FIFO
	//diagnosticManager *diagnosticManager
	currentTime time.Time
	nextTime    time.Time
}

func newDiagnosticWorker(name string, cluster *RaftCluster) *diagnosticWorker {
	summaryFunc, ok := SummaryFuncs[name]
	if !ok {
		log.Error("can't find summary function", zap.String("scheduler-name", name))
	}
	return &diagnosticWorker{
		cluster:       cluster,
		schedulerName: name,
		summaryFunc:   summaryFunc,
	}
}

func (d *diagnosticWorker) init() {
	if d == nil {
		return
	}
	if d.result == nil {
		d.result = cache.NewFIFO(maxDiagnosticResultNum)
	}
}

func (d *diagnosticWorker) isAllowed() bool {
	if d == nil {
		return false
	}
	if d.cluster.opt.GetDiagnosticInterval() == 0 {
		return false
	}
	d.currentTime = time.Now()
	ret := d.nextTime.Before(d.currentTime)
	if ret {
		d.setNextTime()
	}
	return ret
}

func (d *diagnosticWorker) setNextTime() {
	interval := d.cluster.opt.GetDiagnosticInterval()
	d.nextTime = d.currentTime.Add(interval)
}

func (d *diagnosticWorker) getLastResult() *DiagnosticResult {
	if d.result.Len() == 0 {
		return nil
	}
	// TODO: implment
	return nil
}

func (d *diagnosticWorker) generateStatus(status string) {
	if d == nil {
		return
	}
	// TODO: implment
}

func (d *diagnosticWorker) generatePlans(ops []*operator.Operator, plans []plan.Plan) {
	if d == nil {
		return
	}
	// TODO: implment
	d.analyze(ops, plans, uint64(time.Now().Unix()))
}

func (d *diagnosticWorker) analyze(ops []*operator.Operator, plans []plan.Plan, ts uint64) *DiagnosticResult {
	res := &DiagnosticResult{Name: schedulers.BalanceRegionName, Timestamp: ts, Status: normal}
	name := d.schedulerName
	// TODO: support more schedulers and checkers
	switch name {
	default:
	}
	index := len(ops)
	if len(ops) > 0 {
		if ops[0].Kind()&operator.OpMerge != 0 {
			index /= 2
		}
	}
	res.UnschedulablePlans = plans[index:]
	res.SchedulablePlans = plans[:index]
	return res
}

type DiagnosticResult struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	Timestamp uint64 `json:"timestamp"`

	StoreStatus        map[uint64]*plan.Status `json:"-"`
	SchedulablePlans   []plan.Plan             `json:"-"`
	UnschedulablePlans []plan.Plan             `json:"-"`
}

func (r *DiagnosticResult) GetComparableAttribute() string {
	return r.Status
}

type ResultMemorizer struct {
	*cache.FIFO2
}

func NewResultMemorizer(maxCount int) *ResultMemorizer {
	return &ResultMemorizer{FIFO2: cache.NewFIFO2(maxCount)}
}

func (m *ResultMemorizer) Put(result *DiagnosticResult) {
	m.FIFO2.Put(result.Timestamp, result)
}

func (m *ResultMemorizer) GenerateResult() (*DiagnosticResult, error) {
	items := m.FIFO2.FromLastestElems()
	length := len(items)
	if length == 0 {
		return nil, errors.New("todo")
	}
	x1, x2, x3 := length/3, length/3, length/3
	if (length % 3) > 0 {
		x1++
	}
	if (length % 3) > 1 {
		x2++
	}
	pi := 1.0 / float64(3*x1+2*x2+x3)
	counter := make(map[uint64]map[plan.Status]float64)
	for i := 0; i < x1; i++ {
		item := items[i].Value.(*DiagnosticResult)
		for storeID, status := range item.StoreStatus {
			if _, ok := counter[storeID]; !ok {
				counter[storeID] = make(map[plan.Status]float64)
			}
			statusCounter := counter[storeID]
			statusCounter[*status] += pi * 3
		}
	}
	for i := x1; i < x1+x2; i++ {
		item := items[i].Value.(*DiagnosticResult)
		for storeID, status := range item.StoreStatus {
			if _, ok := counter[storeID]; !ok {
				counter[storeID] = make(map[plan.Status]float64)
			}
			statusCounter := counter[storeID]
			statusCounter[*status] += pi * 2
		}
	}
	for i := x1 + x2; i < x1+x2+x3; i++ {
		item := items[i].Value.(*DiagnosticResult)
		for storeID, status := range item.StoreStatus {
			if _, ok := counter[storeID]; !ok {
				counter[storeID] = make(map[plan.Status]float64)
			}
			statusCounter := counter[storeID]
			statusCounter[*status] += pi * 1
		}
	}
	statusCounter := make(map[plan.Status]uint64)
	for _, store := range counter {
		max := 0.
		curStat := *plan.NewStatus(plan.StatusOK)
		for stat, c := range store {
			if c > max {
				max = c
				curStat = stat
			}
		}
		statusCounter[curStat] += 1
	}
	var resStr string
	for k, v := range statusCounter {
		resStr += fmt.Sprintf("%d store(s) %s; ", v, k.String())
	}
	return &DiagnosticResult{
		Name:      items[0].Value.(*DiagnosticResult).Name,
		Status:    items[0].Value.(*DiagnosticResult).Status,
		Summary:   resStr,
		Timestamp: uint64(time.Now().Unix()),
	}, nil
}
