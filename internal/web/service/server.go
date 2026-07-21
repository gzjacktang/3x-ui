package service

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	stdnet "net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"github.com/google/uuid"
	utls "github.com/refraction-networking/utls"
)

// Release represents information about a software release from GitHub.
type Release struct {
	TagName         string `json:"tag_name"`         // The tag name of the release
	Body            string `json:"body"`             // The release notes; the dev channel reads its commit from here
	TargetCommitish string `json:"target_commitish"` // The branch/commit the tag points at
	Prerelease      bool   `json:"prerelease"`       // Whether this is a pre-release
}

// ServerService provides business logic for server monitoring and management.
// It handles system status collection, IP detection, and application statistics.
type ServerService struct {
	xrayService    XrayService
	inboundService InboundService
	settingService SettingService
	cachedIPv4     string
	cachedIPv6     string
	noIPv6         bool
	mu             sync.Mutex

	versionsCacheMu sync.Mutex
	versionsCache   *cachedXrayVersions

	fail2banMu        sync.Mutex
	fail2banInstalled bool
	fail2banCheckedAt time.Time
}

type cachedXrayVersions struct {
	versions  []string
	fetchedAt time.Time
}

// xrayVersionsCacheTTL bounds how often /getXrayVersion hits GitHub. The list
// is purely informational (rendered in the "switch Xray version" picker) so a
// quarter-hour staleness window is fine and saves the API budget.
const xrayVersionsCacheTTL = 15 * time.Minute

// Fail2banStatus tells the frontend whether the per-client IP limit can
// actually be enforced. Enforcement depends on fail2ban, so a limit set
// without it would silently do nothing.
type Fail2banStatus struct {
	Enabled   bool `json:"enabled"`
	Installed bool `json:"installed"`
	Usable    bool `json:"usable"`
	Windows   bool `json:"windows"`
}

const fail2banInstalledCacheTTL = 30 * time.Second

func (s *ServerService) GetFail2banStatus() Fail2banStatus {
	enabled := isFail2banEnabled()

	installed := false
	if enabled {
		installed = s.isFail2banInstalled()
	}

	return Fail2banStatus{
		Enabled:   enabled,
		Installed: installed,
		Usable:    enabled && installed,
		Windows:   runtime.GOOS == "windows",
	}
}

func isFail2banEnabled() bool {
	value, ok := os.LookupEnv("XUI_ENABLE_FAIL2BAN")
	return !ok || value == "true"
}

func (s *ServerService) isFail2banInstalled() bool {
	s.fail2banMu.Lock()
	defer s.fail2banMu.Unlock()

	if !s.fail2banCheckedAt.IsZero() && time.Since(s.fail2banCheckedAt) < fail2banInstalledCacheTTL {
		return s.fail2banInstalled
	}

	err := exec.CommandContext(context.Background(), "fail2ban-client", "-h").Run()
	s.fail2banInstalled = err == nil
	s.fail2banCheckedAt = time.Now()
	return s.fail2banInstalled
}

// GetXrayVersionsCached wraps GetXrayVersions with a TTL cache. On fetch
// failure we serve the last successful list (if any) so the UI doesn't go
// blank during a GitHub API hiccup; if there's no cache at all the underlying
// error is surfaced.
func (s *ServerService) GetXrayVersionsCached() ([]string, error) {
	s.versionsCacheMu.Lock()
	cache := s.versionsCache
	s.versionsCacheMu.Unlock()
	if cache != nil && time.Since(cache.fetchedAt) <= xrayVersionsCacheTTL {
		return cache.versions, nil
	}
	versions, err := s.GetXrayVersions()
	if err != nil {
		if cache != nil {
			logger.Warning("GetXrayVersionsCached: serving stale list:", err)
			return cache.versions, nil
		}
		return nil, err
	}
	s.versionsCacheMu.Lock()
	s.versionsCache = &cachedXrayVersions{versions: versions, fetchedAt: time.Now()}
	s.versionsCacheMu.Unlock()
	return versions, nil
}

// GetDefaultLogOutboundTags scans the default Xray config for freedom and
// blackhole outbound tags so /getXrayLogs can colour-code log lines without
// the controller re-doing the JSON walk. Falls back to the historical
// "direct"/"blocked" defaults when the config can't be read.
func (s *ServerService) GetDefaultLogOutboundTags() (freedoms, blackholes []string) {
	config, err := s.settingService.GetDefaultXrayConfig()
	if err == nil && config != nil {
		if cfgMap, ok := config.(map[string]any); ok {
			if outbounds, ok := cfgMap["outbounds"].([]any); ok {
				for _, outbound := range outbounds {
					obMap, ok := outbound.(map[string]any)
					if !ok {
						continue
					}
					tag, _ := obMap["tag"].(string)
					if tag == "" {
						continue
					}
					switch obMap["protocol"] {
					case "freedom":
						freedoms = append(freedoms, tag)
					case "blackhole":
						blackholes = append(blackholes, tag)
					}
				}
			}
		}
	}
	if len(freedoms) == 0 {
		freedoms = []string{"direct"}
	}
	if len(blackholes) == 0 {
		blackholes = []string{"blocked"}
	}
	return freedoms, blackholes
}

type LogEntry struct {
	DateTime    time.Time
	FromAddress string
	ToAddress   string
	Inbound     string
	Outbound    string
	Email       string
	Event       int
}

func getPublicIP(url string) string {
	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if reqErr != nil {
		return "N/A"
	}
	resp, err := client.Do(req)
	if err != nil {
		return "N/A"
	}
	defer resp.Body.Close()

	// Don't retry if access is blocked or region-restricted
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnavailableForLegalReasons {
		return "N/A"
	}
	if resp.StatusCode != http.StatusOK {
		return "N/A"
	}

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "N/A"
	}

	ipString := strings.TrimSpace(string(ip))
	if ipString == "" {
		return "N/A"
	}

	return ipString
}

var publicIPv4Services = []string{
	"https://api4.ipify.org",
	"https://ipv4.icanhazip.com",
	"https://v4.api.ipinfo.io/ip",
	"https://ipv4.myexternalip.com/raw",
	"https://4.ident.me",
	"https://check-host.net/ip",
}

var publicIPv6Services = []string{
	"https://api6.ipify.org",
	"https://ipv6.icanhazip.com",
	"https://v6.api.ipinfo.io/ip",
	"https://ipv6.myexternalip.com/raw",
	"https://6.ident.me",
}

