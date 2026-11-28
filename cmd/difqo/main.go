package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
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
	"github.com/nep-ai/difqo/internal/githubconnection"
	"github.com/nep-ai/difqo/internal/githubintake"
	"github.com/nep-ai/difqo/internal/slackintake"
	"github.com/nep-ai/difqo/internal/store"
	"github.com/nep-ai/difqo/internal/worker"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	const usage = "usage: difqo setup-database | difqo chat -help | difqo server | difqo worker -repo PATH | difqo github-intake -help | difqo slack-import -help | difqo slack-socket -help"
	if len(args) == 0 {
		return errors.New(usage)
	}
	if args[0] == "setup-database" {
		return runDatabaseSetup(args[1:])
	}
	if args[0] == "chat" {
		return runChat(args[1:])
	}
	if args[0] == "github-intake" {
		return runGitHubIntake(args[1:])
	}
	if args[0] == "slack-import" {
		return runSlack(args[1:], false)
	}
	if args[0] == "slack-socket" {
		return runSlack(args[1:], true)
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	port := flags.Int("port", 8765, "loopback API port")
	var repo *string
	var githubRepositoryID, taskID int64
	var preflight bool
	var webPath, artifactPath, projectsPath, workerProject, bindingVersion, slackFixture, slackRelay string
	var options execution.Options
	var managedSetup string
	switch args[0] {
	case "server":
		flags.StringVar(&managedSetup, "managed-setup", "", "private state directory for setup in the installed Mac UI")
		flags.StringVar(&slackRelay, "slack-relay", "", "HTTPS origin of the official Slack relay")
		flags.StringVar(&slackFixture, "slack-relay-fixture", "", "enable local Slack lifecycle and delivery with an HTTP loopback relay fixture")
		flags.StringVar(&projectsPath, "projects", "", "path to the local project catalog JSON file")
		flags.StringVar(&webPath, "web", "", "path to the built local web UI")
		flags.StringVar(&artifactPath, "artifacts", "", "absolute path to the worker artifact directory")
		flags.StringVar(&options.Runtime, "container-runtime", "", "auto, docker, podman, or a compatible CLI executable for review reruns (default auto)")
	case "worker":
		repo = flags.String("repo", "", "path to a local Git checkout")
		flags.StringVar(&workerProject, "project", "", "claim snapshot-review runs for this registered project")
		flags.StringVar(&bindingVersion, "binding-version", "", "binding version of the registered project checkout")
		flags.Int64Var(&githubRepositoryID, "github-repository-id", 0, "claim tasks for this enrolled GitHub repository only")
		flags.Int64Var(&taskID, "task", 0, "claim only the selected local goal ID")
		flags.BoolVar(&preflight, "preflight", false, "validate Codex worker options without claiming a goal")
		flags.StringVar(&options.Image, "codex-image", "", "local container image for Codex execution")
		flags.StringVar(&options.Runtime, "container-runtime", "", "auto, docker, podman, or a compatible CLI executable (default auto)")
		flags.BoolVar(&options.DisableSELinuxLabel, "container-selinux-label-disable", false, "opt out of Podman SELinux labeling for Codex containers")
		flags.StringVar(&options.Check, "check", "", "shell command to run in the isolated checkout")
		flags.StringVar(&options.ArtifactRoot, "artifacts", "", "absolute host path for execution artifacts")
		flags.StringVar(&options.AuthHome, "codex-home", "", "absolute path to a dedicated private Codex home")
	default:
		return errors.New(usage)
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
	if slackFixture != "" && slackRelay != "" {
		return errors.New("choose either -slack-relay or -slack-relay-fixture")
	}
	address := "127.0.0.1:" + strconv.Itoa(*port)
	if repo != nil {
		if *repo == "" {
			return errors.New("worker requires -repo PATH")
		}
		if githubRepositoryID < 0 || (githubRepositoryID > 0 && options.Image == "") {
			return errors.New("GitHub tasks require a positive -github-repository-id and Codex execution options")
		}
		if taskID < 0 || (taskID > 0 && (githubRepositoryID != 0 || workerProject != "" || bindingVersion != "")) {
			return errors.New("-task requires a positive local goal ID and cannot be combined with GitHub or specialist selectors")
		}
		if preflight && (options.Image == "" || taskID != 0 || githubRepositoryID != 0 || workerProject != "" || bindingVersion != "") {
			return errors.New("-preflight requires local Codex execution options without a claim selector")
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
		if preflight {
			return nil
		}
		return worker.Run(ctx, "http://"+address, *repo, runOptions, githubRepositoryID, taskID, project)
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
	tasks := store.Tasks{DB: db}
	const stagingConnections = "https://connections-staging.difqo.com"
	var setup controlplane.ManagedSetup
	if managedSetup != "" {
		installed, err := os.Executable()
		info, stateErr := os.Lstat(managedSetup)
		if err != nil || stateErr != nil || !filepath.IsAbs(managedSetup) ||
			!info.IsDir() || info.Mode().Perm() != 0o700 {
			return errors.New("managed setup requires a private absolute state directory")
		}
		chatOptions := execution.Options{
			Image: "difqo-codex:0.171.0", Runtime: "docker", ArtifactRoot: artifactPath,
			AuthHome: filepath.Join(filepath.Dir(managedSetup), "codex-home"),
		}
		setup = controlplane.ManagedSetup{
			CodexHelper:  filepath.Join(filepath.Dir(installed), "first-goal-macos"),
			ChiefTalk:    chatOptions.Talk,
			GitHubOrigin: stagingConnections, GitHubConnection: filepath.Join(managedSetup, "github-connection.json"),
			GitHubPoll: func(ctx context.Context, enrollment store.GitHubEnrollment, grant githubconnection.Grant) (int, error) {
				return githubintake.Poll(ctx, githubintake.Config{
					Enrollment: enrollment, Repository: grant.Repository, Handle: "difqo", Token: grant.Token,
				}, tasks)
			},
		}
		if slackRelay == "" && slackFixture == "" {
			slackRelay = stagingConnections
		}
	}
	var slack *slackintake.Connections
	switch {
	case slackRelay != "":
		origin, originErr := slackintake.HTTPSRelayOrigin(slackRelay)
		if originErr != nil {
			return originErr
		}
		token := os.Getenv("DIFQO_SLACK_ENROLLMENT_TOKEN")
		setup.SlackRequiresCode = managedSetup != "" && origin == stagingConnections && token == ""
		setup.SlackPlainText = origin == stagingConnections || token != ""
		slack, err = slackintake.NewPilotConnections(tasks, origin, token)
	case slackFixture != "":
		slack, err = slackintake.NewFixtureConnections(tasks, slackFixture)
	}
	if err != nil {
		return err
	}
	handler := http.NewCrossOriginProtection().Handler(controlplane.Handler(tasks, web, artifacts, options.Runtime, projects, slack, setup))
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
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	if slack != nil {
		slackCtx, stopSlack := context.WithCancel(context.Background())
		defer stopSlack()
		go slack.Run(slackCtx)
	}
	log.Printf("Difqo API listening on http://%s", address)
	return server.Serve(listener)
}
