package remoteaccess

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"tailscale.com/tsnet"
)

func productionNodeFactory(hostname, dir string) Node {
	node := &tsnetNode{}
	node.server = &tsnet.Server{
		Hostname: hostname,
		Dir:      dir,
		// Capture only a validated authorization URL. The manager obtains the
		// typed status first and uses this defensive callback as a fallback for
		// first-run LocalAPI states where AuthURL is not yet populated.
		UserLogf: func(format string, args ...any) {
			if authURL := extractAuthURL(fmt.Sprintf(format, args...)); authURL != "" {
				node.mu.Lock()
				node.authURL = authURL
				node.mu.Unlock()
			}
		},
	}
	return node
}

type tsnetNode struct {
	server  *tsnet.Server
	mu      sync.RWMutex
	authURL string
}

func (n *tsnetNode) Start() error { return n.server.Start() }

func (n *tsnetNode) Status(ctx context.Context) (*NodeStatus, error) {
	client, err := n.server.LocalClient()
	if err != nil {
		return nil, err
	}
	status, err := client.Status(ctx)
	if err != nil {
		return nil, err
	}
	n.mu.RLock()
	fallbackAuthURL := n.authURL
	n.mu.RUnlock()
	if status.AuthURL == "" {
		status.AuthURL = fallbackAuthURL
	}
	result := &NodeStatus{AuthURL: status.AuthURL}
	if status.BackendState == "Running" && status.Self != nil && !status.Self.UserID.IsZero() {
		result.Running = true
		result.DNSName = status.Self.DNSName
		result.OwnerID = status.Self.UserID.String()
	}
	return result, nil
}

func (n *tsnetNode) WhoIs(ctx context.Context, remoteAddr string) (string, error) {
	client, err := n.server.LocalClient()
	if err != nil {
		return "", err
	}
	who, err := client.WhoIs(ctx, remoteAddr)
	if err != nil || who == nil || who.UserProfile == nil || who.UserProfile.ID.IsZero() {
		return "", err
	}
	return who.UserProfile.ID.String(), nil
}

func (n *tsnetNode) ListenTLS(network, address string) (net.Listener, error) {
	return n.server.ListenTLS(network, address)
}

func (n *tsnetNode) ListenFunnel(network, address string) (net.Listener, error) {
	return n.server.ListenFunnel(network, address, tsnet.FunnelOnly())
}

func (n *tsnetNode) Close() error { return n.server.Close() }

func extractAuthURL(message string) string {
	for _, field := range strings.Fields(message) {
		candidate := strings.Trim(field, "\"'<>(),")
		if action := safeAuthAction(candidate); action != nil {
			return action.URL
		}
	}
	return ""
}
