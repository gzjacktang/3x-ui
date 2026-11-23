package service

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// fakeXrayCore handles only the command lines the panel uses to run a core. It exits 99
// on any other, so a changed invocation fails the test.
func fakeXrayCore(version string, acceptsConfig bool) string {
	check := "exit 0"
	if !acceptsConfig {
		check = "echo 'Failed to start: fake core refuses this config'; exit 23"
	}
	return "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"-version) echo 'Xray " + version + " (fake)' ;;\n" +
		"'run -test -c '*) [ -s \"$4\" ] || exit 98; " + check + " ;;\n" +
		"'-c '*) exec sleep 60 ;;\n" +
		"*) exit 99 ;;\n" +
		"esac\n"
}

func xrayReleaseZip(t *testing.T, core string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("xray")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(core)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// xrayReleaseServer stands in for GitHub. It lists one version and serves asset with a
// matching .dgst, or a 404 when asset is nil.
func xrayReleaseServer(t *testing.T, version string, asset []byte) {
	t.Helper()
	sum := sha256.Sum256(asset)
	mux := http.NewServeMux()
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"tag_name":%q}]`, version)
	})
	mux.HandleFunc("/download/"+version+"/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case asset == nil:
			http.NotFound(w, r)
		case strings.HasSuffix(r.URL.Path, ".zip.dgst"):
			fmt.Fprintf(w, "SHA2-256= %s\n", hex.EncodeToString(sum[:]))
		case strings.HasSuffix(r.URL.Path, ".zip"):
			_, _ = w.Write(asset)
		default:
			http.NotFound(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	releases, download := xrayReleasesURL, xrayDownloadURL
	xrayReleasesURL, xrayDownloadURL = srv.URL+"/releases", srv.URL+"/download"
	t.Cleanup(func() { xrayReleasesURL, xrayDownloadURL = releases, download })
}

// startFakeCore runs the installed core on the panel's own config, as RestartXray
// would, and stops whichever core is current when the test ends.
func startFakeCore(t *testing.T) *xray.Process {
	t.Helper()
	cfg, err := (&XrayService{}).GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}
	p := xray.NewProcess(cfg)
	if err := p.Start(); err != nil {
		t.Fatalf("start fake core: %v", err)
	}
	previous := XrayProcess()
	previousManual := isManuallyStopped.Load()
	installTestXrayProcess(p)
	t.Cleanup(func() {
		if current := XrayProcess(); current != nil {
			_ = current.Stop()
		}
		_ = p.Stop()
		installTestXrayProcess(previous)
		isManuallyStopped.Store(previousManual)
		result = ""
	})
	return p
}

func TestUpdateXray(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake cores are shell scripts")
	}
	const current, next = "26.6.27", "26.7.1"
	installed := fakeXrayCore(current, true)

	for _, tc := range []struct {
		name        string
		candidate   string // the release's core; empty makes the download 404
		wantErr     string
		wantBinary  string
		wantVersion string
	}{
		{
			// The running core hosts the panel egress bridge. Stopping it first made the
			// download bypass the bridge, and when that failed every client stayed offline.
			name:        "failed download leaves the running core alone",
			wantErr:     "unexpected HTTP 404",
			wantBinary:  installed,
			wantVersion: current,
		},
		{
			name:        "core that refuses the config is not installed",
			candidate:   fakeXrayCore(next, false),
			wantErr:     "fake core refuses this config",
			wantBinary:  installed,
			wantVersion: current,
		},
		{
			name:        "accepted core replaces the running one",
			candidate:   fakeXrayCore(next, true),
			wantBinary:  fakeXrayCore(next, true),
			wantVersion: next,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupConflictDB(t)
			binFolder := t.TempDir()
			t.Setenv("XUI_BIN_FOLDER", binFolder)
			t.Setenv("XUI_LOG_FOLDER", t.TempDir())

			binary := filepath.Join(binFolder, xray.GetBinaryName())
			if err := os.WriteFile(binary, []byte(installed), 0o755); err != nil {
				t.Fatal(err)
			}
			running := startFakeCore(t)

			var asset []byte
			if tc.candidate != "" {
				asset = xrayReleaseZip(t, tc.candidate)
			}
			xrayReleaseServer(t, "v"+next, asset)

			err := (&ServerService{}).UpdateXray("v" + next)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("UpdateXray: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("UpdateXray error = %v, want it to contain %q", err, tc.wantErr)
			}
			if tc.wantErr != "" && (XrayProcess() != running || !running.IsRunning()) {
				t.Fatal("a failed update stopped or replaced the running core")
			}
			if got := XrayProcess().GetXrayVersion(); got != tc.wantVersion {
				t.Fatalf("running core version = %q, want %q", got, tc.wantVersion)
			}
			got, err := os.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.wantBinary {
				t.Fatalf("installed core:\n%s\nwant:\n%s", got, tc.wantBinary)
			}
			leftovers, err := filepath.Glob(filepath.Join(binFolder, ".*"))
			if err != nil || len(leftovers) > 0 {
				t.Fatalf("temp files left in the bin folder: %v (glob err %v)", leftovers, err)
			}
			markerPath := filepath.Join(binFolder, manualXrayMarkerFile)
			if tc.wantErr != "" {
				if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
					t.Fatalf("failed update wrote manual marker %s: stat err = %v", markerPath, err)
				}
			} else {
				wantSum := sha256.Sum256([]byte(tc.wantBinary))
				wantLine := fmt.Sprintf("%s  v%s\n", hex.EncodeToString(wantSum[:]), tc.wantVersion)
				markerBytes, err := os.ReadFile(markerPath)
				if err != nil {
					t.Fatalf("read manual xray marker: %v", err)
				}
				if string(markerBytes) != wantLine {
					t.Fatalf("manual xray marker = %q, want %q", string(markerBytes), wantLine)
				}
			}
		})
	}
}

func TestUpdateXrayAllowsManualDowngradeAndClearsBundledMarker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake cores are shell scripts")
	}
	setupConflictDB(t)
	binFolder := t.TempDir()
	t.Setenv("XUI_BIN_FOLDER", binFolder)
	t.Setenv("XUI_LOG_FOLDER", t.TempDir())

	installed := fakeXrayCore("26.10.31", true)
	downgraded := fakeXrayCore("26.7.28", true)
	binary := filepath.Join(binFolder, xray.GetBinaryName())
	if err := os.WriteFile(binary, []byte(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	bundledMarker := filepath.Join(binFolder, bundledXrayMarkerFile)
	if err := os.WriteFile(bundledMarker, []byte("deadbeef  xray-linux-amd64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	startFakeCore(t)
	xrayReleaseServer(t, "v26.7.28", xrayReleaseZip(t, downgraded))

	if err := (&ServerService{}).UpdateXray("v26.7.28"); err != nil {
		t.Fatalf("UpdateXray downgrade: %v", err)
	}
	if got := XrayProcess().GetXrayVersion(); got != "26.7.28" {
		t.Fatalf("running core version = %q, want %q", got, "26.7.28")
	}
	if _, err := os.Stat(bundledMarker); !os.IsNotExist(err) {
		t.Fatalf("bundled marker still exists after manual switch: stat err = %v", err)
	}
	wantSum := sha256.Sum256([]byte(downgraded))
	wantLine := fmt.Sprintf("%s  v26.7.28\n", hex.EncodeToString(wantSum[:]))
	gotMarker, err := os.ReadFile(filepath.Join(binFolder, manualXrayMarkerFile))
	if err != nil {
		t.Fatalf("read manual marker: %v", err)
	}
	if string(gotMarker) != wantLine {
		t.Fatalf("manual marker = %q, want %q", string(gotMarker), wantLine)
	}
}

func installTestXrayProcess(proc *xray.Process) {
	lock.Lock()
	p = proc
	lock.Unlock()
}
