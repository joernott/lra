package lra

import (
	"context"
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

// returnData is what the echo handler sends back.
type returnData struct {
	Method     string      `json:"method"`
	Protocol   string      `json:"protocol"`
	Path       string      `json:"path"`
	Header     http.Header `json:"header"`
	StringData string      `json:"stringdata"`
	IntData    int         `json:"intdata"`
	BoolData   bool        `json:"booldata"`
	Error      string      `json:"error"`
}

const (
	epURL  = "/test?stringdata=hello&intdata=42&booldata=true"
	ep404  = "/error/404?stringdata=hello&intdata=42&booldata=true"
	epJSON = "/error/json?stringdata=hello&intdata=42&booldata=true"
	epIn   = "/test"

	testTimeout = 10 * time.Second
)

var inData = []byte(`{"stringdata":"hello","intdata":42,"booldata":true}`)

func testHeaders() HeaderList {
	return HeaderList{"test-header": "test"}
}

// echoHandler returns the request data as JSON. The data is taken from the
// query string if there is one, otherwise from the JSON request body.
// The paths /error/404 and /error/json return a 404 and invalid JSON.
func echoHandler(w http.ResponseWriter, r *http.Request) {
	ret := returnData{
		Method:   r.Method,
		Protocol: "http",
		Path:     "/" + r.PathValue("path"),
		Header:   r.Header,
	}
	if r.TLS != nil {
		ret.Protocol = "https"
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
		var in returnData
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
	host     string
	port     int
	ssl      bool
	protocol string
	url      string
}

// startServers starts a http and a https server with the given handler.
func startServers(t *testing.T, h http.Handler) []testServer {
	t.Helper()
	var servers []testServer
	for _, ssl := range []bool{false, true} {
		var s *httptest.Server
		if ssl {
			s = httptest.NewTLSServer(h)
		} else {
			s = httptest.NewServer(h)
		}
		t.Cleanup(s.Close)
		addr := s.Listener.Addr().(*net.TCPAddr)
		ts := testServer{
			name:     "http",
			host:     addr.IP.String(),
			port:     addr.Port,
			ssl:      ssl,
			protocol: "http",
			url:      s.URL,
		}
		if ssl {
			ts.name, ts.protocol = "https", "https"
		}
		servers = append(servers, ts)
	}
	return servers
}

// newTestConnection creates a connection to ts with the default test settings.
func newTestConnection(t *testing.T, ts testServer) *Connection {
	t.Helper()
	c, err := NewConnection(ts.ssl, ts.host, ts.port, "/base", "", "", false, "", false, testHeaders(), testTimeout)
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	t.Cleanup(c.Client.CloseIdleConnections)
	return c
}

// checkData verifies the echoed request data.
func checkData(t *testing.T, protocol, method string, got returnData, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := returnData{
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
	var got returnData
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("response %q is not valid JSON: %v", raw, err)
	}
	checkData(t, protocol, method, got, nil)
}

func check404(t *testing.T, err error) {
	t.Helper()
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StatusError, got %T: %v", err, err)
	}
	if se.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", se.StatusCode, http.StatusNotFound)
	}
	if se.Error() != "404 Not Found" {
		t.Errorf("Error() = %q, want %q", se.Error(), "404 Not Found")
	}
}

func checkJSONError(t *testing.T, err error) {
	t.Helper()
	if _, ok := errors.AsType[*json.SyntaxError](err); !ok {
		t.Errorf("expected *json.SyntaxError, got %T: %v", err, err)
	}
}

func TestNewConnection(t *testing.T) {
	for _, ts := range startServers(t, echoMux()) {
		t.Run(ts.name, func(t *testing.T) {
			c := newTestConnection(t, ts)
			if c.Protocol != ts.protocol {
				t.Errorf("Protocol = %q, want %q", c.Protocol, ts.protocol)
			}
			if c.Server != ts.host {
				t.Errorf("Server = %q, want %q", c.Server, ts.host)
			}
			if c.Port != ts.port {
				t.Errorf("Port = %d, want %d", c.Port, ts.port)
			}
			if c.BaseEndpoint != "/base" {
				t.Errorf("BaseEndpoint = %q, want %q", c.BaseEndpoint, "/base")
			}
			if c.User != "" || c.Password != "" {
				t.Errorf("User/Password = %q/%q, want empty", c.User, c.Password)
			}
			if c.ValidateSSL {
				t.Error("ValidateSSL = true, want false")
			}
			if c.Proxy != "" || c.ProxyIsSocks {
				t.Errorf("Proxy/ProxyIsSocks = %q/%v, want empty/false", c.Proxy, c.ProxyIsSocks)
			}
			if c.SendHeaders["test-header"] != "test" {
				t.Errorf("SendHeaders[test-header] = %q, want %q", c.SendHeaders["test-header"], "test")
			}
			if want := ts.url + "/base"; c.BaseURL != want {
				t.Errorf("BaseURL = %q, want %q", c.BaseURL, want)
			}
			if c.Timeout != testTimeout || c.Client.Timeout != testTimeout {
				t.Errorf("Timeout = %v/%v, want %v", c.Timeout, c.Client.Timeout, testTimeout)
			}
		})
	}
}

