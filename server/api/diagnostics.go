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
	"net/http"

	"github.com/tikv/pd/pkg/apiutil"
	"github.com/tikv/pd/server"
	"github.com/tikv/pd/server/schedulers"
	"github.com/unrolled/render"
)

type diagnosticsHandler struct {
	svr *server.Server
	rd  *render.Render
}

func newDiagnosticsHandler(svr *server.Server, rd *render.Render) *diagnosticsHandler {
	return &diagnosticsHandler{
		svr: svr,
		rd:  rd,
	}
}

func (h *diagnosticsHandler) GetDiagnosticsResult(w http.ResponseWriter, r *http.Request) {
	var input map[string]interface{}
	if err := apiutil.ReadJSONRespondError(h.rd, w, r.Body, &input); err != nil {
		return
	}

	name, ok := input["name"].(string)
	if !ok {
		h.rd.JSON(w, http.StatusBadRequest, "missing scheduler name")
		return
	}

	switch name {
	case schedulers.BalanceRegionName:
	default:
		h.rd.JSON(w, http.StatusBadRequest, "scheduler name hasn't supported yet")
		return
	}
	rc := getCluster(r)
	result, err := rc.GetCoordinator().GetDiagnosticsResult(name)
	if err != nil {
		h.rd.JSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.rd.JSON(w, http.StatusOK, result)
}
