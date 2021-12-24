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
	"net/http"
	"sync"

	"github.com/tikv/pd/pkg/apiutil"
	"golang.org/x/time/rate"
)

// SelfProtectionManager is a framework to handle self protection mechanism
// Self-protection granularity is a logical service
type SelfProtectionManager struct {
	// ServiceHandlers is a map to store handler owned by different services
	ServiceHandlers map[string]*serviceSelfProtectionHandler
}

// NewSelfProtectionManager returns a new SelfProtectionManager with config
func NewSelfProtectionManager(server *Server) *SelfProtectionManager {
	handler := &SelfProtectionManager{
		ServiceHandlers: make(map[string]*serviceSelfProtectionHandler),
	}
	return handler
}

// ProcessHTTPSelfProtection is used to process http api self protection
func (h *SelfProtectionManager) ProcessHTTPSelfProtection(req *http.Request) bool {
	serviceName, findName := apiutil.GetHTTPRouteName(req)
	// if path is not registered in router, go on process
	if !findName {
		return true
	}

	serviceHandler, ok := h.ServiceHandlers[serviceName]
	// if there is no service handler, go on process
	if !ok {
		return true
	}

	httpHandler := &HTTPServiceSelfProtectionManager{
		req:     req,
		handler: serviceHandler,
	}
	return httpHandler.Handle()
}

// ServiceSelfProtectionManager is a interface for define self-protection handler by service granularity
type ServiceSelfProtectionManager interface {
	Handle() bool
}

// HTTPServiceSelfProtectionManager implement ServiceSelfProtectionManager to handle http
type HTTPServiceSelfProtectionManager struct {
	req     *http.Request
	handler *serviceSelfProtectionHandler
}

// Handle implement ServiceSelfProtectionManager defined function
func (h *HTTPServiceSelfProtectionManager) Handle() bool {
	// to be implemented
	return true
}

// serviceSelfProtectionHandler is a handler which is independent communication mode
type serviceSelfProtectionHandler struct {
	apiRateLimiter *APIRateLimiter
	// todo AuditLogger
}

func (h *serviceSelfProtectionHandler) Handle(componentName string) bool {
	limitAllow := h.rateLimitAllow(componentName)
	// it will include other self-protection actions
	return limitAllow
}

// RateLimitAllow is used to check whether the rate limit allow request process
func (h *serviceSelfProtectionHandler) rateLimitAllow(componentName string) bool {
	if h.apiRateLimiter == nil {
		return true
	}
	return h.apiRateLimiter.Allow(componentName)
}

// APIRateLimiter is used to limit unnecessary and excess request
// Currently support QPS rate limit by compoenent
// It depends on the rate.Limiter which implement a token-bucket algorithm
type APIRateLimiter struct {
	mu sync.RWMutex

	enableQPSLimit bool

	totalQPSRateLimiter *rate.Limiter

	enableComponentQPSLimit bool
	componentQPSRateLimiter map[string]*rate.Limiter
}

// QPSAllow firstly check component token bucket and then check total token bucket
// if component rate limiter doesn't allow, it won't ask total limiter
func (rl *APIRateLimiter) QPSAllow(componentName string) bool {
	if !rl.enableQPSLimit {
		return true
	}
	isComponentQPSLimit := true
	if rl.enableComponentQPSLimit {
		componentRateLimiter, ok := rl.componentQPSRateLimiter[componentName]
		// The current strategy is to ignore the component limit if it is not found
		if ok {
			isComponentQPSLimit = componentRateLimiter.Allow()
		}
	}
	if !isComponentQPSLimit {
		return isComponentQPSLimit
	}
	isTotalQPSLimit := rl.totalQPSRateLimiter.Allow()
	return isTotalQPSLimit
}

// Allow currentlt only supports QPS rate limit
func (rl *APIRateLimiter) Allow(componentName string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.QPSAllow(componentName)
}
