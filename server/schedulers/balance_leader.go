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
	"net/http"
	"sort"
	"strconv"
	"sync"

	"github.com/gorilla/mux"
	"github.com/pingcap/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/tikv/pd/pkg/apiutil"
	"github.com/tikv/pd/pkg/errs"
	"github.com/tikv/pd/server/core"
	"github.com/tikv/pd/server/schedule"
	"github.com/tikv/pd/server/schedule/filter"
	"github.com/tikv/pd/server/schedule/operator"
	"github.com/tikv/pd/server/storage/endpoint"
	"github.com/unrolled/render"
	"go.uber.org/zap"
)

const (
	// BalanceLeaderName is balance leader scheduler name.
	BalanceLeaderName = "balance-leader-scheduler"
	// BalanceLeaderType is balance leader scheduler type.
	BalanceLeaderType = "balance-leader"
	// balanceLeaderRetryLimit is the limit to retry schedule for selected source store and target store.
	balanceLeaderRetryLimit = 10
)

func init() {
	schedule.RegisterSliceDecoderBuilder(BalanceLeaderType, func(args []string) schedule.ConfigDecoder {
		return func(v interface{}) error {
			conf, ok := v.(*balanceLeaderSchedulerConfig)
			if !ok {
				return errs.ErrScheduleConfigNotExist.FastGenByArgs()
			}
			ranges, err := getKeyRanges(args)
			if err != nil {
				return err
			}
			conf.Ranges = ranges
			conf.Name = BalanceLeaderName
			conf.Batch = 1
			if len(args) > len(ranges)*2 {
				conf.Batch, err = strconv.Atoi(args[len(ranges)*2])
				if err != nil {
					return err
				}
			}
			return nil
		}
	})

	schedule.RegisterScheduler(BalanceLeaderType, func(opController *schedule.OperatorController, storage endpoint.ConfigStorage, decoder schedule.ConfigDecoder) (schedule.Scheduler, error) {
		conf := &balanceLeaderSchedulerConfig{}
		if err := decoder(conf); err != nil {
			return nil, err
		}
		return newBalanceLeaderScheduler(opController, conf), nil
	})
}

type balanceLeaderSchedulerConfig struct {
	mu     sync.RWMutex
	Name   string          `json:"name"`
	Ranges []core.KeyRange `json:"ranges"`
	Batch  int             `json:"batch"`
}

type balanceLeaderHandler struct {
	rd     *render.Render
	config *balanceLeaderSchedulerConfig
}

func newBalanceLeaderHandler(conf *balanceLeaderSchedulerConfig) http.Handler {
	handler := &balanceLeaderHandler{
		config: conf,
		rd:     render.New(render.Options{IndentJSON: true}),
	}
	router := mux.NewRouter()
	router.HandleFunc("/config", handler.UpdateConfig).Methods("POST")
	// router.HandleFunc("/list", handler.ListConfig).Methods("GET")
	return router
}

func (handler *balanceLeaderHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	var args []string
	var input map[string]interface{}
	if err := apiutil.ReadJSONRespondError(handler.rd, w, r.Body, &input); err != nil {
		return
	}
	ranges, ok := (input["ranges"]).([]string)
	if ok {
		args = append(args, ranges...)
	}
	if batch, ok := input["batch"].(float64); ok {
		args = append(args, strconv.FormatInt(int64(batch), 10))
	}
	handler.config.mu.Lock()
	handler.config.mu.Unlock()
}

type balanceLeaderScheduler struct {
	*BaseScheduler
	*retryQuota
	conf         *balanceLeaderSchedulerConfig
	handler      http.Handler
	opController *schedule.OperatorController
	filters      []filter.Filter
	counter      *prometheus.CounterVec
}

// newBalanceLeaderScheduler creates a scheduler that tends to keep leaders on
// each store balanced.
func newBalanceLeaderScheduler(opController *schedule.OperatorController, conf *balanceLeaderSchedulerConfig, options ...BalanceLeaderCreateOption) schedule.Scheduler {
	base := NewBaseScheduler(opController)
	s := &balanceLeaderScheduler{
		BaseScheduler: base,
		retryQuota:    newRetryQuota(balanceLeaderRetryLimit, defaultMinRetryLimit, defaultRetryQuotaAttenuation),
		conf:          conf,
		handler:       newBalanceLeaderHandler(conf),
		opController:  opController,
		counter:       balanceLeaderCounter,
	}
	for _, option := range options {
		option(s)
	}
	s.filters = []filter.Filter{
		&filter.StoreStateFilter{ActionScope: s.GetName(), TransferLeader: true},
		filter.NewSpecialUseFilter(s.GetName()),
	}
	return s
}

