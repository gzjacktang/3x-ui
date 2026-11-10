package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func upgradeScriptFunction(t *testing.T, file, name string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	start := strings.Index(source, name+"() {")
	if start < 0 {
		t.Fatalf("%s: missing %s", file, name)
	}
	end := strings.Index(source[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("%s: unterminated %s", file, name)
	}
	return source[start : start+end+3]
}

func upgradeShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func runUpgradeShell(t *testing.T, source string, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "-c", source)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestUpgradeChecksumVerification(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "panel.tar.gz")
	payload := []byte("panel test payload")
	if err := os.WriteFile(archive, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	for _, file := range []string{"install.sh", "update.sh"} {
		for _, tc := range []struct {
			name, checksum, downloadFail string
			ok                           bool
		}{
			{"valid", digest + "  panel.tar.gz", "0", true},
			{"uppercase", strings.ToUpper(digest) + "  panel.tar.gz", "0", true},
			{"mismatch", strings.Repeat("0", 64), "0", false},
			{"malformed", "not-a-checksum", "0", false},
			{"missing", "", "1", false},
			{"empty", "", "0", false},
		} {
			t.Run(file+"/"+tc.name, func(t *testing.T) {
				source := upgradeScriptFunction(t, file, "verify_release_archive") + `
download_release() {
    local output
    while [[ $# -gt 0 ]]; do
        case "$1" in -o) output="$2"; shift 2 ;; *) shift ;; esac
    done
    [[ "$DOWNLOAD_FAIL" != "1" ]] || return 22
    printf '%s\n' "$CHECKSUM_TEXT" > "$output"
}
if ! command -v sha256sum >/dev/null; then sha256sum() { shasum -a 256 "$@"; }; fi
verify_release_archive "$TEST_ARCHIVE" https://example.invalid/panel.tar.gz
`
				out, err := runUpgradeShell(t, source, "TEST_ARCHIVE="+archive, "CHECKSUM_TEXT="+tc.checksum, "DOWNLOAD_FAIL="+tc.downloadFail)
				if (err == nil) != tc.ok {
					t.Fatalf("success=%v, want %v: %s", err == nil, tc.ok, out)
				}
				if _, err := os.Stat(archive); err != nil {
					t.Fatal("verification removed or changed the current installation input")
				}
			})
		}
	}
}

func TestUpgradeArchitectureRejectsUnsupportedMachines(t *testing.T) {
	for _, file := range []string{"install.sh", "update.sh"} {
		for _, machine := range []string{"x86_64", "aarch64", "i686", "armv7l", "s390x"} {
			t.Run(file+"/"+machine, func(t *testing.T) {
				source := upgradeScriptFunction(t, file, "arch") + "\nuname() { printf '%s\\n' \"$TEST_MACHINE\"; }; arch\n"
				out, err := runUpgradeShell(t, source, "TEST_MACHINE="+machine)
				if machine == "x86_64" {
					if err != nil || strings.TrimSpace(out) != "amd64" {
						t.Fatalf("amd64 rejected: %s (%v)", out, err)
					}
				} else if err == nil {
					t.Fatalf("unsupported machine %q accepted", machine)
				}
			})
		}
	}
}

func TestUpgradePreservesShortPathsAndCertificateSettings(t *testing.T) {
	for _, path := range []string{"/a/", "/ab/", "/custom-panel/"} {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			binary := `#!/bin/bash
case "$*" in
  'setting -show true') printf 'hasDefaultCredential: false\nport: 2053\nwebBasePath: %s\n' "$TEST_BASE_PATH" ;;
  'setting -getCert true') printf 'cert: \n' ;;
  *) printf '%s\n' "$*" >> "$TEST_CHANGES"; exit 1 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "x-ui"), []byte(binary), 0o700); err != nil {
				t.Fatal(err)
			}
			changes := filepath.Join(dir, "changes")
			source := upgradeScriptFunction(t, "update.sh", "config_after_update") + "\nxui_folder=" + upgradeShellQuote(dir) + "\nconfig_after_update\n"
			out, err := runUpgradeShell(t, source, "TEST_BASE_PATH="+path, "TEST_CHANGES="+changes)
			if err != nil {
				t.Fatalf("config update failed: %s (%v)", out, err)
			}
			if _, err := os.Stat(changes); !os.IsNotExist(err) {
				t.Fatal("existing settings or certificates were changed")
			}
			if !strings.Contains(out, ":2053"+path) {
				t.Fatalf("short path was not preserved: %s", out)
			}
		})
	}
}

func makeUpgradeArchive(t *testing.T, dir string) string {
	t.Helper()
	file := filepath.Join(dir, "candidate.tar.gz")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		"x-ui/x-ui": `#!/bin/bash
case "$*" in
  migrate) echo migrate >> "$TEST_ACTIONS"; exit "$TEST_MIGRATION_EXIT" ;;
  'setting -show true') printf 'hasDefaultCredential: false\nport: 2053\nwebBasePath: /a/\n' ;;
  'setting -getCert true') printf 'cert: \n' ;;
  *) exit 99 ;;
