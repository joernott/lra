// Copyright 2018-2022 Jörn Ott. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lra is a lowlevel REST api client with some convenient functions like proxy and ssl handling.
//
// This package handles http and socks proxies as well as self signed certificates.
// It also allows the specification of Headers to be sent with every request.

package lra

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/net/proxy"
)

// HeaderList is a key-value list of headers to set on every request. Used when declaring the connection.
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
	Timeout      time.Duration
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
func NewConnection(UseSSL bool, Server string, Port int, BaseEndpoint string, User string, Password string, ValidateSSL bool, Proxy string, ProxyIsSocks bool, SendHeaders HeaderList, Timeout time.Duration) (*Connection, error) {
	var connection *Connection
	var tr *http.Transport

	if Timeout == time.Second*0 {
		Timeout = time.Second * 60
	}

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
			proxyURL, err := url.Parse(Proxy)
			if err != nil {
				return nil, err
			}
			tr.Proxy = http.ProxyURL(proxyURL)
		}
	}
	connection.Client = &http.Client{
		Transport: tr,
		Timeout:   Timeout}
	if UseSSL {
		connection.Protocol = "https"
	} else {
		connection.Protocol = "http"
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
	connection.Timeout = Timeout
	return connection, nil
}

func (connection *Connection) request(method string, endpoint string, jsonData []byte) ([]byte, error) {
	var req *http.Request
	var err error
	var err2 error
	var response []byte

	target := connection.BaseURL + endpoint
	switch method {
	case "CONNECT", "GET", "HEAD", "OPTIONS":
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

	if method != "HEAD" {
		response, err2 = ioutil.ReadAll(r.Body)
	} else {
		response, err2 = json.Marshal(r.Header)
	}
	if err2 != nil {
		return nil, err2
	}
	if r.StatusCode > 399 {
		return response, errors.New(r.Status)
	}
	return response, nil
}

// Connect issues a HTTP CONNECT request and returns the raw data.
func (connection *Connection) Connect(endpoint string) ([]byte, error) {
	var x []byte
	return connection.request("CONNECT", endpoint, x)
}

// ConnectJSON issues a HTTP Connect request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) ConnectJSON(endpoint string, data interface{}) error {
	var x []byte

	response, err := connection.request("CONNECT", endpoint, x)
	err2 := json.Unmarshal(response, data)
	if err2 != nil {
		if err != nil {
			return err
		}
		return err2
	}
	return err
}

// Delete issues a HTTP DELETE request and returns the raw data.
func (connection *Connection) Delete(endpoint string, jsonData []byte) ([]byte, error) {
	return connection.request("DELETE", endpoint, jsonData)
}

// DeleteJSON issues a HTTP DELETE request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) DeleteJSON(endpoint string, jsonData []byte, data interface{}) error {
	response, err := connection.request("DELETE", endpoint, jsonData)
	err2 := json.Unmarshal(response, data)
	if err2 != nil {
		if err != nil {
			return err
		}
		return err2
	}
	return err
}

// Get issues a HTTP GET request and returns the raw data.
func (connection *Connection) Get(endpoint string) ([]byte, error) {
	var x []byte
	return connection.request("GET", endpoint, x)
}

// GetJSON issues a HTTP GET request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) GetJSON(endpoint string, data interface{}) error {
	var x []byte

	response, err := connection.request("GET", endpoint, x)
	err2 := json.Unmarshal(response, data)
	if err2 != nil {
		if err != nil {
			return err
		}
		return err2
	}
	return err
}

// Head issues a HTTP HEAD request and returns the raw data.
func (connection *Connection) Head(endpoint string) ([]byte, error) {
	var x []byte
	return connection.request("HEAD", endpoint, x)
}

// HeadJSON issues a HTTP HEAD request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) HeadJSON(endpoint string, data interface{}) error {
	var x []byte

	response, err := connection.request("HEAD", endpoint, x)
	err2 := json.Unmarshal(response, data)
	if err2 != nil {
		if err != nil {
			return err
		}
		return err2
	}
	return err
}

// Options issues a HTTP OPTIONS request and returns the raw data.
func (connection *Connection) Options(endpoint string) ([]byte, error) {
	var x []byte
	return connection.request("OPTIONS", endpoint, x)
}

// OptionsJSON issues a HTTP OPTIONS request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) OptionsJSON(endpoint string, data interface{}) error {
	var x []byte

	response, err := connection.request("OPTIONS", endpoint, x)
	err2 := json.Unmarshal(response, data)
	if err2 != nil {
		if err != nil {
			return err
		}
		return err2
	}
	return err
}

// Patch issues a HTTP PATCH (RFC 5789) request and returns the raw data.
func (connection *Connection) Patch(endpoint string, jsonData []byte) ([]byte, error) {
	return connection.request("PATCH", endpoint, jsonData)
}

// PatchJSON issues a HTTP PATCH request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) PatchJSON(endpoint string, jsonData []byte, data interface{}) error {
	response, err := connection.request("PATCH", endpoint, jsonData)
	err2 := json.Unmarshal(response, data)
	if err2 != nil {
		if err != nil {
			return err
		}
		return err2
	}
	return err
}

// Post issues a HTTP POST request and returns the raw data.
func (connection *Connection) Post(endpoint string, jsonData []byte) ([]byte, error) {
	return connection.request("POST", endpoint, jsonData)
}

// PostJSON issues a HTTP POST request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) PostJSON(endpoint string, jsonData []byte, data interface{}) error {
	response, err := connection.request("POST", endpoint, jsonData)
	err2 := json.Unmarshal(response, data)
	if err2 != nil {
		if err != nil {
			return err
		}
		return err2
	}
	return err
}

// Put issues a HTTP PUT request and returns the raw data.
func (connection *Connection) Put(endpoint string, jsonData []byte) ([]byte, error) {
	return connection.request("PUT", endpoint, jsonData)
}

// PutJSON issues a HTTP PUT request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) PutJSON(endpoint string, jsonData []byte, data interface{}) error {
	response, err := connection.request("PUT", endpoint, jsonData)
	err2 := json.Unmarshal(response, data)
	if err2 != nil {
		if err != nil {
			return err
		}
		return err2
	}
	return err
}

// Trace issues a HTTP TRACE request and returns the raw data.
func (connection *Connection) Trace(endpoint string) ([]byte, error) {
	var x []byte
	return connection.request("TRACE", endpoint, x)
}

// TraceJSON issues a HTTP TRACE request, parses the resulting data as JSON and returns the parse results.
func (connection *Connection) TraceJSON(endpoint string, data interface{}) error {
	var x []byte

	response, err := connection.request("TRACE", endpoint, x)
	err2 := json.Unmarshal(response, data)
	if err2 != nil {
		if err != nil {
			return err
		}
		return err2
	}
	return err
}
