package lkdr

import (
	"context"
	"net/http"
	"net/url"

	"github.com/jfk9w-go/lkdr-api"
	"github.com/pkg/errors"
)

type Client interface {
	Receipt(ctx context.Context, in *lkdr.ReceiptIn) (*lkdr.ReceiptOut, error)
	FiscalData(ctx context.Context, in *lkdr.FiscalDataIn) (*lkdr.FiscalDataOut, error)
}

type ClientFactory func(params lkdr.ClientParams) (Client, error)

var defaultClientFactory ClientFactory = func(params lkdr.ClientParams) (Client, error) {
	return lkdr.NewClient(params)
}

// NewRedirectTransport возвращает http.RoundTripper, подменяющий адрес
// запросов к API ФНС на baseURL (мок-сервис). Путь, query, тело и заголовки
// сохраняются; изменяются только схема и хост.
func NewRedirectTransport(baseURL string) (http.RoundTripper, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, errors.Wrap(err, "parse base url")
	}

	if base.Scheme == "" || base.Host == "" {
		return nil, errors.New("base url must be absolute")
	}

	return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		req := r.Clone(r.Context())

		u := *r.URL
		u.Scheme = base.Scheme
		u.Host = base.Host
		req.URL = &u

		return http.DefaultTransport.RoundTrip(req)
	}), nil
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
