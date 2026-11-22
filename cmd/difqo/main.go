package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nep-ai/difqo/internal/controlplane"
	"github.com/nep-ai/difqo/internal/store"
	"github.com/nep-ai/difqo/internal/worker"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: difqo server | difqo worker -repo PATH")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	port := flags.Int("port", 8765, "loopback API port")
	var repo *string
	switch args[0] {
	case "server":
	case "worker":
		repo = flags.String("repo", "", "path to a local Git checkout")
	default:
		return errors.New("usage: difqo server | difqo worker -repo PATH")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *port < 1 || *port > 65535 || flags.NArg() > 0 {
		return errors.New("invalid port or extra arguments")
	}
	address := "127.0.0.1:" + strconv.Itoa(*port)
	if repo != nil {
		if *repo == "" {
			return errors.New("worker requires -repo PATH")
		}
		return worker.Run(context.Background(), "http://"+address, *repo)
	}

	dsn := os.Getenv("DIFQO_DATABASE_URL")
	if dsn == "" {
		return errors.New("server requires DIFQO_DATABASE_URL")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("invalid database connection configuration")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = db.PingContext(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("could not connect to PostgreSQL (%T)", err)
	}

	handler := controlplane.Handler(store.Tasks{DB: db})
	server := &http.Server{
		Addr: address,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != address {
				http.Error(w, "use the configured loopback address", http.StatusBadRequest)
				return
			}
			handler.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
	}
	log.Printf("Difqo API listening on http://%s", address)
	return server.ListenAndServe()
}
