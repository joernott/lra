[![Go Report Card](https://goreportcard.com/badge/github.com/joernott/lra/v2)](https://goreportcard.com/report/github.com/joernott/lra/v2) [![Go Reference](https://pkg.go.dev/badge/github.com/joernott/lra/v2.svg)](https://pkg.go.dev/github.com/joernott/lra/v2) [![license](https://img.shields.io/badge/license-BSD%203--clause-blue.svg)](https://github.com/joernott/lra/blob/master/LICENSE) [![codecov](https://codecov.io/github/joernott/lra/graph/badge.svg?token=dKdkGtLmP4)](https://codecov.io/github/joernott/lra)
# lra - a lowlevel REST api client

This package handles REST API calls and allows for using http and socks proxies
as well as self signed certificates. It also allows the specification of
headers to be sent with every request.

I wrote this after copying code for various tools connecting to different REST
APIs. It consolidates the low level functionality of establishing/configuring
a connection and doing the HTTP requests.

lra has no dependencies outside the Go standard library.

## License
BSD 3-clause license

## Contributions
Contributions / Pull requests are welcome.

## Documentation
[https://pkg.go.dev/github.com/joernott/lra/v2](https://pkg.go.dev/github.com/joernott/lra/v2)

## Installation
```
go get github.com/joernott/lra/v2
```

## Usage

### Create a client
```go
	c, err := lra.New("https://elasticsearch.example.com:9200",
		lra.WithBasicAuth("admin", "1234"),
		lra.WithHeader("X-Opaque-Id", "my-tool"),
		lra.WithProxy("socks5://proxy.example.com:1080"),
		lra.WithTimeout(30*time.Second),
	)
```

Available options:

| Option | Description |
| --- | --- |
| `WithBasicAuth(user, password)` | HTTP basic authentication (credentials in the base URL work, too) |
| `WithHeader(key, value)` | header sent with every request, may be repeated |
| `WithTimeout(d)` | overall request timeout, default 60s, 0 means none |
| `WithTLSConfig(cfg)` | custom TLS settings, e.g. a private CA in `RootCAs` |
| `WithInsecureSkipVerify()` | don't verify the server certificate |
| `WithProxy(url)` | `http://`, `https://`, `socks5://` or `socks5h://` proxy, default: from environment |
| `WithoutProxy()` | ignore proxy environment variables |
| `WithHTTPClient(hc)` | use your own `*http.Client` |
| `WithMaxResponseSize(n)` | limit response body size |

### Raw requests
The methods `Get`, `Head`, `Options`, `Delete`, `Patch`, `Post` and `Put`
return the response body (`Head`: the response headers). `Do` sends a request
with any method and returns status, headers and body.
```go
	raw, err := c.Get(ctx, "/_cluster/health")
	res, err := c.Do(ctx, http.MethodPost, "/_refresh", nil)
```

### JSON requests
The generic functions `GetJSON`, `DeleteJSON`, `PatchJSON`, `PostJSON`,
`PutJSON` and `DoJSON` encode the request body and decode the response:
```go
	type ClusterHealth struct {
		ClusterName string `json:"cluster_name"`
		Status      string `json:"status"`
	}
	health, err := lra.GetJSON[ClusterHealth](ctx, c, "/_cluster/health")

	result, err := lra.PostJSON[map[string]any](ctx, c, "/my-index/_doc", doc)
```
A request body of type `[]byte` or `json.RawMessage` is sent as is.

### Errors
If the server responds with a status code of 400 or above, the returned error
is a `*lra.StatusError` containing method, URL, status, headers and body:
```go
	if se, ok := errors.AsType[*lra.StatusError](err); ok && se.StatusCode == http.StatusNotFound {
		// handle 404
	}
```

### Streaming and per-request headers
`NewRequest` creates a `*http.Request` with base URL, headers and credentials
applied, which can be sent with the underlying client:
```go
	req, err := c.NewRequest(ctx, http.MethodPost, "/_bulk", file)
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := c.HTTPClient().Do(req)
```

## Migrating from v1

| v1 | v2 |
| --- | --- |
| `lra.NewConnection(true, "host", 9200, "/base", user, pw, false, proxy, isSocks, hl, timeout)` | `lra.New("https://host:9200/base", lra.WithBasicAuth(user, pw), lra.WithInsecureSkipVerify(), lra.WithProxy(proxy), lra.WithHeader(k, v), lra.WithTimeout(timeout))` |
| SOCKS proxy `"host:1080"` with `ProxyIsSocks=true` | `lra.WithProxy("socks5://host:1080")` |
| `conn.Get(ep)` / `conn.GetContext(ctx, ep)` | `c.Get(ctx, ep)` |
| `conn.GetJSON(ep, &out)` | `out, err := lra.GetJSON[T](ctx, c, ep)` |
| `conn.PostJSON(ep, jsonBytes, &out)` | `out, err := lra.PostJSON[T](ctx, c, ep, jsonBytes)` (or pass the struct directly) |
| `conn.Head(ep)` returning JSON encoded headers | `c.Head(ctx, ep)` returning `http.Header` |
| `conn.Connect`, `conn.Trace` | `c.Do(ctx, http.MethodTrace, ep, nil)` |
| `conn.BaseURL`, `conn.Client`, ... (exported fields) | `c.BaseURL()`, `c.HTTPClient()` |
| `err.Error() == "404 Not Found"` | `errors.AsType[*lra.StatusError](err)` and `StatusCode` |

Behaviour changes:
- Without `WithProxy`/`WithoutProxy`, the proxy environment variables
  (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`) are used, like in the standard library.
- Certificates are verified unless `WithInsecureSkipVerify` is given (v1 had
  `validateSSL` as parameter).
- The JSON functions return the zero value on errors, the body of error
  responses is available in `StatusError.Body`.
- Credentials are sent via the `Authorization` header and never appear in
  `BaseURL()` or error messages.

v1 stays available as `github.com/joernott/lra` (v1.x).
