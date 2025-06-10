package common

import (
	"net/http"
	"net/http/httputil"
)

type HTTPError struct {
	Err      error
	Request  *http.Request
	Response *http.Response
}

func (r *HTTPError) DumpRequest(body bool) (out []byte) {
	if r.Request != nil {
		if r.Request.GetBody != nil {
			r.Request.Body, _ = r.Request.GetBody()
		}
		out, _ = httputil.DumpRequestOut(r.Request, body)
	}
	return out
}

func (r *HTTPError) DumpResponse(body bool) (out []byte) {
	if r.Response != nil {
		out, _ = httputil.DumpResponse(r.Response, body)
	}
	return out
}

func (r *HTTPError) Error() string {
	return r.Err.Error()
}
