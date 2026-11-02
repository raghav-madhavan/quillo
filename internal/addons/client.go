package addons

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const (
	manifestTimeout  = 10 * time.Second
	maxManifestBytes = 2 << 20
	userAgent        = "Quillo (Stremio addon client)"
)

// Client talks to addons over HTTP. Addons may be on the public internet or
// on the operator's own network (a self-hosted AIOStreams), so private
// addresses are allowed; only admins can add an addon URL.
type Client struct {
	http *http.Client
}

func NewClient() *Client {
	return &Client{http: &http.Client{
		// Redirects are followed (addons move hosts) but capped.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}}
}

// FetchManifest downloads and validates the manifest at manifestURL, which
// must already be normalized. It returns the parsed manifest and the raw
// bytes to store. Every error is a *UserError.
func (c *Client) FetchManifest(ctx context.Context, manifestURL string) (*Manifest, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, nil, userErrorf("That link can't be opened.")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fetchError(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil, userErrorf("The addon says that link doesn't exist (404). Check you copied the whole manifest link.")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, nil, userErrorf("The addon refused the request (%d). Its link may have expired; get a new one from the addon.", resp.StatusCode)
	case resp.StatusCode >= 500:
		return nil, nil, userErrorf("The addon is having problems right now (%d). Try again in a few minutes.", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, nil, userErrorf("The addon answered with an unexpected status (%d).", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, nil, fetchError(err)
	}
	if len(body) > maxManifestBytes {
		return nil, nil, userErrorf("That link returned something far too large to be an addon manifest.")
	}
	m, err := ParseManifest(body)
	if err != nil {
		return nil, nil, err
	}
	return m, body, nil
}

func fetchError(err error) *UserError {
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return userErrorf("This addon isn't responding. It took longer than %d seconds.", int(manifestTimeout.Seconds()))
	case errors.As(err, &dnsErr):
		return userErrorf("Couldn't find the addon's server (%s). Check the link for typos.", dnsErr.Name)
	default:
		return &UserError{Message: fmt.Sprintf("Couldn't reach the addon. %s", plainCause(err))}
	}
}

func plainCause(err error) string {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return "Its server refused the connection or is offline."
	}
	return "Check the link and that the addon's server is online."
}
