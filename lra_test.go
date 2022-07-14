package lra

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
)

type ReturnData struct {
	Method     string      `json:"method"`
	Protocol   string      `json:"protocol"`
	Path       string      `json:"path"`
	Header     http.Header `json:"header"`
	StringData string      `json:"stringdata"`
	IntData    int         `json:"intdata"`
	BoolData   bool        `json:"booldata"`
	Error      string      `json:"error"`
}

type TestServer struct {
	Server   *httptest.Server
	Host     string
	Port     int
	SSL      bool
	Protocol string
}

var TestServers [2]TestServer
var HL HeaderList

const epurl = "/test?stringdata=hello&intdata=42&booldata=true"
const ep404 = "/error/404?stringdata=hello&intdata=42&booldata=true"
const epjson = "/error/json?stringdata=hello&intdata=42&booldata=true"
const epin = "/test"

var Timeout time.Duration

var indata = []byte(`{"stringdata":"hello","intdata":42,"booldata":true}`)

func urlParamFunc(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	e := ""
	u := strings.Split(r.URL.String(), "?")
	qv, err := url.ParseQuery(u[1])
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		e = err.Error()
	}
	protocol := "http"
	if r.TLS != nil {
		protocol = "https"
	}
	intdata, err := strconv.Atoi(qv.Get("intdata"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		e = err.Error()
	}
	booldata, err := strconv.ParseBool(qv.Get("booldata"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		e = err.Error()
	}

	ret := ReturnData{
		Method:     r.Method,
		Protocol:   protocol,
		Path:       params.ByName("path"),
		Header:     r.Header,
		StringData: qv.Get("stringdata"),
		IntData:    intdata,
		BoolData:   booldata,
		Error:      e,
	}
	body, err := json.Marshal(ret)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
	p := strings.Split(params.ByName("path"), "/")
	if len(p) >= 3 {
		if p[1] == "error" {
			switch p[2] {
			case "404":
				w.WriteHeader(http.StatusNotFound)
			case "json":
				body = []byte("{{")
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func contentDataFunc(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
	e := ""
	protocol := "http"
	if r.TLS != nil {
		protocol = "https"
	}
	decoder := json.NewDecoder(r.Body)
	var in ReturnData
	err := decoder.Decode(&in)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		e = err.Error()
	}

	ret := ReturnData{
		Method:     r.Method,
		Protocol:   protocol,
		Path:       params.ByName("path"),
		Header:     r.Header,
		StringData: in.StringData,
		IntData:    in.IntData,
		BoolData:   in.BoolData,
		Error:      e,
	}
	body, err := json.Marshal(ret)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
	p := strings.Split(params.ByName("path"), "/")
	if len(p) >= 3 {
		if p[1] == "error" {
			switch p[2] {
			case "404":
				w.WriteHeader(http.StatusNotFound)
				return
			case "json":
				body = []byte("{{")
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)

}

func TestMain(m *testing.M) {
	var err error

	router := httprouter.New()
	router.GET("/base/*path", urlParamFunc)
	router.POST("/base/*path", contentDataFunc)
	router.PUT("/base/*path", contentDataFunc)
	router.DELETE("/base/*path", urlParamFunc)
	router.HEAD("/base/*path", urlParamFunc)
	router.OPTIONS("/base/*path", urlParamFunc)
	router.PATCH("/base/*path", contentDataFunc)
	router.Handle("TRACE", "/base/*path", urlParamFunc)
	router.Handle("CONNECT", "/base/*path", urlParamFunc)

	HL = make(HeaderList)
	HL["test-header"] = "test"

	Timeout = time.Second * 120

	server := httptest.NewServer(router)
	defer server.Close()
	u := strings.Split(server.URL, ":")
	if len(u) != 3 {
		fmt.Println("Could not split http server url")
		os.Exit(10)
	}
	host := strings.Replace(u[1], "//", "", 1)
	port, err := strconv.Atoi(u[2])
	if err != nil {
		fmt.Println(err)
		os.Exit(10)
	}

	TestServers[0] = TestServer{
		Server:   server,
		Host:     host,
		Port:     port,
		SSL:      false,
		Protocol: "http",
	}

	httpsserver := httptest.NewTLSServer(router)
	defer httpsserver.Close()
	u = strings.Split(httpsserver.URL, ":")
	if len(u) != 3 {
		fmt.Println("Could not split http server url")
		os.Exit(10)
	}
	host = strings.Replace(u[1], "//", "", 1)
	port, err = strconv.Atoi(u[2])
	if err != nil {
		fmt.Println(err)
		os.Exit(10)
	}
	TestServers[1] = TestServer{
		Server:   httpsserver,
		Host:     host,
		Port:     port,
		SSL:      true,
		Protocol: "https",
	}

	flag.Parse()
	exitCode := m.Run()

	// Exit
	os.Exit(exitCode)
}

func TestNewConnection_HTTP(t *testing.T) {

	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection")
		}
		if connection.Protocol != server.Protocol {
			t.Errorf("Expected Protocol '%v', got '%v' instead.", server.Protocol, connection.Protocol)
		}
		if connection.Server != server.Host {
			t.Errorf("Expected Server to be '%v', got '%v' instead.", server.Host, connection.Server)
		}

		if connection.Port != server.Port {
			t.Errorf("Expected Port to be '%v', got '%v' instead.", server.Port, connection.Port)
		}

		if connection.BaseEndpoint != "/base" {
			t.Errorf("Expected BaseEndpoint to be '/base', got '%v' instead.", connection.BaseEndpoint)
		}

		if connection.User != "" {
			t.Errorf("Expected User to be '', got '%v' instead.", connection.User)
		}

		if connection.Password != "" {
			t.Errorf("Expected Password to be '', got '%v' instead.", connection.Password)
		}

		if connection.ValidateSSL {
			t.Errorf("Expected ValidateSSL to be false got '%v' instead.", connection.ValidateSSL)
		}

		if connection.Proxy != "" {
			t.Errorf("Expected proxy to be '', got '%v' instead.", connection.Proxy)
		}

		if connection.ProxyIsSocks {
			t.Errorf("Expected ProxyIsSocks to be false got '%v' instead.", connection.ProxyIsSocks)
		}

		if connection.SendHeaders["test-header"] != "test" {
			t.Errorf("Expected test-header to be 'test', got '%v' instead.", connection.SendHeaders["test-header"])
		}
		cu := server.Server.URL + "/base"
		if connection.BaseURL != cu {
			t.Errorf("Expected BaseEndpoint to be '%v', got '%v' instead.", cu, connection.BaseURL)
		}
	}
}

func TestNewConnection_User(t *testing.T) {
	for _, server := range TestServers {
		user := "admin"
		password := "123456"
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", user, password, false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection")
		}

		if connection.User != user {
			t.Errorf("Expected User to be '%v', got '%v' instead.", user, connection.User)
		}

		if connection.Password != password {
			t.Errorf("Expected Password to be '%v', got '%v' instead.", password, connection.Password)
		}

		cu := server.Protocol + "://" + user + ":" + password + "@" + server.Host + ":" + strconv.Itoa(server.Port) + "/base"
		if connection.BaseURL != cu {
			t.Errorf("Expected BaseEndpoint to be '%v', got '%v' instead.", cu, connection.BaseURL)
		}
	}
}

func TestNewConnection_HTTPProxy_OK(t *testing.T) {
	httpproxy := "http://proxy.example.com:3128"
	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, httpproxy, false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection")
		}
		if connection.Proxy != httpproxy {
			t.Errorf("Expected proxy to be '%v', got '%v' instead.", httpproxy, connection.Proxy)
		}

		if connection.ProxyIsSocks {
			t.Errorf("Expected ProxyIsSocks to be false got '%v' instead.", connection.ProxyIsSocks)
		}
	}
}

