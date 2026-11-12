package lra

import (
	"errors"
	"net/http"
)

// ErrResponseTooLarge is returned when a response body exceeds the limit set
// with [WithMaxResponseSize].
var ErrResponseTooLarge = errors.New("lra: response body too large")

// StatusError is returned when the server answers with a HTTP status code of
// 400 or above:
//
//	if se, ok := errors.AsType[*lra.StatusError](err); ok && se.StatusCode == http.StatusNotFound {
//		...
//	}
type StatusError struct {
	// Method is the HTTP method of the request.
	Method string
	// URL is the request URL without query string.
	URL string
	// StatusCode is the numeric HTTP status code, e.g. 404.
	StatusCode int
	// Status is the status line as returned by the server, e.g. "404 Not Found".
	Status string
	// Header contains the response headers.
	Header http.Header
	// Body is the response body.
	Body []byte
}

// Error returns a message like "lra: GET https://example.com/x: 404 Not Found".
func (e *StatusError) Error() string {
	return "lra: " + e.Method + " " + e.URL + ": " + e.Status
}