func TestNewConnection_User(t *testing.T) {
	const user, password = "admin", "123456"
	for _, ts := range startServers(t, echoMux()) {
		t.Run(ts.name, func(t *testing.T) {
			c, err := NewConnection(ts.ssl, ts.host, ts.port, "/base", user, password, false, "", false, testHeaders(), testTimeout)
			if err != nil {
				t.Fatalf("NewConnection: %v", err)
			}
			if c.User != user || c.Password != password {
				t.Errorf("User/Password = %q/%q, want %q/%q", c.User, c.Password, user, password)
			}
			want := ts.protocol + "://" + user + ":" + password + "@" + ts.host + ":" + strconv.Itoa(ts.port) + "/base"
			if c.BaseURL != want {
				t.Errorf("BaseURL = %q, want %q", c.BaseURL, want)
			}
		})
	}
}

func TestNewConnection_DefaultTimeout(t *testing.T) {
	c, err := NewConnection(false, "localhost", 80, "", "", "", true, "", false, nil, 0)
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	if c.Timeout != DefaultTimeout || c.Client.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v/%v, want %v", c.Timeout, c.Client.Timeout, DefaultTimeout)
	}
}

func TestNewConnection_Proxy(t *testing.T) {
	tests := []struct {
		name    string
		proxy   string
		socks   bool
		wantErr bool
	}{
		{"http url", "http://proxy.example.com:3128", false, false},
		{"http without scheme", "proxy.example.com:3128", false, false},
		{"http invalid url", "http://%zz", false, true},
		{"http missing host", "http://", false, true},
		{"socks host:port", "socks.example.com:8181", true, false},
		{"socks url", "socks5://socks.example.com:8181", true, false},
		{"socks url with auth", "socks5h://user:pw@socks.example.com:8181", true, false},
		{"socks missing port", "socks.example.com", true, true},
		{"socks unknown scheme", "ftp://socks.example.com:8181", true, true},
		{"socks invalid url", "socks5://%zz", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewConnection(false, "localhost", 80, "", "", "", true, tt.proxy, tt.socks, nil, testTimeout)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewConnection: %v", err)
			}
			if c.Proxy != tt.proxy || c.ProxyIsSocks != tt.socks {
				t.Errorf("Proxy/ProxyIsSocks = %q/%v, want %q/%v", c.Proxy, c.ProxyIsSocks, tt.proxy, tt.socks)
			}
		})
	}
}

func TestRawMethods(t *testing.T) {
	tests := []struct {
		method string
		call   func(*Connection) ([]byte, error)
	}{
		{http.MethodConnect, func(c *Connection) ([]byte, error) { return c.Connect(epURL) }},
		{http.MethodDelete, func(c *Connection) ([]byte, error) { return c.Delete(epURL, inData) }},
		{http.MethodGet, func(c *Connection) ([]byte, error) { return c.Get(epURL) }},
		{http.MethodOptions, func(c *Connection) ([]byte, error) { return c.Options(epURL) }},
		{http.MethodPatch, func(c *Connection) ([]byte, error) { return c.Patch(epIn, inData) }},
		{http.MethodPost, func(c *Connection) ([]byte, error) { return c.Post(epIn, inData) }},
		{http.MethodPut, func(c *Connection) ([]byte, error) { return c.Put(epIn, inData) }},
		{http.MethodTrace, func(c *Connection) ([]byte, error) { return c.Trace(epURL) }},
	}
	for _, ts := range startServers(t, echoMux()) {
		c := newTestConnection(t, ts)
		for _, tt := range tests {
			t.Run(ts.name+"/"+tt.method, func(t *testing.T) {
				raw, err := tt.call(c)
				checkRaw(t, ts.protocol, tt.method, raw, err)
			})
		}
	}
}

