// Package lra is a lowlevel REST api client with some convenient functions like proxy and ssl handling.
//
// This package handles http and socks proxies as well as self signed certificates.
// It also allows the specification of Headers to be sent with every request.
package lra

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/net/proxy"
)

// Key-value list of headers to set on every request. Used when declaring the connection.
type HeaderList map[string]string

// The Connection object stores all the information needed to handle requests.
// Apart from the http.Client, it also contains the original settings and additional
// headers to send with every request.
//
// The fields are mostly set by the parameters passed to the NewConnection function
//except for Protocol, BaseURL and Client which are constructed based on those informations.
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
	BaseURL      string
	SendHeaders  HeaderList
	Client       *http.Client
}

// NewConnection builds a Connection object with a configured http client.
//
// Protocol can either be http or https, Server and Port specify the server name
// or IP and the port to connect to. The BaseEndpoint is added directly behind the port
// which allows skipping some base path before the api itself, User and Password
// are used to pass authentication in the URL (like http://admin:1234@localhost:8000/).
// ValidateSSL, if set to false, allows to skip SSL validation, which is needed for
// self signed certificates.
// If ProxyIsSocks is set to true, Proxy is the URL of a SOCKS5 proxy, if set to false
// it is the URL of a HTTP proxy to use
// SendHeaders is a list of Headers to send with the requests. This allows to pass
// authentication headers, content type definitions etc.
func NewConnection(UseSSL bool, Server string, Port int, BaseEndpoint string, User string, Password string, ValidateSSL bool, Proxy string, ProxyIsSocks bool, SendHeaders HeaderList) (*Connection, error) {
	var connection *Connection
	var tr *http.Transport

	connection = new(Connection)
	tr = &http.Transport{
		DisableKeepAlives:   false,
		IdleConnTimeout:     0,
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 100,
	}
	if !ValidateSSL {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if Proxy != "" {
		if ProxyIsSocks {
			dialer, err := proxy.SOCKS5("tcp", Proxy, nil, proxy.Direct)
			if err != nil {
				return nil, err
			}
			tr.Dial = dialer.Dial
		} else {
			proxyUrl, err := url.Parse(Proxy)
			if err != nil {
				return nil, err
			}
			tr.Proxy = http.ProxyURL(proxyUrl)
		}
	}
	connection.Client = &http.Client{
		Transport: tr,
		Timeout:   time.Second * 10}
	if UseSSL {
		connection.Protocol = "https"
	} else {
		connection.Protocol = "http"
	}
	if BaseEndpoint == "" {
		BaseEndpoint = "/engine-rest"
	}
	connection.BaseURL = connection.Protocol + "://"
	connection.Server = Server
	connection.Port = Port
	connection.BaseEndpoint = BaseEndpoint
	connection.User = User
	connection.Password = Password
	connection.ValidateSSL = ValidateSSL
	connection.Proxy = Proxy
	connection.ProxyIsSocks = ProxyIsSocks
	connection.SendHeaders = SendHeaders
	if User != "" {
		connection.BaseURL = connection.BaseURL + User + ":" + Password + "@"
	}
	connection.BaseURL = connection.BaseURL + Server + ":" + strconv.Itoa(Port) + BaseEndpoint
	return connection, nil
}

func (connection *Connection) request(method string, endpoint string, jsonData []byte) ([]byte, error) {
	var req *http.Request
	var err error

	target := connection.BaseURL + endpoint
	switch method {
	case "CONNECT", "DELETE", "GET", "HEAD", "OPTIONS":
		req, err = http.NewRequest(method, target, nil)
	default:
		req, err = http.NewRequest(method, target, bytes.NewBuffer(jsonData))
	}

	if err != nil {
		return nil, err
	}
	for h, v := range connection.SendHeaders {
		req.Header.Set(h, v)
	}

	r, err := connection.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	response, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func toJSON(response []byte) (map[string]interface{}, error) {
	var data interface{}
	err := json.Unmarshal(response, &data)
	if err != nil {
		return nil, err
	}
	m := data.(map[string]interface{})
	return m, nil
}

// Connect issues a HTTP CONNECT request and returns the raw data.
func (connection *Connection) Connect(endpoint string) ([]byte, error) {
	var x []byte
	return connection.request("CONNECT", endpoint, x)
}

func (connection *Connection) ConnectJSON(endpoint string) (map[string]interface{}, error) {
	var x []byte

	response, err := connection.request("CONNECT", endpoint, x)
	if err != nil {
		return nil, err
	}
	return toJSON(response)
}

// Delete issues a HTTP DELETE request and returns the raw data.
func (connection *Connection) Delete(endpoint string) ([]byte, error) {
	var x []byte
	return connection.request("DELETE", endpoint, x)
}

// DeleteJSON issues a HTTP DELETE request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) DeleteJSON(endpoint string) (map[string]interface{}, error) {
	var x []byte

	response, err := connection.request("DELETE", endpoint, x)
	if err != nil {
		return nil, err
	}
	return toJSON(response)
}

// Get issues a HTTP GET request and returns the raw data.
func (connection *Connection) Get(endpoint string) ([]byte, error) {
	var x []byte
	return connection.request("GET", endpoint, x)
}

// GetJSON issues a HTTP GET request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) GetJSON(endpoint string) (map[string]interface{}, error) {
	var x []byte

	response, err := connection.request("GET", endpoint, x)
	if err != nil {
		return nil, err
	}
	return toJSON(response)
}

