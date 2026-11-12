package lra

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// echo is what the echo handler sends back.
type echo struct {
	Method     string      `json:"method"`
	Protocol   string      `json:"protocol"`
	Path       string      `json:"path"`
	Header     http.Header `json:"header"`
	StringData string      `json:"stringdata"`
	IntData    int         `json:"intdata"`
	BoolData   bool        `json:"booldata"`
	Error      string      `json:"error"`
}

// payload is the request data.
type payload struct {
	StringData string `json:"stringdata"`
	IntData    int    `json:"intdata"`
	BoolData   bool   `json:"booldata"`
}

const (
	epURL  = "/test?stringdata=hello&intdata=42&booldata=true"
	ep404  = "/error/404?stringdata=hello&intdata=42&booldata=true"
	epJSON = "/error/json?stringdata=hello&intdata=42&booldata=true"
	epNone = "/error/empty"
	epIn   = "/test"

	testTimeout = 10 * time.Second
)

var (
	inData    = []byte(`{"stringdata":"hello","intdata":42,"booldata":true}`)
	inPayload = payload{StringData: "hello", IntData: 42, BoolData: true}
)

// echoHandler returns the request data as JSON. The data is taken from the
// query string if there is one, otherwise from the JSON request body.
// The paths /error/404, /error/json and /error/empty return a 404, invalid
// JSON and an empty body (204).
func echoHandler(w http.ResponseWriter, r *http.Request) {
	ret := echo{
		Method:   r.Method,
		Protocol: "http",
		Path:     "/" + r.PathValue("path"),
		Header:   r.Header,
	}
	if r.TLS != nil {
		ret.Protocol = "https"
	}
	if ret.Path == "/error/empty" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	status := http.StatusOK
	if r.URL.RawQuery != "" {
		q := r.URL.Query()
		var intErr, boolErr error
		ret.StringData = q.Get("stringdata")
		ret.IntData, intErr = strconv.Atoi(q.Get("intdata"))
		ret.BoolData, boolErr = strconv.ParseBool(q.Get("booldata"))
		if err := errors.Join(intErr, boolErr); err != nil {
			status = http.StatusBadRequest
			ret.Error = err.Error()
		}
	} else {
		var in payload
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			status = http.StatusBadRequest
			ret.Error = err.Error()
		}
		ret.StringData, ret.IntData, ret.BoolData = in.StringData, in.IntData, in.BoolData
	}

	body, err := json.Marshal(ret)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch ret.Path {
	case "/error/404":
		status = http.StatusNotFound
	case "/error/json":
		body = []byte("{{")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func echoMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/base/{path...}", echoHandler)
	return mux
}

type testServer struct {
	name     string
	protocol string
	server   *httptest.Server
}

// startServers starts a http and a https server with the given handler.
func startServers(t *testing.T, h http.Handler) []testServer {
	t.Helper()
	httpServer := httptest.NewServer(h)
	t.Cleanup(httpServer.Close)
	httpsServer := httptest.NewTLSServer(h)
	t.Cleanup(httpsServer.Close)
	return []testServer{
		{"http", "http", httpServer},
		{"https", "https", httpsServer},
	}
}

// newTestClient creates a client for ts with the default test settings.
func newTestClient(t *testing.T, ts testServer, opts ...Option) *Client {
	t.Helper()
	base := []Option{
		WithHeader("test-header", "test"),
		WithTimeout(testTimeout),
	}
	// Trust the certificate of the https test server.
	if cfg := ts.server.Client().Transport.(*http.Transport).TLSClientConfig; cfg != nil {
		base = append(base, WithTLSConfig(cfg))
	}
	opts = append(base, opts...)
	c, err := New(ts.server.URL+"/base", opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.HTTPClient().CloseIdleConnections)
	return c
}

