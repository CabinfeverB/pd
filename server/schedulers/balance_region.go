// Copyright 2017 TiKV Project Authors.
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

package schedulers

import (
	"sort"

	"github.com/pingcap/kvproto/pkg/metapb"
	"github.com/pingcap/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/tikv/pd/pkg/errs"
	"github.com/tikv/pd/server/core"
	"github.com/tikv/pd/server/schedule"
	"github.com/tikv/pd/server/schedule/filter"
	"github.com/tikv/pd/server/schedule/operator"
	"github.com/tikv/pd/server/schedule/plan"
	"github.com/tikv/pd/server/storage/endpoint"
	"go.uber.org/zap"
)

func init() {
	schedule.RegisterSliceDecoderBuilder(BalanceRegionType, func(args []string) schedule.ConfigDecoder {
		return func(v interface{}) error {
			conf, ok := v.(*balanceRegionSchedulerConfig)
			if !ok {
				return errs.ErrScheduleConfigNotExist.FastGenByArgs()
			}
			ranges, err := getKeyRanges(args)
			if err != nil {
				return err
			}
			conf.Ranges = ranges
			conf.Name = BalanceRegionName
			return nil
		}
	})
	schedule.RegisterScheduler(BalanceRegionType, func(opController *schedule.OperatorController, storage endpoint.ConfigStorage, decoder schedule.ConfigDecoder) (schedule.Scheduler, error) {
		conf := &balanceRegionSchedulerConfig{}
		if err := decoder(conf); err != nil {
			return nil, err
		}
		return newBalanceRegionScheduler(opController, conf), nil
	})
}

const (
	// balanceRegionRetryLimit is the limit to retry schedule for selected store.
	balanceRegionRetryLimit = 10
	// BalanceRegionName is balance region scheduler name.
	BalanceRegionName = "balance-region-scheduler"
	// BalanceRegionType is balance region scheduler type.
	BalanceRegionType = "balance-region"
)

type balanceRegionSchedulerConfig struct {
	Name   string          `json:"name"`
	Ranges []core.KeyRange `json:"ranges"`
}

type balanceRegionScheduler struct {
	*BaseScheduler
	*retryQuota
	conf         *balanceRegionSchedulerConfig
	opController *schedule.OperatorController
	filters      []filter.Filter
	counter      *prometheus.CounterVec
	steps        []plan.Step
}

// newBalanceRegionScheduler creates a scheduler that tends to keep regions on
// each store balanced.
func newBalanceRegionScheduler(opController *schedule.OperatorController, conf *balanceRegionSchedulerConfig, opts ...BalanceRegionCreateOption) schedule.Scheduler {
	base := NewBaseScheduler(opController)
	scheduler := &balanceRegionScheduler{
		BaseScheduler: base,
		retryQuota:    newRetryQuota(balanceRegionRetryLimit, defaultMinRetryLimit, defaultRetryQuotaAttenuation),
		conf:          conf,
		opController:  opController,
		counter:       balanceRegionCounter,
	}
	for _, setOption := range opts {
		setOption(scheduler)
	}
	scheduler.filters = []filter.Filter{
		&filter.StoreStateFilter{ActionScope: scheduler.GetName(), MoveRegion: true},
		filter.NewSpecialUseFilter(scheduler.GetName()),
	}
	scheduler.steps = []plan.Step{
		&stepSourceStore{bs: scheduler},
		&stepRegion{balanceRegionScheduler: scheduler},
		&stepTargetStore{balanceRegionScheduler: scheduler},
		&stepShouldBalance{balanceRegionScheduler: scheduler},
	}
	return scheduler
}

// BalanceRegionCreateOption is used to create a scheduler with an option.
type BalanceRegionCreateOption func(s *balanceRegionScheduler)

// WithBalanceRegionCounter sets the counter for the scheduler.
func WithBalanceRegionCounter(counter *prometheus.CounterVec) BalanceRegionCreateOption {
	return func(s *balanceRegionScheduler) {
		s.counter = counter
	}
}

// WithBalanceRegionName sets the name for the scheduler.
func WithBalanceRegionName(name string) BalanceRegionCreateOption {
	return func(s *balanceRegionScheduler) {
		s.conf.Name = name
	}
}

func (s *balanceRegionScheduler) GetName() string {
	return s.conf.Name
}

func (s *balanceRegionScheduler) GetType() string {
	return BalanceRegionType
}

func (s *balanceRegionScheduler) EncodeConfig() ([]byte, error) {
	return schedule.EncodeConfig(s.conf)
}