func TestNewConnection_SocksProxy_OK(t *testing.T) {
	socksproxy := "socks.example.com:8181"
	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, socksproxy, true, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection")
		}
		if connection.Proxy != socksproxy {
			t.Errorf("Expected proxy to be '%v', got '%v' instead.", socksproxy, connection.Proxy)
		}

		if !connection.ProxyIsSocks {
			t.Errorf("Expected ProxyIsSocks to be true got '%v' instead.", connection.ProxyIsSocks)
		}

	}
}

func TestConnect_OK(t *testing.T) {
	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		b, err := connection.Connect(epurl)
		checkRawResults(server, b, err, "CONNECT", t)
	}
}

func TestConnectJSON_OK(t *testing.T) {
	for _, server := range TestServers {
		data := make(map[string]interface{})
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		err = connection.ConnectJSON(epurl, &data)
		checkJSONResults(server, data, err, "CONNECT", t)
		err = connection.ConnectJSON(ep404, &data)
		check404(err, t)
		err = connection.ConnectJSON(epjson, &data)
		checkJSONError(err, t)
	}
}

func TestDelete_OK(t *testing.T) {
	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		b, err := connection.Delete(epurl, indata)
		checkRawResults(server, b, err, "DELETE", t)
	}
}

func TestDeleteJSON_OK(t *testing.T) {
	for _, server := range TestServers {
		data := make(map[string]interface{})
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		err = connection.DeleteJSON(epurl, indata, &data)
		checkJSONResults(server, data, err, "DELETE", t)
		err = connection.DeleteJSON(ep404, indata, &data)
		check404(err, t)
		err = connection.DeleteJSON(epjson, indata, &data)
		checkJSONError(err, t)
	}
}

