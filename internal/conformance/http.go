package conformance

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nslaughter/financial-data-api/internal/expected"
)

// maxBody is the most bytes of a response body the runner reads.
const maxBody = 64 << 20

// call is a request the runner sends.
type call struct {
	method string
	// path starts with /; query holds its parameters, each value in order.
	path  string
	query url.Values
	// authorization is the Authorization header, or nil to send none.
	authorization *string
	// body is a JSON body, or nil to send none.
	body []byte
}

// String returns the request line's method and target, such as
// "GET /v1/observations?series_id=activity-index".
func (c call) String() string {
	s := c.method + " " + c.path
	if q := c.query.Encode(); q != "" {
		s += "?" + q
	}
	return s
}

// exchange is a request the runner sent and the response it received.
type exchange struct {
	call
	status int
	header http.Header
	body   []byte
}

// statusLine returns the response's status and the start of its body, for a
// report.
func (e *exchange) statusLine() string {
	s := strconv.Itoa(e.status)
	if body := strings.TrimSpace(string(e.body)); body != "" {
		s += " " + expected.Shorten(body)
	}
	return s
}

// newClient returns a client that sends requests as written: it does not
// follow redirects, ask for compression, or use a proxy.
func newClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableCompression = true
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// do sends c to the server at baseURL and reads the whole response.
func do(ctx context.Context, client *http.Client, baseURL string, c call) (*exchange, error) {
	target := baseURL + c.path
	if q := c.query.Encode(); q != "" {
		target += "?" + q
	}
	var body io.Reader
	if c.body != nil {
		body = bytes.NewReader(c.body)
	}
	req, err := http.NewRequestWithContext(ctx, c.method, target, body)
	if err != nil {
		return nil, err
	}
	if c.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.authorization != nil {
		req.Header.Set("Authorization", *c.authorization)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("reading the response: %w", err)
	}
	if len(data) > maxBody {
		return nil, fmt.Errorf("the response body is longer than %d bytes", maxBody)
	}
	return &exchange{call: c, status: resp.StatusCode, header: resp.Header, body: data}, nil
}