// resolvePublicIPs caches the public IPv4/IPv6 addresses on first use. Guarded
// by s.mu because the bot's ServerService may call it from sendBackup while a
// status report runs concurrently.
func (s *ServerService) resolvePublicIPs() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cachedIPv4 == "" {
		for _, ip4Service := range publicIPv4Services {
			s.cachedIPv4 = getPublicIP(ip4Service)
			if s.cachedIPv4 != "N/A" {
				break
			}
		}
	}

	if s.cachedIPv6 == "" && !s.noIPv6 {
		for _, ip6Service := range publicIPv6Services {
			s.cachedIPv6 = getPublicIP(ip6Service)
			if s.cachedIPv6 != "N/A" {
				break
			}
		}
	}

	if s.cachedIPv6 == "N/A" {
		s.noIPv6 = true
	}
}

const (
	maxXrayArchiveBytes = 200 << 20
	maxXrayBinaryBytes  = 200 << 20
	// maxXrayDigestBytes caps the .dgst checksum sidecar read; it is a few
	// hundred bytes in practice.
	maxXrayDigestBytes = 64 << 10
)

func (s *ServerService) GetXrayVersions() ([]string, error) {
	const (
		XrayURL    = "https://api.github.com/repos/XTLS/Xray-core/releases"
		bufferSize = 8192
	)

	req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, XrayURL, nil)
	if reqErr != nil {
		return nil, reqErr
	}
	resp, err := s.settingService.NewProxiedHTTPClient(10 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Check HTTP status code - GitHub API returns object instead of array on error
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		var errorResponse struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(bodyBytes, &errorResponse) == nil && errorResponse.Message != "" {
			return nil, fmt.Errorf("GitHub API error: %s", errorResponse.Message)
		}
		return nil, fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, resp.Status)
	}

	buffer := bytes.NewBuffer(make([]byte, bufferSize))
	buffer.Reset()
	if _, err := buffer.ReadFrom(resp.Body); err != nil {
		return nil, err
	}

	var releases []Release
	if err := json.Unmarshal(buffer.Bytes(), &releases); err != nil {
		return nil, err
	}

	var versions []string
	for _, release := range releases {
		tagVersion := strings.TrimPrefix(release.TagName, "v")
		tagParts := strings.Split(tagVersion, ".")
		if len(tagParts) != 3 {
			continue
		}

		major, err1 := strconv.Atoi(tagParts[0])
		minor, err2 := strconv.Atoi(tagParts[1])
		patch, err3 := strconv.Atoi(tagParts[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}

		if major > 26 || (major == 26 && minor > 6) || (major == 26 && minor == 6 && patch >= 27) {
			versions = append(versions, release.TagName)
		}
	}
	return versions, nil
}

func (s *ServerService) StopXrayService() error {
	err := s.xrayService.StopXray()
	if err != nil {
		logger.Error("stop xray failed:", err)
		return err
	}
	return nil
}

func (s *ServerService) RestartXrayService() error {
	err := s.xrayService.RestartXray(true)
	if err != nil {
		logger.Error("start xray failed:", err)
		return err
	}
	return nil
}

func (s *ServerService) downloadXRay(version string) (string, error) {
	osName := runtime.GOOS
	arch := runtime.GOARCH

	switch osName {
	case "darwin":
		osName = "macos"
	case "windows":
		osName = "windows"
	}

	switch arch {
	case "amd64":
		arch = "64"
	case "arm64":
		arch = "arm64-v8a"
	case "armv7":
		arch = "arm32-v7a"
	case "armv6":
		arch = "arm32-v6"
	case "armv5":
		arch = "arm32-v5"
	case "386":
		arch = "32"
	case "s390x":
		arch = "s390x"
	}

	fileName := fmt.Sprintf("Xray-%s-%s.zip", osName, arch)
	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/%s/%s", version, fileName)
	client := s.settingService.NewProxiedHTTPClient(60 * time.Second)
	req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if reqErr != nil {
		return "", reqErr
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download xray: unexpected HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxXrayArchiveBytes {
		return "", fmt.Errorf("download xray: archive exceeds %d bytes", maxXrayArchiveBytes)
	}

	file, err := os.CreateTemp("", "xray-*.zip")
	if err != nil {
		return "", err
	}
	path := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()

	n, err := io.Copy(file, io.LimitReader(resp.Body, maxXrayArchiveBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxXrayArchiveBytes {
		return "", fmt.Errorf("download xray: archive exceeds %d bytes", maxXrayArchiveBytes)
	}

	// Verify the archive against the SHA2-256 published in the release's .dgst
	// sidecar before installing it. TLS protects the transport, not the artifact;
	// a corrupted or tampered asset must not be installed and run as xray.
	want, err := s.fetchXrayDigestSHA256(client, url+".dgst")
	if err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); !strings.EqualFold(got, want) {
		// User-facing warning: the archive's SHA-256 does not match the official
		// release checksum, so the download is corrupted or has been tampered
		// with. Abort the install so a bad binary is never run, and tell the user
		// to retry/re-download rather than proceed with a mismatched image.
		return "", fmt.Errorf("Xray update aborted: the downloaded archive does not match the official SHA-256 checksum, so the image is corrupted or differs from the official release. Please exit and re-download the official image, then try again (expected %s, got %s)", want, got)
	}

	ok = true
	return path, nil
}

// fetchXrayDigestSHA256 downloads the .dgst sidecar XTLS publishes next to each
// release asset and returns the SHA2-256 hex digest it lists.
func (s *ServerService) fetchXrayDigestSHA256(client *http.Client, dgstURL string) (string, error) {
	req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, dgstURL, nil)
	if reqErr != nil {
		return "", fmt.Errorf("download xray checksum: %w", reqErr)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download xray checksum: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download xray checksum: unexpected HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxXrayDigestBytes))
	if err != nil {
		return "", fmt.Errorf("download xray checksum: %w", err)
	}
	return parseXrayDigestSHA256(raw)
}

// parseXrayDigestSHA256 extracts the lowercase SHA2-256 hex from an XTLS .dgst
// file, whose lines are "ALGO= <hex>" (the relevant one being "SHA2-256= ...").
func parseXrayDigestSHA256(dgst []byte) (string, error) {
	for line := range strings.SplitSeq(string(dgst), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "SHA2-256=")
		if !ok {
			continue
		}
		h := strings.ToLower(strings.TrimSpace(rest))
		if len(h) != 64 {
			return "", fmt.Errorf("xray checksum: malformed SHA2-256 entry in digest")
		}
		return h, nil
	}
	return "", fmt.Errorf("xray checksum: no SHA2-256 entry in digest")
}