func TestGet_OK(t *testing.T) {
	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		ep := "/test?stringdata=hello&intdata=42&booldata=true"
		b, err := connection.Get(ep)
		checkRawResults(server, b, err, "GET", t)
	}
}

func TestGetJSON_OK(t *testing.T) {
	for _, server := range TestServers {
		data := make(map[string]interface{})
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		err = connection.GetJSON(epurl, &data)
		checkJSONResults(server, data, err, "GET", t)
		err = connection.GetJSON(ep404, &data)
		check404(err, t)
		err = connection.GetJSON(epjson, &data)
		checkJSONError(err, t)
	}
}

func TestHead_OK(t *testing.T) {
	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		b, err := connection.Head(epurl)
		if err != nil {
			t.Errorf("Error creating connection: %v", err.Error())
		}
		expected := `{"Content-Length":\["[0-9]+"\],"Content-Type":\["application/json"\],"Date":\[".*"\]}`
		ok, err := regexp.Match(expected, b)
		if err != nil {
			t.Fatalf("Regexp parse error in HEAD: %v", err.Error())
		} else {
			if !ok {
				t.Errorf("Wrong HEAD result, expected '%v', got '%v'", expected, string(b[:]))
			}
		}
	}
}

func TestHeadJSON_OK(t *testing.T) {
	for _, server := range TestServers {
		hdr := make(map[string]interface{})
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		err = connection.HeadJSON(epurl, &hdr)
		if err != nil {
			t.Errorf("Error creating connection: %v", err.Error())
		}

		if data, ok := hdr["Content-Length"].([]interface{}); ok {
			if s, ok := data[0].(string); ok {
				i, err := strconv.Atoi(s)
				if err != nil {
					t.Errorf("Content-Length is not a number: %v", s)
				} else {
					if i <= 100 {
						t.Errorf("Wrong Content-Length, expected >100, got %v", data[0])
					}
				}
			} else {
				t.Errorf("Could not assert Content-Length, got %v", reflect.TypeOf(data[0]))
			}
		} else {
			t.Errorf("Could not assert Content-Length array, got %v", reflect.TypeOf(hdr["Content-Length"]))
		}
		if data, ok := hdr["Content-Type"].([]interface{}); ok {
			if data[0] != "application/json" {
				t.Errorf("Wrong Content-Type, expected application/json, got %v", data[0])
			}
		} else {
			t.Errorf("Could not assert Content-Type array, got %v", reflect.TypeOf(hdr["Content-Type"]))
		}
	}
}

func TestOptions_OK(t *testing.T) {
	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		b, err := connection.Options(epurl)
		checkRawResults(server, b, err, "OPTIONS", t)
	}
}

func TestOptionsJSON_OK(t *testing.T) {
	for _, server := range TestServers {
		data := make(map[string]interface{})
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		err = connection.OptionsJSON(epurl, &data)
		checkJSONResults(server, data, err, "OPTIONS", t)
		err = connection.OptionsJSON(ep404, &data)
		check404(err, t)
		err = connection.OptionsJSON(epjson, &data)
		checkJSONError(err, t)
	}
}

func TestPatch_OK(t *testing.T) {
	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)

		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		b, err := connection.Patch(epin, indata)
		checkRawResults(server, b, err, "PATCH", t)
	}
}

func TestPatchJSON_OK(t *testing.T) {
	for _, server := range TestServers {
		data := make(map[string]interface{})
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		err = connection.PatchJSON(epin, indata, &data)
		checkJSONResults(server, data, err, "PATCH", t)
		err = connection.PatchJSON(ep404, indata, &data)
		check404(err, t)
		err = connection.PatchJSON(epjson, indata, &data)
		checkJSONError(err, t)
	}
}

func TestPost_OK(t *testing.T) {
	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		b, err := connection.Post(epin, indata)
		checkRawResults(server, b, err, "POST", t)
	}
}

func TestPostJSON_OK(t *testing.T) {
	for _, server := range TestServers {
		data := make(map[string]interface{})
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		err = connection.PostJSON(epin, indata, &data)
		checkJSONResults(server, data, err, "POST", t)
		err = connection.PostJSON(ep404, indata, &data)
		check404(err, t)
		err = connection.PostJSON(epjson, indata, &data)
		checkJSONError(err, t)
	}
}

func TestPut_OK(t *testing.T) {
	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		b, err := connection.Put(epin, indata)
		checkRawResults(server, b, err, "PUT", t)
	}
}

func TestPutJSON_OK(t *testing.T) {
	for _, server := range TestServers {
		data := make(map[string]interface{})
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		err = connection.PutJSON(epin, indata, &data)
		checkJSONResults(server, data, err, "PUT", t)
		err = connection.PutJSON(ep404, indata, &data)
		check404(err, t)
		err = connection.PutJSON(epjson, indata, &data)
		checkJSONError(err, t)
	}
}