// checkEcho verifies the echoed request data.
func checkEcho(t *testing.T, protocol, method string, got echo, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := echo{
		Method:     method,
		Protocol:   protocol,
		Path:       "/test",
		StringData: "hello",
		IntData:    42,
		BoolData:   true,
	}
	if got.Header.Get("Test-Header") != "test" {
		t.Errorf("Test-Header = %q, want %q", got.Header.Get("Test-Header"), "test")
	}
	got.Header = nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// checkRaw decodes raw and verifies the echoed request data.
func checkRaw(t *testing.T, protocol, method string, raw []byte, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got echo
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("response %q is not valid JSON: %v", raw, err)
	}
	checkEcho(t, protocol, method, got, nil)
}

func check404(t *testing.T, err error) *StatusError {
	t.Helper()
	se, ok := errors.AsType[*StatusError](err)
	if !ok {
		t.Fatalf("expected *StatusError, got %T: %v", err, err)
	}
	if se.StatusCode != http.StatusNotFound || se.Status != "404 Not Found" {
		t.Errorf("StatusCode/Status = %d/%q, want 404/%q", se.StatusCode, se.Status, "404 Not Found")
	}
	if strings.Contains(se.Error(), "?") || strings.Contains(se.URL, "stringdata") {
		t.Errorf("StatusError leaks the query string: %q", se.Error())
	}
	return se
}

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		opts     []Option
		wantBase string
	}{
		{"plain", "https://example.com", nil, "https://example.com"},
		{"port and path", "http://example.com:8080/api/v1", nil, "http://example.com:8080/api/v1"},
		{"trailing slash", "http://example.com/api/", nil, "http://example.com/api"},
		{"credentials removed", "https://admin:secret@example.com/x", nil, "https://example.com/x"},
		{"ipv6", "http://[::1]:9200", nil, "http://[::1]:9200"},
		{"all options", "https://example.com", []Option{
			WithBasicAuth("u", "p"),
			WithHeader("a", "b"),
			WithTimeout(0),
			WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS13}),
			WithInsecureSkipVerify(),
			WithProxy("socks5h://user:pw@proxy.example.com:1080"),
			WithMaxResponseSize(1024),
		}, "https://example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.baseURL, tt.opts...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := c.BaseURL(); got != tt.wantBase {
				t.Errorf("BaseURL() = %q, want %q", got, tt.wantBase)
			}
		})
	}
}

func TestNew_Defaults(t *testing.T) {
	c, err := New("https://example.com")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.HTTPClient().Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", c.HTTPClient().Timeout, DefaultTimeout)
	}
	tr := c.HTTPClient().Transport.(*http.Transport)
	if tr.Proxy == nil {
		t.Error("expected proxy from environment, got nil")
	}
	if tr.TLSClientConfig != nil {
		t.Error("expected default TLS config")
	}

	c, err = New("https://example.com", WithoutProxy(), WithInsecureSkipVerify())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr = c.HTTPClient().Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Error("WithoutProxy: expected no proxy")
	}
	if tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("WithInsecureSkipVerify: expected InsecureSkipVerify")
	}
}

