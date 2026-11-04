package host

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPResponse ответ на GET.
type HTTPResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// HTTPClient выполняет GET с заданным заголовком Host. Нужен для проверок Caddy и здоровья сервисов.
type HTTPClient interface {
	Get(ctx context.Context, url, hostHeader string) (HTTPResponse, error)
}

// NetHTTP настоящий клиент: без переходов по редиректам, таймаут 5 секунд, тело до 64 КиБ.
type NetHTTP struct{}

// Get выполняет запрос.
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
		return HTTPResponse{}, fmt.Errorf("чтение ответа: %w", err)
	}
	return HTTPResponse{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
}

// FakeHTTP подмена для тестов.
type FakeHTTP struct {
	Calls   []string
	Handler func(url, hostHeader string) (HTTPResponse, error)
}

// Get записывает вызов и вызывает Handler.
func (f *FakeHTTP) Get(_ context.Context, url, hostHeader string) (HTTPResponse, error) {
	f.Calls = append(f.Calls, url+" Host="+hostHeader)
	if f.Handler == nil {
		return HTTPResponse{}, fmt.Errorf("FakeHTTP: нет обработчика для %s", url)
	}
	return f.Handler(url, hostHeader)
}