func (s *balanceRegionScheduler) IsScheduleAllowed(cluster schedule.Cluster) bool {
	allowed := s.opController.OperatorCount(operator.OpRegion) < cluster.GetOpts().GetRegionScheduleLimit()
	if !allowed {
		operator.OperatorLimitCounter.WithLabelValues(s.GetType(), operator.OpRegion.String()).Inc()
	}
	return allowed
}

// type search func(p plan.Plan) plan.Plan

// type sourceStoreStepCreator struct {
// 	bs *balanceRegionScheduler
// }

// func (s *sourceStoreStepCreator) Create() func(solver *solver) search {
// 	return func(solver *solver) search {
// 		stores := solver.GetStores()
// 		opts := solver.GetOpts()
// 		stores = filter.SelectSourceStores(stores, s.bs.filters, opts)
// 		sort.Slice(stores, func(i, j int) bool {
// 			iOp := solver.GetOpInfluence(stores[i].GetID())
// 			jOp := solver.GetOpInfluence(stores[j].GetID())
// 			return stores[i].RegionScore(opts.GetRegionScoreFormulaVersion(), opts.GetHighSpaceRatio(), opts.GetLowSpaceRatio(), iOp) >
// 				stores[j].RegionScore(opts.GetRegionScoreFormulaVersion(), opts.GetHighSpaceRatio(), opts.GetLowSpaceRatio(), jOp)
// 		})
// 		length := len(stores)
// 		i := 0
// 		return func(p plan.Plan) plan.Plan {
// 			if i < length {
// 				p.SetSourceStore(stores[i])
// 				i++
// 				return p
// 			}
// 			return nil
// 		}
// 	}
// }

type stepSourceStore struct {
	bs *balanceRegionScheduler
}

func (s *stepSourceStore) Search(informer interface{}) func(p plan.Plan) (plan.Plan, bool) {
	solver := informer.(*solver)
	stores := solver.GetStores()
	opts := solver.GetOpts()
	stores = filter.SelectSourceStores(stores, s.bs.filters, opts)
	sort.Slice(stores, func(i, j int) bool {
		iOp := solver.GetOpInfluence(stores[i].GetID())
		jOp := solver.GetOpInfluence(stores[j].GetID())
		return stores[i].RegionScore(opts.GetRegionScoreFormulaVersion(), opts.GetHighSpaceRatio(), opts.GetLowSpaceRatio(), iOp) >
			stores[j].RegionScore(opts.GetRegionScoreFormulaVersion(), opts.GetHighSpaceRatio(), opts.GetLowSpaceRatio(), jOp)
	})
	length := len(stores)
	i := 0
	return func(p plan.Plan) (plan.Plan, bool) {
		if i < length {
			p.SetSourceStore(stores[i])
			i++
			return p, true
		}
		return nil, false
	}
}

type stepRegion struct {
	*balanceRegionScheduler
}

func (s *stepRegion) Search(informer interface{}) func(p plan.Plan) (plan.Plan, bool) {
	solver := informer.(*solver)

	pendingFilter := filter.NewRegionPengdingFilter()
	downFilter := filter.NewRegionDownFilter()
	replicaFilter := filter.NewRegionReplicatedFilter(solver.Cluster)
	baseRegionFilters := []filter.RegionFilter{downFilter, replicaFilter}
	switch solver.Cluster.(type) {
	case *schedule.RangeCluster:
		// allow empty region to be scheduled in range cluster
	default:
		baseRegionFilters = append(baseRegionFilters, filter.NewRegionEmptyFilter(solver.Cluster))
	}

	retryLimit := s.retryQuota.GetLimit(solver.source)
	i := 0
	return func(p plan.Plan) (plan.Plan, bool) {
		if i < retryLimit {
			schedulerCounter.WithLabelValues(s.GetName(), "total").Inc()
			// Priority pick the region that has a pending peer.
			// Pending region may means the disk is overload, remove the pending region firstly.
			region := filter.SelectOneRegion(solver.RandPendingRegions(solver.SourceStoreID(), s.conf.Ranges),
				baseRegionFilters...)
			if region == nil {
				// Then pick the region that has a follower in the source store.
				region = filter.SelectOneRegion(solver.RandFollowerRegions(solver.SourceStoreID(), s.conf.Ranges),
					append(baseRegionFilters, pendingFilter)...)
			}
			if region == nil {
				// Then pick the region has the leader in the source store.
				region = filter.SelectOneRegion(solver.RandLeaderRegions(solver.SourceStoreID(), s.conf.Ranges),
					append(baseRegionFilters, pendingFilter)...)
			}
			if region == nil {
				// Finally pick learner.
				region = filter.SelectOneRegion(solver.RandLearnerRegions(solver.SourceStoreID(), s.conf.Ranges),
					append(baseRegionFilters, pendingFilter)...)
			}
			if region == nil {
				schedulerCounter.WithLabelValues(s.GetName(), "no-region").Inc()
				return nil, true
			}
			log.Debug("select region", zap.String("scheduler", s.GetName()), zap.Uint64("region-id", solver.region.GetID()))

			// Skip hot regions.
			if solver.IsRegionHot(region) {
				log.Debug("region is hot", zap.String("scheduler", s.GetName()), zap.Uint64("region-id", solver.region.GetID()))
				schedulerCounter.WithLabelValues(s.GetName(), "region-hot").Inc()
				return nil, true
			}
			// Check region whether have leader
			if region.GetLeader() == nil {
				log.Warn("region have no leader", zap.String("scheduler", s.GetName()), zap.Uint64("region-id", solver.region.GetID()))
				schedulerCounter.WithLabelValues(s.GetName(), "no-leader").Inc()
				return nil, true
			}
			p.SetRegion(region)
			i++
			return p, true
		}
		return nil, false
	}
}

