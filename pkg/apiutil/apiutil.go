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
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/pingcap/errcode"
	"github.com/pingcap/errors"
	"github.com/pingcap/log"
	"github.com/unrolled/render"
)

var (
	// componentSignatureKey is used for http request header key
	// to identify component signature
	componentSignatureKey = "component"
	// componentAnonymousValue identifies anonymous request source
	componentAnonymousValue = "anonymous"
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

// GetIPAddrFromHTTPRequest returns http client IP from context.
// Because `X-Forwarded-For ` header has been written into RFC 7239(Forwarded HTTP Extension),
// so `X-Forwarded-For` has the higher priority than `X-Real-IP`.
// And both of them have the higher priority than `RemoteAddr`
func GetIPAddrFromHTTPRequest(r *http.Request) string {
	ips := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(strings.Trim(ips[0], " ")) > 0 {
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

// GetComponentNameOnHTTP returns component name from Request Header
func GetComponentNameOnHTTP(r *http.Request) string {
	componentName := r.Header.Get(componentSignatureKey)
	if len(componentName) == 0 {
		componentName = componentAnonymousValue
	}
	return componentName
}

// ComponentSignatureRoundTripper is used to add component signature in HTTP header
type ComponentSignatureRoundTripper struct {
	proxied   http.RoundTripper
	component string
}

// NewComponentSignatureRoundTripper returns a new ComponentSignatureRoundTripper.
func NewComponentSignatureRoundTripper(roundTripper http.RoundTripper, componentName string) *ComponentSignatureRoundTripper {
	return &ComponentSignatureRoundTripper{
		proxied:   roundTripper,
		component: componentName,
	}
}

// RoundTrip is used to implement RoundTripper
func (rt *ComponentSignatureRoundTripper) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	req.Header.Add(componentSignatureKey, rt.component)
	// Send the request, get the response and the error
	resp, err = rt.proxied.RoundTrip(req)
	return
}

// GetRouteName return mux route name registered
func GetRouteName(req *http.Request) string {
	route := mux.CurrentRoute(req)
	if route != nil {
		return route.GetName()
	}
	return ""
}

// APIAccessPath is used to identify HTTP api access path including path and method
type APIAccessPath struct {
	Path   string
	Method string
}

// NewAPIAccessPath returns an ApiAccessPath
func NewAPIAccessPath(path, method string) APIAccessPath {
	return APIAccessPath{Path: path, Method: method}
}
