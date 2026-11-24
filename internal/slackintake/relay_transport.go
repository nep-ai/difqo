package slackintake

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nep-ai/difqo/internal/store"
)

var (
	pairingIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	dnsLabel         = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

func NewHTTPSConnections(tasks store.Tasks, raw string) (*Connections, error) {
	u, err := parseRelayOrigin(raw, "https")
	if err != nil {
		return nil, err
	}
	return newConnections(tasks, u.String()), nil
}

// NewFixtureConnections preserves the separate numeric-loopback HTTP mode.
func NewFixtureConnections(tasks store.Tasks, raw string) (*Connections, error) {
	u, err := parseRelayOrigin(raw, "http")
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.IsLoopback() || u.Port() == "" {
		return nil, errors.New("Slack relay fixture requires a numeric HTTP loopback origin with a port")
	}
	return newConnections(tasks, u.String()), nil
}

func newConnections(tasks store.Tasks, origin string) *Connections {
	return &Connections{tasks: tasks, origin: origin, client: &http.Client{
		Timeout: 5 * time.Second,
		// Send the pairing proof directly to the configured origin, using
		// the system trust store and ordinary certificate hostname checks.
		Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2: true,
			IdleConnTimeout:   90 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func parseRelayOrigin(raw, scheme string) (*url.URL, error) {
	invalid := errors.New("Slack relay requires an " + strings.ToUpper(scheme) + " origin without credentials, path, query or fragment")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != scheme || u.User != nil || u.Path != "" || strings.ContainsAny(raw, "?#") {
		return nil, invalid
	}
	host := u.Hostname()
	canonical := strings.ToLower(host)
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return nil, invalid
		}
		canonical = ip.String()
		if ip.Is6() {
			host, canonical = "["+host+"]", "["+canonical+"]"
		}
	} else if !validRelayHostname(host) {
		return nil, invalid
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 || strconv.Itoa(value) != port {
			return nil, invalid
		}
		host += ":" + port
		if scheme != "https" || port != "443" {
			canonical += ":" + port
		}
	}
	if u.Host != host {
		return nil, invalid
	}
	if scheme == "https" {
		u.Host = canonical
	}
	return u, nil
}

func validRelayHostname(host string) bool {
	labels := strings.Split(host, ".")
	if len(labels) < 2 || len(host) > 253 || strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return false
	}
	for _, label := range labels {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}
