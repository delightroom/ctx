package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const installerURL = "https://ctx.droom.dev/install.sh"

func update(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dryRun := flags.Bool("dry-run", false, "show the update command without running it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("update takes flags only")
	}

	method := installationMethod()
	switch method {
	case "homebrew":
		fmt.Fprintln(stdout, "Updating ctx with Homebrew...")
		if *dryRun {
			fmt.Fprintln(stdout, "brew upgrade delightroom/tap/ctx")
			return nil
		}
		return runUpdateCommand(stdin, stdout, stderr, "brew", "upgrade", "delightroom/tap/ctx")
	case "standalone", "manual":
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		installDir := filepath.Dir(executable)
		fmt.Fprintf(stdout, "Updating the standalone ctx installation in %s...\n", installDir)
		if *dryRun {
			fmt.Fprintf(stdout, "curl -fsSL %s | sh -s -- --install-dir %s --no-modify-path\n",
				installerURL, installDir)
			return nil
		}
		script, err := downloadInstaller()
		if err != nil {
			return err
		}
		defer os.Remove(script)
		return runUpdateCommand(
			stdin, stdout, stderr,
			"sh", script, "--install-dir", installDir, "--no-modify-path",
		)
	default:
		fmt.Fprintf(stdout, "This is a %s and cannot be updated in place.\n", method)
		fmt.Fprintf(stdout, "Install a managed release with:\n  curl -fsSL %s | sh\n", installerURL)
		return nil
	}
}

func downloadInstaller() (string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Get(installerURL)
	if err != nil {
		return "", fmt.Errorf("download installer: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download installer: %s", response.Status)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil {
		return "", fmt.Errorf("read installer: %w", err)
	}
	if len(content) > 1024*1024 {
		return "", errors.New("installer is unexpectedly large")
	}
	if !strings.HasPrefix(string(content), "#!/bin/sh") {
		return "", errors.New("downloaded installer has an unexpected format")
	}
	handle, err := os.CreateTemp("", "ctx-install-*.sh")
	if err != nil {
		return "", err
	}
	path := handle.Name()
	if _, err := handle.Write(content); err != nil {
		handle.Close()
		os.Remove(path)
		return "", err
	}
	if err := handle.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func runUpdateCommand(
	stdin io.Reader,
	stdout, stderr io.Writer,
	name string,
	args ...string,
) error {
	command := exec.Command(name, args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s update failed: %w", name, err)
	}
	return nil
}