type stepTargetStore struct {
	*balanceRegionScheduler
}

func (s *stepTargetStore) Search(informer interface{}) func(p plan.Plan) (plan.Plan, bool) {
	solver := informer.(*solver)

	filters := []filter.Filter{
		filter.NewExcludedFilter(s.GetName(), nil, solver.region.GetStoreIDs()),
		filter.NewPlacementSafeguard(s.GetName(), solver.GetOpts(), solver.GetBasicCluster(), solver.GetRuleManager(), solver.region, solver.source),
		filter.NewRegionScoreFilter(s.GetName(), solver.source, solver.GetOpts()),
		filter.NewSpecialUseFilter(s.GetName()),
		&filter.StoreStateFilter{ActionScope: s.GetName(), MoveRegion: true},
	}

	candidates := filter.NewCandidates(solver.GetStores()).
		FilterTarget(solver.GetOpts(), filters...).
		Sort(filter.RegionScoreComparer(solver.GetOpts()))

	length := len(candidates.Stores)
	i := 0
	return func(p plan.Plan) (plan.Plan, bool) {
		if i < length {
			p.SetTargetStore(candidates.Stores[i])
			i++
			return p, true
		}
		return nil, false
	}
}

type stepShouldBalance struct {
	*balanceRegionScheduler
}

func (s *stepShouldBalance) Search(informer interface{}) func(p plan.Plan) (plan.Plan, bool) {
	solver := informer.(*solver)
	used := false
	return func(p plan.Plan) (plan.Plan, bool) {
		if !used {
			p.SetSchedulable(solver.shouldBalance(s.GetName()))
			return p, false
		}
		return nil, false
	}
}

func (s *balanceRegionScheduler) Schedule(cluster schedule.Cluster, dryRun bool) ([]*operator.Operator, []plan.Plan) {
	schedulerCounter.WithLabelValues(s.GetName(), "schedule").Inc()
	basePlan := NewBalanceSchedulerBasePlan()
	opInfluence := s.opController.GetOpInfluence(cluster)
	s.OpController.GetFastOpInfluence(cluster, opInfluence)
	kind := core.NewScheduleKind(core.RegionKind, core.BySize)
	solver := newSolver(basePlan, kind, cluster, opInfluence)
	s.searchPlan(solver, basePlan, 0)
	return solver.ops, nil
}

func (s *balanceRegionScheduler) searchPlan(solver *solver, plan plan.Plan, index int) {
	if index == len(s.steps) {
		oldPeer := solver.region.GetStorePeer(solver.SourceStoreID())
		newPeer := &metapb.Peer{StoreId: solver.target.GetID(), Role: oldPeer.Role}
		op, err := operator.CreateMovePeerOperator(BalanceRegionType, solver, solver.region, operator.OpRegion, oldPeer.GetStoreId(), newPeer)
		if err != nil {
			solver.ops = []*operator.Operator{op}
		}
		return
	}
	search := s.steps[index].Search(solver)
	for len(solver.ops) == 0 {
		p, next := search(plan)
		s.searchPlan(solver, p, index+1)
		if !next {
			break
		}
	}
}