esac
`,
		"x-ui/x-ui.sh":              "#!/bin/bash\nexit 0\n",
		"x-ui/bin/xray-linux-amd64": "test xray",
		"x-ui/x-ui.service.debian":  "[Service]\n",
	}
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	for _, close := range []func() error{tw.Close, gz.Close, f.Close} {
		if err := close(); err != nil {
			t.Fatal(err)
		}
	}
	return file
}

func TestUpgradeFailuresAndMigrationOrdering(t *testing.T) {
	for _, tc := range []struct {
		name, downloadFail, migrationExit string
		wantSuccess                       bool
	}{
		{"download-fails-before-stop", "1", "0", false},
		{"checksum-fails-before-stop", "2", "0", false},
		{"migration-fails-before-start", "0", "1", false},
		{"success-preserves-custom-bin", "0", "0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			archive := makeUpgradeArchive(t, dir)
			payload, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			panel := filepath.Join(dir, "x-ui")
			if err := os.MkdirAll(filepath.Join(panel, "bin"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(panel, "x-ui"), []byte("#!/bin/bash\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			custom := filepath.Join(panel, "bin", "custom.dat")
			if err := os.WriteFile(custom, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			actions := filepath.Join(dir, "actions")
			// Redirect only the fixed system CLI path into this test's private directory.
			// All download and service commands below are local stubs; no root access.
			update := strings.ReplaceAll(upgradeScriptFunction(t, "update.sh", "update_x-ui"), "/usr/bin/x-ui", filepath.Join(dir, "cli"))
			source := upgradeScriptFunction(t, "update.sh", "verify_release_archive") + "\n" + upgradeScriptFunction(t, "update.sh", "config_after_update") + "\n" + update + `
_fail() { echo "$*" >&2; exit 2; }
load_xui_env() { :; }
setup_fail2ban() { :; }
_install_xui_service_unit() { :; }
chown() { :; }
systemctl() { echo "systemctl $*" >> "$TEST_ACTIONS"; }
download_release() {
    local output url
    [[ "$TEST_DOWNLOAD_FAIL" != "1" ]] || return 22
    while [[ $# -gt 0 ]]; do
        case "$1" in -o) output="$2"; shift 2 ;; *) url="$1"; shift ;; esac
    done
    if [[ "$url" == *.sha256 ]]; then
        [[ "$TEST_DOWNLOAD_FAIL" != "2" ]] || return 22
        printf '%s  panel.tar.gz\n' "$TEST_DIGEST" > "$output"
    else
        command cp "$TEST_ARCHIVE" "$output"
    fi
}
if ! command -v sha256sum >/dev/null; then sha256sum() { shasum -a 256 "$@"; }; fi
release=debian
xui_repo=gzjacktang/3x-ui
xui_github=https://example.invalid
xui_raw=https://example.invalid
xui_stage_dir=""
update_x-ui
`
			out, err := runUpgradeShell(t, source, "XUI_UPDATE_TAG=dev-latest", "xui_folder="+panel, "TEST_ACTIONS="+actions, "TEST_ARCHIVE="+archive,
				"TEST_DIGEST="+fmt.Sprintf("%x", sha256.Sum256(payload)), "TEST_DOWNLOAD_FAIL="+tc.downloadFail, "TEST_MIGRATION_EXIT="+tc.migrationExit)
			if (err == nil) != tc.wantSuccess {
				t.Fatalf("success=%v, want %v: %s (%v)", err == nil, tc.wantSuccess, out, err)
			}
			log, _ := os.ReadFile(actions)
			if tc.downloadFail != "0" && len(log) != 0 {
				t.Fatalf("download/checksum failure touched running service: %s", log)
			}
			if tc.migrationExit != "0" && strings.Contains(string(log), "systemctl start") {
				t.Fatalf("started service after failed migration: %s", log)
			}
			if tc.wantSuccess {
				if !strings.Contains(string(log), "systemctl stop x-ui\nmigrate\nsystemctl daemon-reload\nsystemctl enable x-ui\nsystemctl start x-ui") {
					t.Fatalf("wrong migration/start order: %s", log)
				}
			}
			if content, err := os.ReadFile(custom); err != nil || string(content) != "keep" {
				t.Fatal("custom bin file was lost")
			}
		})
	}
}