func TestNew_Errors(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		opts    []Option
	}{
		{"invalid url", "http://%zz", nil},
		{"unsupported scheme", "ftp://example.com", nil},
		{"no scheme", "example.com:8080", nil},
		{"no host", "http:///path", nil},
		{"query", "http://example.com/?x=1", nil},
		{"fragment", "http://example.com/#x", nil},
		{"negative timeout", "http://example.com", []Option{WithTimeout(-time.Second)}},
		{"nil tls config", "http://example.com", []Option{WithTLSConfig(nil)}},
		{"invalid proxy url", "http://example.com", []Option{WithProxy("http://%zz")}},
		{"proxy without scheme", "http://example.com", []Option{WithProxy("proxy.example.com:3128")}},
		{"unsupported proxy scheme", "http://example.com", []Option{WithProxy("ftp://proxy.example.com")}},
		{"proxy without host", "http://example.com", []Option{WithProxy("socks5://")}},
		{"nil http client", "http://example.com", []Option{WithHTTPClient(nil)}},
		{"http client and timeout", "http://example.com", []Option{WithHTTPClient(&http.Client{}), WithTimeout(time.Second)}},
		{"http client and proxy", "http://example.com", []Option{WithHTTPClient(&http.Client{}), WithoutProxy()}},
		{"negative max response size", "http://example.com", []Option{WithMaxResponseSize(-1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.baseURL, tt.opts...); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestRawMethods(t *testing.T) {
	tests := []struct {
		method string
		call   func(*Client) ([]byte, error)
	}{
		{http.MethodDelete, func(c *Client) ([]byte, error) { return c.Delete(t.Context(), epURL, inData) }},
		{http.MethodGet, func(c *Client) ([]byte, error) { return c.Get(t.Context(), epURL) }},
		{http.MethodOptions, func(c *Client) ([]byte, error) { return c.Options(t.Context(), epURL) }},
		{http.MethodPatch, func(c *Client) ([]byte, error) { return c.Patch(t.Context(), epIn, inData) }},
		{http.MethodPost, func(c *Client) ([]byte, error) { return c.Post(t.Context(), epIn, inData) }},
		{http.MethodPut, func(c *Client) ([]byte, error) { return c.Put(t.Context(), epIn, inData) }},
	}
	for _, ts := range startServers(t, echoMux()) {
		c := newTestClient(t, ts)
		for _, tt := range tests {
			t.Run(ts.name+"/"+tt.method, func(t *testing.T) {
				raw, err := tt.call(c)
				checkRaw(t, ts.protocol, tt.method, raw, err)
			})
		}
	}
}

func TestDo(t *testing.T) {
	for _, ts := range startServers(t, echoMux()) {
		t.Run(ts.name, func(t *testing.T) {
			c := newTestClient(t, ts)
			res, err := c.Do(t.Context(), http.MethodTrace, epURL, nil)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/json" {
				t.Errorf("StatusCode/Content-Type = %d/%q", res.StatusCode, res.Header.Get("Content-Type"))
			}
			checkRaw(t, ts.protocol, http.MethodTrace, res.Body, nil)

			res, err = c.Do(t.Context(), http.MethodGet, ep404, nil)
			se := check404(t, err)
			if res == nil || string(res.Body) != string(se.Body) || len(se.Body) == 0 {
				t.Errorf("expected response body in both Response and StatusError")
			}
			if se.Method != http.MethodGet || se.URL != ts.server.URL+"/base/error/404" {
				t.Errorf("StatusError Method/URL = %q/%q", se.Method, se.URL)
			}
			if se.Header.Get("Content-Type") != "application/json" {
				t.Errorf("StatusError.Header not set")
			}
			if want := "lra: GET " + se.URL + ": 404 Not Found"; se.Error() != want {
				t.Errorf("Error() = %q, want %q", se.Error(), want)
			}
		})
	}
}

func TestHead(t *testing.T) {
	for _, ts := range startServers(t, echoMux()) {
		t.Run(ts.name, func(t *testing.T) {
			c := newTestClient(t, ts)
			h, err := c.Head(t.Context(), epURL)
			if err != nil {
				t.Fatalf("Head: %v", err)
			}
			if ct := h.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}
			if cl, _ := strconv.Atoi(h.Get("Content-Length")); cl <= 100 {
				t.Errorf("Content-Length = %q, want > 100", h.Get("Content-Length"))
			}
			_, err = c.Head(t.Context(), ep404)
			check404(t, err)
		})
	}
}

func TestJSONFunctions(t *testing.T) {
	tests := []struct {
		method string
		ep     string
		call   func(c *Client, ep string) (echo, error)
	}{
		{http.MethodDelete, epURL, func(c *Client, ep string) (echo, error) { return DeleteJSON[echo](t.Context(), c, ep, nil) }},
		{http.MethodGet, epURL, func(c *Client, ep string) (echo, error) { return GetJSON[echo](t.Context(), c, ep) }},
		{http.MethodPatch, epIn, func(c *Client, ep string) (echo, error) { return PatchJSON[echo](t.Context(), c, ep, inPayload) }},
		{http.MethodPost, epIn, func(c *Client, ep string) (echo, error) { return PostJSON[echo](t.Context(), c, ep, inPayload) }},
		{http.MethodPut, epIn, func(c *Client, ep string) (echo, error) { return PutJSON[echo](t.Context(), c, ep, inPayload) }},
		{http.MethodOptions, epURL, func(c *Client, ep string) (echo, error) {
			return DoJSON[echo](t.Context(), c, http.MethodOptions, ep, nil)
		}},
	}
	for _, ts := range startServers(t, echoMux()) {
		c := newTestClient(t, ts)
		for _, tt := range tests {
			t.Run(ts.name+"/"+tt.method, func(t *testing.T) {
				got, err := tt.call(c, tt.ep)
				if err == nil && got.Header.Get("Accept") != "application/json" {
					t.Errorf("Accept = %q, want application/json", got.Header.Get("Accept"))
				}
				checkEcho(t, ts.protocol, tt.method, got, err)

				got, err = tt.call(c, ep404)
				check404(t, err)
				if !reflect.DeepEqual(got, echo{}) {
					t.Errorf("expected zero value on error, got %+v", got)
				}

				got, err = tt.call(c, epJSON)
				if _, ok := errors.AsType[*json.SyntaxError](err); !ok {
					t.Errorf("expected *json.SyntaxError, got %T: %v", err, err)
				}
				if err != nil && strings.Contains(err.Error(), "?") {
					t.Errorf("decode error leaks the query string: %q", err.Error())
				}
				if !reflect.DeepEqual(got, echo{}) {
					t.Errorf("expected zero value on error, got %+v", got)
				}

				got, err = tt.call(c, epNone)
				if err != nil || !reflect.DeepEqual(got, echo{}) {
					t.Errorf("empty body: got %+v, %v, want zero value and nil", got, err)
				}
			})
		}
	}
}

func TestDoJSON_Body(t *testing.T) {
	ts := startServers(t, echoMux())[0]
	c := newTestClient(t, ts)
	tests := []struct {
		name string
		in   any
	}{
		{"struct", inPayload},
		{"pointer", &inPayload},
		{"map", map[string]any{"stringdata": "hello", "intdata": 42, "booldata": true}},
		{"bytes", inData},
		{"raw message", json.RawMessage(inData)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PostJSON[echo](t.Context(), c, epIn, tt.in)
			if err == nil && got.Header.Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got.Header.Get("Content-Type"))
			}
			checkEcho(t, ts.protocol, http.MethodPost, got, err)
		})
	}

	t.Run("unencodable", func(t *testing.T) {
		if _, err := PostJSON[echo](t.Context(), c, epIn, make(chan int)); err == nil {
			t.Error("expected encoding error, got nil")
		}
	})

	t.Run("configured content type wins", func(t *testing.T) {
		c := newTestClient(t, ts, WithHeader("Content-Type", "application/vnd.test+json"))
		got, err := PostJSON[echo](t.Context(), c, epIn, inPayload)
		if err != nil {
			t.Fatalf("PostJSON: %v", err)
		}
		if ct := got.Header.Get("Content-Type"); ct != "application/vnd.test+json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/vnd.test+json")
		}
	})
}

