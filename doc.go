// Copyright 2018-2026 Jörn Ott. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lra is a lowlevel REST api client with some convenient functions
// like proxy and TLS handling.
//
// A [Client] is created with [New] from a base URL and a set of options:
//
//	c, err := lra.New("https://elasticsearch.example.com:9200",
//		lra.WithBasicAuth("admin", "1234"),
//		lra.WithHeader("Content-Type", "application/json"),
//		lra.WithProxy("socks5://proxy.example.com:1080"),
//		lra.WithTimeout(30*time.Second),
//	)
//
// Endpoints passed to the request methods are appended to the base URL.
// The raw methods ([Client.Get], [Client.Post], ...) return the response body,
// [Client.Do] returns the complete [Response]. The generic functions
// [GetJSON], [PostJSON], ... encode the request and decode the response as
// JSON:
//
//	health, err := lra.GetJSON[ClusterHealth](ctx, c, "/_cluster/health")
//
// Responses with a status code of 400 or above result in a [*StatusError].
//
// A Client is immutable after creation and safe for concurrent use by
// multiple goroutines.
package lra
