package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/util/netpolicy"
)

// xrayCheckTimeout bounds `run -test`, which loads the config and its geo files but opens
// no listeners, so it can run while the live core holds the ports.
const xrayCheckTimeout = 30 * time.Second

// CheckConfig runs the core at binaryPath in test mode against cfg. Updates call it first,
// so a core that rejects cfg never replaces the running one.
func CheckConfig(binaryPath string, cfg *Config) error {
	// The test run builds every outbound. A kernel-TUN WireGuard one would add a second wg
	// device and IPv6 policy rules to the host, and the run exits without removing the rules.
	checked := *cfg
	checked.OutboundConfigs, _ = ForceUserspaceWireGuardTun(cfg.OutboundConfigs)
	data, err := json.Marshal(&checked)
	if err != nil {
		return err
	}
	// CreateTemp creates the file as 0600; the config holds client credentials.
	file, err := os.CreateTemp(filepath.Dir(binaryPath), ".config-check-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), xrayCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "run", "-test", "-c", file.Name())
	cmd.Env = netpolicy.ChildEnvironment(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
