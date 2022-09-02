package cases

import (
	"time"

	"github.com/docker/go-units"
	"github.com/pingcap/kvproto/pkg/metapb"
	"github.com/tikv/pd/server/core"
	"github.com/tikv/pd/server/schedule/placement"
	"github.com/tikv/pd/tools/pd-simulator/simulator/info"
	"github.com/tikv/pd/tools/pd-simulator/simulator/simutil"
	"go.uber.org/zap"
)

func newRule1() *Case {
	var simCase Case

	simCase.Rules = make([]*placement.Rule, 0, 2)
	simCase.Rules = append(simCase.Rules, &placement.Rule{
		GroupID:     "test1",
		ID:          "test1",
		StartKeyHex: "",
		EndKeyHex:   "",
		Role:        placement.Learner,
		Count:       1,
		LabelConstraints: []placement.LabelConstraint{
			{
				Key:    "region",
				Op:     "in",
				Values: []string{"region1"},
			},
		},
		LocationLabels: []string{"host"},
	}, &placement.Rule{
		GroupID:     "pd",
		ID:          "default",
		StartKeyHex: "",
		EndKeyHex:   "",
		Role:        placement.Voter,
		Count:       5,
		LabelConstraints: []placement.LabelConstraint{
			{
				Key:    "region",
				Op:     "in",
				Values: []string{"region2"},
			},
		},
		LocationLabels: []string{"idc", "host"},
	})

	storeNum, regionNum := 9, 300
	for i := 0; i < storeNum; i++ {
		id := IDAllocator.nextID()
		simCase.Stores = append(simCase.Stores, &Store{
			ID:        id,
			Status:    metapb.StoreState_Up,
			Capacity:  1000 * units.GiB,
			Available: 500 * units.GiB,
			Version:   "2.1.0",
		})
	}
	simCase.Stores[0].Labels = []*metapb.StoreLabel{{Key: "region", Value: "region2"}, {Key: "idc", Value: "idc1"}}
	simCase.Stores[1].Labels = []*metapb.StoreLabel{{Key: "region", Value: "region2"}, {Key: "idc", Value: "idc1"}}
	simCase.Stores[2].Labels = []*metapb.StoreLabel{{Key: "region", Value: "region2"}, {Key: "idc", Value: "idc1"}}
	simCase.Stores[3].Labels = []*metapb.StoreLabel{{Key: "region", Value: "region2"}, {Key: "idc", Value: "idc1"}}
	simCase.Stores[4].Labels = []*metapb.StoreLabel{{Key: "region", Value: "region2"}, {Key: "idc", Value: "idc2"}}
	simCase.Stores[5].Labels = []*metapb.StoreLabel{{Key: "region", Value: "region2"}, {Key: "idc", Value: "idc3"}}
	simCase.Stores[6].Labels = []*metapb.StoreLabel{{Key: "region", Value: "region2"}, {Key: "idc", Value: "idc4"}}
	simCase.Stores[7].Labels = []*metapb.StoreLabel{{Key: "region", Value: "region2"}, {Key: "idc", Value: "idc5"}}
	simCase.Stores[8].Labels = []*metapb.StoreLabel{{Key: "region", Value: "region1"}}

	for i := 0; i < regionNum; i++ {
		peers := []*metapb.Peer{
			{Id: IDAllocator.nextID(), StoreId: uint64(i%(storeNum-5) + 5)},
			{Id: IDAllocator.nextID(), StoreId: uint64((i+1)%(storeNum-5) + 5)},
			{Id: IDAllocator.nextID(), StoreId: uint64((i+2)%(storeNum-5) + 5)},
			{Id: IDAllocator.nextID(), StoreId: uint64((i+3)%(storeNum-5) + 5)},
			{Id: IDAllocator.nextID(), StoreId: uint64(i%(storeNum-5) + 1)},
			{Id: IDAllocator.nextID(), StoreId: uint64(9), Role: metapb.PeerRole_Learner},
		}
		simCase.Regions = append(simCase.Regions, Region{
			ID:     IDAllocator.nextID(),
			Peers:  peers,
			Leader: peers[0],
			Size:   96 * units.MiB,
			Keys:   960000,
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
			if curTime-storesLastUpdateTime[i] > 180 {
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