func (s *ServerService) UpdateXray(version string) error {
	versions, err := s.GetXrayVersions()
	if err != nil {
		return err
	}
	if !slices.Contains(versions, version) {
		return fmt.Errorf("xray version %q is not in the fetched release list", version)
	}

	// 1. Stop xray before doing anything
	if err := s.StopXrayService(); err != nil {
		logger.Warning("failed to stop xray before update:", err)
	}

	// 2. Download the zip
	zipFileName, err := s.downloadXRay(version)
	if err != nil {
		return err
	}
	defer os.Remove(zipFileName)

	zipFile, err := os.Open(zipFileName)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	stat, err := zipFile.Stat()
	if err != nil {
		return err
	}
	reader, err := zip.NewReader(zipFile, stat.Size())
	if err != nil {
		return err
	}

	// 3. Helper to extract files
	copyZipFile := func(zipName string, fileName string) error {
		zipFile, err := reader.Open(zipName)
		if err != nil {
			return err
		}
		defer zipFile.Close()
		if err := os.MkdirAll(filepath.Dir(fileName), 0o755); err != nil {
			return err
		}
		tmpFile, err := os.CreateTemp(filepath.Dir(fileName), ".xray-*")
		if err != nil {
			return err
		}
		tmpPath := tmpFile.Name()
		ok := false
		defer func() {
			_ = tmpFile.Close()
			if !ok {
				_ = os.Remove(tmpPath)
			}
		}()
		n, err := io.Copy(tmpFile, io.LimitReader(zipFile, maxXrayBinaryBytes+1))
		if err != nil {
			return err
		}
		if n > maxXrayBinaryBytes {
			return fmt.Errorf("xray binary exceeds %d bytes", maxXrayBinaryBytes)
		}
		if err := tmpFile.Chmod(0o755); err != nil {
			return err
		}
		if err := tmpFile.Close(); err != nil {
			return err
		}
		if runtime.GOOS == "windows" {
			_ = os.Remove(fileName)
		}
		if err := os.Rename(tmpPath, fileName); err != nil {
			return err
		}
		ok = true
		return nil
	}

	// 4. Extract correct binary
	if runtime.GOOS == "windows" {
		targetBinary := filepath.Join(config.GetBinFolderPath(), "xray-windows-amd64.exe")
		err = copyZipFile("xray.exe", targetBinary)
	} else {
		err = copyZipFile("xray", xray.GetBinaryPath())
	}
	if err != nil {
		return err
	}

	// 5. Restart xray
	if err := s.xrayService.RestartXray(true); err != nil {
		logger.Error("start xray failed:", err)
		return err
	}

	return nil
}

func (s *ServerService) GetLogs(count string, level string, syslog string) []string {
	c, _ := strconv.Atoi(count)
	var lines []string

	if syslog == "true" {
		// Check if running on Windows - journalctl is not available
		if runtime.GOOS == "windows" {
			return []string{"Syslog is not supported on Windows. Please use application logs instead by unchecking the 'Syslog' option."}
		}

		// Validate and sanitize count parameter
		countInt, err := strconv.Atoi(count)
		if err != nil || countInt < 1 || countInt > 10000 {
			return []string{"Invalid count parameter - must be a number between 1 and 10000"}
		}

		// Validate level parameter - only allow valid syslog levels
		validLevels := map[string]bool{
			"0": true, "emerg": true,
			"1": true, "alert": true,
			"2": true, "crit": true,
			"3": true, "err": true,
			"4": true, "warning": true,
			"5": true, "notice": true,
			"6": true, "info": true,
			"7": true, "debug": true,
		}
		if !validLevels[level] {
			return []string{"Invalid level parameter - must be a valid syslog level"}
		}

		// Use hardcoded command with validated parameters
		cmd := exec.CommandContext(context.Background(), "journalctl", "-u", "x-ui", "--no-pager", "-n", strconv.Itoa(countInt), "-p", level)
		var out bytes.Buffer
		cmd.Stdout = &out
		err = cmd.Run()
		if err != nil {
			return []string{"Failed to run journalctl command! Make sure systemd is available and x-ui service is registered."}
		}
		lines = strings.Split(out.String(), "\n")
	} else {
		lines = logger.GetLogs(c, level)
	}

	return lines
}

// parseAccessLogFields extracts the structured fields from one Xray access-log
// line. Lines are attacker-influenced (a client's requested destination lands in
// the log verbatim) and may be truncated, so every positional lookup is length
// guarded: a malformed line yields a partial entry rather than panicking.
func parseAccessLogFields(line string) LogEntry {
	var entry LogEntry
	parts := strings.Fields(line)

	for i, part := range parts {

		if i == 0 && len(parts) > 1 {
			dateTime, err := time.ParseInLocation("2006/01/02 15:04:05.999999", parts[0]+" "+parts[1], time.Local)
			if err != nil {
				continue
			}
			entry.DateTime = dateTime.UTC()
		}

		if part == "from" && i+1 < len(parts) {
			entry.FromAddress = strings.TrimLeft(parts[i+1], "/")
		} else if part == "accepted" && i+1 < len(parts) {
			entry.ToAddress = strings.TrimLeft(parts[i+1], "/")
		} else if strings.HasPrefix(part, "[") {
			entry.Inbound = part[1:]
		} else if strings.HasSuffix(part, "]") {
			entry.Outbound = part[:len(part)-1]
		} else if part == "email:" && i+1 < len(parts) {
			entry.Email = parts[i+1]
		}
	}

	return entry
}

func (s *ServerService) GetXrayLogs(
	count string,
	filter string,
	showDirect string,
	showBlocked string,
	showProxy string,
	freedoms []string,
	blackholes []string,
) []LogEntry {
	const (
		Direct = iota
		Blocked
		Proxied
	)

	countInt, _ := strconv.Atoi(count)
	var entries []LogEntry

	pathToAccessLog, err := xray.GetAccessLogPath()
	if err != nil {
		return nil
	}

	file, err := os.Open(pathToAccessLog)
	if err != nil {
		return nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.Contains(line, "api -> api") {
			// skipping empty lines and api calls
			continue
		}

		if filter != "" && !strings.Contains(line, filter) {
			// applying filter if it's not empty
			continue
		}

		entry := parseAccessLogFields(line)

		if logEntryContains(line, freedoms) {
			if showDirect == "false" {
				continue
			}
			entry.Event = Direct
		} else if logEntryContains(line, blackholes) {
			if showBlocked == "false" {
				continue
			}
			entry.Event = Blocked
		} else {
			if showProxy == "false" {
				continue
			}
			entry.Event = Proxied
		}

		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil
	}

	if len(entries) > countInt {
		entries = entries[len(entries)-countInt:]
	}

	return entries
}

func logEntryContains(line string, suffixes []string) bool {
	for _, sfx := range suffixes {
		if strings.Contains(line, sfx+"]") {
			return true
		}
	}
	return false
}

func (s *ServerService) GetConfigJson() (any, error) {
	config, err := s.xrayService.GetXrayConfig()
	if err != nil {
		return nil, err
	}
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}

	var jsonData any
	err = json.Unmarshal(contents, &jsonData)
	if err != nil {
		return nil, err
	}

	return jsonData, nil
}

