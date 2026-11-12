package lra

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Client sends requests to a REST API below a base URL. Create it with [New].
type Client struct {
	base            *url.URL
	header          http.Header
	user, password  string
	basicAuth       bool
	hc              *http.Client
	maxResponseSize int64
}

// Response is the result of [Client.Do].
type Response struct {
	// StatusCode is the numeric HTTP status code, e.g. 200.
	StatusCode int
	// Status is the status line, e.g. "200 OK".
	Status string
	// Header contains the response headers.
	Header http.Header
	// Body is the complete response body.
	Body []byte
}

// New creates a Client for the API at baseURL, e.g.
// "https://elasticsearch.example.com:9200" or "http://[::1]:8080/api/v1".
// The scheme must be http or https. Credentials in the URL
// ("https://user:password@host") are used for basic authentication.
func New(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("lra: invalid base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("lra: unsupported base URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("lra: base URL %q has no host", u.Redacted())
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("lra: base URL %q must not contain a query or fragment", u.Redacted())
	}

	o := options{header: make(http.Header)}
	if u.User != nil {
		o.user = u.User.Username()
		o.password, _ = u.User.Password()
		o.basicAuth = true
		u.User = nil
	}
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return nil, err
		}
	}

	hc := o.httpClient
	if hc != nil {
		if o.timeoutSet || o.tlsConfig != nil || o.insecure || o.proxySet {
			return nil, errors.New("lra: WithHTTPClient can't be combined with timeout, TLS or proxy options")
		}
	} else {
		hc = &http.Client{
			Transport: newTransport(&o),
			Timeout:   DefaultTimeout,
		}
		if o.timeoutSet {
			hc.Timeout = o.timeout
		}
	}

	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawPath = strings.TrimSuffix(u.RawPath, "/")

	return &Client{
		base:            u,
		header:          o.header,
		user:            o.user,
		password:        o.password,
		basicAuth:       o.basicAuth,
		hc:              hc,
		maxResponseSize: o.maxResponseSize,
	}, nil
}

// newTransport creates a transport with the defaults of http.DefaultTransport,
// a larger connection pool and the configured TLS and proxy settings.
func newTransport(o *options) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if o.proxySet {
		tr.Proxy = nil
		if o.proxyURL != nil {
			tr.Proxy = http.ProxyURL(o.proxyURL)
		}
	}
	if o.tlsConfig != nil || o.insecure {
		cfg := o.tlsConfig
		if cfg == nil {
			cfg = &tls.Config{}
		}
		if o.insecure {
			cfg.InsecureSkipVerify = true //nolint:gosec // explicitly requested
		}
		tr.TLSClientConfig = cfg
	}
	return tr
}

// BaseURL returns the base URL without credentials.
func (c *Client) BaseURL() string {
	return c.base.String()
}

// HTTPClient returns the underlying http.Client, e.g. to send requests created
// with [Client.NewRequest].
func (c *Client) HTTPClient() *http.Client {
	return c.hc
}

// NewRequest creates a request for endpoint (which is appended to the base URL)
// with the configured headers and credentials. Use it together with
// [Client.HTTPClient] for streaming or per-request headers.
func (c *Client) NewRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	if endpoint != "" && endpoint[0] != '/' && endpoint[0] != '?' {
		endpoint = "/" + endpoint
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+endpoint, body)
	if err != nil {
		return nil, err
	}
	for k, v := range c.header {
		req.Header[k] = slices.Clone(v)
	}
	if host := c.header.Get("Host"); host != "" {
		req.Host = host
	}
	if c.basicAuth {
		req.SetBasicAuth(c.user, c.password)
	}
	return req, nil
}

// Do sends a request and returns the response. For status codes of 400 and
// above, both the response and a [*StatusError] are returned.
func (c *Client) Do(ctx context.Context, method, endpoint string, body []byte) (*Response, error) {
	return c.do(ctx, method, endpoint, body, nil)
}

// do sends a request. defaultHeader is only applied for headers not
// configured for the client.
func (c *Client) do(ctx context.Context, method, endpoint string, body []byte, defaultHeader http.Header) (*Response, error) {
	var r io.Reader
	if len(body) > 0 {
		r = bytes.NewReader(body)
	}
	req, err := c.NewRequest(ctx, method, endpoint, r)
	if err != nil {
		return nil, err
	}
	for k, v := range defaultHeader {
		if _, ok := req.Header[k]; !ok {
			req.Header[k] = v
		}
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := c.readBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lra: reading response of %s %s: %w", method, redact(req.URL), err)
	}
	res := &Response{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Header:     resp.Header,
		Body:       data,
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return res, &StatusError{
			Method:     method,
			URL:        redact(req.URL),
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Header:     resp.Header,
			Body:       data,
		}
	}
	return res, nil
}

func (c *Client) readBody(r io.Reader) ([]byte, error) {
	if c.maxResponseSize == 0 {
		return io.ReadAll(r)
	}
	data, err := io.ReadAll(io.LimitReader(r, c.maxResponseSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > c.maxResponseSize {
		return nil, fmt.Errorf("%w (limit %d bytes)", ErrResponseTooLarge, c.maxResponseSize)
	}
	return data, nil
}

// redact returns u without credentials, query and fragment.
func redact(u *url.URL) string {
	r := *u
	r.User = nil
	r.RawQuery = ""
	r.ForceQuery = false
	r.Fragment = ""
	r.RawFragment = ""
	return r.String()
}

func (c *Client) body(ctx context.Context, method, endpoint string, body []byte) ([]byte, error) {
	res, err := c.Do(ctx, method, endpoint, body)
	if res == nil {
		return nil, err
	}
	return res.Body, err
}

// Get sends a GET request and returns the response body.
func (c *Client) Get(ctx context.Context, endpoint string) ([]byte, error) {
	return c.body(ctx, http.MethodGet, endpoint, nil)
}

// Head sends a HEAD request and returns the response headers.
func (c *Client) Head(ctx context.Context, endpoint string) (http.Header, error) {
	res, err := c.Do(ctx, http.MethodHead, endpoint, nil)
	if res == nil {
		return nil, err
	}
	return res.Header, err
}

// Options sends an OPTIONS request and returns the response body.
func (c *Client) Options(ctx context.Context, endpoint string) ([]byte, error) {
	return c.body(ctx, http.MethodOptions, endpoint, nil)
}

// Delete sends a DELETE request with an optional body and returns the response body.
func (c *Client) Delete(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	return c.body(ctx, http.MethodDelete, endpoint, body)
}

// Patch sends a PATCH (RFC 5789) request and returns the response body.
func (c *Client) Patch(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	return c.body(ctx, http.MethodPatch, endpoint, body)
}

// Post sends a POST request and returns the response body.
func (c *Client) Post(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	return c.body(ctx, http.MethodPost, endpoint, body)
}

// Put sends a PUT request and returns the response body.
func (c *Client) Put(ctx context.Context, endpoint string, body []byte) ([]byte, error) {
	return c.body(ctx, http.MethodPut, endpoint, body)
}
