package host

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPResponse is the answer to a GET.
type HTTPResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// HTTPClient performs GET with a given Host header. It is needed for Caddy and service health checks.
type HTTPClient interface {
	Get(ctx context.Context, url, hostHeader string) (HTTPResponse, error)
}

// NetHTTP is the real client: no redirects, a 5 second timeout, a body of up to 64 KiB.
type NetHTTP struct{}

// Get performs the request.
func (NetHTTP) Get(ctx context.Context, url, hostHeader string) (HTTPResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return HTTPResponse{}, err
	}
	if hostHeader != "" {
		req.Host = hostHeader
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		return HTTPResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("read the response: %w", err)
	}
	return HTTPResponse{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
}

// FakeHTTP is a test double.
type FakeHTTP struct {
	Calls   []string
	Handler func(url, hostHeader string) (HTTPResponse, error)
}

// Get records the call and calls Handler.
func (f *FakeHTTP) Get(_ context.Context, url, hostHeader string) (HTTPResponse, error) {
	f.Calls = append(f.Calls, url+" Host="+hostHeader)
	if f.Handler == nil {
		return HTTPResponse{}, fmt.Errorf("FakeHTTP: no handler for %s", url)
	}
	return f.Handler(url, hostHeader)
}
