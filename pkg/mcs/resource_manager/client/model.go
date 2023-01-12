// Copyright 2022 TiKV Project Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,g
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

import (
	"context"

	rmpb "github.com/pingcap/kvproto/pkg/resource_manager"
)

type RequestUnit float64

type RequestInfo interface {
	IsWrite() bool
	WriteBytes() uint64
}

type ResponseInfo interface {
	ReadBytes() uint64
	KVCPUms() uint64
}

func GetRUValueFromConsumption(custom *rmpb.Consumption, typ rmpb.RequestUnitType) float64 {
	switch typ {
	case 0:
		return custom.RRU
	case 1:
		return custom.WRU
	}
	return 0
}

func GetResourceValueFromConsumption(custom *rmpb.Consumption, typ rmpb.ResourceType) float64 {
	switch typ {
	case 0:
		return custom.TotalCpuTimeMs
	case 1:
		return custom.ReadBytes
	case 2:
		return custom.WriteBytes
	}
	return 0
}

func Add(custom1 *rmpb.Consumption, custom2 *rmpb.Consumption) {
	custom1.RRU += custom1.RRU
	custom1.WRU += custom1.WRU
	custom1.ReadBytes += custom1.ReadBytes
	custom1.WriteBytes += custom1.WriteBytes
	custom1.TotalCpuTimeMs += custom1.TotalCpuTimeMs
	custom1.SqlLayerCpuTimeMs += custom1.SqlLayerCpuTimeMs
	custom1.KvReadRpcCount += custom1.KvReadRpcCount
	custom1.KvWriteRpcCount += custom1.KvWriteRpcCount
}

func Sub(custom1 *rmpb.Consumption, custom2 *rmpb.Consumption) {
	custom1.RRU -= custom1.RRU
	custom1.WRU -= custom1.WRU
	custom1.ReadBytes -= custom1.ReadBytes
	custom1.WriteBytes -= custom1.WriteBytes
	custom1.TotalCpuTimeMs -= custom1.TotalCpuTimeMs
	custom1.SqlLayerCpuTimeMs -= custom1.SqlLayerCpuTimeMs
	custom1.KvReadRpcCount -= custom1.KvReadRpcCount
	custom1.KvWriteRpcCount -= custom1.KvWriteRpcCount
}

type ResourceCalculator interface {
	Trickle(*rmpb.Consumption, context.Context)
	BeforeKVRequest(*rmpb.Consumption, RequestInfo)
	AfterKVRequest(*rmpb.Consumption, RequestInfo, ResponseInfo)
}

type KVCalculator struct {
	*Config
}

func newKVCalculator(cfg *Config) *KVCalculator {
	return &KVCalculator{Config: cfg}
}

func (kc *KVCalculator) Trickle(consumption *rmpb.Consumption, ctx context.Context) {
}

func (kc *KVCalculator) BeforeKVRequest(consumption *rmpb.Consumption, req RequestInfo) {
	if req.IsWrite() {
		writeBytes := float64(req.WriteBytes())
		wru := float64(kc.WriteBytesCost) * writeBytes
		consumption.KvWriteRpcCount += 1
		consumption.WRU += wru
		consumption.WriteBytes += writeBytes

	} else {
		consumption.KvReadRpcCount += 1
	}
}
func (kc *KVCalculator) AfterKVRequest(consumption *rmpb.Consumption, req RequestInfo, res ResponseInfo) {
	readBytes := float64(res.ReadBytes())
	kvCPUms := float64(res.KVCPUms())
	ru_io := readBytes * float64(kc.ReadBytesCost)
	ru_cpu := kvCPUms * float64(kc.WriteCPUMsCost)
	// for consumption
	consumption.RRU += ru_cpu + ru_io
	consumption.ReadBytes += readBytes
	consumption.TotalCpuTimeMs += kvCPUms
}

type SQLLayerCPUCalculateor struct {
	*Config
}

func newSQLLayerCPUCalculateor(cfg *Config) *SQLLayerCPUCalculateor {
	return &SQLLayerCPUCalculateor{Config: cfg}
}

func (sc *SQLLayerCPUCalculateor) Trickle(consumption *rmpb.Consumption, ctx context.Context) {
	// TODO: SQL Layer RU/resource custom
	cpuFunc := func(ctx context.Context) float64 {
		return 0.
	}
	cpu := cpuFunc(ctx)
	ru_cpu := cpu * 0.
	// TODO: SQL Layer RU/resource custom type
	consumption.RRU += ru_cpu / 2
	consumption.RRU += ru_cpu / 2
	consumption.TotalCpuTimeMs += cpu
	consumption.SqlLayerCpuTimeMs += cpu
}

func (sc *SQLLayerCPUCalculateor) BeforeKVRequest(consumption *rmpb.Consumption, req RequestInfo) {
}
func (sc *SQLLayerCPUCalculateor) AfterKVRequest(consumption *rmpb.Consumption, req RequestInfo, res ResponseInfo) {
}
