//go:build !js

package release

import (
	"context"
	"errors"
	"io"
	"net/http"
)

const maxRedirects = 10

type response struct {
	StatusCode int
	Header     map[string]string
	Body       io.ReadCloser
}

var httpClient = &http.Client{
	Timeout:       httpTimeout,
	Transport:     &http.Transport{Proxy: http.ProxyFromEnvironment},
	CheckRedirect: checkRedirect,
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("the server redirected too many times")
	}
	if req.URL.Scheme != "https" && via[0].URL.Scheme != "http" {
		return errors.New("the server redirected to a plain http address")
	}
	return nil
}

func httpGet(ctx context.Context, url, accept, token string) (*response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "tuios-update")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	header := make(map[string]string, len(resp.Header))
	for name, values := range resp.Header {
		if len(values) > 0 {
			header[http.CanonicalHeaderKey(name)] = values[0]
		}
	}
	return &response{StatusCode: resp.StatusCode, Header: header, Body: resp.Body}, nil
}
