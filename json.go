package lra

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// DoJSON sends a request with in encoded as JSON body and decodes the JSON
// response into a value of type T.
//
// If in is nil, no body is sent. If in is a []byte or [json.RawMessage], it
// is sent as is. The headers "Content-Type: application/json" (if there is a
// body) and "Accept: application/json" are set unless configured with
// [WithHeader].
//
// An empty response body results in the zero value of T. If the request fails
// or the server returns a status code of 400 or above, the zero value and the
// error are returned; the response body is available in [StatusError.Body].
func DoJSON[T any](ctx context.Context, c *Client, method, endpoint string, in any) (T, error) {
	var zero T

	header := http.Header{"Accept": {"application/json"}}
	var body []byte
	if in != nil {
		switch v := in.(type) {
		case []byte:
			body = v
		case json.RawMessage:
			body = v
		default:
			b, err := json.Marshal(in)
			if err != nil {
				return zero, fmt.Errorf("lra: encoding request body: %w", err)
			}
			body = b
		}
		header.Set("Content-Type", "application/json")
	}

	res, err := c.do(ctx, method, endpoint, body, header)
	if err != nil {
		return zero, err
	}
	if len(res.Body) == 0 {
		return zero, nil
	}
	var out T
	if err := json.Unmarshal(res.Body, &out); err != nil {
		path, _, _ := strings.Cut(endpoint, "?")
		return zero, fmt.Errorf("lra: decoding response of %s %s: %w", method, path, err)
	}
	return out, nil
}

// GetJSON sends a GET request and decodes the JSON response, see [DoJSON].
func GetJSON[T any](ctx context.Context, c *Client, endpoint string) (T, error) {
	return DoJSON[T](ctx, c, http.MethodGet, endpoint, nil)
}

// DeleteJSON sends a DELETE request with an optional JSON body (nil for none)
// and decodes the JSON response, see [DoJSON].
func DeleteJSON[T any](ctx context.Context, c *Client, endpoint string, in any) (T, error) {
	return DoJSON[T](ctx, c, http.MethodDelete, endpoint, in)
}

// PatchJSON sends a PATCH request with a JSON body and decodes the JSON
// response, see [DoJSON].
func PatchJSON[T any](ctx context.Context, c *Client, endpoint string, in any) (T, error) {
	return DoJSON[T](ctx, c, http.MethodPatch, endpoint, in)
}

// PostJSON sends a POST request with a JSON body and decodes the JSON
// response, see [DoJSON].
func PostJSON[T any](ctx context.Context, c *Client, endpoint string, in any) (T, error) {
	return DoJSON[T](ctx, c, http.MethodPost, endpoint, in)
}

// PutJSON sends a PUT request with a JSON body and decodes the JSON response,
// see [DoJSON].
func PutJSON[T any](ctx context.Context, c *Client, endpoint string, in any) (T, error) {
	return DoJSON[T](ctx, c, http.MethodPut, endpoint, in)
}
