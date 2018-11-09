[![Build Status](https://travis-ci.com/joernott/lra.svg?branch=master)](https://travis-ci.org/joernott/lra) [![Go Report Card](https://goreportcard.com/badge/joernott/lra)](https://goreportcard.com/report/joernott/lra) [![GoDoc](https://godoc.org/github.com/joernott/lra?status.svg)](https://godoc.org/github.com/joernott/lra)

# lra - a lowlevel REST api client

This package handles REST API calls and allows for using http and socks proxies
as well as self signed certificates. It also allows the specification of
headers to be sent with every request.

I wrote this after copying code for various tools connecting to different REST
APIs. It consolidates the low level functionality of establishing/configuring
a connction and doing the HTTP requests.

## License
BSD 3-clause license

## Contributions
Contributions / Pull requests are welcome. 

## Documentation
[https://godoc.org/github.com/joernott/lra](https://godoc.org/github.com/joernott/lra)

## Usage

### Create connection
A new connection can pe created using the NewConnection function
```
	hl := make(lra.HeaderList)
	hl["Content-Type"] = "application/json"
	connection,err := lra.NewConnection(
		true,                             // use SSL
		"elasticsearch.example.com",      // server name
		9200,                             // port
		"",                               // no additional base endpoint
		"admin",                          // user name
		"1234",                           // password
		false,                            // we use a certificate generatewd by the elasticsearch CA
		"https://proxy.example.com:3128", // We use a proxy
		false,                            // it is not a socks5 proxy
		hl                                // We want to pass those headers
	)
```
### Get a result
Getting a raw []byte:
```
  statusRaw,err := connection.Get("/_cluster/health")
```

Getting parsed JSON:
```
  statusJson,err := connection.GetJSON("/_cluster/health")
```

### Other API functions
Currently the standard CRUD operations DELETE, GET, PUT, POST are implemented.
In addition, CONNECT, HEAD, OPTIONS, PATCH and TRACE are implemented but so far,
I didn't need them. So they have not been battle-tested.

For every type, there is a function returning the raw []byte data and a function
with the suffix JSON which attempts to parse the data into a json map[string]interface{}.
