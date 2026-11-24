package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
)

func runRailway(ctx context.Context, backend, port string) error {
	address, err := netip.ParseAddrPort(backend)
	if err != nil || !address.Addr().IsLoopback() || address.Port() == 0 {
		return errors.New("Connections requires a numeric loopback backend")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1024 || n > 65535 || n == int(address.Port()) {
		return errors.New("Railway PORT must be an unprivileged port distinct from Connections")
	}
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(n))
	if err != nil {
		return err
	}
	defer listener.Close()
	target := &url.URL{Scheme: "http", Host: address.String()}
	proxy := &httputil.ReverseProxy{
		ErrorLog: log.New(io.Discard, "", 0),
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
		},
		// Forward the original body without parsing. Never log request
		// paths, query strings, headers, or body from the ingress.
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.WriteHeader(http.StatusServiceUnavailable)
		},
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- run(ctx, backend) }()
	go func() { results <- serve(ctx, listener, proxy) }()
	first := <-results
	cancel()
	second := <-results
	if first != nil {
		return first
	}
	return second
}
