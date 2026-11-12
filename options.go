package lra

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// DefaultTimeout is the overall request timeout used if [WithTimeout] is not given.
const DefaultTimeout = 60 * time.Second

// An Option configures a [Client]. Options are applied in order, a later
// option overrides an earlier one of the same kind.
type Option func(*options) error

type options struct {
	header          http.Header
	user, password  string
	basicAuth       bool
	timeout         time.Duration
	timeoutSet      bool
	tlsConfig       *tls.Config
	insecure        bool
	proxyURL        *url.URL
	proxySet        bool
	httpClient      *http.Client
	maxResponseSize int64
}

// WithBasicAuth sets the credentials for HTTP basic authentication. It
// overrides credentials contained in the base URL.
func WithBasicAuth(user, password string) Option {
	return func(o *options) error {
		o.user, o.password, o.basicAuth = user, password, true
		return nil
	}
}

// WithHeader adds a header which is sent with every request. It may be given
// multiple times, also for the same key.
func WithHeader(key, value string) Option {
	return func(o *options) error {
		o.header.Add(key, value)
		return nil
	}
}

// WithTimeout sets the overall timeout for a request including reading the
// response body. A timeout of 0 means no timeout. Without this option,
// [DefaultTimeout] is used. Use contexts for per-request deadlines.
func WithTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d < 0 {
			return fmt.Errorf("lra: negative timeout %v", d)
		}
		o.timeout, o.timeoutSet = d, true
		return nil
	}
}

// WithTLSConfig sets the TLS configuration, e.g. to trust a private CA via
// RootCAs or to use client certificates. The configuration is cloned.
func WithTLSConfig(cfg *tls.Config) Option {
	return func(o *options) error {
		if cfg == nil {
			return errors.New("lra: nil TLS config")
		}
		o.tlsConfig = cfg.Clone()
		return nil
	}
}

// WithInsecureSkipVerify disables the verification of the server certificate.
// This makes the connection vulnerable to man-in-the-middle attacks. Prefer
// [WithTLSConfig] with the CA certificate in RootCAs.
func WithInsecureSkipVerify() Option {
	return func(o *options) error {
		o.insecure = true
		return nil
	}
}

// WithProxy sets the proxy for all requests. Supported schemes are http,
// https, socks5 and socks5h, e.g. "http://proxy.example.com:3128" or
// "socks5://user:password@proxy.example.com:1080".
//
// Without this option, the proxy is taken from the environment
// (HTTP_PROXY, HTTPS_PROXY and NO_PROXY), see [http.ProxyFromEnvironment].
func WithProxy(proxyURL string) Option {
	return func(o *options) error {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return fmt.Errorf("lra: invalid proxy URL: %w", err)
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("lra: unsupported proxy scheme %q", u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("lra: proxy URL %q has no host", u.Redacted())
		}
		o.proxyURL, o.proxySet = u, true
		return nil
	}
}

// WithoutProxy connects directly, ignoring proxy settings from the environment.
func WithoutProxy() Option {
	return func(o *options) error {
		o.proxyURL, o.proxySet = nil, true
		return nil
	}
}

// WithHTTPClient uses the given http.Client instead of creating one. This gives
// full control over the transport, but it can't be combined with
// [WithTimeout], [WithTLSConfig], [WithInsecureSkipVerify], [WithProxy] or
// [WithoutProxy].
func WithHTTPClient(hc *http.Client) Option {
	return func(o *options) error {
		if hc == nil {
			return errors.New("lra: nil http.Client")
		}
		o.httpClient = hc
		return nil
	}
}

// WithMaxResponseSize limits the size of response bodies. Larger responses
// result in [ErrResponseTooLarge]. A size of 0 (the default) means no limit.
func WithMaxResponseSize(n int64) Option {
	return func(o *options) error {
		if n < 0 {
			return fmt.Errorf("lra: negative maximum response size %d", n)
		}
		o.maxResponseSize = n
		return nil
	}
}