func TestHeaders(t *testing.T) {
	ts := startServers(t, echoMux())[0]
	c := newTestClient(t, ts,
		WithHeader("X-Multi", "a"),
		WithHeader("x-multi", "b"),
		WithHeader("Host", "api.example.com"),
	)
	got, err := GetJSON[echo](t.Context(), c, epURL)
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if v := got.Header.Values("X-Multi"); !reflect.DeepEqual(v, []string{"a", "b"}) {
		t.Errorf("X-Multi = %q, want [a b]", v)
	}

	req, err := c.NewRequest(t.Context(), http.MethodGet, "test", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if req.Host != "api.example.com" {
		t.Errorf("Host = %q, want %q", req.Host, "api.example.com")
	}
	if want := ts.server.URL + "/base/test"; req.URL.String() != want {
		t.Errorf("URL = %q, want %q", req.URL, want)
	}
	// Modifying the request must not modify the client.
	req.Header.Add("X-Multi", "c")
	req2, _ := c.NewRequest(t.Context(), http.MethodGet, "", nil)
	if v := req2.Header.Values("X-Multi"); len(v) != 2 {
		t.Errorf("client headers were modified through a request: %q", v)
	}
}

func TestBasicAuth(t *testing.T) {
	type auth struct {
		user, password string
		ok             bool
	}
	got := make(chan auth, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		got <- auth{u, p, ok}
	}))
	t.Cleanup(s.Close)
	host := strings.TrimPrefix(s.URL, "http://")

	tests := []struct {
		name    string
		baseURL string
		opts    []Option
		want    auth
	}{
		{"none", s.URL, nil, auth{}},
		{"option", s.URL, []Option{WithBasicAuth("us@er", "p@ss:w/o#rd?%")}, auth{"us@er", "p@ss:w/o#rd?%", true}},
		{"url", "http://admin:p%40ss@" + host, nil, auth{"admin", "p@ss", true}},
		{"option overrides url", "http://admin:p%40ss@" + host, []Option{WithBasicAuth("other", "pw")}, auth{"other", "pw", true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.baseURL, tt.opts...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(c.HTTPClient().CloseIdleConnections)
			if strings.Contains(c.BaseURL(), "@") {
				t.Errorf("BaseURL() contains credentials: %q", c.BaseURL())
			}
			if _, err := c.Get(t.Context(), "/"); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if a := <-got; a != tt.want {
				t.Errorf("basic auth = %+v, want %+v", a, tt.want)
			}
		})
	}
}

