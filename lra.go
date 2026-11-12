// Copyright 2018-2026 Jörn Ott. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lra is a lowlevel REST api client with some convenient functions like proxy and ssl handling.
//
// This package handles http and socks proxies as well as self signed certificates.
// It also allows the specification of Headers to be sent with every request.
//
// Every request method exists in a variant taking a [context.Context] (for
// example [Connection.GetContext]), which allows cancelling requests and
// setting per-request deadlines. The variants without context use
// [context.Background].
package lra

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// DefaultTimeout is the overall request timeout used when NewConnection is
// called with a zero timeout.
const DefaultTimeout = 60 * time.Second

// HeaderList is a key-value list of headers to set on every request. Used when declaring the connection.
type HeaderList map[string]string

// StatusError is returned when the server answers with a HTTP status code of
// 400 or above. Use [errors.AsType] (or [errors.As]) to inspect it:
//
//	if se, ok := errors.AsType[*lra.StatusError](err); ok && se.StatusCode == http.StatusNotFound {
//		...
//	}
type StatusError struct {
	// StatusCode is the numeric HTTP status code, e.g. 404.
	StatusCode int
	// Status is the status line as returned by the server, e.g. "404 Not Found".
	Status string
	// Body holds the response body (for HEAD requests: the JSON encoded headers).
	Body []byte
}

// Error returns the HTTP status line, e.g. "404 Not Found".
func (e *StatusError) Error() string {
	return e.Status
}

// The Connection object stores all the information needed to handle requests.
// Apart from the http.Client, it also contains the original settings and additional
// headers to send with every request.
//
// The fields are mostly set by the parameters passed to the NewConnection function
// except for Protocol, BaseURL and Client which are constructed based on those informations.
// Changing the fields after creating the connection has no effect on the requests,
// except for SendHeaders and Client.
//
// A Connection is safe for concurrent use by multiple goroutines as long as
// its fields are not modified.
type Connection struct {
	Protocol     string
	Server       string
	Port         int
	BaseEndpoint string
	User         string
	Password     string
	ValidateSSL  bool
	Proxy        string
	ProxyIsSocks bool
	// BaseURL is the URL all endpoints are appended to. If a user is set, it
	// contains the (URL-escaped) credentials.
	BaseURL     string
	SendHeaders HeaderList
	Client      *http.Client
	Timeout     time.Duration
}

// NewConnection builds a Connection object with a configured http client.
//
// If useSSL is true, https is used, otherwise http. server and port specify the
// server name or IP (IPv6 addresses may be given with or without brackets) and
// the port to connect to. The baseEndpoint is added directly behind the port
// which allows skipping some base path before the api itself. user and password
// are used for basic authentication.
// validateSSL, if set to false, allows to skip SSL validation, which is needed for
// self signed certificates.
// If proxyIsSocks is set to true, proxyAddr is the address of a SOCKS5 proxy,
// either as host:port or as URL (socks5://[user:password@]host:port).
// If proxyIsSocks is false, proxyAddr is the URL of a HTTP proxy to use
// (http://host:port; a missing scheme defaults to http).
// sendHeaders is a list of Headers to send with the requests. This allows to pass
// authentication headers, content type definitions etc.
// timeout is the overall timeout for a request, a value of 0 means DefaultTimeout.
func NewConnection(useSSL bool, server string, port int, baseEndpoint string, user string, password string, validateSSL bool, proxyAddr string, proxyIsSocks bool, sendHeaders HeaderList, timeout time.Duration) (*Connection, error) {
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	tr, err := newTransport(validateSSL, proxyAddr, proxyIsSocks)
	if err != nil {
		return nil, err
	}

	protocol := "http"
	if useSSL {
		protocol = "https"
	}

	host := strings.TrimSuffix(strings.TrimPrefix(server, "["), "]")
	base := url.URL{
		Scheme: protocol,
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
	}
	if user != "" {
		base.User = url.UserPassword(user, password)
	}

	return &Connection{
		Protocol:     protocol,
		Server:       server,
		Port:         port,
		BaseEndpoint: baseEndpoint,
		User:         user,
		Password:     password,
		ValidateSSL:  validateSSL,
		Proxy:        proxyAddr,
		ProxyIsSocks: proxyIsSocks,
		BaseURL:      base.String() + baseEndpoint,
		SendHeaders:  sendHeaders,
		Client: &http.Client{
			Transport: tr,
			Timeout:   timeout,
		},
		Timeout: timeout,
	}, nil
}