func TestJSONMethods(t *testing.T) {
	tests := []struct {
		method string
		ep     string
		call   func(c *Connection, ep string, data any) error
	}{
		{http.MethodConnect, epURL, func(c *Connection, ep string, d any) error { return c.ConnectJSON(ep, d) }},
		{http.MethodDelete, epURL, func(c *Connection, ep string, d any) error { return c.DeleteJSON(ep, inData, d) }},
		{http.MethodGet, epURL, func(c *Connection, ep string, d any) error { return c.GetJSON(ep, d) }},
		{http.MethodOptions, epURL, func(c *Connection, ep string, d any) error { return c.OptionsJSON(ep, d) }},
		{http.MethodPatch, epIn, func(c *Connection, ep string, d any) error { return c.PatchJSON(ep, inData, d) }},
		{http.MethodPost, epIn, func(c *Connection, ep string, d any) error { return c.PostJSON(ep, inData, d) }},
		{http.MethodPut, epIn, func(c *Connection, ep string, d any) error { return c.PutJSON(ep, inData, d) }},
		{http.MethodTrace, epURL, func(c *Connection, ep string, d any) error { return c.TraceJSON(ep, d) }},
	}
	for _, ts := range startServers(t, echoMux()) {
		c := newTestConnection(t, ts)
		for _, tt := range tests {
			t.Run(ts.name+"/"+tt.method, func(t *testing.T) {
				var data returnData
				err := tt.call(c, tt.ep, &data)
				checkData(t, ts.protocol, tt.method, data, err)

				check404(t, tt.call(c, ep404, &returnData{}))
				checkJSONError(t, tt.call(c, epJSON, &returnData{}))
			})
		}
	}
}

func checkHeadHeader(t *testing.T, h http.Header) {
	t.Helper()
	if ct := h.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	cl, err := strconv.Atoi(h.Get("Content-Length"))
	if err != nil {
		t.Fatalf("Content-Length %q is not a number: %v", h.Get("Content-Length"), err)
	}
	if cl <= 100 {
		t.Errorf("Content-Length = %d, want > 100", cl)
	}
}

func TestHead(t *testing.T) {
	for _, ts := range startServers(t, echoMux()) {
		t.Run(ts.name, func(t *testing.T) {
			c := newTestConnection(t, ts)
			raw, err := c.Head(epURL)
			if err != nil {
				t.Fatalf("Head: %v", err)
			}
			var h http.Header
			if err := json.Unmarshal(raw, &h); err != nil {
				t.Fatalf("Head result %q is not valid JSON: %v", raw, err)
			}
			checkHeadHeader(t, h)

			_, err = c.Head(ep404)
			check404(t, err)
		})
	}
}

func TestHeadJSON(t *testing.T) {
	for _, ts := range startServers(t, echoMux()) {
		t.Run(ts.name, func(t *testing.T) {
			c := newTestConnection(t, ts)
			var h http.Header
			if err := c.HeadJSON(epURL, &h); err != nil {
				t.Fatalf("HeadJSON: %v", err)
			}
			checkHeadHeader(t, h)
		})
	}
}

func TestStatusError_Body(t *testing.T) {
	ts := startServers(t, echoMux())[0]
	c := newTestConnection(t, ts)
	raw, err := c.Get(ep404)
	check404(t, err)
	var se *StatusError
	if errors.As(err, &se) && string(se.Body) != string(raw) {
		t.Errorf("StatusError.Body = %q, want %q", se.Body, raw)
	}
	if len(raw) == 0 {
		t.Error("expected response body for 404, got none")
	}
}