func TestTrace_OK(t *testing.T) {
	for _, server := range TestServers {
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		b, err := connection.Trace(epurl)
		checkRawResults(server, b, err, "TRACE", t)
	}
}

func TestTraceJSON_OK(t *testing.T) {
	for _, server := range TestServers {
		data := make(map[string]interface{})
		connection, err := NewConnection(server.SSL, server.Host, server.Port, "/base", "", "", false, "", false, HL, Timeout)
		if err != nil {
			t.Fatalf("Error creating connection: %v", err.Error())
		}
		err = connection.TraceJSON(epurl, &data)
		checkJSONResults(server, data, err, "TRACE", t)
		err = connection.TraceJSON(ep404, &data)
		check404(err, t)
		err = connection.TraceJSON(epjson, &data)
		checkJSONError(err, t)
	}
}

func checkJSONResults(server TestServer, data map[string]interface{}, err error, method string, t *testing.T) {
	if err != nil {
		t.Errorf("Error creating connection: %v", err.Error())
	}

	h := make(http.Header)
	h.Add("Accept-Encoding", "gzip")
	h.Add("Test-Header", "test")
	h.Add("User-Agent", "Go-http-client/1.1")
	expected := ReturnData{
		Method:     method,
		Protocol:   server.Protocol,
		Path:       "/test",
		Header:     h,
		StringData: "hello",
		IntData:    42,
		BoolData:   true,
		Error:      "",
	}
	if data["method"] != expected.Method {
		t.Errorf("Wrong Method, expected '%v', got '%v'", expected.Method, data["method"])
	}
	if data["protocol"] != expected.Protocol {
		t.Errorf("Wrong Protocol, expected '%v', got '%v'", expected.Protocol, data["protocol"])
	}
	if data["path"] != expected.Path {
		t.Errorf("Wrong Path, expected '%v', got '%v'", expected.Path, data["path"])
	}
	if data["stringdata"] != expected.StringData {
		t.Errorf("Wrong StringData, expected '%v', got '%v'", expected.StringData, data["stringdata"])
	}
	if f, ok := data["intdata"].(float64); ok {

		if int(f) != expected.IntData {
			t.Errorf("Wrong IntData, expected %v, got %v", expected.IntData, int(f))
		}
	} else {
		t.Errorf("Could not assert intdata as float64, got %v", reflect.TypeOf(data["intdata"]))
	}
	if data["booldata"] != expected.BoolData {
		t.Errorf("Wrong BoolData, expected '%v', got '%v'", expected.BoolData, data["booldata"])
	}
	if hdr, ok := data["header"].(map[string]interface{}); ok {
		if data, ok := hdr["Test-Header"].([]interface{}); ok {
			if data[0] != expected.Header.Get("Test-Header") {
				t.Errorf("Wrong Header, expected '%v', got '%v'", expected.Header.Get("Test-Header"), data[0])
			}
		} else {
			t.Errorf("Could not assert header content array, got %v", reflect.TypeOf(hdr["Test-Header"]))
		}
	} else {
		t.Errorf("Could not assert header data structure, got %v", reflect.TypeOf(data["header"]))
	}

}

func checkRawResults(server TestServer, raw []byte, err error, method string, t *testing.T) {
	if err != nil {
		t.Errorf("Error creating connection: %v", err.Error())
	}
	expected := `{"method":"` + method + `","protocol":"` + server.Protocol + `","path":"/test","header":{"Accept-Encoding":\["gzip"\],.*"Test-Header":\["test"\],"User-Agent":\["Go-http-client/1.1"\]},"stringdata":"hello","intdata":42,"booldata":true,"error":""}`
	ok, err := regexp.Match(expected, raw)
	if err != nil {
		t.Fatalf("Regexp parse error in %v: %v", method, err.Error())
	} else {
		if !ok {
			t.Errorf("Wrong %v result, expected '%v', got '%v'", method, expected, string(raw[:]))
		}
	}
}

func check404(err error, t *testing.T) {
	if err == nil {
		t.Errorf("Expected http error, got nil")
	} else {
		if err.Error() != "404 Not Found" {
			t.Errorf("Expected error '404 Not Found', got error '%v' instead.", err.Error())
		}
	}
}

func checkJSONError(err error, t *testing.T) {
	if err == nil {
		t.Errorf("Expected json decode error, got nil")
	} else {
		if err.Error() != "invalid character '{' looking for beginning of object key string" {
			t.Logf("Expected error 'invalid character '{' looking for beginning of object key string', got error '%v' instead", err.Error())
		}
	}
}
