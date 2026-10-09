package module

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/djangbahevans/goerp/internal/cli/adminclient"
	"github.com/djangbahevans/goerp/internal/cli/clierr"
	"github.com/spf13/cobra"
)

type devOptions struct {
	repo      string
	port      int
	adminPort int
	uiPort    int
	teardown  bool
}

func newDevCmd() *cobra.Command {
	var opts devOptions
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Run the local engine, shell and module with a ready-to-use dev tenant",
		Args:  clierr.WrapArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if environment := cmd.Flags().Lookup("env"); environment != nil && environment.Value.String() != "dev" {
				return clierr.Usage(fmt.Errorf("module dev requires --env dev"))
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			err := runDev(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
			if ctx.Err() != nil && errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		},
	}
	cmd.Flags().StringVar(&opts.repo, "repo", "", "GoERP source checkout (default: locate above the current directory)")
	cmd.Flags().IntVar(&opts.port, "port", 8080, "Engine HTTP port")
	cmd.Flags().IntVar(&opts.adminPort, "admin-port", 8081, "Engine loopback admin port")
	cmd.Flags().IntVar(&opts.uiPort, "ui-port", 5173, "Shell Vite port")
	cmd.Flags().BoolVar(&opts.teardown, "teardown", false, "Stop and remove the shared compose containers, keeping their volumes")

	return cmd
}

