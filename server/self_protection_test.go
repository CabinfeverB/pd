// Copyright 2021 TiKV Project Authors.
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

import (
	"sync"
	"time"

	. "github.com/pingcap/check"
	"golang.org/x/time/rate"
)

var _ = Suite(&testSelfProtectHandler{})

type testSelfProtectHandler struct {
	rateLimiterOnlyTotal         *APIRateLimiter
	rateLimiterDisabled          *APIRateLimiter
	rateLimiterZeroBucket        *APIRateLimiter
	rateLimiterComopnent         *APIRateLimiter
	rateLimiterNoComopnentConfig *APIRateLimiter
}

func (s *testSelfProtectHandler) SetUpSuite(c *C) {
	s.rateLimiterOnlyTotal = &APIRateLimiter{
		enableQPSLimit:          true,
		totalQPSRateLimiter:     rate.NewLimiter(100, 100),
		enableComponentQPSLimit: false,
	}
	s.rateLimiterDisabled = &APIRateLimiter{
		enableQPSLimit: false,
	}
	s.rateLimiterZeroBucket = &APIRateLimiter{
		enableQPSLimit:      true,
		totalQPSRateLimiter: rate.NewLimiter(0, 0),
	}
	s.rateLimiterComopnent = &APIRateLimiter{
		enableQPSLimit:          true,
		totalQPSRateLimiter:     rate.NewLimiter(100, 100),
		enableComponentQPSLimit: true,
		componentQPSRateLimiter: make(map[string]*rate.Limiter),
	}
	s.rateLimiterComopnent.componentQPSRateLimiter["pdctl"] = rate.NewLimiter(100, 100)
	s.rateLimiterComopnent.componentQPSRateLimiter["anonymous"] = rate.NewLimiter(100, 100)

	s.rateLimiterNoComopnentConfig = &APIRateLimiter{
		enableQPSLimit:          true,
		totalQPSRateLimiter:     rate.NewLimiter(200, 200),
		enableComponentQPSLimit: true,
		componentQPSRateLimiter: make(map[string]*rate.Limiter),
	}
	s.rateLimiterNoComopnentConfig.componentQPSRateLimiter["pdctl"] = rate.NewLimiter(10, 10)
}

func CountRateLimiterHandleResult(handler *serviceSelfProtectionHandler, component string, successCount *int,
	failedCount *int, lock *sync.Mutex, wg *sync.WaitGroup) {
	result := handler.Handle(component)
	lock.Lock()
	defer lock.Unlock()
	if result {
		*successCount++
	} else {
		*failedCount++
	}
	wg.Done()
}

func (s *testSelfProtectHandler) TestRateLimiterOnlyTotal(c *C) {
	time.Sleep(1 * time.Second)
	handler := serviceSelfProtectionHandler{
		apiRateLimiter: s.rateLimiterOnlyTotal,
	}
	var lock sync.Mutex
	successCount, failedCount := 0, 0
	var wg sync.WaitGroup
	wg.Add(110)
	for i := 0; i < 110; i++ {
		go CountRateLimiterHandleResult(&handler, "", &successCount, &failedCount, &lock, &wg)
	}
	wg.Wait()
	c.Assert(failedCount, Equals, 10)
	c.Assert(successCount, Equals, 100)
}

func (s *testSelfProtectHandler) TestRateLimiterDisabled(c *C) {
	time.Sleep(1 * time.Second)
	handler := serviceSelfProtectionHandler{
		apiRateLimiter: s.rateLimiterDisabled,
	}
	c.Assert(handler.Handle(""), Equals, true)
}

func (s *testSelfProtectHandler) TestRateLimiterZeroBucket(c *C) {
	time.Sleep(1 * time.Second)
	handler := serviceSelfProtectionHandler{
		apiRateLimiter: s.rateLimiterZeroBucket,
	}
	var lock sync.Mutex
	successCount, failedCount := 0, 0
	var wg sync.WaitGroup
	wg.Add(110)
	for i := 0; i < 110; i++ {
		go CountRateLimiterHandleResult(&handler, "", &successCount, &failedCount, &lock, &wg)
	}
	wg.Wait()
	c.Assert(failedCount, Equals, 110)
	c.Assert(successCount, Equals, 0)
}

func (s *testSelfProtectHandler) TestRateLimiterComopnent(c *C) {
	time.Sleep(1 * time.Second)
	handler := serviceSelfProtectionHandler{
		apiRateLimiter: s.rateLimiterComopnent,
	}
	var lock sync.Mutex
	successCount, failedCount := 0, 0
	var wg sync.WaitGroup
	wg.Add(300)
	for i := 0; i < 150; i++ {
		go CountRateLimiterHandleResult(&handler, "anonymous", &successCount, &failedCount, &lock, &wg)
		go CountRateLimiterHandleResult(&handler, "pdctl", &successCount, &failedCount, &lock, &wg)
	}
	wg.Wait()
	c.Assert(failedCount, Equals, 200)
	c.Assert(successCount, Equals, 100)

	time.Sleep(2 * time.Second)
	successCount, failedCount = 0, 0
	wg.Add(150)
	for i := 0; i < 150; i++ {
		go CountRateLimiterHandleResult(&handler, "anonymous", &successCount, &failedCount, &lock, &wg)
	}
	wg.Wait()
	c.Assert(failedCount, Equals, 50)
	c.Assert(successCount, Equals, 100)
}

func (s *testSelfProtectHandler) TestRateLimiterComponentNoConfig(c *C) {
	time.Sleep(1 * time.Second)
	handler := serviceSelfProtectionHandler{
		apiRateLimiter: s.rateLimiterNoComopnentConfig,
	}
	var lock sync.Mutex
	successAnonymousCount, failedAnonymousCount := 0, 0
	successPdctlCount, failedPdctlCount := 0, 0
	var wg sync.WaitGroup
	wg.Add(400)
	for i := 0; i < 200; i++ {
		go CountRateLimiterHandleResult(&handler, "anonymous", &successAnonymousCount, &failedAnonymousCount, &lock, &wg)
		go CountRateLimiterHandleResult(&handler, "pdctl", &successPdctlCount, &failedPdctlCount, &lock, &wg)
	}
	wg.Wait()
	c.Assert(successAnonymousCount, Equals, 190)
	c.Assert(failedAnonymousCount, Equals, 10)
	c.Assert(failedPdctlCount, Equals, 190)
	c.Assert(successPdctlCount, Equals, 10)
}
