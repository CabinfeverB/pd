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

package cases

import (
	"time"

	"github.com/docker/go-units"
	"github.com/pingcap/kvproto/pkg/metapb"
	"github.com/tikv/pd/server/core"
	"github.com/tikv/pd/tools/pd-simulator/simulator/info"
	"github.com/tikv/pd/tools/pd-simulator/simulator/simutil"
	"go.uber.org/zap"
)

func newRegionScatter() *Case {
	var simCase Case
	simCase.RemovedSchedulers = []string{"balance-region-scheduler", "balance-leader-scheduler", "balance-hot-region-scheduler"}

	storeNum := simutil.CaseConfigure.StoreNum
	regionNum := simutil.CaseConfigure.RegionNum * storeNum / 3
	if storeNum == 0 || regionNum == 0 {
		storeNum, regionNum = 60, 20000
	}

	for i := 0; i < storeNum; i++ {
		simCase.Stores = append(simCase.Stores, &Store{
			ID:        IDAllocator.nextID(),
			Status:    metapb.StoreState_Up,
			Capacity:  1 * units.TiB,
			Available: 980 * units.GiB,
			Version:   "2.1.0",
		})
	}

	for i := 0; i < regionNum; i++ {
		peers := []*metapb.Peer{
			{Id: IDAllocator.nextID(), StoreId: uint64(1)},
			{Id: IDAllocator.nextID(), StoreId: uint64(2)},
			{Id: IDAllocator.nextID(), StoreId: uint64(3)},
		}
		simCase.Regions = append(simCase.Regions, Region{
			ID:     IDAllocator.nextID(),
			Peers:  peers,
			Leader: peers[0],
			Size:   0 * units.MiB,
			Keys:   0,
		})
	}

	storesLastUpdateTime := make([]int64, storeNum+1)
	storeLastAvailable := make([]uint64, storeNum+1)
	simCase.Checker = func(regions *core.RegionsInfo, stats []info.StoreStats) bool {
		res := true
		curTime := time.Now().Unix()
		storesAvailable := make([]uint64, 0, storeNum+1)
		for i := 1; i <= storeNum; i++ {
			available := stats[i].GetAvailable()
			storesAvailable = append(storesAvailable, available)
			if curTime-storesLastUpdateTime[i] > 600 {
				if storeLastAvailable[i] != available {
					res = false
				}
				if stats[i].ToCompactionSize != 0 {
					res = false
				}
				storesLastUpdateTime[i] = curTime
				storeLastAvailable[i] = available
			} else {
				res = false
			}
		}
		simutil.Logger.Info("current counts", zap.Uint64s("storesAvailable", storesAvailable))
		return res
	}
	return &simCase
}
