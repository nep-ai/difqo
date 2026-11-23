package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nep-ai/difqo/internal/controlplane"
	"github.com/nep-ai/difqo/internal/execution"
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
	var webPath, artifactPath, projectsPath, workerProject, bindingVersion string
	var options execution.Options
	switch args[0] {
	case "server":
		flags.StringVar(&projectsPath, "projects", "", "path to the local project catalog JSON file")
		flags.StringVar(&webPath, "web", "", "path to the built local web UI")
		flags.StringVar(&artifactPath, "artifacts", "", "absolute path to the worker artifact directory")
		flags.StringVar(&options.Runtime, "container-runtime", "", "auto, docker, podman, or a compatible CLI executable for review reruns (default auto)")
	case "worker":
		repo = flags.String("repo", "", "path to a local Git checkout")
		flags.StringVar(&workerProject, "project", "", "claim snapshot-review runs for this registered project")
		flags.StringVar(&bindingVersion, "binding-version", "", "binding version of the registered project checkout")
		flags.StringVar(&options.Image, "codex-image", "", "local container image for Codex execution")
		flags.StringVar(&options.Runtime, "container-runtime", "", "auto, docker, podman, or a compatible CLI executable (default auto)")
		flags.BoolVar(&options.DisableSELinuxLabel, "container-selinux-label-disable", false, "opt out of Podman SELinux labeling for Codex containers")
		flags.StringVar(&options.Check, "check", "", "shell command to run in the isolated checkout")
		flags.StringVar(&options.ArtifactRoot, "artifacts", "", "absolute host path for execution artifacts")
		flags.StringVar(&options.AuthHome, "codex-home", "", "absolute path to a dedicated private Codex home")
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
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		var project store.SpecialistProject
		if workerProject != "" || bindingVersion != "" {
			version := strings.TrimSpace(bindingVersion)
			if !projectID.MatchString(workerProject) || version == "" || len(version) > 128 || options != (execution.Options{}) {
				return errors.New("a specialist worker requires -project and -binding-version with its checkout and uses the snapshot review profile")
			}
			checkout, binding, err := resolveBinding(*repo, version)
			if err != nil {
				return errors.New("could not validate the specialist worker checkout")
			}
			*repo = checkout
			project = store.SpecialistProject{ID: workerProject, Binding: binding}
		}
		var runOptions *execution.Options
		if options != (execution.Options{}) {
			resolved, err := filepath.Abs(*repo)
			if err != nil {
				return err
			}
			*repo = resolved
			if err := options.Validate(ctx, *repo); err != nil {
				return err
			}
			runOptions = &options
		}
		return worker.Run(ctx, "http://"+address, *repo, runOptions, project)
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

	var web fs.FS
	if webPath != "" {
		web = os.DirFS(webPath)
		if info, err := fs.Stat(web, "index.html"); err != nil || !info.Mode().IsRegular() {
			return errors.New("server -web must point to the built frontend directory")
		}
	}
	var artifacts *os.Root
	if artifactPath != "" {
		if !filepath.IsAbs(artifactPath) {
			return errors.New("server -artifacts requires an absolute path")
		}
		artifacts, err = os.OpenRoot(artifactPath)
		if err != nil {
			return errors.New("could not open the worker artifact directory")
		}
		defer artifacts.Close()
	}
	projects, err := loadProjects(projectsPath)
	if err != nil {
		return err
	}
	handler := http.NewCrossOriginProtection().Handler(controlplane.Handler(store.Tasks{DB: db}, web, artifacts, options.Runtime, projects))
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