// newTransport creates a http.Transport with the same defaults as
// http.DefaultTransport, but with a larger connection pool and the
// configured TLS and proxy settings. Proxy settings from the environment
// (HTTP_PROXY etc.) are deliberately not used.
func newTransport(validateSSL bool, proxyAddr string, proxyIsSocks bool) (*http.Transport, error) {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if !validateSSL {
		// Explicitly requested by the caller, e.g. for self signed certificates.
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}

	if proxyAddr == "" {
		return tr, nil
	}

	if proxyIsSocks {
		d, err := socksDialer(proxyAddr, dialer)
		if err != nil {
			return nil, err
		}
		tr.DialContext = d.DialContext
		return tr, nil
	}

	if !strings.Contains(proxyAddr, "://") {
		proxyAddr = "http://" + proxyAddr
	}
	proxyURL, err := url.Parse(proxyAddr)
	if err != nil {
		return nil, err
	}
	if proxyURL.Host == "" {
		return nil, fmt.Errorf("lra: invalid HTTP proxy URL %q: missing host", proxyAddr)
	}
	tr.Proxy = http.ProxyURL(proxyURL)
	return tr, nil
}

// socksDialer returns a SOCKS5 dialer for addr, which is either host:port or a
// socks5:// or socks5h:// URL.
func socksDialer(addr string, forward *net.Dialer) (proxy.ContextDialer, error) {
	var (
		d   proxy.Dialer
		err error
	)
	if strings.Contains(addr, "://") {
		u, perr := url.Parse(addr)
		if perr != nil {
			return nil, perr
		}
		d, err = proxy.FromURL(u, forward)
	} else {
		if _, _, err = net.SplitHostPort(addr); err != nil {
			return nil, fmt.Errorf("lra: invalid SOCKS5 proxy address %q: %w", addr, err)
		}
		d, err = proxy.SOCKS5("tcp", addr, nil, forward)
	}
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("lra: SOCKS5 dialer for %q does not support contexts", addr)
	}
	return cd, nil
}

// request executes the HTTP request. For HEAD requests, the response headers
// are returned as JSON. For status codes >= 400, the response is returned
// together with a *StatusError.
func (c *Connection) request(ctx context.Context, method string, endpoint string, body []byte) ([]byte, error) {
	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+endpoint, reqBody)
	if err != nil {
		return nil, err
	}
	for h, v := range c.SendHeaders {
		req.Header.Set(h, v)
	}

	r, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()

	var response []byte
	if method == http.MethodHead {
		response, err = json.Marshal(r.Header)
	} else {
		response, err = io.ReadAll(r.Body)
	}
	if err != nil {
		return nil, err
	}
	if r.StatusCode >= http.StatusBadRequest {
		return response, &StatusError{
			StatusCode: r.StatusCode,
			Status:     r.Status,
			Body:       response,
		}
	}
	return response, nil
}

// requestJSON executes the request and decodes the response into data. If the
// request fails, its error takes precedence over a JSON decoding error. data is
// populated even if the server returned an error status, as long as the
// response body is valid JSON.
func (c *Connection) requestJSON(ctx context.Context, method string, endpoint string, body []byte, data any) error {
	response, err := c.request(ctx, method, endpoint, body)
	if jsonErr := json.Unmarshal(response, data); jsonErr != nil && err == nil {
		return jsonErr
	}
	return err
}

// Connect issues a HTTP CONNECT request and returns the raw data.
func (c *Connection) Connect(endpoint string) ([]byte, error) {
	return c.ConnectContext(context.Background(), endpoint)
}

// ConnectContext is like Connect but uses the given context.
func (c *Connection) ConnectContext(ctx context.Context, endpoint string) ([]byte, error) {
	return c.request(ctx, http.MethodConnect, endpoint, nil)
}

