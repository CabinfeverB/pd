package api

import (
	"net/http"

	"github.com/tikv/pd/server"
	"github.com/unrolled/render"
)

type diagHandler struct {
	svr *server.Server
	rd  *render.Render
}

func newDiagHandler(svr *server.Server, rd *render.Render) *diagHandler {
	return &diagHandler{
		svr: svr,
		rd:  rd,
	}
}

type Diag struct {
	Timestamp uint64 `json:"timestamp"`
	Len1      int    `json:"len1"`
	Len2      int    `json:"len2"`
}

func (h *diagHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	rc := getCluster(r)
	timestamp, count1, count2 := rc.GetCoordinator().DiagnoseDryRun()

	d := &Diag{

		Timestamp: timestamp,
		Len1:      count1,
		Len2:      count2,
	}

	h.rd.JSON(w, http.StatusOK, d)
}
