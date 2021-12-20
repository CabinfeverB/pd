// Copyright 2016 TiKV Project Authors.
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

package apiutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/pingcap/errcode"
	"github.com/pingcap/errors"
	"github.com/pingcap/log"
	"github.com/unrolled/render"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// DeferClose captures the error returned from closing (if an error occurs).
// This is designed to be used in a defer statement.
func DeferClose(c io.Closer, err *error) {
	if cerr := c.Close(); cerr != nil && *err == nil {
		*err = errors.WithStack(cerr)
	}
}

// JSONError lets callers check for just one error type
type JSONError struct {
	Err error
}

func (e JSONError) Error() string {
	return e.Err.Error()
}

func tagJSONError(err error) error {
	switch err.(type) {
	case *json.SyntaxError, *json.UnmarshalTypeError:
		return JSONError{err}
	}
	return err
}

// ReadJSON reads a JSON data from r and then closes it.
// An error due to invalid json will be returned as a JSONError
func ReadJSON(r io.ReadCloser, data interface{}) error {
	var err error
	defer DeferClose(r, &err)
	b, err := io.ReadAll(r)
	if err != nil {
		return errors.WithStack(err)
	}

	err = json.Unmarshal(b, data)
	if err != nil {
		return tagJSONError(err)
	}

	return err
}

// FieldError connects an error to a particular field
type FieldError struct {
	error
	field string
}

// ParseUint64VarsField connects strconv.ParseUint with request variables
// It hardcodes the base to 10 and bit size to 64
// Any error returned will connect the requested field to the error via FieldError
func ParseUint64VarsField(vars map[string]string, varName string) (uint64, *FieldError) {
	str, ok := vars[varName]
	if !ok {
		return 0, &FieldError{field: varName, error: fmt.Errorf("field %s not present", varName)}
	}
	parsed, err := strconv.ParseUint(str, 10, 64)
	if err == nil {
		return parsed, nil
	}
	return parsed, &FieldError{field: varName, error: err}
}

// ReadJSONRespondError writes json into data.
// On error respond with a 400 Bad Request
func ReadJSONRespondError(rd *render.Render, w http.ResponseWriter, body io.ReadCloser, data interface{}) error {
	err := ReadJSON(body, data)
	if err == nil {
		return nil
	}
	var errCode errcode.ErrorCode
	if jsonErr, ok := errors.Cause(err).(JSONError); ok {
		errCode = errcode.NewInvalidInputErr(jsonErr.Err)
	} else {
		errCode = errcode.NewInternalErr(err)
	}
	ErrorResp(rd, w, errCode)
	return err
}

// ErrorResp Respond to the client about the given error, integrating with errcode.ErrorCode.
//
// Important: if the `err` is just an error and not an errcode.ErrorCode (given by errors.Cause),
// then by default an error is assumed to be a 500 Internal Error.
//
// If the error is nil, this also responds with a 500 and logs at the error level.
func ErrorResp(rd *render.Render, w http.ResponseWriter, err error) {
	if err == nil {
		log.Error("nil is given to errorResp")
		rd.JSON(w, http.StatusInternalServerError, "nil error")
		return
	}
	if errCode := errcode.CodeChain(err); errCode != nil {
		w.Header().Set("TiDB-Error-Code", errCode.Code().CodeStr().String())
		rd.JSON(w, errCode.Code().HTTPCode(), errcode.NewJSONFormat(errCode))
	} else {
		rd.JSON(w, http.StatusInternalServerError, err.Error())
	}
}

// GetIPAddrFromGRPCContext returns gRPC client IP from context
func GetIPAddrFromGRPCContext(ctx context.Context) string {
	ip, ok := GetRealIPAddrFromGRPCContext(ctx)
	if ok {
		return ip
	}
	ip = GetPeerAddrFromGRPCContext(ctx)
	return ip
}

// GetPeerAddrFromGRPCContext return gRPC client real IP if gateway or proxy put real IP In
func GetRealIPAddrFromGRPCContext(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ok
	}
	realIPs := md.Get("x-real-ip")
	if len(realIPs) == 0 {
		return "", false
	}
	return realIPs[0], true
}

// GetPeerAddrFromGRPCContext return gRPC client IP which may be a proxy IP from peer info
func GetPeerAddrFromGRPCContext(ctx context.Context) string {
	var addr string
	if pr, ok := peer.FromContext(ctx); ok {
		if tcpAddr, ok := pr.Addr.(*net.TCPAddr); ok {
			addr = tcpAddr.IP.String()
		} else {
			addr = pr.Addr.String()
		}
	}
	return addr
}

// GetIPAddrFromHTTPRequest returns http client IP from context
func GetIPAddrFromHTTPRequest(r *http.Request) string {
	ips := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if ips[0] != "" {
		return ips[0]
	}

	ip := r.Header.Get("X-Real-Ip")
	if ip != "" {
		return ip
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return ip
}
