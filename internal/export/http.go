package export

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A dedicated client and transport: instrumentation the application installs
// on http.DefaultTransport does not see the SDK's requests. Redirects are not
// followed: they would turn the POST into a GET or drop the Authorization
// header.
func defaultHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: RequestTimeout, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        2,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: RequestTimeout,
		},
		Timeout:       RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type outcomeKind int

const (
	sent outcomeKind = iota
	retry
	split
	drop
	stop
)

type outcome struct {
	kind       outcomeKind
	status     int
	retryAfter time.Duration
	hasAfter   bool
	reason     string
	body       string
}

func classify(status int, retryAfter string, body string, now time.Time) outcome {
	switch {
	case status >= 200 && status < 300:
		return outcome{kind: sent, body: body}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return outcome{kind: stop, status: status}
	case status == http.StatusRequestEntityTooLarge:
		return outcome{kind: split}
	case status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout:
		d, ok := parseRetryAfter(retryAfter, now)
		return outcome{kind: retry, retryAfter: d, hasAfter: ok, reason: "HTTP " + strconv.Itoa(status)}
	}
	reason := "HTTP " + strconv.Itoa(status)
	if body != "" {
		if len(body) > 500 {
			body = body[:500]
		}
		reason += ": " + body
	}
	return outcome{kind: drop, reason: reason}
}

// Retry-After: seconds or an HTTP date.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if n, err := strconv.ParseUint(v, 10, 32); err == nil {
		return time.Duration(n) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}