func (s *ServerService) GetDb() ([]byte, error) {
	if database.IsPostgres() {
		return s.exportPostgresDB()
	}
	// Update by manually trigger a checkpoint operation
	err := database.Checkpoint()
	if err != nil {
		return nil, err
	}
	// Open the file for reading
	file, err := os.Open(config.GetDBPath())
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Read the file contents
	fileContents, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	return fileContents, nil
}

func (s *ServerService) BackupFilename(requestHost string) string {
	ext := ".db"
	if database.IsPostgres() {
		ext = ".dump"
	}
	return s.backupHost(requestHost) + backupDateSuffix(time.Now()) + ext
}

func backupDateSuffix(now time.Time) string {
	return "_" + now.Format("2006-01-02_150405")
}

func (s *ServerService) backupHost(requestHost string) string {
	host := extractHostname(strings.TrimSpace(requestHost))
	if host == "" {
		if domain, err := s.settingService.GetWebDomain(); err == nil {
			host = strings.TrimSpace(domain)
		}
	}
	if host == "" {
		s.resolvePublicIPs()
		if ip := s.cachedIPv4; ip != "" && ip != "N/A" {
			host = ip
		} else if ip := s.cachedIPv6; ip != "" && ip != "N/A" {
			host = ip
		}
	}
	return sanitizeBackupHost(host)
}

// sanitizeBackupHost reduces a host to characters safe in a download filename
// (the getDb handler enforces ^[a-zA-Z0-9_\-.]+$). IPv6 brackets are stripped
// and any other character — such as the colons in an IPv6 address — becomes a
// hyphen. Returns "x-ui" when nothing usable remains.
func sanitizeBackupHost(host string) string {
	host = strings.Trim(host, "[]")
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), ".-")
	if out == "" {
		return "x-ui"
	}
	return out
}

// GetMigration produces a cross-engine migration file plus its filename: on a
// SQLite panel it returns a portable .dump (SQL text), and on a PostgreSQL panel
// it returns a .db SQLite database built from the live data. Either output can
// then seed a panel running on the other backend.
func (s *ServerService) GetMigration() ([]byte, string, error) {
	if database.IsPostgres() {
		tmp, err := os.CreateTemp("", "x-ui-migration-*.db")
		if err != nil {
			return nil, "", err
		}
		tmpPath := tmp.Name()
		tmp.Close()
		defer os.Remove(tmpPath)

		if err := database.ExportPostgresToSQLite(config.GetDBDSN(), tmpPath); err != nil {
			return nil, "", err
		}
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			return nil, "", err
		}
		return data, "x-ui.db", nil
	}

	// SQLite panel: checkpoint so the .db reflects the latest writes, then dump.
	if err := database.Checkpoint(); err != nil {
		return nil, "", err
	}
	data, err := database.DumpSQLiteToBytes(config.GetDBPath())
	if err != nil {
		return nil, "", err
	}
	return data, "x-ui.dump", nil
}

func (s *ServerService) ImportDB(file multipart.File) error {
	if database.IsPostgres() {
		return s.importPostgresDB(file)
	}
	kind, err := sniffUploadKind(file)
	if err != nil {
		return common.NewErrorf("Error reading uploaded file: %v", err)
	}
	switch kind {
	case importKindSQLiteDB, importKindSQLiteDump:
	case importKindPgDump:
		return common.NewError("This file is a PostgreSQL backup; it can only be restored on a panel running PostgreSQL")
	default:
		return common.NewError("Invalid file: expected a SQLite database (.db) from Back Up or a SQLite migration dump (.dump)")
	}

	tempPath := fmt.Sprintf("%s.temp", config.GetDBPath())

	if _, err := os.Stat(tempPath); err == nil {
		if errRemove := os.Remove(tempPath); errRemove != nil {
			return common.NewErrorf("Error removing existing temporary db file: %v", errRemove)
		}
	}
	defer func() {
		if _, err := os.Stat(tempPath); err == nil {
			if rerr := os.Remove(tempPath); rerr != nil {
				logger.Warningf("Warning: failed to remove temp file: %v", rerr)
			}
		}
	}()

	if err := stageSQLiteUpload(file, kind, tempPath); err != nil {
		return err
	}

	if err = database.ValidateSQLiteDB(tempPath); err != nil {
		return common.NewErrorf("Invalid or corrupt db file: %v", err)
	}
	if err = database.PrepareSQLiteForMigration(tempPath); err != nil {
		return common.NewErrorf("This file cannot be imported: %v", err)
	}

	xrayStopped := true
	defer func() {
		if xrayStopped {
			if errR := s.RestartXrayService(); errR != nil {
				logger.Warningf("Failed to restart Xray after DB import error: %v", errR)
			}
		}
	}()
	if errStop := s.StopXrayService(); errStop != nil {
		logger.Warningf("Failed to stop Xray before DB import: %v", errStop)
	}

	if errClose := database.CloseDB(); errClose != nil {
		logger.Warningf("Failed to close existing DB before replacement: %v", errClose)
	}

	// Registered after the xray-restart defer so it runs first (LIFO): every
	// error return below leaves a database file at the configured path, and the
	// restart needs an open pool to build the xray config from it.
	dbReopened := false
	defer func() {
		if dbReopened {
			return
		}
		if errReopen := database.InitDB(config.GetDBPath()); errReopen != nil {
			logger.Warningf("Failed to reopen the database after import error: %v", errReopen)
		}
	}()

	// Backup the current database for fallback
	fallbackPath := fmt.Sprintf("%s.backup", config.GetDBPath())

	// Remove the existing fallback file (if any)
	if _, err := os.Stat(fallbackPath); err == nil {
		if errRemove := os.Remove(fallbackPath); errRemove != nil {
			return common.NewErrorf("Error removing existing fallback db file: %v", errRemove)
		}
	}

	// Move the current database to the fallback location
	if err = os.Rename(config.GetDBPath(), fallbackPath); err != nil {
		return common.NewErrorf("Error backing up current db file: %v", err)
	}

	// Move temp to DB path
	if err = os.Rename(tempPath, config.GetDBPath()); err != nil {
		// Restore from fallback
		if errRename := os.Rename(fallbackPath, config.GetDBPath()); errRename != nil {
			return common.NewErrorf("Error moving db file and restoring fallback: %v", errRename)
		}
		return common.NewErrorf("Error moving db file: %v", err)
	}

	// Open & migrate new DB
	if err = database.InitDB(config.GetDBPath()); err != nil {
		// A failed InitDB still holds the imported file open; close before the
		// rename or Windows refuses to replace it.
		if errClose := database.CloseDB(); errClose != nil {
			logger.Warningf("Failed to close the imported DB before restoring fallback: %v", errClose)
		}
		if errRename := os.Rename(fallbackPath, config.GetDBPath()); errRename != nil {
			return common.NewErrorf("Error migrating db and restoring fallback: %v", errRename)
		}
		return common.NewErrorf("Error migrating db: %v", err)
	}
	dbReopened = true

	s.inboundService.MigrateDB()

	xrayStopped = false
	if err = s.RestartXrayService(); err != nil {
		return common.NewErrorf("Imported DB but failed to start Xray: %v; the previous database was kept at %s", err, fallbackPath)
	}

	if _, err := os.Stat(fallbackPath); err == nil {
		if rerr := os.Remove(fallbackPath); rerr != nil {
			logger.Warningf("Warning: failed to remove fallback file: %v", rerr)
		}
	}
	return nil
}