func TestCredentialsSpecialCharacters(t *testing.T) {
	const user, password = "us@er", "p@ss:w/o#rd?%"
	type auth struct {
		user, password string
		ok             bool
	}
	got := make(chan auth, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		got <- auth{u, p, ok}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(s.Close)
	addr := s.Listener.Addr().(*net.TCPAddr)

	c, err := NewConnection(false, addr.IP.String(), addr.Port, "", user, password, true, "", false, nil, testTimeout)
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	t.Cleanup(c.Client.CloseIdleConnections)
	if _, err := c.Get("/"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	a := <-got
	if !a.ok || a.user != user || a.password != password {
		t.Errorf("basic auth = %q/%q (ok=%v), want %q/%q", a.user, a.password, a.ok, user, password)
	}
}

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
	port := ln.Addr().(*net.TCPAddr).Port

	for _, host := range []string{"::1", "[::1]"} {
		t.Run(host, func(t *testing.T) {
			c, err := NewConnection(false, host, port, "/base", "", "", true, "", false, testHeaders(), testTimeout)
			if err != nil {
				t.Fatalf("NewConnection: %v", err)
			}
			t.Cleanup(c.Client.CloseIdleConnections)
			var data returnData
			err = c.GetJSON(epURL, &data)
			checkData(t, "http", http.MethodGet, data, err)
		})
	}
}

// blockingServer returns a server whose handler blocks until the request is
// cancelled or the test ends.
func blockingServer(t *testing.T) (host string, port int) {
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
	addr := s.Listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func TestTimeout(t *testing.T) {
	host, port := blockingServer(t)
	c, err := NewConnection(false, host, port, "", "", "", true, "", false, nil, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	_, err = c.Get("/")
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Errorf("expected timeout error, got %v", err)
	}
}

func TestContext(t *testing.T) {
	host, port := blockingServer(t)
	c, err := NewConnection(false, host, port, "", "", "", true, "", false, nil, testTimeout)
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	t.Cleanup(c.Client.CloseIdleConnections)

	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := c.GetContext(ctx, "/")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected context.DeadlineExceeded, got %v", err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		calls := map[string]func() error{
			"ConnectJSONContext": func() error { return c.ConnectJSONContext(ctx, "/", &returnData{}) },
			"DeleteJSONContext":  func() error { return c.DeleteJSONContext(ctx, "/", inData, &returnData{}) },
			"GetJSONContext":     func() error { return c.GetJSONContext(ctx, "/", &returnData{}) },
			"HeadJSONContext":    func() error { return c.HeadJSONContext(ctx, "/", &http.Header{}) },
			"OptionsJSONContext": func() error { return c.OptionsJSONContext(ctx, "/", &returnData{}) },
			"PatchJSONContext":   func() error { return c.PatchJSONContext(ctx, "/", inData, &returnData{}) },
			"PostJSONContext":    func() error { return c.PostJSONContext(ctx, "/", inData, &returnData{}) },
			"PutJSONContext":     func() error { return c.PutJSONContext(ctx, "/", inData, &returnData{}) },
			"TraceJSONContext":   func() error { return c.TraceJSONContext(ctx, "/", &returnData{}) },
		}
		for name, call := range calls {
			if err := call(); !errors.Is(err, context.Canceled) {
				t.Errorf("%s: expected context.Canceled, got %v", name, err)
			}
		}
	})
}

func TestRequestErrors(t *testing.T) {
	t.Run("invalid endpoint", func(t *testing.T) {
		c, err := NewConnection(false, "localhost", 80, "", "", "", true, "", false, nil, testTimeout)
		if err != nil {
			t.Fatalf("NewConnection: %v", err)
		}
		if _, err := c.Get("/\x7f"); err == nil {
			t.Error("expected error for invalid URL, got nil")
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		c, err := NewConnection(false, "127.0.0.1", port, "", "", "", true, "", false, nil, testTimeout)
		if err != nil {
			t.Fatalf("NewConnection: %v", err)
		}
		var data returnData
		if err := c.GetJSON("/", &data); err == nil {
			t.Error("expected connection error, got nil")
		}
	})
}

func TestHTTPProxy(t *testing.T) {
	got := make(chan string, 1)
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"method":"` + r.Method + `"}`))
	}))
	t.Cleanup(p.Close)

	for name, proxyAddr := range map[string]string{
		"url":            p.URL,
		"without scheme": strings.TrimPrefix(p.URL, "http://"),
	} {
		t.Run(name, func(t *testing.T) {
			c, err := NewConnection(false, "target.example.invalid", 8080, "/base", "", "", true, proxyAddr, false, nil, testTimeout)
			if err != nil {
				t.Fatalf("NewConnection: %v", err)
			}
			t.Cleanup(c.Client.CloseIdleConnections)
			var data returnData
			if err := c.GetJSON("/test", &data); err != nil {
				t.Fatalf("GetJSON: %v", err)
			}
			if want := "http://target.example.invalid:8080/base/test"; <-got != want {
				t.Errorf("proxy did not receive request for %q", want)
			}
			if data.Method != http.MethodGet {
				t.Errorf("Method = %q, want %q", data.Method, http.MethodGet)
			}
		})
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

func TestSocksProxy(t *testing.T) {
	socksAddr, count := startSOCKS5(t)
	for _, ts := range startServers(t, echoMux()) {
		for name, proxyAddr := range map[string]string{
			"host:port": socksAddr,
			"url":       "socks5://" + socksAddr,
		} {
			t.Run(ts.name+"/"+name, func(t *testing.T) {
				before := count.Load()
				c, err := NewConnection(ts.ssl, ts.host, ts.port, "/base", "", "", false, proxyAddr, true, testHeaders(), testTimeout)
				if err != nil {
					t.Fatalf("NewConnection: %v", err)
				}
				t.Cleanup(c.Client.CloseIdleConnections)
				var data returnData
				err = c.GetJSON(epURL, &data)
				checkData(t, ts.protocol, http.MethodGet, data, err)
				if count.Load() == before {
					t.Error("request did not go through the SOCKS5 proxy")
				}
			})
		}
	}
}
