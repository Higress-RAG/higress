package httpx

import (
    "crypto/tls"
    "errors"
    "math/rand"
    "net"
    "net/http"
    "net/url"
    "strings"
    "sync/atomic"
    "time"

    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
    "github.com/envoyproxy/envoy/contrib/golang/common/go/api"
)

type Client struct {
    hc   *http.Client
    opt  Options
    fail int32 // consecutive failures
    openUntil int64 // unix nanos for circuit open deadline
}

type Options struct {
    Timeout            time.Duration
    Retry              int
    BackoffMin         time.Duration
    BackoffMax         time.Duration
    HostAllowlist      []string
    MaxConsecutiveFail int
    CircuitOpen        time.Duration
    // TLS settings can be extended (insecureSkipVerify, rootCAs, etc.)
}

func NewFromConfig(cfg *config.HTTPClientConfig) *Client {
    // defaults
    to := 1200 * time.Millisecond
    if cfg != nil && cfg.TimeoutMs > 0 { to = time.Duration(cfg.TimeoutMs) * time.Millisecond }
    retry := 1
    if cfg != nil && cfg.Retry > 0 { retry = cfg.Retry }
    bmin := 100 * time.Millisecond
    if cfg != nil && cfg.BackoffMinMs > 0 { bmin = time.Duration(cfg.BackoffMinMs) * time.Millisecond }
    bmax := 800 * time.Millisecond
    if cfg != nil && cfg.BackoffMaxMs > 0 { bmax = time.Duration(cfg.BackoffMaxMs) * time.Millisecond }
    mcf := 5
    if cfg != nil && cfg.MaxConsecutiveFailures > 0 { mcf = cfg.MaxConsecutiveFailures }
    cop := 5 * time.Second
    if cfg != nil && cfg.CircuitOpenSeconds > 0 { cop = time.Duration(cfg.CircuitOpenSeconds) * time.Second }

    transport := &http.Transport{
        DialContext: (&net.Dialer{Timeout: to}).DialContext,
        TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
        MaxIdleConns: 100,
        IdleConnTimeout: 30 * time.Second,
    }
    return &Client{
        hc: &http.Client{Timeout: to, Transport: transport},
        opt: Options{
            Timeout: to, Retry: retry, BackoffMin: bmin, BackoffMax: bmax,
            HostAllowlist: func() []string { if cfg != nil { return cfg.HostAllowlist }; return nil }(),
            MaxConsecutiveFail: mcf, CircuitOpen: cop,
        },
    }
}

func (c *Client) allowed(u string) bool {
    if len(c.opt.HostAllowlist) == 0 { return true }
    pu, err := url.Parse(u)
    if err != nil { return false }
    host := pu.Hostname()
    for _, h := range c.opt.HostAllowlist {
        if matchHost(h, host) { return true }
    }
    return false
}

func matchHost(pattern, host string) bool {
    if pattern == "*" { return true }
    if strings.EqualFold(pattern, host) { return true }
    if strings.HasPrefix(pattern, "*.") {
        suf := strings.TrimPrefix(pattern, "*.")
        return strings.HasSuffix(host, "."+suf) || host == suf
    }
    return false
}

var ErrCircuitOpen = errors.New("circuit open")
var ErrHostNotAllowed = errors.New("host not allowed")

func (c *Client) Do(req *http.Request) (*http.Response, error) {
    if !c.allowed(req.URL.String()) {
        api.LogWarnf("httpx: blocked outbound host: %s", req.URL.String())
        return nil, ErrHostNotAllowed
    }
    now := time.Now().UnixNano()
    if atomic.LoadInt64(&c.openUntil) > now {
        return nil, ErrCircuitOpen
    }
    var resp *http.Response
    var err error
    for i := 0; i <= c.opt.Retry; i++ {
        resp, err = c.hc.Do(req)
        if err == nil && resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 500 {
            atomic.StoreInt32(&c.fail, 0)
            return resp, nil
        }
        // close body on failure to reuse connection
        if resp != nil && resp.Body != nil { _ = resp.Body.Close() }
        api.LogWarnf("httpx: request failed (try %d/%d) to %s: %v", i+1, c.opt.Retry+1, req.URL.String(), err)
        // backoff
        if i < c.opt.Retry {
            d := backoffJitter(c.opt.BackoffMin, c.opt.BackoffMax)
            time.Sleep(d)
        }
    }
    // open circuit on consecutive failures
    if atomic.AddInt32(&c.fail, 1) >= int32(c.opt.MaxConsecutiveFail) {
        atomic.StoreInt64(&c.openUntil, time.Now().Add(c.opt.CircuitOpen).UnixNano())
        atomic.StoreInt32(&c.fail, 0)
        api.LogWarnf("httpx: circuit opened for %v", c.opt.CircuitOpen)
    }
    return resp, err
}

func backoffJitter(min, max time.Duration) time.Duration {
    if max <= min { return min }
    d := min + time.Duration(rand.Int63n(int64(max-min)))
    return d
}
