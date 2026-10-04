//go:build !js

package release

import (
	"context"
	"errors"
	"io"
	"net/http"
)

// maxRedirects bounds the redirects one release request follows. A GitHub
// release asset takes one or two, to objects.githubusercontent.com.
const maxRedirects = 10

// response is what a request answered: the status and headers of the last
// response after redirects, and its body.
type response struct {
	StatusCode int
	Header     map[string]string // canonical name -> first value
	Body       io.ReadCloser
}

// httpClient is the client every release request uses. It is not
// http.DefaultClient, so nothing else in the process can change its
// behaviour. It reads HTTP_PROXY, HTTPS_PROXY and NO_PROXY, and the system
// certificate store. Timeout bounds the whole request including the body,
// so a stalled download ends instead of hanging.
//
// A redirect goes only to https, unless the first request was plain http
// (a test server or a mirror the person chose). That keeps a release
// download from falling back to plain text. net/http drops the
// Authorization header when a redirect leaves the host, so the API token is
// never sent to objects.githubusercontent.com.
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

// httpGet fetches url. The token, when set, goes in an Authorization header.
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
