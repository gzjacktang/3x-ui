// Package netpolicy defines the panel's TCP listener policy.
package netpolicy

import (
	"context"
	"net"
	"strings"
)

// ListenTCP uses ordinary TCP even if GODEBUG enables MPTCP. Disabling MPTCP
// mitigates the demonstrated CVE-2026-98432 exploit, not the underlying kernel bug.
func ListenTCP(ctx context.Context, address string) (net.Listener, error) {
	var lc net.ListenConfig
	lc.SetMultipathTCP(false)
	return lc.Listen(ctx, "tcp", address)
}

// ChildEnvironment disables implicit MPTCP in separately built Go children
// (Xray and mtg). Keep unrelated GODEBUG options and all other environment entries.
// An explicit SetMultipathTCP(true) in a child still overrides this default.
func ChildEnvironment(env []string) []string {
	result := make([]string, 0, len(env)+1)
	var options []string
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if key != "GODEBUG" {
			result = append(result, entry)
			continue
		}
		for option := range strings.SplitSeq(value, ",") {
			name, _, _ := strings.Cut(option, "=")
			if option != "" && name != "multipathtcp" {
				options = append(options, option)
			}
		}
	}
	options = append(options, "multipathtcp=0")
	return append(result, "GODEBUG="+strings.Join(options, ","))
}