// pgConnEnv turns the configured PostgreSQL DSN into the PG* environment used by
// pg_dump/pg_restore, keeping the password out of the process argument list.
func pgConnEnv(dsn string) (env []string, dbname string, err error) {
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil {
		return nil, "", err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, "", common.NewErrorf("unsupported DSN scheme %q", u.Scheme)
	}
	dbname = strings.TrimPrefix(u.Path, "/")
	if dbname == "" {
		return nil, "", common.NewError("PostgreSQL DSN is missing a database name")
	}
	host := u.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	env = append(os.Environ(), "PGHOST="+host, "PGPORT="+port, "PGDATABASE="+dbname)
	if user := u.User.Username(); user != "" {
		env = append(env, "PGUSER="+user)
	}
	if pass, ok := u.User.Password(); ok {
		env = append(env, "PGPASSWORD="+pass)
	}
	if sslmode := u.Query().Get("sslmode"); sslmode != "" {
		env = append(env, "PGSSLMODE="+sslmode)
	}
	return env, dbname, nil
}

func (s *ServerService) exportPostgresDB() ([]byte, error) {
	bin, err := exec.LookPath("pg_dump")
	if err != nil {
		return nil, common.NewError("pg_dump not found on the server; install the postgresql-client package to back up a PostgreSQL database")
	}
	env, dbname, err := pgConnEnv(config.GetDBDSN())
	if err != nil {
		return nil, common.NewErrorf("invalid PostgreSQL DSN: %v", err)
	}
	cmd := exec.CommandContext(context.Background(), bin, "--format=custom", "--no-owner", "--no-privileges", "--dbname", dbname)
	cmd.Env = env
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, common.NewErrorf("pg_dump failed: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

var (
	pgUnsupportedDumpVersionPattern = regexp.MustCompile(`unsupported version \((\d+\.\d+)\) in file header`)
	pgToolVersionPattern            = regexp.MustCompile(`\d+(?:\.\d+)+`)
)

var pgArchiveVersionIntroducedIn = map[string]int{
	"1.15": 16,
	"1.16": 17,
}

// checkPgRestoreCanRead probes the dump with pg_restore --list (reads only the
// TOC, no database connection) so an unreadable file fails before Xray is stopped.
func checkPgRestoreCanRead(bin, dumpPath string) error {
	cmd := exec.CommandContext(context.Background(), bin, "--list", dumpPath)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if cmd.Run() == nil {
		return nil
	}
	return pgRestoreReadFailureError(strings.TrimSpace(stderr.String()), pgRestoreVersion(bin))
}

func pgRestoreReadFailureError(probeOutput, localVersion string) error {
	m := pgUnsupportedDumpVersionPattern.FindStringSubmatch(probeOutput)
	if m == nil {
		return common.NewErrorf("pg_restore cannot read this dump file: %s", probeOutput)
	}
	if localVersion == "" {
		localVersion = "unknown"
	}
	if major, known := pgArchiveVersionIntroducedIn[m[1]]; known {
		return common.NewErrorf("This backup was created by pg_dump from PostgreSQL %d or newer, but the server's pg_restore is version %s and cannot read it; run 'x-ui pgclient %d' on the server (or upgrade the postgresql-client package to version %d or newer), then retry the import", major, localVersion, major, major)
	}
	return common.NewErrorf("This backup was created by a newer pg_dump than the server's pg_restore (version %s) can read; upgrade the postgresql-client package and retry the import", localVersion)
}

func pgRestoreVersion(bin string) string {
	out, err := exec.CommandContext(context.Background(), bin, "--version").Output()
	if err != nil {
		return ""
	}
	return parsePgToolVersion(string(out))
}

func parsePgToolVersion(versionOutput string) string {
	return pgToolVersionPattern.FindString(versionOutput)
}

const (
	importKindUnknown = iota
	importKindPgDump
	importKindSQLiteDB
	importKindSQLiteDump
)

// sniffImportKind classifies an uploaded restore file by its leading bytes:
// a pg_dump custom archive, a raw SQLite database, or a SQLite SQL text dump.
func sniffImportKind(header []byte) int {
	if bytes.HasPrefix(header, []byte("PGDMP")) {
		return importKindPgDump
	}
	if bytes.HasPrefix(header, []byte("SQLite format 3\x00")) {
		return importKindSQLiteDB
	}
	text := bytes.TrimLeft(bytes.TrimPrefix(header, []byte("\xef\xbb\xbf")), " \t\r\n")
	if bytes.HasPrefix(text, []byte("PRAGMA")) || bytes.HasPrefix(text, []byte("BEGIN TRANSACTION")) {
		return importKindSQLiteDump
	}
	return importKindUnknown
}

func sniffUploadKind(file multipart.File) (int, error) {
	header := make([]byte, 64)
	n, err := file.ReadAt(header, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return importKindUnknown, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return importKindUnknown, err
	}
	return sniffImportKind(header[:n]), nil
}

func (s *ServerService) importPostgresDB(file multipart.File) error {
	kind, err := sniffUploadKind(file)
	if err != nil {
		return common.NewErrorf("Error reading uploaded file: %v", err)
	}
	switch kind {
	case importKindPgDump:
		return s.restorePostgresDump(file)
	case importKindSQLiteDB:
		return s.migrateSQLiteIntoPostgres(file, false)
	case importKindSQLiteDump:
		return s.migrateSQLiteIntoPostgres(file, true)
	default:
		return common.NewError("Invalid file: expected a PostgreSQL custom-format dump (.dump) from this panel's Back Up, a SQLite database (.db), or a SQLite migration dump")
	}
}

func (s *ServerService) restorePostgresDump(file multipart.File) error {
	bin, err := exec.LookPath("pg_restore")
	if err != nil {
		return common.NewError("pg_restore not found on the server; install the postgresql-client package to restore a PostgreSQL database")
	}
	env, dbname, err := pgConnEnv(config.GetDBDSN())
	if err != nil {
		return common.NewErrorf("invalid PostgreSQL DSN: %v", err)
	}

	tempFile, err := os.CreateTemp("", "x-ui-pg-restore-*.dump")
	if err != nil {
		return common.NewErrorf("Error creating temporary dump file: %v", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(tempFile, file); err != nil {
		tempFile.Close()
		return common.NewErrorf("Error saving dump: %v", err)
	}
	if err := tempFile.Close(); err != nil {
		return common.NewErrorf("Error closing temporary dump file: %v", err)
	}

	if err := checkPgRestoreCanRead(bin, tempPath); err != nil {
		return err
	}

	xrayStopped := true
	defer func() {
		if xrayStopped {
			if errR := s.RestartXrayService(); errR != nil {
				logger.Warningf("Failed to restart Xray after DB restore error: %v", errR)
			}
		}
	}()
	if errStop := s.StopXrayService(); errStop != nil {
		logger.Warningf("Failed to stop Xray before DB restore: %v", errStop)
	}

	if errClose := database.CloseDB(); errClose != nil {
		logger.Warningf("Failed to close existing DB before restore: %v", errClose)
	}

	cmd := exec.CommandContext(context.Background(), bin,
		"--clean", "--if-exists", "--no-owner", "--no-privileges",
		"--single-transaction", "--dbname", dbname, tempPath,
	)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	if errInit := database.InitDB(config.GetDBPath()); errInit != nil {
		return common.NewErrorf("Restore finished but reopening the database failed: %v", errInit)
	}
	s.inboundService.MigrateDB()

	if runErr != nil {
		return common.NewErrorf("pg_restore failed (database left unchanged): %v: %s", runErr, strings.TrimSpace(stderr.String()))
	}

	xrayStopped = false
	if err := s.RestartXrayService(); err != nil {
		return common.NewErrorf("Restored DB but failed to start Xray: %v", err)
	}
	return nil
}

func (s *ServerService) migrateSQLiteIntoPostgres(file multipart.File, isSQLDump bool) error {
	tempDir, err := os.MkdirTemp("", "x-ui-pg-migrate-*")
	if err != nil {
		return common.NewErrorf("Error creating temporary folder: %v", err)
	}
	defer os.RemoveAll(tempDir)

	uploadPath := filepath.Join(tempDir, "upload.db")
	if isSQLDump {
		uploadPath = filepath.Join(tempDir, "upload.dump")
	}
	if err := saveUploadedFile(file, uploadPath); err != nil {
		return common.NewErrorf("Error saving uploaded file: %v", err)
	}

	dbPath := uploadPath
	if isSQLDump {
		dbPath = filepath.Join(tempDir, "restored.db")
		if err := database.RestoreSQLite(uploadPath, dbPath); err != nil {
			return common.NewErrorf("Error rebuilding a SQLite database from the migration dump: %v", err)
		}
	}
	if err := database.ValidateSQLiteDB(dbPath); err != nil {
		return common.NewErrorf("Invalid or corrupt db file: %v", err)
	}
	if err := database.PrepareSQLiteForMigration(dbPath); err != nil {
		return common.NewErrorf("This file cannot be imported: %v", err)
	}

	xrayStopped := true
	defer func() {
		if xrayStopped {
			if errR := s.RestartXrayService(); errR != nil {
				logger.Warningf("Failed to restart Xray after DB restore error: %v", errR)
			}
		}
	}()
	if errStop := s.StopXrayService(); errStop != nil {
		logger.Warningf("Failed to stop Xray before DB restore: %v", errStop)
	}

	if errClose := database.CloseDB(); errClose != nil {
		logger.Warningf("Failed to close existing DB before restore: %v", errClose)
	}

	migrateErr := database.MigrateData(dbPath, config.GetDBDSN())

	if errInit := database.InitDB(config.GetDBPath()); errInit != nil {
		return common.NewErrorf("Restore finished but reopening the database failed: %v", errInit)
	}
	s.inboundService.MigrateDB()

	if migrateErr != nil {
		return common.NewErrorf("Importing the SQLite data into PostgreSQL failed: %v; the import runs in a single transaction, so the database was left unchanged", migrateErr)
	}

	xrayStopped = false
	if err := s.RestartXrayService(); err != nil {
		return common.NewErrorf("Restored DB but failed to start Xray: %v", err)
	}
	return nil
}

func saveUploadedFile(file multipart.File, dstPath string) error {
	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

func stageSQLiteUpload(file multipart.File, kind int, tempPath string) error {
	if kind == importKindSQLiteDump {
		dumpPath := tempPath + ".dump"
		defer os.Remove(dumpPath)
		if err := saveUploadedFile(file, dumpPath); err != nil {
			return common.NewErrorf("Error saving migration dump: %v", err)
		}
		if err := database.RestoreSQLite(dumpPath, tempPath); err != nil {
			return common.NewErrorf("Error rebuilding a SQLite database from the migration dump: %v", err)
		}
		return nil
	}
	if err := saveUploadedFile(file, tempPath); err != nil {
		return common.NewErrorf("Error saving db: %v", err)
	}
	return nil
}

// IsValidGeofileName validates that the filename is safe for geofile operations.
// It checks for path traversal attempts and ensures the filename contains only safe characters.
func (s *ServerService) IsValidGeofileName(filename string) bool {
	if filename == "" {
		return false
	}

	// Check for path traversal attempts
	if strings.Contains(filename, "..") {
		return false
	}

	// Check for path separators (both forward and backward slash)
	if strings.ContainsAny(filename, `/\`) {
		return false
	}

	// Check for absolute path indicators
	if filepath.IsAbs(filename) {
		return false
	}

	// Additional security: only allow alphanumeric, dots, underscores, and hyphens
	// This is stricter than the general filename regex
	validGeofilePattern := `^[a-zA-Z0-9._-]+\.dat$`
	matched, _ := regexp.MatchString(validGeofilePattern, filename)
	return matched
}

func (s *ServerService) UpdateGeofile(fileName string) error {
	type geofileEntry struct {
		URL      string
		FileName string
	}
	geofileAllowlist := map[string]geofileEntry{
		"geoip.dat":      {"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat", "geoip.dat"},
		"geosite.dat":    {"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat", "geosite.dat"},
		"geoip_IR.dat":   {"https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geoip.dat", "geoip_IR.dat"},
		"geosite_IR.dat": {"https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geosite.dat", "geosite_IR.dat"},
		"geoip_RU.dat":   {"https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geoip.dat", "geoip_RU.dat"},
		"geosite_RU.dat": {"https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geosite.dat", "geosite_RU.dat"},
	}

	// Strict allowlist check to avoid writing uncontrolled files
	if fileName != "" {
		if _, ok := geofileAllowlist[fileName]; !ok {
			return common.NewErrorf("Invalid geofile name: %q not in allowlist", fileName)
		}
	}

	client := s.settingService.NewProxiedHTTPClient(0)

	downloadFile := func(url, destPath string) error {
		var req *http.Request
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			return common.NewErrorf("Failed to create HTTP request for %s: %v", url, err)
		}

		var localFileModTime time.Time
		if fileInfo, err := os.Stat(destPath); err == nil {
			localFileModTime = fileInfo.ModTime()
			if !localFileModTime.IsZero() {
				req.Header.Set("If-Modified-Since", localFileModTime.UTC().Format(http.TimeFormat))
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			return common.NewErrorf("Failed to download Geofile from %s: %v", url, err)
		}
		defer resp.Body.Close()

		// Parse Last-Modified header from server
		var serverModTime time.Time
		serverModTimeStr := resp.Header.Get("Last-Modified")
		if serverModTimeStr != "" {
			parsedTime, err := time.Parse(http.TimeFormat, serverModTimeStr)
			if err != nil {
				logger.Warningf("Failed to parse Last-Modified header for %s: %v", url, err)
			} else {
				serverModTime = parsedTime
			}
		}

		// Function to update local file's modification time
		updateFileModTime := func() {
			if !serverModTime.IsZero() {
				if err := os.Chtimes(destPath, serverModTime, serverModTime); err != nil {
					logger.Warningf("Failed to update modification time for %s: %v", destPath, err)
				}
			}
		}

		// Handle 304 Not Modified
		if resp.StatusCode == http.StatusNotModified {
			updateFileModTime()
			return nil
		}

		if resp.StatusCode != http.StatusOK {
			return common.NewErrorf("Failed to download Geofile from %s: received status code %d", url, resp.StatusCode)
		}

		file, err := os.Create(destPath)
		if err != nil {
			return common.NewErrorf("Failed to create Geofile %s: %v", destPath, err)
		}
		defer file.Close()

		_, err = io.Copy(file, resp.Body)
		if err != nil {
			return common.NewErrorf("Failed to save Geofile %s: %v", destPath, err)
		}

		updateFileModTime()
		return nil
	}

	var errorMessages []string

	if fileName == "" {
		// Download all geofiles
		for _, entry := range geofileAllowlist {
			destPath := filepath.Join(config.GetBinFolderPath(), entry.FileName)
			if err := downloadFile(entry.URL, destPath); err != nil {
				errorMessages = append(errorMessages, fmt.Sprintf("Error downloading Geofile '%s': %v", entry.FileName, err))
			}
		}
	} else {
		entry := geofileAllowlist[fileName]
		destPath := filepath.Join(config.GetBinFolderPath(), entry.FileName)
		if err := downloadFile(entry.URL, destPath); err != nil {
			errorMessages = append(errorMessages, fmt.Sprintf("Error downloading Geofile '%s': %v", entry.FileName, err))
		}
	}

	err := s.RestartXrayService()
	if err != nil {
		errorMessages = append(errorMessages, fmt.Sprintf("Updated Geofile '%s' but Failed to start Xray: %v", fileName, err))
	}

	if len(errorMessages) > 0 {
		return common.NewErrorf("%s", strings.Join(errorMessages, "\r\n"))
	}

	return nil
}

// parseXrayKeyPairOutput reads the two-line "Label: value" output that xray's
// key-generation subcommands (x25519, mldsa65, mlkem768) print and returns the
// two values. Short or label-less output yields an error instead of panicking
// on an out-of-range slice index, so a future xray version that changes the
// format degrades to a 500 with a message rather than a crash.
func parseXrayKeyPairOutput(output string) (string, string, error) {
	lines := strings.Split(output, "\n")
	if len(lines) < 2 {
		return "", "", common.NewError("unexpected key generator output")
	}
	first := strings.Split(lines[0], ":")
	second := strings.Split(lines[1], ":")
	if len(first) < 2 || len(second) < 2 {
		return "", "", common.NewError("unexpected key generator output")
	}
	return strings.TrimSpace(first[1]), strings.TrimSpace(second[1]), nil
}

func (s *ServerService) GetNewX25519Cert() (any, error) {
	// Run the command
	cmd := exec.CommandContext(context.Background(), xray.GetBinaryPath(), "x25519")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	privateKey, publicKey, err := parseXrayKeyPairOutput(out.String())
	if err != nil {
		return nil, err
	}

	keyPair := map[string]any{
		"privateKey": privateKey,
		"publicKey":  publicKey,
	}

	return keyPair, nil
}

func (s *ServerService) GetNewmldsa65() (any, error) {
	// Run the command
	cmd := exec.CommandContext(context.Background(), xray.GetBinaryPath(), "mldsa65")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	seed, verify, err := parseXrayKeyPairOutput(out.String())
	if err != nil {
		return nil, err
	}

	keyPair := map[string]any{
		"seed":   seed,
		"verify": verify,
	}

	return keyPair, nil
}

// GetCertHash parses a certificate (from a file path or inline PEM/DER content)
// and returns the hex-encoded SHA-256 over each certificate's raw DER — the
// value xray-core's pinnedPeerCertSha256 (pcs) expects. Lets the panel fill the
// pinned-cert field from the inbound's own certificate without the user
// computing the hash by hand.
func (s *ServerService) GetCertHash(certFile string, certContent string) ([]string, error) {
	var certBytes []byte
	if path := strings.TrimSpace(certFile); path != "" {
		// Guard against path traversal: only hash certificate files the panel
		// already references in its own configuration (an inbound's TLS
		// certificateFile or the panel's own web cert). The path handed to
		// os.ReadFile comes from that allow-list, never directly from the
		// caller-supplied value.
		known, ok := s.resolveKnownCertFile(path)
		if !ok {
			return nil, common.NewError("certificate file is not referenced by any inbound or panel setting")
		}
		b, err := os.ReadFile(known)
		if err != nil {
			return nil, err
		}
		certBytes = b
	} else if strings.TrimSpace(certContent) != "" {
		certBytes = []byte(certContent)
	} else {
		return nil, common.NewError("no certificate provided")
	}

	var certs []*x509.Certificate
	if bytes.Contains(certBytes, []byte("BEGIN")) {
		rest := certBytes
		for {
			block, remain := pem.Decode(rest)
			if block == nil {
				break
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, common.NewError("unable to decode certificate: ", err)
			}
			certs = append(certs, cert)
			rest = remain
		}
	} else {
		parsed, err := x509.ParseCertificates(certBytes)
		if err != nil {
			return nil, common.NewError("unable to parse certificates: ", err)
		}
		certs = parsed
	}

	if len(certs) == 0 {
		return nil, common.NewError("no certificates found")
	}

	hashes := make([]string, 0, len(certs))
	for _, cert := range certs {
		sum := sha256.Sum256(cert.Raw)
		hashes = append(hashes, hex.EncodeToString(sum[:]))
	}
	return hashes, nil
}

// resolveKnownCertFile checks the caller-supplied certificate path against the
// set of certificate files the panel already references (inbound TLS configs
// plus the panel's own web cert) and, on a match, returns the path taken from
// that configuration — not the caller's value. This both confines reads to
// known certificates and breaks the user-input-to-filesystem taint flow.
func (s *ServerService) resolveKnownCertFile(certFile string) (string, bool) {
	want := filepath.Clean(certFile)
	for _, known := range s.knownCertFiles() {
		if filepath.Clean(known) == want {
			return known, true
		}
	}
	return "", false
}

// knownCertFiles collects every certificate file path the panel legitimately
// references: the certificateFile of each inbound's TLS settings and the
// panel's own web TLS certificate.
func (s *ServerService) knownCertFiles() []string {
	var files []string
	if cert, err := s.settingService.GetCertFile(); err == nil {
		if cert = strings.TrimSpace(cert); cert != "" {
			files = append(files, cert)
		}
	}
	if inbounds, err := s.inboundService.GetAllInbounds(); err == nil {
		for _, inbound := range inbounds {
			files = collectCertFiles(inbound.StreamSettings, files)
		}
	}
	return files
}

// collectCertFiles walks a stream-settings JSON document and appends the value
// of every "certificateFile" field it finds (TLS settings may nest them under
// several keys depending on the security type).
func collectCertFiles(streamSettings string, out []string) []string {
	streamSettings = strings.TrimSpace(streamSettings)
	if streamSettings == "" {
		return out
	}
	var parsed any
	if err := json.Unmarshal([]byte(streamSettings), &parsed); err != nil {
		return out
	}
	return walkCertFiles(parsed, out)
}

func walkCertFiles(node any, out []string) []string {
	switch v := node.(type) {
	case map[string]any:
		for key, val := range v {
			if key == "certificateFile" {
				if path, ok := val.(string); ok {
					if path = strings.TrimSpace(path); path != "" {
						out = append(out, path)
					}
				}
			}
			out = walkCertFiles(val, out)
		}
	case []any:
		for _, item := range v {
			out = walkCertFiles(item, out)
		}
	}
	return out
}

// GetRemoteCertHash opens a uTLS (Chrome fingerprint) handshake to a remote
// endpoint and returns the hex-encoded SHA-256 of its leaf certificate — the
// value to put in pinnedPeerCertSha256 (pcs) when pinning a server whose
// certificate file you don't hold (a CDN front, a REALITY dest, an external
// proxy). A native handshake replaces the old `xray tls ping` subprocess so the
// real dial/handshake failure (connection refused, timeout, …) surfaces
// verbatim. `server` may be host or host:port; the port defaults to 443.
func (s *ServerService) GetRemoteCertHash(server string) ([]string, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return nil, common.NewError("no server provided")
	}

	host, port := server, "443"
	if h, p, err := stdnet.SplitHostPort(server); err == nil {
		host, port = h, p
	}

	dialer := stdnet.Dialer{Timeout: 10 * time.Second}
	tcpConn, err := dialer.Dial("tcp", stdnet.JoinHostPort(host, port))
	if err != nil {
		return nil, common.NewErrorf("failed to dial %s: %s", stdnet.JoinHostPort(host, port), err)
	}
	defer tcpConn.Close()
	_ = tcpConn.SetDeadline(time.Now().Add(15 * time.Second))

	tlsConn := utls.UClient(tcpConn, &utls.Config{
		ServerName:         host,
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1"},
	}, utls.HelloChrome_Auto)
	defer tlsConn.Close()
	if err := tlsConn.Handshake(); err != nil {
		return nil, common.NewErrorf("tls handshake with %s failed: %s", host, err)
	}

	certs := tlsConn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, common.NewError("no certificate returned by ", host)
	}
	// PeerCertificates[0] is always the leaf the connection verifies against —
	// robust for IP-only self-signed certs that carry no DNS SANs.
	sum := sha256.Sum256(certs[0].Raw)
	return []string{hex.EncodeToString(sum[:])}, nil
}

func (s *ServerService) GetNewEchCert(sni string) (any, error) {
	// Run the command
	cmd := exec.CommandContext(context.Background(), xray.GetBinaryPath(), "tls", "ech", "--serverName", sni)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")
	if len(lines) < 4 {
		return nil, common.NewError("invalid ech cert")
	}

	configList := lines[1]
	serverKeys := lines[3]

	return map[string]any{
		"echServerKeys": serverKeys,
		"echConfigList": configList,
	}, nil
}

func (s *ServerService) GetNewVlessEnc() (any, error) {
	cmd := exec.CommandContext(context.Background(), xray.GetBinaryPath(), "vlessenc")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	auths := parseVlessEncAuths(out.String())
	auths = append(auths, deriveVlessEncModes(auths)...)

	return map[string]any{
		"auths": auths,
	}, nil
}

func deriveVlessEncModes(auths []map[string]string) []map[string]string {
	var extra []map[string]string
	for _, a := range auths {
		for _, mode := range []string{"xorpub", "random"} {
			dec := strings.Replace(a["decryption"], ".native.", "."+mode+".", 1)
			enc := strings.Replace(a["encryption"], ".native.", "."+mode+".", 1)
			if dec == a["decryption"] && enc == a["encryption"] {
				continue
			}
			extra = append(extra, map[string]string{
				"id":         a["id"] + "_" + mode,
				"label":      a["label"] + " (" + mode + ")",
				"decryption": dec,
				"encryption": enc,
			})
		}
	}
	return extra
}

func parseVlessEncAuths(output string) []map[string]string {
	lines := strings.Split(output, "\n")
	var auths []map[string]string
	var current map[string]string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Authentication:") {
			if current != nil {
				auths = append(auths, current)
			}
			label := strings.TrimSpace(strings.TrimPrefix(line, "Authentication:"))
			current = map[string]string{
				"id":    vlessEncAuthID(label),
				"label": label,
			}
		} else if strings.HasPrefix(line, `"decryption"`) || strings.HasPrefix(line, `"encryption"`) {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && current != nil {
				key := strings.Trim(parts[0], `" `)
				val := strings.TrimSpace(parts[1])
				val = strings.TrimSuffix(val, ",")
				val = strings.Trim(val, `" `)
				current[key] = val
			}
		}
	}

	if current != nil {
		auths = append(auths, current)
	}

	return auths
}

func vlessEncAuthID(label string) string {
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(label))
	switch {
	case strings.Contains(normalized, "mlkem768"):
		return "mlkem768"
	case strings.Contains(normalized, "x25519"):
		return "x25519"
	default:
		return normalized
	}
}

func (s *ServerService) GetNewUUID() (map[string]string, error) {
	newUUID, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate UUID: %w", err)
	}

	return map[string]string{
		"uuid": newUUID.String(),
	}, nil
}

func (s *ServerService) GetNewmlkem768() (any, error) {
	// Run the command
	cmd := exec.CommandContext(context.Background(), xray.GetBinaryPath(), "mlkem768")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	seed, client, err := parseXrayKeyPairOutput(out.String())
	if err != nil {
		return nil, err
	}

	keyPair := map[string]any{
		"seed":   seed,
		"client": client,
	}

	return keyPair, nil
}
