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

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/pingcap/errors"
	"github.com/tikv/pd/pkg/apiutil"
	"github.com/tikv/pd/pkg/ratelimit"
	"github.com/tikv/pd/pkg/reflectutil"
	"github.com/tikv/pd/server"
	"github.com/tikv/pd/server/config"

	"github.com/unrolled/render"
)

type serviceMiddlewareHandler struct {
	svr *server.Server
	rd  *render.Render
}

func newServiceMiddlewareHandler(svr *server.Server, rd *render.Render) *serviceMiddlewareHandler {
	return &serviceMiddlewareHandler{
		svr: svr,
		rd:  rd,
	}
}

// @Tags service_middleware
// @Summary Get Service Middleware config.
// @Produce json
// @Success 200 {object} config.Config
// @Router /service-middleware/config [get]
func (h *serviceMiddlewareHandler) GetServiceMiddlewareConfig(w http.ResponseWriter, r *http.Request) {
	h.rd.JSON(w, http.StatusOK, h.svr.GetServiceMiddlewareConfig())
}

// @Tags service_middleware
// @Summary Update some service-middleware's config items.
// @Accept json
// @Param body body object false "json params"
// @Produce json
// @Success 200 {string} string "The config is updated."
// @Failure 400 {string} string "The input is invalid."
// @Failure 500 {string} string "PD server failed to proceed the request."
// @Router /service-middleware/config [post]
func (h *serviceMiddlewareHandler) SetServiceMiddlewareConfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.svr.GetServiceMiddlewareConfig()
	data, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		h.rd.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	conf := make(map[string]interface{})
	if err := json.Unmarshal(data, &conf); err != nil {
		h.rd.JSON(w, http.StatusBadRequest, err.Error())
		return
	}

	if len(conf) == 0 {
		h.rd.JSON(w, http.StatusOK, "The input is empty.")
	}

	for k, v := range conf {
		if s := strings.Split(k, "."); len(s) > 1 {
			if err := h.updateServiceMiddlewareConfig(cfg, k, v); err != nil {
				h.rd.JSON(w, http.StatusBadRequest, err.Error())
				return
			}
			continue
		}
		key := reflectutil.FindJSONFullTagByChildTag(reflect.TypeOf(config.ServiceMiddlewareConfig{}), k)
		if key == "" {
			h.rd.JSON(w, http.StatusBadRequest, fmt.Sprintf("config item %s not found", k))
			return
		}
		if err := h.updateServiceMiddlewareConfig(cfg, key, v); err != nil {
			h.rd.JSON(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	h.rd.JSON(w, http.StatusOK, "The service-middleware config is updated.")
}

func (h *serviceMiddlewareHandler) updateServiceMiddlewareConfig(cfg *config.ServiceMiddlewareConfig, key string, value interface{}) error {
	kp := strings.Split(key, ".")
	switch kp[0] {
	case "audit":
		return h.updateAudit(cfg, kp[len(kp)-1], value)
	case "rate-limit":
		return h.updateRateLimit(cfg, kp[len(kp)-1], value)
	}
	return errors.Errorf("config prefix %s not found", kp[0])
}

func (h *serviceMiddlewareHandler) updateAudit(config *config.ServiceMiddlewareConfig, key string, value interface{}) error {
	data, err := json.Marshal(map[string]interface{}{key: value})
	if err != nil {
		return err
	}

	updated, found, err := mergeConfig(&config.AuditConfig, data)
	if err != nil {
		return err
	}

	if !found {
		return errors.Errorf("config item %s not found", key)
	}

	if updated {
		err = h.svr.SetAuditConfig(config.AuditConfig)
	}
	return err
}

func (h *serviceMiddlewareHandler) updateRateLimit(config *config.ServiceMiddlewareConfig, key string, value interface{}) error {
	data, err := json.Marshal(map[string]interface{}{key: value})
	if err != nil {
		return err
	}

	updated, found, err := mergeConfig(&config.RateLimitConfig, data)
	if err != nil {
		return err
	}

	if !found {
		return errors.Errorf("config item %s not found", key)
	}

	if updated {
		err = h.svr.SetRateLimitConfig(config.RateLimitConfig)
	}
	return err
}

func (h *serviceMiddlewareHandler) updateRateLimitConfig(key, label string, value *ratelimit.DimensionConfig) error {
	cfg := h.svr.GetServiceMiddlewareConfig()
	rateLimitCfg := make(map[string]*ratelimit.DimensionConfig)
	for label, item := range cfg.LimiterConfig {
		rateLimitCfg[label] = item
	}
	rateLimitCfg[label] = value
	return h.updateRateLimit(cfg, key, &rateLimitCfg)
}

// @Tags service_middleware
// @Summary update ratelimit config
// @Param body body object string "json params"
// @Produce json
// @Success 200 {string} string ""
// @Failure 400 {string} string ""
// @Router /service-middleware/rate-limit/config [POST]
func (h *serviceMiddlewareHandler) SetRatelimitConfig(w http.ResponseWriter, r *http.Request) {
	var input map[string]interface{}
	if err := apiutil.ReadJSONRespondError(h.rd, w, r.Body, &input); err != nil {
		return
	}
	typeStr, ok := input["type"].(string)
	if !ok {
		h.rd.JSON(w, http.StatusBadRequest, "The type is empty.")
		return
	}
	var serviceLabel string
	switch typeStr {
	case "label":
		serviceLabel, ok = input["label"].(string)
		if !ok || len(serviceLabel) == 0 {
			h.rd.JSON(w, http.StatusBadRequest, "The label is empty.")
			return
		}
		if len(h.svr.GetServiceLabels(serviceLabel)) == 0 {
			h.rd.JSON(w, http.StatusBadRequest, "There is no label matched.")
			return
		}
	case "path":
		method, _ := input["method"].(string)
		path, ok := input["path"].(string)
		if !ok || len(path) == 0 {
			h.rd.JSON(w, http.StatusBadRequest, "The path is empty.")
			return
		}
		serviceLabel = h.svr.GetAPIAccessServiceLabel(apiutil.NewAccessPath(path, method))
		if len(serviceLabel) == 0 {
			h.rd.JSON(w, http.StatusBadRequest, "There is no label matched.")
			return
		}
	default:
		h.rd.JSON(w, http.StatusBadRequest, "The type is invalid.")
		return
	}
	if h.svr.IsInRateLimitAllowList(serviceLabel) {
		h.rd.JSON(w, http.StatusBadRequest, "This service is in allow list whose config can not be changed.")
		return
	}
	cfg := h.svr.GetRateLimitConfig().LimiterConfig[serviceLabel]
	// update concurrency limiter
	concurrencyUpdatedFlag := "Concurrency limiter is not changed."
	concurrencyFloat, okc := input["concurrency"].(float64)
	if okc {
		concurrency := uint64(concurrencyFloat)
		cfg.ConcurrencyLimit = concurrency
	}
	// update qps rate limiter
	qpsRateUpdatedFlag := "QPS rate limiter is not changed."
	qps, okq := input["qps"].(float64)
	if okq {
		brust := 0
		if qps > 0 {
			if int(qps) > 1 {
				brust = int(qps)
			} else {
				brust = 1
			}
		}
		cfg.QPS = qps
		cfg.QPSBurst = brust
	}
	status := h.svr.UpdateServiceRateLimiter(serviceLabel, ratelimit.UpdateDimensionConfig(cfg))
	switch {
	case status&ratelimit.QPSChanged != 0:
		qpsRateUpdatedFlag = "QPS rate limiter is changed."
	case status&ratelimit.QPSDeleted != 0:
		qpsRateUpdatedFlag = "QPS rate limiter is deleted."
	}
	switch {
	case status&ratelimit.ConcurrencyChanged != 0:
		concurrencyUpdatedFlag = "Concurrency limiter is changed."
	case status&ratelimit.ConcurrencyDeleted != 0:
		concurrencyUpdatedFlag = "Concurrency limiter is deleted."
	}
	if !okc && !okq {
		h.rd.JSON(w, http.StatusOK, "No changed.")
	} else {
		err := h.updateRateLimitConfig("limiter-config", serviceLabel, cfg)
		if err != nil {
			h.rd.JSON(w, http.StatusInternalServerError, err.Error())
		} else {
			h.rd.JSON(w, http.StatusOK, fmt.Sprintf("%s %s", concurrencyUpdatedFlag, qpsRateUpdatedFlag))
		}
	}
}