func (s *balanceLeaderScheduler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// BalanceLeaderCreateOption is used to create a scheduler with an option.
type BalanceLeaderCreateOption func(s *balanceLeaderScheduler)

// WithBalanceLeaderCounter sets the counter for the scheduler.
func WithBalanceLeaderCounter(counter *prometheus.CounterVec) BalanceLeaderCreateOption {
	return func(s *balanceLeaderScheduler) {
		s.counter = counter
	}
}

// WithBalanceLeaderName sets the name for the scheduler.
func WithBalanceLeaderName(name string) BalanceLeaderCreateOption {
	return func(s *balanceLeaderScheduler) {
		s.conf.Name = name
	}
}

func (l *balanceLeaderScheduler) GetName() string {
	return l.conf.Name
}

func (l *balanceLeaderScheduler) GetType() string {
	return BalanceLeaderType
}

func (l *balanceLeaderScheduler) EncodeConfig() ([]byte, error) {
	return schedule.EncodeConfig(l.conf)
}

func (l *balanceLeaderScheduler) IsScheduleAllowed(cluster schedule.Cluster) bool {
	allowed := l.opController.OperatorCount(operator.OpLeader) < cluster.GetOpts().GetLeaderScheduleLimit()
	if !allowed {
		operator.OperatorLimitCounter.WithLabelValues(l.GetType(), operator.OpLeader.String()).Inc()
	}
	return allowed
}

func (l *balanceLeaderScheduler) Schedule(cluster schedule.Cluster) []*operator.Operator {
	l.conf.mu.RLock()
	defer l.conf.mu.RUnlock()
	schedulerCounter.WithLabelValues(l.GetName(), "schedule").Inc()

	leaderSchedulePolicy := cluster.GetOpts().GetLeaderSchedulePolicy()
	opInfluence := l.opController.GetOpInfluence(cluster)
	kind := core.NewScheduleKind(core.LeaderKind, leaderSchedulePolicy)
	plan := newBalancePlan(kind, cluster, opInfluence)

	stores := cluster.GetStores()
	sources := filter.SelectSourceStores(stores, l.filters, cluster.GetOpts())
	targets := filter.SelectTargetStores(stores, l.filters, cluster.GetOpts())
	result := make([]*operator.Operator, 0, l.conf.Batch)
	sort.Slice(sources, func(i, j int) bool {
		iOp := plan.GetOpInfluence(sources[i].GetID())
		jOp := plan.GetOpInfluence(sources[j].GetID())
		return sources[i].LeaderScore(leaderSchedulePolicy, iOp) >
			sources[j].LeaderScore(leaderSchedulePolicy, jOp)
	})
	sort.Slice(targets, func(i, j int) bool {
		iOp := plan.GetOpInfluence(targets[i].GetID())
		jOp := plan.GetOpInfluence(targets[j].GetID())
		return targets[i].LeaderScore(leaderSchedulePolicy, iOp) <
			targets[j].LeaderScore(leaderSchedulePolicy, jOp)
	})

	usedStores := make(map[uint64]struct{})

	for i := 0; i < len(sources) || i < len(targets); i++ {
		if i < len(sources) {
			if _, ok := usedStores[sources[i].GetID()]; !ok {
				plan.source, plan.target = sources[i], nil
				retryLimit := l.retryQuota.GetLimit(plan.source)
				log.Debug("store leader score", zap.String("scheduler", l.GetName()), zap.Uint64("source-store", plan.SourceStoreID()))
				l.counter.WithLabelValues("high-score", plan.SourceMetricLabel()).Inc()
				for j := 0; j < retryLimit; j++ {
					schedulerCounter.WithLabelValues(l.GetName(), "total").Inc()
					if ops := l.transferLeaderOut(plan, usedStores); len(ops) > 0 {
						l.retryQuota.ResetLimit(plan.source)
						ops[0].Counters = append(ops[0].Counters, l.counter.WithLabelValues("transfer-out", plan.SourceMetricLabel()))
						result = append(result, ops...)
						if len(result) >= l.conf.Batch {
							return result
						}
						usedStores[plan.SourceStoreID()] = struct{}{}
						usedStores[plan.TargetStoreID()] = struct{}{}
						break
					}
				}
				if _, ok := usedStores[sources[i].GetID()]; !ok {
					l.Attenuate(plan.source)
					log.Debug("no operator created for selected stores", zap.String("scheduler", l.GetName()), zap.Uint64("source", plan.SourceStoreID()))
				}
			}
		}
		if i < len(targets) {
			if _, ok := usedStores[targets[i].GetID()]; !ok {
				plan.source, plan.target = nil, targets[i]
				retryLimit := l.retryQuota.GetLimit(plan.target)
				log.Debug("store leader score", zap.String("scheduler", l.GetName()), zap.Uint64("target-store", plan.TargetStoreID()))
				l.counter.WithLabelValues("low-score", plan.TargetMetricLabel()).Inc()
				for j := 0; j < retryLimit; j++ {
					schedulerCounter.WithLabelValues(l.GetName(), "total").Inc()
					if ops := l.transferLeaderIn(plan, usedStores); len(ops) > 0 {
						l.retryQuota.ResetLimit(plan.target)
						ops[0].Counters = append(ops[0].Counters, l.counter.WithLabelValues("transfer-in", plan.TargetMetricLabel()))
						result = append(result, ops...)
						if len(result) >= l.conf.Batch {
							return result
						}
						usedStores[plan.SourceStoreID()] = struct{}{}
						usedStores[plan.TargetStoreID()] = struct{}{}
						break
					}
				}
				if _, ok := usedStores[sources[i].GetID()]; !ok {
					l.Attenuate(plan.target)
					log.Debug("no operator created for selected stores", zap.String("scheduler", l.GetName()), zap.Uint64("target", plan.TargetStoreID()))
				}
			}
		}
	}
	l.retryQuota.GC(append(sources, targets...))
	return result
}

// transferLeaderOut transfers leader from the source store.
// It randomly selects a health region from the source store, then picks
// the best follower peer and transfers the leader.
func (l *balanceLeaderScheduler) transferLeaderOut(plan *balancePlan, usedStores map[uint64]struct{}) []*operator.Operator {
	plan.region = plan.RandLeaderRegion(plan.SourceStoreID(), l.conf.Ranges, schedule.IsRegionHealthy)
	if plan.region == nil {
		log.Debug("store has no leader", zap.String("scheduler", l.GetName()), zap.Uint64("store-id", plan.SourceStoreID()))
		schedulerCounter.WithLabelValues(l.GetName(), "no-leader-region").Inc()
		return nil
	}
	targets := plan.GetFollowerStores(plan.region)
	finalFilters := l.filters
	opts := plan.GetOpts()
	if leaderFilter := filter.NewPlacementLeaderSafeguard(l.GetName(), opts, plan.GetBasicCluster(), plan.GetRuleManager(), plan.region, plan.source); leaderFilter != nil {
		finalFilters = append(l.filters, leaderFilter)
	}
	usedFilter := filter.NewExcludedFilter(l.GetName(), nil, usedStores)
	finalFilters = append(finalFilters, usedFilter)
	targets = filter.SelectTargetStores(targets, finalFilters, opts)
	leaderSchedulePolicy := opts.GetLeaderSchedulePolicy()
	sort.Slice(targets, func(i, j int) bool {
		iOp := plan.GetOpInfluence(targets[i].GetID())
		jOp := plan.GetOpInfluence(targets[j].GetID())
		return targets[i].LeaderScore(leaderSchedulePolicy, iOp) < targets[j].LeaderScore(leaderSchedulePolicy, jOp)
	})
	for _, plan.target = range targets {
		if op := l.createOperator(plan); len(op) > 0 {
			return op
		}
	}
	log.Debug("region has no target store", zap.String("scheduler", l.GetName()), zap.Uint64("region-id", plan.region.GetID()))
	schedulerCounter.WithLabelValues(l.GetName(), "no-target-store").Inc()
	return nil
}

// transferLeaderIn transfers leader to the target store.
// It randomly selects a health region from the target store, then picks
// the worst follower peer and transfers the leader.
func (l *balanceLeaderScheduler) transferLeaderIn(plan *balancePlan, usedStores map[uint64]struct{}) []*operator.Operator {
	plan.region = plan.RandFollowerRegion(plan.TargetStoreID(), l.conf.Ranges, schedule.IsRegionHealthy)
	if plan.region == nil {
		log.Debug("store has no follower", zap.String("scheduler", l.GetName()), zap.Uint64("store-id", plan.TargetStoreID()))
		schedulerCounter.WithLabelValues(l.GetName(), "no-follower-region").Inc()
		return nil
	}
	leaderStoreID := plan.region.GetLeader().GetStoreId()
	plan.source = plan.GetStore(leaderStoreID)
	if plan.source == nil {
		log.Debug("region has no leader or leader store cannot be found",
			zap.String("scheduler", l.GetName()),
			zap.Uint64("region-id", plan.region.GetID()),
			zap.Uint64("store-id", leaderStoreID),
		)
		schedulerCounter.WithLabelValues(l.GetName(), "no-leader").Inc()
		return nil
	}
	if _, ok := usedStores[plan.source.GetID()]; ok {
		schedulerCounter.WithLabelValues(l.GetName(), "no-source-store").Inc()
		return nil
	}
	finalFilters := l.filters
	opts := plan.GetOpts()
	if leaderFilter := filter.NewPlacementLeaderSafeguard(l.GetName(), opts, plan.GetBasicCluster(), plan.GetRuleManager(), plan.region, plan.source); leaderFilter != nil {
		finalFilters = append(l.filters, leaderFilter)
	}
	target := filter.NewCandidates([]*core.StoreInfo{plan.target}).
		FilterTarget(opts, finalFilters...).
		PickFirst()
	if target == nil {
		log.Debug("region has no target store", zap.String("scheduler", l.GetName()), zap.Uint64("region-id", plan.region.GetID()))
		schedulerCounter.WithLabelValues(l.GetName(), "no-target-store").Inc()
		return nil
	}
	return l.createOperator(plan)
}

// createOperator creates the operator according to the source and target store.
// If the region is hot or the difference between the two stores is tolerable, then
// no new operator need to be created, otherwise create an operator that transfers
// the leader from the source store to the target store for the region.
func (l *balanceLeaderScheduler) createOperator(plan *balancePlan) []*operator.Operator {
	if plan.IsRegionHot(plan.region) {
		log.Debug("region is hot region, ignore it", zap.String("scheduler", l.GetName()), zap.Uint64("region-id", plan.region.GetID()))
		schedulerCounter.WithLabelValues(l.GetName(), "region-hot").Inc()
		return nil
	}

	if !plan.shouldBalance(l.GetName()) {
		schedulerCounter.WithLabelValues(l.GetName(), "skip").Inc()
		return nil
	}

	op, err := operator.CreateTransferLeaderOperator(BalanceLeaderType, plan, plan.region, plan.region.GetLeader().GetStoreId(), plan.TargetStoreID(), []uint64{}, operator.OpLeader)
	if err != nil {
		log.Debug("fail to create balance leader operator", errs.ZapError(err))
		return nil
	}
	op.Counters = append(op.Counters,
		schedulerCounter.WithLabelValues(l.GetName(), "new-operator"),
	)
	op.FinishedCounters = append(op.FinishedCounters,
		balanceDirectionCounter.WithLabelValues(l.GetName(), plan.SourceMetricLabel(), plan.TargetMetricLabel()),
		l.counter.WithLabelValues("move-leader", plan.SourceMetricLabel()+"-out"),
		l.counter.WithLabelValues("move-leader", plan.TargetMetricLabel()+"-in"),
	)
	op.AdditionalInfos["sourceScore"] = strconv.FormatFloat(plan.sourceScore, 'f', 2, 64)
	op.AdditionalInfos["targetScore"] = strconv.FormatFloat(plan.targetScore, 'f', 2, 64)
	return []*operator.Operator{op}
}
