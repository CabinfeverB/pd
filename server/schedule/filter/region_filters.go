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

package filter

import (
	"github.com/tikv/pd/pkg/slice"
	"github.com/tikv/pd/server/config"
	"github.com/tikv/pd/server/core"
	"github.com/tikv/pd/server/schedule/placement"
)

// SelectRegions selects regions that be selected from the list.
func SelectRegions(regions []*core.RegionInfo, filters ...RegionFilter) []*core.RegionInfo {
	return filterRegionsBy(regions, func(r *core.RegionInfo) bool {
		return slice.AllOf(filters, func(i int) bool {
			return filters[i].Select(r)
		})
	})
}

func filterRegionsBy(regions []*core.RegionInfo, keepPred func(*core.RegionInfo) bool) (selected []*core.RegionInfo) {
	for _, s := range regions {
		if keepPred(s) {
			selected = append(selected, s)
		}
	}
	return
}

// SelectOneRegion selects one region that be selected from the list.
func SelectOneRegion(regions []*core.RegionInfo, filters ...RegionFilter) *core.RegionInfo {
	for _, r := range regions {
		if slice.AllOf(filters, func(i int) bool { return filters[i].Select(r) }) {
			return r
		}
	}
	return nil
}

// type RegionFilterType int32

// const (
// 	RegionUnhealthy           RegionFilterType = 0
// 	RegionNotFitPlacementRule RegionFilterType = 1
// )

// var RegionFilterTypeName = map[int32]string{
// 	0: "Unhealthy",
// 	1: "NotFitPlacementRule",
// }

// var RegionFilterTypeValue = map[string]int32{
// 	"Unhealthy":           0,
// 	"NotFitPlacementRule": 1,
// }

// RegionFilter is an interface to filter region.
type RegionFilter interface {
	// RegionFilter is used to indicate where the filter will act on.
	Scope() string
	Type() string
	Reason() string
	// Return true if the region can be used to schedule.
	Select(region *core.RegionInfo) bool
}

type RegionPengdingFilter struct {
	scope string
}

func NewRegionPengdingFilter(scope string) RegionFilter {
	return &RegionPengdingFilter{scope: scope}
}

func (f *RegionPengdingFilter) Scope() string {
	return f.scope
}

func (f *RegionPengdingFilter) Type() string {
	return "Unhealthy"
}

func (f *RegionPengdingFilter) Reason() string {
	return "This region has too many pending pees."
}

func (f *RegionPengdingFilter) Select(region *core.RegionInfo) bool {
	return len(region.GetPendingPeers()) == 0
}

type RegionDownFilter struct {
	scope string
}

func NewRegionDownFilter(scope string) RegionFilter {
	return &RegionDownFilter{scope: scope}
}

func (f *RegionDownFilter) Scope() string {
	return f.scope
}

func (f *RegionDownFilter) Type() string {
	return "Unhealthy"
}

func (f *RegionDownFilter) Reason() string {
	return "This region has too many down pees."
}

func (f *RegionDownFilter) Select(region *core.RegionInfo) bool {
	return len(region.GetDownPeers()) == 0
}

type RegionReplicatedFilter struct {
	scope   string
	cluster Cluster
}

func NewRegionReplicatedFilter(scope string, cluster Cluster) RegionFilter {
	return &RegionReplicatedFilter{scope: scope, cluster: cluster}
}

func (f *RegionReplicatedFilter) Scope() string {
	return f.scope
}

func (f *RegionReplicatedFilter) Type() string {
	return "NotReplicated"
}

func (f *RegionReplicatedFilter) Reason() string {
	if f.cluster.GetOpts().IsPlacementRulesEnabled() {
		return "This region does not fit placement rule."
	}
	return "This region is not replicated"
}

func (f *RegionReplicatedFilter) Select(region *core.RegionInfo) bool {
	if f.cluster.GetOpts().IsPlacementRulesEnabled() {
		return f.cluster.GetRuleManager().FitRegion(f.cluster, region).IsSatisfied()
	}
	return len(region.GetLearners()) == 0 && len(region.GetPeers()) == f.cluster.GetOpts().GetMaxReplicas()
}

type RegionEmptyFilter struct {
	scope   string
	cluster Cluster
}

func NewRegionEmptyFilter(scope string, cluster Cluster) RegionFilter {
	return &RegionEmptyFilter{scope: scope, cluster: cluster}
}

func (f *RegionEmptyFilter) Scope() string {
	return f.scope
}

func (f *RegionEmptyFilter) Type() string {
	return "EmptyRegion"
}

func (f *RegionEmptyFilter) Reason() string {
	return "This region is empty"
}

func (f *RegionEmptyFilter) Select(region *core.RegionInfo) bool {
	return isEmptyRegionAllowBalance(f.cluster, region)
}

type RegionAlwaysAllowFilter struct{}

func NewRegionAlwaysAllowFilter() RegionFilter {
	return &RegionAlwaysAllowFilter{}
}

func (f *RegionAlwaysAllowFilter) Scope() string {
	return ""
}

func (f *RegionAlwaysAllowFilter) Type() string {
	return ""
}

func (f *RegionAlwaysAllowFilter) Reason() string {
	return ""
}

func (f *RegionAlwaysAllowFilter) Select(region *core.RegionInfo) bool {
	return true
}

// isEmptyRegionAllowBalance checks if a region is an empty region and can be balanced.
func isEmptyRegionAllowBalance(cluster Cluster, region *core.RegionInfo) bool {
	return region.GetApproximateSize() > core.EmptyRegionApproximateSize || cluster.GetRegionCount() < core.InitClusterRegionThreshold
}

// Cluster provides an overview of a cluster's regions distribution.
type Cluster interface {
	core.StoreSetInformer
	core.StoreSetController
	core.RegionSetInformer
	GetOpts() *config.PersistOptions
	GetRuleManager() *placement.RuleManager
}
