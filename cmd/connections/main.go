package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nep-ai/difqo/internal/connections"
	"github.com/nep-ai/difqo/internal/githubconnection"
	"github.com/nep-ai/difqo/internal/slackrelay"
	"github.com/nep-ai/difqo/relay/migrations"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8766", "numeric loopback listener behind HTTPS ingress")
	railway := flag.Bool("railway", false, "serve through the same-container Railway ingress")
	migrate := flag.Bool("migrate", false, "apply pending relay database migrations")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch {
	case *railway && *migrate:
		err = errors.New("choose either -railway or -migrate")
	case *migrate:
		err = migrations.Apply(ctx, os.Getenv("DIFQO_RELAY_DATABASE_URL"))
	case *railway:
		err = runRailway(ctx, *listen, os.Getenv("PORT"))
	default:
		err = run(ctx, *listen)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, address string) error {
	listen, err := netip.ParseAddrPort(address)
	if err != nil || !listen.Addr().IsLoopback() || listen.Port() == 0 {
		return errors.New("Connections requires a numeric loopback listen address and port")
	}
	providers := http.NewServeMux()
	ready := func(context.Context) error { return nil }
	if os.Getenv("DIFQO_GITHUB_APP_ID") != "" {
		provider, err := githubconnection.NewPilot(githubPilotConfig())
		if err != nil {
			return err
		}
		providers.Handle(githubconnection.TokenPath, provider)
	}
	if os.Getenv("DIFQO_SLACK_APP_ID") != "" || os.Getenv("DIFQO_GITHUB_APP_ID") == "" {
		provider, db, err := slackPilot(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		providers.Handle("/", provider)
		ready = func(ctx context.Context) error { return slackrelay.CheckSchema(ctx, db) }
	}
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = ready(startup)
	cancel()
	if err != nil {
		return errors.New("relay database unavailable or relay migrations missing")
	}
	return serve(ctx, address, connections.Handler(providers, ready))
}

func githubPilotConfig() githubconnection.PilotConfig {
	id := func(key string) int64 {
		value, err := strconv.ParseInt(os.Getenv(key), 10, 64)
		if err != nil {
			return 0
		}
		return value
	}
	return githubconnection.PilotConfig{
		AppID: id("DIFQO_GITHUB_APP_ID"), InstallationID: id("DIFQO_GITHUB_INSTALLATION_ID"),
		RepositoryID: id("DIFQO_GITHUB_REPOSITORY_ID"), OwnerID: id("DIFQO_GITHUB_OWNER_ID"),
		Repository: os.Getenv("DIFQO_GITHUB_REPOSITORY"), OwnerLogin: os.Getenv("DIFQO_GITHUB_OWNER_LOGIN"),
		PrivateKeyPEM: os.Getenv("DIFQO_GITHUB_APP_PRIVATE_KEY"), ProofSHA256: os.Getenv("DIFQO_GITHUB_PILOT_PROOF_SHA256"),
	}
}

func slackPilot(ctx context.Context) (http.Handler, *pgxpool.Pool, error) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("DIFQO_SLACK_TOKEN_KEY"))
	if err != nil || len(key) != 32 {
		return nil, nil, errors.New("DIFQO_SLACK_TOKEN_KEY must encode 32 bytes in base64")
	}
	dsn := os.Getenv("DIFQO_RELAY_DATABASE_URL")
	if dsn == "" {
		return nil, nil, errors.New("DIFQO_RELAY_DATABASE_URL is required")
	}
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, errors.New("invalid relay database configuration")
	}
	provider, err := slackrelay.NewPilot(db, slackrelay.PilotConfig{
		Origin:          os.Getenv("DIFQO_CONNECTIONS_ORIGIN"),
		AppID:           os.Getenv("DIFQO_SLACK_APP_ID"),
		ClientID:        os.Getenv("DIFQO_SLACK_CLIENT_ID"),
		ClientSecret:    os.Getenv("DIFQO_SLACK_CLIENT_SECRET"),
		SigningSecret:   os.Getenv("DIFQO_SLACK_SIGNING_SECRET"),
		WorkspaceID:     os.Getenv("DIFQO_SLACK_WORKSPACE_ID"),
		OperatorID:      os.Getenv("DIFQO_SLACK_OPERATOR_ID"),
		EnrollmentToken: os.Getenv("DIFQO_SLACK_ENROLLMENT_TOKEN"),
		TokenKey:        [32]byte(key),
	})
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return provider, db, nil
}

func serve(ctx context.Context, address string, handler http.Handler) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	return serve(ctx, listener, connections.Handler(provider, ready))
}

func serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if server.Shutdown(shutdown) != nil {
				server.Close()
			}
		case <-done:
		}
	}()
	err := server.Serve(listener)
	close(done)
	<-stopped
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