// ConnectJSON issues a HTTP CONNECT request, parses the resulting data as JSON and returns the parse results.
func (c *Connection) ConnectJSON(endpoint string, data any) error {
	return c.ConnectJSONContext(context.Background(), endpoint, data)
}

// ConnectJSONContext is like ConnectJSON but uses the given context.
func (c *Connection) ConnectJSONContext(ctx context.Context, endpoint string, data any) error {
	return c.requestJSON(ctx, http.MethodConnect, endpoint, nil, data)
}

// Delete issues a HTTP DELETE request and returns the raw data.
func (c *Connection) Delete(endpoint string, jsonData []byte) ([]byte, error) {
	return c.DeleteContext(context.Background(), endpoint, jsonData)
}

// DeleteContext is like Delete but uses the given context.
func (c *Connection) DeleteContext(ctx context.Context, endpoint string, jsonData []byte) ([]byte, error) {
	return c.request(ctx, http.MethodDelete, endpoint, jsonData)
}

// DeleteJSON issues a HTTP DELETE request, parses the resulting data as JSON and returns the parse results.
func (c *Connection) DeleteJSON(endpoint string, jsonData []byte, data any) error {
	return c.DeleteJSONContext(context.Background(), endpoint, jsonData, data)
}

// DeleteJSONContext is like DeleteJSON but uses the given context.
func (c *Connection) DeleteJSONContext(ctx context.Context, endpoint string, jsonData []byte, data any) error {
	return c.requestJSON(ctx, http.MethodDelete, endpoint, jsonData, data)
}

// Get issues a HTTP GET request and returns the raw data.
func (c *Connection) Get(endpoint string) ([]byte, error) {
	return c.GetContext(context.Background(), endpoint)
}

// GetContext is like Get but uses the given context.
func (c *Connection) GetContext(ctx context.Context, endpoint string) ([]byte, error) {
	return c.request(ctx, http.MethodGet, endpoint, nil)
}

// GetJSON issues a HTTP GET request, parses the resulting data as JSON and returns the parse results.
func (c *Connection) GetJSON(endpoint string, data any) error {
	return c.GetJSONContext(context.Background(), endpoint, data)
}

// GetJSONContext is like GetJSON but uses the given context.
func (c *Connection) GetJSONContext(ctx context.Context, endpoint string, data any) error {
	return c.requestJSON(ctx, http.MethodGet, endpoint, nil, data)
}

// Head issues a HTTP HEAD request and returns the response headers encoded as JSON.
func (c *Connection) Head(endpoint string) ([]byte, error) {
	return c.HeadContext(context.Background(), endpoint)
}

// HeadContext is like Head but uses the given context.
func (c *Connection) HeadContext(ctx context.Context, endpoint string) ([]byte, error) {
	return c.request(ctx, http.MethodHead, endpoint, nil)
}

// HeadJSON issues a HTTP HEAD request and decodes the response headers into data.
func (c *Connection) HeadJSON(endpoint string, data any) error {
	return c.HeadJSONContext(context.Background(), endpoint, data)
}

// HeadJSONContext is like HeadJSON but uses the given context.
func (c *Connection) HeadJSONContext(ctx context.Context, endpoint string, data any) error {
	return c.requestJSON(ctx, http.MethodHead, endpoint, nil, data)
}

// Options issues a HTTP OPTIONS request and returns the raw data.
func (c *Connection) Options(endpoint string) ([]byte, error) {
	return c.OptionsContext(context.Background(), endpoint)
}

// OptionsContext is like Options but uses the given context.
func (c *Connection) OptionsContext(ctx context.Context, endpoint string) ([]byte, error) {
	return c.request(ctx, http.MethodOptions, endpoint, nil)
}

// OptionsJSON issues a HTTP OPTIONS request, parses the resulting data as JSON and returns the parse results.
func (c *Connection) OptionsJSON(endpoint string, data any) error {
	return c.OptionsJSONContext(context.Background(), endpoint, data)
}

