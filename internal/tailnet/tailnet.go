package tailnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
)

const ServePort = 8443

type Peer struct {
	HostName string
	DNSName  string
	Online   bool
}

type Status struct {
	Self Peer
	Peer map[string]Peer
}

func ReadStatus(ctx context.Context) (Status, error) {
	command := exec.CommandContext(ctx, "tailscale", "status", "--json")
	output, err := command.Output()
	if err != nil {
		return Status{}, fmt.Errorf("tailscale status: %w", err)
	}
	var raw struct {
		Self struct {
			HostName string `json:"HostName"`
			DNSName  string `json:"DNSName"`
			Online   bool   `json:"Online"`
		} `json:"Self"`
		Peer map[string]struct {
			HostName string `json:"HostName"`
			DNSName  string `json:"DNSName"`
			Online   bool   `json:"Online"`
		} `json:"Peer"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return Status{}, fmt.Errorf("decode tailscale status: %w", err)
	}
	status := Status{
		Self: Peer{HostName: raw.Self.HostName, DNSName: trimDNS(raw.Self.DNSName), Online: raw.Self.Online},
		Peer: make(map[string]Peer, len(raw.Peer)),
	}
	for key, peer := range raw.Peer {
		status.Peer[key] = Peer{
			HostName: peer.HostName,
			DNSName:  trimDNS(peer.DNSName),
			Online:   peer.Online,
		}
	}
	return status, nil
}

func ResolveHost(ctx context.Context, host string) (string, error) {
	if strings.Contains(host, "://") {
		parsed, err := url.Parse(host)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/"), nil
	}
	if strings.HasPrefix(host, "localhost:") || strings.HasPrefix(host, "127.0.0.1:") {
		return "http://" + host, nil
	}
	status, err := ReadStatus(ctx)
	if err != nil {
		return "", err
	}
	if matches(status.Self, host) {
		return fmt.Sprintf("https://%s:%d", status.Self.DNSName, ServePort), nil
	}
	for _, peer := range status.Peer {
		if peer.Online && matches(peer, host) {
			return fmt.Sprintf("https://%s:%d", peer.DNSName, ServePort), nil
		}
	}
	if strings.Contains(host, ".") {
		return fmt.Sprintf("https://%s:%d", strings.TrimSuffix(host, "."), ServePort), nil
	}
	return "", fmt.Errorf("tailnet host %q not found", host)
}

func OnlinePeerURLs(ctx context.Context) ([]string, error) {
	status, err := ReadStatus(ctx)
	if err != nil {
		return nil, err
	}
	return OnlinePeerURLsFromStatus(status), nil
}

func OnlinePeerURLsFromStatus(status Status) []string {
	var result []string
	if status.Self.DNSName != "" {
		result = append(result, fmt.Sprintf("https://%s:%d", status.Self.DNSName, ServePort))
	}
	for _, peer := range status.Peer {
		if peer.Online && peer.DNSName != "" {
			result = append(result, fmt.Sprintf("https://%s:%d", peer.DNSName, ServePort))
		}
	}
	return result
}

func ShortName(peer Peer) string {
	if peer.DNSName != "" {
		name, _, _ := strings.Cut(peer.DNSName, ".")
		return name
	}
	return peer.HostName
}

func Serve(ctx context.Context, localAddress string, stdout, stderr io.Writer) error {
	target := "http://" + localAddress
	command := exec.CommandContext(
		ctx, "tailscale", "serve", "--yes", fmt.Sprintf("--https=%d", ServePort), target,
	)
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("tailscale serve: %w", err)
	}
	return nil
}

func matches(peer Peer, host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	dns := strings.TrimSuffix(strings.ToLower(peer.DNSName), ".")
	name := strings.ToLower(peer.HostName)
	return host == name || host == dns || strings.HasPrefix(dns, host+".")
}

func trimDNS(value string) string {
	return strings.TrimSuffix(value, ".")
}