func runDev(ctx context.Context, stdout, stderr io.Writer, opts devOptions) error {
	moduleDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve module directory: %w", err)
	}
	repo, err := devRepo(moduleDir, opts.repo)
	if err != nil {
		return clierr.Usage(err)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	s := &devProcesses{ctx: ctx, cancel: cancel, stdout: stdout, stderr: stderr}
	defer s.stop()

	if opts.teardown {
		return s.compose(repo, stdout, "down")
	}
	if err := validateDevPorts(opts); err != nil {
		return clierr.Usage(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(moduleDir, "manifest.json"))
	if err != nil {
		return clierr.Usage(fmt.Errorf("read module manifest in the current directory: %w", err))
	}
	var manifest struct {
		Name          string           `json:"name"`
		WorkflowTypes []jsontext.Value `json:"workflow_types"`
		Analytics     []jsontext.Value `json:"analytics_projections"`
		Frontend      *struct {
			Bundle bool `json:"bundle"`
		} `json:"frontend"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil || manifest.Name == "" {
		return clierr.Usage(fmt.Errorf("manifest.json must contain a module name and valid JSON"))
	}
	if len(manifest.WorkflowTypes) > 0 || len(manifest.Analytics) > 0 {
		return clierr.Usage(fmt.Errorf("module dev does not yet support workflow_types or analytics_projections; run their infrastructure and engine separately"))
	}
	for _, tool := range []string{"docker", "go", "npm"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("module dev requires %s on PATH: %w", tool, err)
		}
	}
	for _, port := range []int{opts.port, opts.adminPort, opts.uiPort} {
		if err := checkDevPort(ctx, port); err != nil {
			return err
		}
	}
	if err := s.startInfra(repo); err != nil {
		return err
	}

	stateDir := filepath.Join(repo, ".dev", "module-dev")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create dev state directory: %w", err)
	}
	sessionDir, err := os.MkdirTemp(stateDir, "session-")
	if err != nil {
		return fmt.Errorf("create dev session directory: %w", err)
	}
	// Child processes must release their files before the session directory is removed.
	defer func() {
		s.stop()
		_ = os.RemoveAll(sessionDir)
	}()
	moduleDirOut := filepath.Join(sessionDir, "modules")
	if err := os.MkdirAll(moduleDirOut, 0o700); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(stdout, "Preparing the shell and building the module...")
	shellDir := filepath.Join(repo, "shell")
	if err := s.run(shellDir, nil, stdout, "npm", "ci", "--prefer-offline", "--no-audit", "--no-fund"); err != nil {
		return err
	}
	if err := s.run(shellDir, nil, stdout, "npm", "run", "build", "-w", "@goerp/sdk"); err != nil {
		return err
	}
	buildArgs := []string{"run", "./cmd/goerp", "module", "build", moduleDir, "--output", filepath.Join(moduleDirOut, "module.erp")}
	if manifest.Frontend == nil || !manifest.Frontend.Bundle {
		buildArgs = append(buildArgs, "--no-frontend")
	}
	if err := s.run(repo, nil, stdout, "go", buildArgs...); err != nil {
		return err
	}
	enginePath := filepath.Join(sessionDir, "engine")
	if err := s.run(repo, nil, stdout, "go", "build", "-o", enginePath, "./cmd/engine"); err != nil {
		return err
	}

	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return fmt.Errorf("generate dev admin token: %w", err)
	}
	adminToken := hex.EncodeToString(token[:])
	engineURL := "http://127.0.0.1:" + strconv.Itoa(opts.port)
	adminURL := "http://127.0.0.1:" + strconv.Itoa(opts.adminPort)
	uiURL := "http://dev.localhost:" + strconv.Itoa(opts.uiPort)
	engineEnv := devEngineEnv(repo, moduleDirOut, adminToken, opts)
	if _, err := s.start(repo, engineEnv, stdout, true, enginePath); err != nil {
		return err
	}
	if err := waitDevHTTP(ctx, engineURL+"/_ready", false); err != nil {
		return fmt.Errorf("wait for engine: %w", err)
	}
	client, err := adminclient.New(adminURL, adminToken, 2*time.Minute)
	if err != nil {
		return err
	}
	data, err := client.Post(ctx, "/admin/dev/bootstrap", struct {
		Module string `json:"module"`
	}{Module: manifest.Name})
	if err != nil {
		return fmt.Errorf("prepare dev tenant: %w", err)
	}
	var login struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(data, &login); err != nil {
		return fmt.Errorf("decode dev login: %w", err)
	}
	if login.Email == "" || login.Password == "" {
		return fmt.Errorf("dev bootstrap returned empty credentials")
	}
	uiEnv := append(os.Environ(), "GOERP_ENGINE_URL="+engineURL)
	if _, err := s.start(shellDir, uiEnv, stdout, true, "npm", "run", "dev", "-w", "@goerp/shell-app", "--", "--host", "127.0.0.1", "--port", strconv.Itoa(opts.uiPort), "--strictPort"); err != nil {
		return err
	}
	if err := waitDevHTTP(ctx, "http://127.0.0.1:"+strconv.Itoa(opts.uiPort)+"/", true); err != nil {
		return fmt.Errorf("wait for shell: %w", err)
	}
	if _, err := fmt.Fprintf(stdout, "\nModule %s is ready.\nURL: %s\nAdmin: %s\nPassword: %s\nCtrl-C stops the engine and shell; shared containers stay running.\n", manifest.Name, uiURL, login.Email, login.Password); err != nil {
		return err
	}

	<-ctx.Done()
	return context.Cause(ctx)
}

func devRepo(start, explicit string) (string, error) {
	dir := start
	if explicit != "" {
		var err error
		dir, err = filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
	}
	for {
		valid := true
		for _, path := range []string{"compose.dev.yml", "cmd/engine/main.go", "shell/package.json"} {
			if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
				valid = false
				break
			}
		}
		if valid {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if explicit != "" || parent == dir {
			return "", fmt.Errorf("GoERP source checkout not found; pass --repo /path/to/goerp")
		}
		dir = parent
	}
}

func validateDevPorts(opts devOptions) error {
	seen := make(map[int]bool)
	for _, port := range []int{opts.port, opts.adminPort, opts.uiPort} {
		if port < 1 || port > 65535 {
			return fmt.Errorf("dev ports must be between 1 and 65535")
		}
		if seen[port] {
			return fmt.Errorf("engine, admin and UI ports must be distinct (port %d is repeated)", port)
		}
		seen[port] = true
	}
	return nil
}

func devEngineEnv(repo, modules, token string, opts devOptions) []string {
	env := make([]string, 0, len(os.Environ())+16)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOERP_") {
			env = append(env, entry)
		}
	}
	return append(env,
		"GOERP_ENV=development",
		"GOERP_MODULE_DEV=true",
		"GOERP_LISTEN_ADDR=127.0.0.1:"+strconv.Itoa(opts.port),
		"GOERP_ADMIN_ADDR=127.0.0.1:"+strconv.Itoa(opts.adminPort),
		"GOERP_ADMIN_TOKEN="+token,
		"GOERP_DB_PRIMARY_DSN=postgres://engine_user:dev@localhost:6432/goerp_dev",
		"GOERP_DB_SCHEMA_SYNC_DSN=postgres://schema_sync_user:dev@localhost:15432/goerp_dev",
		"GOERP_REDIS_ADDR=localhost:6379",
		"GOERP_STORAGE_BACKEND=local",
		"GOERP_STORAGE_LOCAL_DIR="+filepath.Join(repo, "storage"),
		"GOERP_MODULE_DIR="+modules,
		"GOERP_COMPILATION_CACHE="+filepath.Join(repo, "wasm-cache"),
		"GOERP_PLATFORM_DOMAIN=localhost",
		"GOERP_APP_BASE_URL=http://localhost:"+strconv.Itoa(opts.uiPort),
		"GOERP_NOTIFICATION_SMTP_ALLOW_PRIVATE_HOSTS=true",
		"GOERP_SHUTDOWN_TIMEOUT=4s",
		"GOERP_SHUTDOWN_DRAIN_DELAY=0s",
	)
}

func waitDevHTTP(ctx context.Context, address string, html bool) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		if html {
			req.Header.Set("Accept", "text/html")
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(200 * time.Millisecond):
		}
	}
}