func TestTLSVerification(t *testing.T) {
	ts := startServers(t, echoMux())[1]

	c, err := New(ts.server.URL + "/base")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Get(t.Context(), epURL); err == nil {
		t.Error("expected certificate error with default TLS config, got nil")
	}

	c, err = New(ts.server.URL+"/base", WithInsecureSkipVerify())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.HTTPClient().CloseIdleConnections)
	if _, err := c.Get(t.Context(), epURL); err != nil {
		t.Errorf("WithInsecureSkipVerify: %v", err)
	}
	// TLS config with the test CA is covered by all https tests via newTestClient.
}

func TestWithHTTPClient(t *testing.T) {
	ts := startServers(t, echoMux())[0]
	var calls atomic.Int32
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	})}
	c, err := New(ts.server.URL+"/base", WithHTTPClient(hc), WithHeader("test-header", "test"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.HTTPClient() != hc {
		t.Error("HTTPClient() does not return the configured client")
	}
	got, err := GetJSON[echo](t.Context(), c, epURL)
	checkEcho(t, ts.protocol, http.MethodGet, got, err)
	if calls.Load() != 1 {
		t.Errorf("custom client was called %d times, want 1", calls.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestIPv6(t *testing.T) {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback not available: %v", err)
	}
	s := httptest.NewUnstartedServer(echoMux())
	_ = s.Listener.Close()
	s.Listener = ln
	s.Start()
	t.Cleanup(s.Close)

	c := newTestClient(t, testServer{"ipv6", "http", s})
	got, err := GetJSON[echo](t.Context(), c, epURL)
	checkEcho(t, "http", http.MethodGet, got, err)
}

func TestMaxResponseSize(t *testing.T) {
	ts := startServers(t, echoMux())[0]
	c := newTestClient(t, ts, WithMaxResponseSize(10))
	if _, err := c.Get(t.Context(), epURL); !errors.Is(err, ErrResponseTooLarge) {
		t.Errorf("expected ErrResponseTooLarge, got %v", err)
	}
	c = newTestClient(t, ts, WithMaxResponseSize(10_000))
	raw, err := c.Get(t.Context(), epURL)
	checkRaw(t, ts.protocol, http.MethodGet, raw, err)
}

// blockingServer returns a server whose handler blocks until the request is
// cancelled or the test ends.
func blockingServer(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(s.Close)
	t.Cleanup(func() { close(release) }) // runs before s.Close
	return s.URL
}

func TestTimeout(t *testing.T) {
	c, err := New(blockingServer(t), WithTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Get(t.Context(), "/")
	if ne, ok := errors.AsType[net.Error](err); !ok || !ne.Timeout() {
		t.Errorf("expected timeout error, got %v", err)
	}
}

func TestContext(t *testing.T) {
	c, err := New(blockingServer(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.HTTPClient().CloseIdleConnections)

	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		if _, err := GetJSON[echo](ctx, c, "/"); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected context.DeadlineExceeded, got %v", err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := c.Post(ctx, "/", inData); !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

func TestRequestErrors(t *testing.T) {
	c, err := New("http://localhost")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Get(t.Context(), "/\x7f"); err == nil {
		t.Error("invalid endpoint: expected error, got nil")
	}
	if _, err := c.Head(t.Context(), "/\x7f"); err == nil {
		t.Error("invalid endpoint: expected error, got nil")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	c, err = New("http://"+addr, WithoutProxy())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := GetJSON[echo](t.Context(), c, "/"); err == nil {
		t.Error("connection refused: expected error, got nil")
	}
}

func TestHTTPProxy(t *testing.T) {
	got := make(chan string, 1)
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"method":"` + r.Method + `"}`))
	}))
	t.Cleanup(p.Close)

	c, err := New("http://target.example.invalid:8080/base", WithProxy(p.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.HTTPClient().CloseIdleConnections)
	data, err := GetJSON[echo](t.Context(), c, "/test")
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if want := "http://target.example.invalid:8080/base/test"; <-got != want {
		t.Errorf("proxy did not receive request for %q", want)
	}
	if data.Method != http.MethodGet {
		t.Errorf("Method = %q, want %q", data.Method, http.MethodGet)
	}
}

// startSOCKS5 starts a minimal SOCKS5 server (no authentication, CONNECT
// only) and returns its address and a counter of accepted connections.
func startSOCKS5(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var count atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			count.Add(1)
			go serveSOCKS5(conn)
		}
	}()
	return ln.Addr().String(), &count
}

func serveSOCKS5(c net.Conn) {
	defer c.Close()
	buf := make([]byte, 256)
	// Greeting: VER NMETHODS METHODS...
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	if _, err := io.ReadFull(c, buf[:buf[1]]); err != nil {
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return
	}
	// Request: VER CMD RSV ATYP DST.ADDR DST.PORT
	if _, err := io.ReadFull(c, buf[:4]); err != nil {
		return
	}
	var host string
	switch buf[3] {
	case 1:
		if _, err := io.ReadFull(c, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 3:
		if _, err := io.ReadFull(c, buf[:1]); err != nil {
			return
		}
		n := int(buf[0])
		if _, err := io.ReadFull(c, buf[:n]); err != nil {
			return
		}
		host = string(buf[:n])
	case 4:
		if _, err := io.ReadFull(c, buf[:16]); err != nil {
			return
		}
		host = net.IP(buf[:16]).String()
	default:
		return
	}
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	port := int(buf[0])<<8 | int(buf[1])

	target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		_, _ = c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() { _, _ = io.Copy(target, c) }()
	_, _ = io.Copy(c, target)
}

func TestSOCKS5Proxy(t *testing.T) {
	socksAddr, count := startSOCKS5(t)
	for _, ts := range startServers(t, echoMux()) {
		for _, scheme := range []string{"socks5", "socks5h"} {
			t.Run(ts.name+"/"+scheme, func(t *testing.T) {
				before := count.Load()
				c := newTestClient(t, ts, WithProxy(scheme+"://"+socksAddr))
				got, err := GetJSON[echo](t.Context(), c, epURL)
				checkEcho(t, ts.protocol, http.MethodGet, got, err)
				if count.Load() == before {
					t.Error("request did not go through the SOCKS5 proxy")
				}
			})
		}
	}
}
