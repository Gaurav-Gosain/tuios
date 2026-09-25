//go:build !js

package release

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// The release requests go through curl rather than net/http. net/http and
// crypto/tls were 2.7 MB of the binary, and this was the only code that made
// an HTTPS request. curl is on every machine tuios installs on (the
// installer runs through it), and it honours HTTPS_PROXY, NO_PROXY and the
// system certificate store as net/http did.
//
// Ways this can differ from the net/http client, and what covers each:
//   - No curl. The error says so and names the releases page, so the update
//     can still be done by hand. `tuios update` is the only thing affected.
//   - The token in the process list. It goes in a config on stdin, never in
//     an argument, so another user's ps cannot read it.
//   - The token sent to the host a download redirects to. curl sends a
//     custom Authorization header to the first host only, as net/http does.
//   - Status and headers. They come from the header dump of the last
//     response after redirects, which is the one net/http returned.
//   - A transport failure (DNS, refused, TLS, timeout). It is a
//     *TransportError, which is a net.Error, so update.go reports it as the
//     network being unreachable, as before.
//   - A hung connection. --max-time bounds each request by httpTimeout, and
//     the process runs under ctx.

// response is what a request answered: the status and headers of the last
// response after redirects, and its body.
type response struct {
	StatusCode int
	Header     map[string]string // canonical name -> first value
	Body       io.ReadCloser
}

// TransportError is a request that got no answer: no route, a name that did
// not resolve, a refused or reset connection, a TLS failure, a timeout. It
// is a net.Error, like the error net/http gave for the same failures.
type TransportError struct {
	URL  string
	Code int // curl's exit status, -1 when it did not run to an exit
	Msg  string
}

func (e *TransportError) Error() string {
	if e.Msg != "" {
		return fmt.Sprintf("%s: %s", e.URL, e.Msg)
	}
	return fmt.Sprintf("%s: curl exited with status %d", e.URL, e.Code)
}

// Timeout reports whether the request ran out of time (curl's status 28).
func (e *TransportError) Timeout() bool { return e.Code == 28 }

// Temporary is part of net.Error. Nothing here retries on it.
func (e *TransportError) Temporary() bool { return false }

// curlGet fetches url with curl.
func curlGet(ctx context.Context, url, accept, token string) (*response, error) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		return nil, errors.New("tuios update downloads with curl, which is not on PATH; install curl, or download the release from https://github.com/" + Repo + "/releases")
	}
	headers, err := os.CreateTemp("", "tuios-update-headers-*")
	if err != nil {
		return nil, err
	}
	headerPath := headers.Name()
	_ = headers.Close()
	defer func() { _ = os.Remove(headerPath) }()

	cmd := exec.CommandContext(ctx, curl,
		"--config", "-",
		"--silent", "--show-error",
		"--location", "--max-redirs", "10",
		"--max-time", strconv.Itoa(int(httpTimeout/time.Second)),
	)
	var body, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(curlConfig(url, accept, token, headerPath))
	cmd.Stdout, cmd.Stderr = &body, &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		code := -1
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exitErr.ExitCode()
		}
		return nil, &TransportError{URL: url, Code: code, Msg: strings.TrimSpace(stderr.String())}
	}
	dump, err := os.ReadFile(headerPath)
	if err != nil {
		return nil, err
	}
	status, header := parseHeaderDump(dump)
	if status == 0 {
		return nil, &TransportError{URL: url, Msg: "the answer had no HTTP status"}
	}
	return &response{StatusCode: status, Header: header, Body: io.NopCloser(&body)}, nil
}

// curlConfig is the request as a curl config file. Everything that varies,
// the token above all, goes here rather than on the command line.
func curlConfig(url, accept, token, headerPath string) string {
	quote := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", "", "\r", "")
	var b strings.Builder
	line := func(key, val string) {
		fmt.Fprintf(&b, "%s = \"%s\"\n", key, quote.Replace(val))
	}
	line("url", url)
	line("header", "Accept: "+accept)
	line("user-agent", "tuios-update")
	if token != "" {
		line("header", "Authorization: Bearer "+token)
	}
	line("dump-header", headerPath)
	return b.String()
}

// parseHeaderDump reads curl's --dump-header output, which holds one block
// per response when redirects were followed, and returns the last block's
// status and headers. Names are canonicalised as net/http does
// (x-ratelimit-reset becomes X-Ratelimit-Reset), since HTTP/2 sends them in
// lower case.
func parseHeaderDump(dump []byte) (int, map[string]string) {
	status := 0
	var header map[string]string
	sc := bufio.NewScanner(bytes.NewReader(dump))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, "HTTP/") {
			status = 0
			if fields := strings.Fields(line); len(fields) >= 2 {
				status, _ = strconv.Atoi(fields[1])
			}
			header = map[string]string{}
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || header == nil {
			continue
		}
		name = canonicalHeader(strings.TrimSpace(name))
		if _, seen := header[name]; !seen {
			header[name] = strings.TrimSpace(value)
		}
	}
	return status, header
}

// canonicalHeader upper-cases the first letter of each dash-separated word
// and lower-cases the rest.
func canonicalHeader(name string) string {
	b := []byte(strings.ToLower(name))
	upper := true
	for i, c := range b {
		if upper && c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
		upper = c == '-'
	}
	return string(b)
}
