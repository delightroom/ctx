package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/delightroom/ctx/internal/source"
	"github.com/delightroom/ctx/internal/tailnet"
)

type diagnostic struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func doctor(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("doctor takes flags only")
	}

	checks := diagnostics()
	if *asJSON {
		return json.NewEncoder(stdout).Encode(checks)
	}
	fmt.Fprintf(stdout, "ctx doctor (%s)\n\n", Version)
	for _, check := range checks {
		marker := "✓"
		if check.Status == "warning" {
			marker = "–"
		}
		if check.Status == "error" {
			marker = "✗"
		}
		fmt.Fprintf(stdout, "%s %s: %s\n", marker, check.Name, check.Detail)
		if check.Fix != "" {
			fmt.Fprintf(stdout, "  Fix: %s\n", check.Fix)
		}
	}
	return nil
}

func diagnostics() []diagnostic {
	checks := []diagnostic{{
		Name:   "Installation",
		Status: "ok",
		Detail: fmt.Sprintf("%s (%s)", Version, installationMethod()),
	}}

	if _, err := exec.LookPath("tailscale"); err != nil {
		checks = append(checks, diagnostic{
			Name:   "Tailscale",
			Status: "error",
			Detail: "command not found",
			Fix:    "Install and sign in to Tailscale, then rerun ctx doctor.",
		})
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		status, err := tailnet.ReadStatus(ctx)
		cancel()
		if err != nil || status.Self.DNSName == "" {
			checks = append(checks, diagnostic{
				Name:   "Tailscale",
				Status: "error",
				Detail: "installed but not connected",
				Fix:    "Connect Tailscale and verify that `tailscale status` succeeds.",
			})
		} else {
			checks = append(checks, diagnostic{
				Name:   "Tailscale",
				Status: "ok",
				Detail: fmt.Sprintf("connected as %s", tailnet.ShortName(status.Self)),
			})
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		checks = append(checks, diagnostic{
			Name: "Session discovery", Status: "warning", Detail: "current directory is unavailable",
		})
	} else {
		session, discoverErr := source.Discover(cwd)
		if discoverErr != nil {
			checks = append(checks, diagnostic{
				Name:   "Session discovery",
				Status: "warning",
				Detail: "no current Claude or Codex session found",
				Fix:    "Run ctx from an active agent workspace, or use ctx host --source SESSION.jsonl.",
			})
		} else {
			checks = append(checks, diagnostic{
				Name: "Session discovery", Status: "ok", Detail: session.Path(),
			})
		}
	}

	agents := installedAgents()
	if len(agents) == 0 {
		checks = append(checks, diagnostic{
			Name:   "Consumer agents",
			Status: "warning",
			Detail: "Claude Code and Codex were not found",
			Fix:    "Install an agent before using ctx continue; hosting and tailing still work.",
		})
	} else {
		checks = append(checks, diagnostic{
			Name: "Consumer agents", Status: "ok", Detail: strings.Join(agents, ", "),
		})
	}
	return checks
}

func installationMethod() string {
	if method := os.Getenv("CTX_INSTALL_METHOD"); method != "" {
		return method
	}
	executable, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	resolved, resolveErr := filepath.EvalSymlinks(executable)
	if resolveErr == nil {
		executable = resolved
	}
	lower := strings.ToLower(filepath.ToSlash(executable))
	if strings.Contains(lower, "/cellar/ctx/") || strings.Contains(lower, "/linuxbrew/") {
		return "homebrew"
	}
	if standaloneInstallMatches(executable) {
		return "standalone"
	}
	if Version == "dev" {
		return "development build"
	}
	return "manual"
}

func standaloneInstallMatches(executable string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	value, err := os.ReadFile(filepath.Join(dataHome, "ctx", "install-method"))
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(value)), "\n")
	if len(lines) != 2 || lines[0] != "standalone" {
		return false
	}
	recorded, err := filepath.Abs(lines[1])
	if err != nil {
		return false
	}
	if resolved, resolveErr := filepath.EvalSymlinks(recorded); resolveErr == nil {
		recorded = resolved
	}
	actual, err := filepath.Abs(executable)
	return err == nil && recorded == actual
}