// Head issues a HTTP HEAD request and returns the raw data.
func (connection *Connection) Head(endpoint string) ([]byte, error) {
	var x []byte
	return connection.request("HEAD", endpoint, x)
}

// HeadJSON issues a HTTP HEAD request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) HeadJSON(endpoint string) (map[string]interface{}, error) {
	var x []byte

	response, err := connection.request("HEAD", endpoint, x)
	if err != nil {
		return nil, err
	}
	return toJSON(response)
}

// Options issues a HTTP OPTIONS request and returns the raw data.
func (connection *Connection) Options(endpoint string) ([]byte, error) {
	var x []byte
	return connection.request("OPTIONS", endpoint, x)
}

// OptionsJSON issues a HTTP OPTIONS request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) OptionsJSON(endpoint string) (map[string]interface{}, error) {
	var x []byte

	response, err := connection.request("OPTIONS", endpoint, x)
	if err != nil {
		return nil, err
	}
	return toJSON(response)
}

// Patch issues a HTTP PATCH (RFC 5789) request and returns the raw data.
func (connection *Connection) Patch(endpoint string, jsonData []byte) ([]byte, error) {
	return connection.request("PATCH", endpoint, jsonData)
}

// PatchJSON issues a HTTP PATCH request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) PatchJSON(endpoint string, jsonData []byte) (map[string]interface{}, error) {
	response, err := connection.request("PATCH", endpoint, jsonData)
	if err != nil {
		return nil, err
	}
	return toJSON(response)
}

// Post issues a HTTP POST request and returns the raw data.
func (connection *Connection) Post(endpoint string, jsonData []byte) ([]byte, error) {
	return connection.request("POST", endpoint, jsonData)
}

// PostJSON issues a HTTP POST request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) PostJSON(endpoint string, jsonData []byte) (map[string]interface{}, error) {
	response, err := connection.request("POST", endpoint, jsonData)
	if err != nil {
		return nil, err
	}
	return toJSON(response)
}

// Put issues a HTTP PUT request and returns the raw data.
func (connection *Connection) Put(endpoint string, jsonData []byte) ([]byte, error) {
	return connection.request("PUT", endpoint, jsonData)
}

// PutJSON issues a HTTP PUT request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) PutJSON(endpoint string, jsonData []byte) (map[string]interface{}, error) {
	response, err := connection.request("PUT", endpoint, jsonData)
	if err != nil {
		return nil, err
	}
	return toJSON(response)
}

// Trace issues a HTTP TRACE request and returns the raw data.
func (connection *Connection) Trace(endpoint string) ([]byte, error) {
	var x []byte
	return connection.request("TRACE", endpoint, x)
}

// TraceJSON issues a HTTP TRACE request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) TraceJSON(endpoint string) (map[string]interface{}, error) {
	var x []byte

	response, err := connection.request("TRACE", endpoint, x)
	if err != nil {
		return nil, err
	}
	return toJSON(response)
}
