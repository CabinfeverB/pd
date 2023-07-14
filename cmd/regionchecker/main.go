package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/pingcap/kvproto/pkg/metapb"
	"github.com/tikv/pd/server/api"
)

// TableRegions is the response data for list table's regions.
// It contains regions list for record and indices.
type TableRegions struct {
	TableName     string         `json:"name"`
	TableID       int64          `json:"id"`
	RecordRegions []RegionMeta   `json:"record_regions"`
	Indices       []IndexRegions `json:"indices"`
}

type RegionMeta struct {
	ID          uint64              `json:"region_id"`
	Leader      *metapb.Peer        `json:"leader"`
	Peers       []*metapb.Peer      `json:"peers"`
	RegionEpoch *metapb.RegionEpoch `json:"region_epoch"`
}

// IndexRegions is the region info for one index.
type IndexRegions struct {
	Name    string       `json:"name"`
	ID      int64        `json:"id"`
	Regions []RegionMeta `json:"regions"`
}

func main() {
	var regionsFile, storeFile string
	flag.StringVar(&regionsFile, "region", "", "")
	flag.StringVar(&storeFile, "store", "", "")
	flag.Parse()
	storesB, _ := os.ReadFile(storeFile)
	regionB, _ := os.ReadFile(regionsFile)
	stores := &api.StoresInfo{}
	err := json.Unmarshal(storesB, stores)
	if err != nil {
		fmt.Println(err)
		return
	}
	tables := []*TableRegions{}
	err = json.Unmarshal(regionB, &tables)
	if err != nil {
		fmt.Println(err)
		return
	}
	total := 0
	hotStores := getHotStore(stores.Stores)
	for _, table := range tables {
		total += len(table.RecordRegions)
		cnt := checkTable(table, hotStores)
		fmt.Println(table.TableName, "the number in hot store ", cnt, "  total : ", len(table.RecordRegions))
	}
	fmt.Println(" total region : ", total)
}

func checkTable(table *TableRegions, stores []*api.StoreInfo) int {
	cnt := 0
	for _, region := range table.RecordRegions {
		if checkRegionInStore(&region, stores) {
			cnt++
		}
	}
	return cnt
}

func getHotStore(stores []*api.StoreInfo) []*api.StoreInfo {
	ret := make([]*api.StoreInfo, 0)
	for _, store := range stores {
		for _, label := range store.Store.Labels {
			if label.Value == "hotdata" {
				ret = append(ret, store)
				break
			}
		}
	}
	return ret
}

func checkRegionInStore(region *RegionMeta, stores []*api.StoreInfo) bool {
	for _, peer := range region.Peers {
		for _, store := range stores {
			if peer.StoreId == store.Store.Id {
				//fmt.Println(region, region.ID, peer.Id, store.Store.Id)
				return true
			}
		}
	}
	return false
}