// OptionsJSONContext is like OptionsJSON but uses the given context.
func (c *Connection) OptionsJSONContext(ctx context.Context, endpoint string, data any) error {
	return c.requestJSON(ctx, http.MethodOptions, endpoint, nil, data)
}

// Patch issues a HTTP PATCH (RFC 5789) request and returns the raw data.
func (c *Connection) Patch(endpoint string, jsonData []byte) ([]byte, error) {
	return c.PatchContext(context.Background(), endpoint, jsonData)
}

// PatchContext is like Patch but uses the given context.
func (c *Connection) PatchContext(ctx context.Context, endpoint string, jsonData []byte) ([]byte, error) {
	return c.request(ctx, http.MethodPatch, endpoint, jsonData)
}

// PatchJSON issues a HTTP PATCH request, parses the resulting data as JSON and returns the parse results.
func (c *Connection) PatchJSON(endpoint string, jsonData []byte, data any) error {
	return c.PatchJSONContext(context.Background(), endpoint, jsonData, data)
}

// PatchJSONContext is like PatchJSON but uses the given context.
func (c *Connection) PatchJSONContext(ctx context.Context, endpoint string, jsonData []byte, data any) error {
	return c.requestJSON(ctx, http.MethodPatch, endpoint, jsonData, data)
}

// Post issues a HTTP POST request and returns the raw data.
func (c *Connection) Post(endpoint string, jsonData []byte) ([]byte, error) {
	return c.PostContext(context.Background(), endpoint, jsonData)
}

// PostContext is like Post but uses the given context.
func (c *Connection) PostContext(ctx context.Context, endpoint string, jsonData []byte) ([]byte, error) {
	return c.request(ctx, http.MethodPost, endpoint, jsonData)
}

// PostJSON issues a HTTP POST request, parses the resulting data as JSON and returns the parse results.
func (c *Connection) PostJSON(endpoint string, jsonData []byte, data any) error {
	return c.PostJSONContext(context.Background(), endpoint, jsonData, data)
}

// PostJSONContext is like PostJSON but uses the given context.
func (c *Connection) PostJSONContext(ctx context.Context, endpoint string, jsonData []byte, data any) error {
	return c.requestJSON(ctx, http.MethodPost, endpoint, jsonData, data)
}

// Put issues a HTTP PUT request and returns the raw data.
func (c *Connection) Put(endpoint string, jsonData []byte) ([]byte, error) {
	return c.PutContext(context.Background(), endpoint, jsonData)
}

// PutContext is like Put but uses the given context.
func (c *Connection) PutContext(ctx context.Context, endpoint string, jsonData []byte) ([]byte, error) {
	return c.request(ctx, http.MethodPut, endpoint, jsonData)
}

// PutJSON issues a HTTP PUT request, parses the resulting data as JSON and returns the parse results.
func (c *Connection) PutJSON(endpoint string, jsonData []byte, data any) error {
	return c.PutJSONContext(context.Background(), endpoint, jsonData, data)
}

// PutJSONContext is like PutJSON but uses the given context.
func (c *Connection) PutJSONContext(ctx context.Context, endpoint string, jsonData []byte, data any) error {
	return c.requestJSON(ctx, http.MethodPut, endpoint, jsonData, data)
}

// Trace issues a HTTP TRACE request and returns the raw data.
func (c *Connection) Trace(endpoint string) ([]byte, error) {
	return c.TraceContext(context.Background(), endpoint)
}

// TraceContext is like Trace but uses the given context.
func (c *Connection) TraceContext(ctx context.Context, endpoint string) ([]byte, error) {
	return c.request(ctx, http.MethodTrace, endpoint, nil)
}

// TraceJSON issues a HTTP TRACE request, parses the resulting data as JSON and returns the parse results.
func (c *Connection) TraceJSON(endpoint string, data any) error {
	return c.TraceJSONContext(context.Background(), endpoint, data)
}

// TraceJSONContext is like TraceJSON but uses the given context.
func (c *Connection) TraceJSONContext(ctx context.Context, endpoint string, data any) error {
	return c.requestJSON(ctx, http.MethodTrace, endpoint, nil, data)
}
