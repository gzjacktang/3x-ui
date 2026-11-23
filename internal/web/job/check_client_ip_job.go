package job

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"gorm.io/gorm"
)

// IPWithTimestamp tracks an IP address with its last seen timestamp
type IPWithTimestamp struct {
	IP        string `json:"ip"`
	Timestamp int64  `json:"timestamp"`
}

type onlineUsersProvider interface {
	IsXrayRunning() bool
	GetOnlineUsersSample(fresh bool) (service.OnlineUsersSample, bool, error)
	IsCurrentProcess(core *xray.Process) bool
	RemoveUserForProcess(core *xray.Process, inboundTag, email string) (bool, error)
	AddUserForProcess(core *xray.Process, protocol, inboundTag string, clientConfig map[string]any) (bool, error)
}

// CheckClientIpJob monitors client IP addresses and manages IP blocking based
// on configured limits. The per-client IPs come from the core's online-stats
// API; no access log is involved. On a core too old to expose that API the job
// simply skips the run (the bundled core always supports it).
type CheckClientIpJob struct {
	disAllowedIps  []string
	bannedSeen     map[string]int64
	xrayService    service.XrayService
	onlineSource   onlineUsersProvider
	disconnectWait func()
}

func (j *CheckClientIpJob) onlineProvider() onlineUsersProvider {
	if j.onlineSource != nil {
		return j.onlineSource
	}
	return &j.xrayService
}

func (j *CheckClientIpJob) pauseForDisconnect() {
	if j.disconnectWait != nil {
		j.disconnectWait()
		return
	}
	time.Sleep(100 * time.Millisecond)
}

var job *CheckClientIpJob

const defaultXrayAPIPort = 62789

const ipStaleAfterSeconds = int64(30 * 60)

// NewCheckClientIpJob creates a new client IP monitoring job instance.
func NewCheckClientIpJob() *CheckClientIpJob {
	job = new(CheckClientIpJob)
	return job
}

func (j *CheckClientIpJob) Run() {
	if !isFail2BanEnabled() || !j.onlineProvider().IsXrayRunning() {
		return
	}

	hasLimit := j.hasLimitIp()
	f2bInstalled := false
	if hasLimit {
		f2bInstalled = j.checkFail2BanInstalled()
	}
	enforce := j.resolveEnforce(hasLimit, f2bInstalled)

	observed, sampleProcess, apiMode := j.collectFromOnlineAPI(enforce)
	if !apiMode {
		logger.Debug("[LimitIP] online-stats API unavailable this run; skipping")
		return
	}

	j.processObservedForProcess(observed, enforce, true, sampleProcess)
}

// resolveEnforce decides whether limits can actually be enforced this run.
// Without fail2ban on a platform that needs it the limit can't be applied, so
// enforcement is skipped (the panel resets these limits to 0 on upgrade and
// disables the field, so this is normally a no-op).
func (j *CheckClientIpJob) resolveEnforce(hasLimit, f2bInstalled bool) bool {
	if hasLimit && runtime.GOOS != "windows" && !f2bInstalled {
		return false
	}
	return hasLimit
}

// collectFromOnlineAPI builds per-email IP observations (email -> ip ->
// last-seen unix seconds) from the core's online-stats API. ok=false means the
// API is unavailable — xray not running, an older core, or a transient gRPC
// failure — and the caller skips the run (there is no access-log fallback).
func (j *CheckClientIpJob) collectFromOnlineAPI(fresh bool) (map[string]map[string]int64, *xray.Process, bool) {
	sample, ok, err := j.onlineProvider().GetOnlineUsersSample(fresh)
	if err != nil {
		logger.Debug("[LimitIP] online-stats API unavailable this run:", err)
		return nil, nil, false
	}
	if !ok {
		return nil, nil, false
	}
	now := time.Now().Unix()
	observed := make(map[string]map[string]int64, len(sample.Users))
	for _, user := range sample.Users {
		for _, entry := range user.IPs {
			ts := entry.LastSeen
			if ts <= 0 {
				ts = now
			}
			if _, exists := observed[user.Email]; !exists {
				observed[user.Email] = make(map[string]int64)
			}
			if existing, seen := observed[user.Email][entry.IP]; !seen || ts > existing {
				observed[user.Email][entry.IP] = ts
			}
		}
	}
	return observed, sample.Process, true
}

// hasLimitIp reports whether any client carries an IP limit. It probes the
// normalized clients table (limit_ip is synced there by SyncInbound and the
// legacy seeder), replacing the old `settings LIKE '%limitIp%'` scan that
// loaded and JSON-parsed every inbound's settings blob on each 10s run.
func (j *CheckClientIpJob) hasLimitIp() bool {
	db := database.GetDB()
	var probe int64
	err := db.Model(&model.ClientRecord{}).Where("limit_ip > 0").Limit(1).Count(&probe).Error
	return err == nil && probe > 0
}

const ipScanChunk = 400

func chunkEmails(s []string, size int) [][]string {
	if len(s) == 0 {
		return nil
	}
	chunks := make([][]string, 0, (len(s)+size-1)/size)
	for size < len(s) {
		s, chunks = s[size:], append(chunks, s[:size])
	}
	return append(chunks, s)
}

// loadClientLimits maps each observed email to its clients.limit_ip in a few
// chunked queries, replacing the per-email settings-JSON parse that previously
// resolved the limit.
func (j *CheckClientIpJob) loadClientLimits(emails []string) map[string]int {
	db := database.GetDB()
	out := make(map[string]int, len(emails))
	for _, batch := range chunkEmails(emails, ipScanChunk) {
		var rows []struct {
			Email   string
			LimitIp int
		}
		if err := db.Model(&model.ClientRecord{}).
			Select("email, limit_ip").
			Where("email IN ?", batch).
			Scan(&rows).Error; err != nil {
			j.checkError(err)
			continue
		}
		for _, r := range rows {
			out[r.Email] = r.LimitIp
		}
	}
	return out
}

// loadInboundsByEmails resolves each email's owning inbound through the
// clients/client_inbounds relation in chunked queries. Like the old per-email
// First() it keeps the lowest inbound id when a client spans several inbounds.
func (j *CheckClientIpJob) loadInboundsByEmails(emails []string) map[string]*model.Inbound {
	db := database.GetDB()
	minInboundByEmail := make(map[string]int, len(emails))
	for _, batch := range chunkEmails(emails, ipScanChunk) {
		var pairs []struct {
			Email     string
			InboundId int
		}
		if err := db.Table("client_inbounds").
			Select("clients.email AS email, client_inbounds.inbound_id AS inbound_id").
			Joins("JOIN clients ON clients.id = client_inbounds.client_id").
			Where("clients.email IN ?", batch).
			Scan(&pairs).Error; err != nil {
			j.checkError(err)
			return nil
		}
		for _, p := range pairs {
			if cur, ok := minInboundByEmail[p.Email]; !ok || p.InboundId < cur {
				minInboundByEmail[p.Email] = p.InboundId
			}
		}
	}
	if len(minInboundByEmail) == 0 {
		return nil
	}

	idSet := make(map[int]struct{}, len(minInboundByEmail))
	ids := make([]int, 0, len(minInboundByEmail))
	for _, id := range minInboundByEmail {
		if _, seen := idSet[id]; !seen {
			idSet[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	inboundsById := make(map[int]*model.Inbound, len(ids))
	for lo := 0; lo < len(ids); lo += ipScanChunk {
		hi := min(lo+ipScanChunk, len(ids))
		var page []*model.Inbound
		if err := db.Model(&model.Inbound{}).Where("id IN ?", ids[lo:hi]).Find(&page).Error; err != nil {
			j.checkError(err)
			return nil
		}
		for _, ib := range page {
			inboundsById[ib.Id] = ib
		}
	}

	out := make(map[string]*model.Inbound, len(minInboundByEmail))
	for email, id := range minInboundByEmail {
		if ib, ok := inboundsById[id]; ok {
			out[email] = ib
		}
	}
	return out
}

func (j *CheckClientIpJob) loadClientIpRows(emails []string) map[string]*model.InboundClientIps {
	db := database.GetDB()
	out := make(map[string]*model.InboundClientIps, len(emails))
	for _, batch := range chunkEmails(emails, ipScanChunk) {
		var rows []model.InboundClientIps
		if err := db.Where("client_email IN ?", batch).Find(&rows).Error; err != nil {
			j.checkError(err)
			continue
		}
		for i := range rows {
			out[rows[i].ClientEmail] = &rows[i]
		}
	}
	return out
}

// processObserved runs collection + enforcement for one scan's observations
// (email -> ip -> last-seen unix seconds). observedAreLive marks the
// observations as live connections, which bypass the stale cutoff: a connection
// that opened hours ago is still live even though its timestamp is old. The
// online-stats API always reports live connections, so the job passes true.
// Lookups are batched up front and all inbound_client_ips writes share one
// transaction, so a scan costs a handful of queries and one fsync instead of
// several per observed email.
type pendingBanLog struct {
	email     string
	keptCount int
	entries   []IPWithTimestamp
}

func (j *CheckClientIpJob) processObserved(observed map[string]map[string]int64, enforce, observedAreLive bool) bool {
	return j.processObservedForProcess(observed, enforce, observedAreLive, nil)
}

func (j *CheckClientIpJob) processObservedForProcess(observed map[string]map[string]int64, enforce, observedAreLive bool, sampleProcess *xray.Process) bool {
	if enforce && sampleProcess != nil && !j.onlineProvider().IsCurrentProcess(sampleProcess) {
		logger.Debug("[LimitIP] skipping enforcement: Xray process changed after sampling")
		return false
	}

	shouldCleanLog := false
	now := time.Now().Unix()

	emails := make([]string, 0, len(observed))
	for email := range observed {
		emails = append(emails, email)
	}
	sort.Strings(emails)

	limitByEmail := j.loadClientLimits(emails)
	inboundByEmail := j.loadInboundsByEmails(emails)
	ipRowByEmail := j.loadClientIpRows(emails)

	attribution := make(map[string][]model.ClientIpEntry, len(observed))

	type pendingDisconnect struct {
		inbound *model.Inbound
		email   string
	}
	var disconnects []pendingDisconnect
	var pendingBans []pendingBanLog

	stagedSeen := j.bannedSeen
	if sampleProcess != nil {
		stagedSeen = cloneBannedSeen(j.bannedSeen)
	}

	db := database.GetDB()
	tx := db.Begin()
	if tx.Error != nil {
		j.checkError(tx.Error)
		return false
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	for _, email := range emails {
		if enforce && sampleProcess != nil && !j.onlineProvider().IsCurrentProcess(sampleProcess) {
			logger.Debug("[LimitIP] aborting enforcement scan: Xray process changed during evaluation")
			return false
		}
		ipTimestamps := observed[email]

		inbound, ok := inboundByEmail[email]
		if !ok {
			var err error
			inbound, err = j.getInboundByEmail(email)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					logger.Debugf("[LimitIP] skipping stale observed email %q (renamed or deleted)", email)
					j.delInboundClientIps(tx, email)
				} else {
					j.checkError(err)
				}
				continue
			}
		}

		ipsWithTime := make([]IPWithTimestamp, 0, len(ipTimestamps))
		attrEntries := make([]model.ClientIpEntry, 0, len(ipTimestamps))
		for ip, timestamp := range ipTimestamps {
			ipsWithTime = append(ipsWithTime, IPWithTimestamp{IP: ip, Timestamp: timestamp})
			attrTs := timestamp
			if observedAreLive {
				attrTs = now
			}
			attrEntries = append(attrEntries, model.ClientIpEntry{IP: ip, Timestamp: attrTs})
		}
		if len(attrEntries) > 0 {
			attribution[email] = attrEntries
		}

		clientIpsRecord, ok := ipRowByEmail[email]
		if !ok {
			jsonIps, err := json.Marshal(ipsWithTime)
			if err != nil {
				j.checkError(err)
				continue
			}
			if err := tx.Save(&model.InboundClientIps{ClientEmail: email, Ips: string(jsonIps)}).Error; err != nil {
				j.checkError(err)
			}
			continue
		}

		cleaned, banned, banLog := j.updateInboundClientIpsWithSeen(tx, clientIpsRecord, inbound, email, limitByEmail[email], ipsWithTime, enforce, observedAreLive, &stagedSeen, sampleProcess != nil)
		shouldCleanLog = cleaned || shouldCleanLog
		if banned {
			disconnects = append(disconnects, pendingDisconnect{inbound: inbound, email: email})
			if len(banLog.entries) > 0 {
				pendingBans = append(pendingBans, banLog)
			}
		}
	}

	if enforce && sampleProcess != nil && !j.onlineProvider().IsCurrentProcess(sampleProcess) {
		logger.Debug("[LimitIP] aborting enforcement commit: Xray process changed before commit")
		return false
	}

	if err := tx.Commit().Error; err != nil {
		j.checkError(err)
		return shouldCleanLog
	}
	committed = true

	if sampleProcess != nil {
		if enforce && len(pendingBans) > 0 {
			if !j.onlineProvider().IsCurrentProcess(sampleProcess) {
				logger.Debug("[LimitIP] skipping fail2ban log and disconnects: Xray process changed after commit")
				return false
			}
			if !j.writePendingBanLogs(pendingBans) {
				disconnects = nil
				shouldCleanLog = false
			} else {
				j.bannedSeen = stagedSeen
			}
		} else {
			j.bannedSeen = stagedSeen
		}
	}

	clientsCache := make(map[int][]model.Client)
	for _, d := range disconnects {
		if sampleProcess != nil && !j.onlineProvider().IsCurrentProcess(sampleProcess) {
			logger.Debug("[LimitIP] stopping remaining disconnects: Xray process changed")
			break
		}
		clients, cached := clientsCache[d.inbound.Id]
		if !cached {
			clients, _ = service.ParseInboundSettingsClients(d.inbound.Settings)
			clientsCache[d.inbound.Id] = clients
		}
		j.disconnectClientTemporarilyForProcess(d.inbound, d.email, clients, sampleProcess)
	}

	j.recordLocalAttribution(attribution)

	return shouldCleanLog
}

func cloneBannedSeen(src map[string]int64) map[string]int64 {
	if len(src) == 0 {
		return make(map[string]int64)
	}
	dst := make(map[string]int64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (j *CheckClientIpJob) writePendingBanLogs(pending []pendingBanLog) bool {
	logIpFile, err := os.OpenFile(xray.GetIPLimitLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		logger.Errorf("failed to open IP limit log file: %s", err)
		return false
	}
	defer logIpFile.Close()
	ipLogger := log.New(logIpFile, "", log.LstdFlags)

	for _, item := range pending {
		j.disAllowedIps = make([]string, 0, len(item.entries))
		for _, ipTime := range item.entries {
			j.disAllowedIps = append(j.disAllowedIps, ipTime.IP)
			ipLogger.Printf("[LIMIT_IP] Email = %s || Disconnecting OLD IP = %s || Timestamp = %d", item.email, ipTime.IP, ipTime.Timestamp)
		}
		logger.Infof("[LIMIT_IP] Client %s: Kept %d live IPs, queued %d old IPs for fail2ban", item.email, item.keptCount, len(j.disAllowedIps))
	}
	return true
}

// recordLocalAttribution stores this scan's local observations under this panel's
// own guid so a parent panel can attribute each IP to the node it is on.
// Best-effort: attribution is advisory and must never block IP-limit enforcement.
func (j *CheckClientIpJob) recordLocalAttribution(attribution map[string][]model.ClientIpEntry) {
	if len(attribution) == 0 {
		return
	}
	guid, err := (&service.SettingService{}).GetPanelGuid()
	if err != nil || guid == "" {
		return
	}
	if err := (&service.InboundService{}).RecordLocalClientIps(guid, attribution); err != nil {
		logger.Debug("[LimitIP] record local ip attribution failed:", err)
	}
}

// mergeClientIps folds this scan's observations into the persisted set,
// dropping entries older than staleCutoff. newAlwaysLive exempts the new
// entries from that cutoff: an API-observed IP is a live connection by
// definition, even when its lastSeen (set at dispatch time) is hours old.
func mergeClientIps(old, new []IPWithTimestamp, staleCutoff int64, newAlwaysLive bool) map[string]int64 {
	ipMap := make(map[string]int64, len(old)+len(new))
	for _, ipTime := range old {
		if ipTime.Timestamp < staleCutoff {
			continue
		}
		ipMap[ipTime.IP] = ipTime.Timestamp
	}
	for _, ipTime := range new {
		if !newAlwaysLive && ipTime.Timestamp < staleCutoff {
			continue
		}
		if existingTime, ok := ipMap[ipTime.IP]; !ok || ipTime.Timestamp > existingTime {
			ipMap[ipTime.IP] = ipTime.Timestamp
		}
	}
	return ipMap
}

// selectIpsToBan splits the live IPs (sorted oldest-first by partitionLiveIps)
// into the newest `limit` entries to keep and the older remainder to ban.
func selectIpsToBan(live []IPWithTimestamp, limit int) (kept, banned []IPWithTimestamp) {
	if limit <= 0 || len(live) <= limit {
		return live, nil
	}
	cutoff := len(live) - limit
	return live[cutoff:], live[:cutoff]
}

func partitionLiveIps(ipMap map[string]int64, observedThisScan map[string]bool) (live, historical []IPWithTimestamp) {
	live = make([]IPWithTimestamp, 0, len(observedThisScan))
	historical = make([]IPWithTimestamp, 0, len(ipMap))
	now := time.Now().Unix()
	for ip, ts := range ipMap {
		entry := IPWithTimestamp{IP: ip, Timestamp: ts}
		// Consider an IP "live" if it was seen locally in this scan, OR if its
		// timestamp from the synced database is very recent (e.g. within 2 minutes).
		// This ensures cluster-wide limits work even if the IP was seen on another node.
		if observedThisScan[ip] || now-ts < 120 {
			live = append(live, entry)
		} else {
			historical = append(historical, entry)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].Timestamp < live[j].Timestamp })
	sort.Slice(historical, func(i, j int) bool { return historical[i].Timestamp < historical[j].Timestamp })
	return live, historical
}

func (j *CheckClientIpJob) checkFail2BanInstalled() bool {
	if !isFail2BanEnabled() {
		return false
	}

	cmd := "fail2ban-client"
	args := []string{"-h"}
	err := exec.CommandContext(context.Background(), cmd, args...).Run()
	return err == nil
}

func isFail2BanEnabled() bool {
	value, ok := os.LookupEnv("XUI_ENABLE_FAIL2BAN")
	return !ok || value == "true"
}

func (j *CheckClientIpJob) checkError(e error) {
	if e != nil {
		logger.Warning("client ip job err:", e)
	}
}

// delInboundClientIps drops the inbound_client_ips tracking row for an email
// that no longer maps to any inbound (a renamed or deleted client), so stale
// access-log entries don't keep a ghost row alive (#4963).
func (j *CheckClientIpJob) delInboundClientIps(tx *gorm.DB, clientEmail string) {
	if err := tx.Where("client_email = ?", clientEmail).Delete(&model.InboundClientIps{}).Error; err != nil {
		j.checkError(err)
	}
}

// updateInboundClientIps merges one email's observed IPs into its tracking row
// and applies the IP limit. limitIp comes from the caller (the clients table);
// writes go through the caller's transaction. banned=true asks the caller to
// disconnect the client after the transaction commits.
func (j *CheckClientIpJob) updateInboundClientIps(tx *gorm.DB, inboundClientIps *model.InboundClientIps, inbound *model.Inbound, clientEmail string, limitIp int, newIpsWithTime []IPWithTimestamp, enforce, observedAreLive bool) (shouldCleanLog, banned bool) {
	cleaned, isBanned, _ := j.updateInboundClientIpsWithSeen(tx, inboundClientIps, inbound, clientEmail, limitIp, newIpsWithTime, enforce, observedAreLive, &j.bannedSeen, false)
	return cleaned, isBanned
}

func (j *CheckClientIpJob) updateInboundClientIpsWithSeen(tx *gorm.DB, inboundClientIps *model.InboundClientIps, inbound *model.Inbound, clientEmail string, limitIp int, newIpsWithTime []IPWithTimestamp, enforce, observedAreLive bool, seen *map[string]int64, deferBanLog bool) (shouldCleanLog, banned bool, banLog pendingBanLog) {
	if inbound.Settings == "" {
		logger.Debug("wrong data:", inbound)
		return false, false, pendingBanLog{}
	}

	if !enforce || limitIp <= 0 || !inbound.Enable {
		jsonIps, _ := json.Marshal(newIpsWithTime)
		inboundClientIps.Ips = string(jsonIps)
		if err := tx.Save(inboundClientIps).Error; err != nil {
			logger.Error("failed to save inboundClientIps:", err)
		}
		return false, false, pendingBanLog{}
	}

	var oldIpsWithTime []IPWithTimestamp
	if inboundClientIps.Ips != "" {
		_ = json.Unmarshal([]byte(inboundClientIps.Ips), &oldIpsWithTime)
	}

	ipMap := mergeClientIps(oldIpsWithTime, newIpsWithTime, time.Now().Unix()-ipStaleAfterSeconds, observedAreLive)

	observedThisScan := make(map[string]bool, len(newIpsWithTime))
	for _, ipTime := range newIpsWithTime {
		observedThisScan[ipTime.IP] = true
	}
	liveIps, historicalIps := partitionLiveIps(ipMap, observedThisScan)

	j.disAllowedIps = []string{}

	keptLive, bannedLive := selectIpsToBan(liveIps, limitIp)
	actionable := filterAdvancedSinceLastBanMap(seen, clientEmail, bannedLive)
	if len(actionable) > 0 {
		shouldCleanLog = true
		banned = true

		if deferBanLog {
			banLog = pendingBanLog{
				email:     clientEmail,
				keptCount: len(keptLive),
				entries:   actionable,
			}
		} else {
			logIpFile, err := os.OpenFile(xray.GetIPLimitLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				logger.Errorf("failed to open IP limit log file: %s", err)
				return false, false, pendingBanLog{}
			}
			defer logIpFile.Close()
			ipLogger := log.New(logIpFile, "", log.LstdFlags)

			for _, ipTime := range actionable {
				j.disAllowedIps = append(j.disAllowedIps, ipTime.IP)
				ipLogger.Printf("[LIMIT_IP] Email = %s || Disconnecting OLD IP = %s || Timestamp = %d", clientEmail, ipTime.IP, ipTime.Timestamp)
			}
		}
	}

	dbIps := make([]IPWithTimestamp, 0, len(keptLive)+len(historicalIps))
	dbIps = append(dbIps, keptLive...)
	dbIps = append(dbIps, historicalIps...)
	jsonIps, _ := json.Marshal(dbIps)
	inboundClientIps.Ips = string(jsonIps)

	if err := tx.Save(inboundClientIps).Error; err != nil {
		logger.Error("failed to save inboundClientIps:", err)
		return false, banned, banLog
	}

	if !deferBanLog && len(j.disAllowedIps) > 0 {
		logger.Infof("[LIMIT_IP] Client %s: Kept %d live IPs, queued %d old IPs for fail2ban", clientEmail, len(keptLive), len(j.disAllowedIps))
	}

	return shouldCleanLog, banned, banLog
}

func (j *CheckClientIpJob) filterAdvancedSinceLastBan(email string, banned []IPWithTimestamp) []IPWithTimestamp {
	return filterAdvancedSinceLastBanMap(&j.bannedSeen, email, banned)
}

func filterAdvancedSinceLastBanMap(seen *map[string]int64, email string, banned []IPWithTimestamp) []IPWithTimestamp {
	if *seen == nil {
		*seen = make(map[string]int64)
	}
	bannedSeen := *seen
	current := make(map[string]struct{}, len(banned))
	actionable := make([]IPWithTimestamp, 0, len(banned))
	for _, ipTime := range banned {
		key := email + "|" + ipTime.IP
		current[key] = struct{}{}
		if last, ok := bannedSeen[key]; ok && ipTime.Timestamp <= last {
			continue
		}
		bannedSeen[key] = ipTime.Timestamp
		actionable = append(actionable, ipTime)
	}
	prefix := email + "|"
	for key := range bannedSeen {
		if strings.HasPrefix(key, prefix) {
			if _, still := current[key]; !still {
				delete(bannedSeen, key)
			}
		}
	}
	return actionable
}

func (j *CheckClientIpJob) disconnectClientTemporarily(inbound *model.Inbound, clientEmail string, clients []model.Client) {
	j.disconnectClientTemporarilyForProcess(inbound, clientEmail, clients, nil)
}

func (j *CheckClientIpJob) disconnectClientTemporarilyForProcess(inbound *model.Inbound, clientEmail string, clients []model.Client, sampleProcess *xray.Process) {
	var clientConfig map[string]any
	for _, client := range clients {
		if client.Email == clientEmail {
			clientBytes, _ := json.Marshal(client)
			_ = json.Unmarshal(clientBytes, &clientConfig)
			break
		}
	}

	if clientConfig == nil {
		return
	}

	protocol := string(inbound.Protocol)
	switch protocol {
	case "vmess", "vless", "trojan", "shadowsocks":
	default:
		logger.Warningf("[LIMIT_IP] Temporary disconnect is not supported for protocol %s on inbound %s", protocol, inbound.Tag)
		return
	}

	if string(inbound.Protocol) == "shadowsocks" {
		var inboundSettings map[string]any
		if err := json.Unmarshal([]byte(inbound.Settings), &inboundSettings); err != nil {
			logger.Warningf("[LIMIT_IP] Failed to parse inbound settings for shadowsocks cipher: %v", err)
		} else {
			if method, ok := inboundSettings["method"].(string); ok && method != "" {
				clientConfig["cipher"] = method
			}
		}
	}

	if sampleProcess != nil {
		provider := j.onlineProvider()
		removed, err := provider.RemoveUserForProcess(sampleProcess, inbound.Tag, clientEmail)
		if err != nil {
			logger.Warningf("[LIMIT_IP] Failed to remove user %s: %v", clientEmail, err)
			return
		}
		if !removed {
			logger.Debugf("[LIMIT_IP] Skipping disconnect for %s: Xray process changed", clientEmail)
			return
		}

		j.pauseForDisconnect()
		if !provider.IsCurrentProcess(sampleProcess) {
			logger.Debugf("[LIMIT_IP] Not restoring %s: Xray process changed during temporary disconnect", clientEmail)
			return
		}
		if !j.clientStillServed(inbound.Id, clientEmail) {
			logger.Infof("[LIMIT_IP] Not restoring %s: disabled or removed during its temporary disconnect", clientEmail)
			return
		}

		added, err := provider.AddUserForProcess(sampleProcess, protocol, inbound.Tag, clientConfig)
		if err != nil {
			logger.Warningf("[LIMIT_IP] Failed to re-add user %s: %v", clientEmail, err)
			return
		}
		if !added {
			logger.Debugf("[LIMIT_IP] Skipped restoring %s: Xray process changed", clientEmail)
			return
		}
		if !j.clientStillServed(inbound.Id, clientEmail) {
			if _, err := provider.RemoveUserForProcess(sampleProcess, inbound.Tag, clientEmail); err != nil {
				logger.Warningf("[LIMIT_IP] Failed to remove user %s again: %v", clientEmail, err)
			}
		}
		return
	}

	var xrayAPI xray.XrayAPI
	apiPort := j.resolveXrayAPIPort()

	err := xrayAPI.Init(apiPort)
	if err != nil {
		logger.Warningf("[LIMIT_IP] Failed to init Xray API for disconnection: %v", err)
		return
	}
	defer xrayAPI.Close()

	err = xrayAPI.RemoveUser(inbound.Tag, clientEmail)
	if err != nil {
		logger.Warningf("[LIMIT_IP] Failed to remove user %s: %v", clientEmail, err)
		return
	}

	j.pauseForDisconnect()
	if !j.clientStillServed(inbound.Id, clientEmail) {
		logger.Infof("[LIMIT_IP] Not restoring %s: disabled or removed during its temporary disconnect", clientEmail)
		return
	}

	err = xrayAPI.AddUser(protocol, inbound.Tag, clientConfig)
	if err != nil {
		logger.Warningf("[LIMIT_IP] Failed to re-add user %s: %v", clientEmail, err)
		return
	}
	if !j.clientStillServed(inbound.Id, clientEmail) {
		if err := xrayAPI.RemoveUser(inbound.Tag, clientEmail); err != nil {
			logger.Warningf("[LIMIT_IP] Failed to remove user %s again: %v", clientEmail, err)
		}
	}
}

func (j *CheckClientIpJob) clientStillServed(inboundID int, email string) bool {
	var served int64
	err := database.GetDB().Table("client_inbounds").
		Joins("JOIN clients ON clients.id = client_inbounds.client_id").
		Joins("JOIN inbounds ON inbounds.id = client_inbounds.inbound_id").
		Where("client_inbounds.inbound_id = ? AND inbounds.enable = ?", inboundID, true).
		Where("clients.email = ? AND clients.enable = ?", email, true).
		Where("NOT EXISTS (SELECT 1 FROM client_traffics WHERE client_traffics.email = clients.email AND client_traffics.enable = ?)", false).
		Count(&served).Error
	j.checkError(err)
	return err == nil && served > 0
}

// resolveXrayAPIPort returns the API inbound port from running config, then template config, then default.
func (j *CheckClientIpJob) resolveXrayAPIPort() int {
	var configErr error
	var templateErr error

	if port, err := getAPIPortFromConfigPath(xray.GetConfigPath()); err == nil {
		return port
	} else {
		configErr = err
	}

	db := database.GetDB()
	var template model.Setting
	if err := db.Where("key = ?", "xrayTemplateConfig").First(&template).Error; err == nil {
		if port, parseErr := getAPIPortFromConfigData([]byte(template.Value)); parseErr == nil {
			return port
		} else {
			templateErr = parseErr
		}
	} else {
		templateErr = err
	}

	logger.Warningf(
		"[LIMIT_IP] Could not determine Xray API port from config or template; falling back to default port %d (config error: %v, template error: %v)",
		defaultXrayAPIPort,
		configErr,
		templateErr,
	)

	return defaultXrayAPIPort
}

func getAPIPortFromConfigPath(configPath string) (int, error) {
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return 0, err
	}

	return getAPIPortFromConfigData(configData)
}

func getAPIPortFromConfigData(configData []byte) (int, error) {
	xrayConfig := &xray.Config{}
	if err := json.Unmarshal(configData, xrayConfig); err != nil {
		return 0, err
	}

	for _, inboundConfig := range xrayConfig.InboundConfigs {
		if inboundConfig.Tag == "api" && inboundConfig.Port > 0 {
			return inboundConfig.Port, nil
		}
	}

	return 0, errors.New("api inbound port not found")
}

// getInboundByEmail resolves the inbound that owns a client email. It prefers
// the exact clients/client_inbounds relation; a substring "settings LIKE
// %email%" can match the wrong inbound (an email that is a substring of another,
// or text that merely appears elsewhere in the settings JSON). The LIKE + JSON
// scan stays only as a fallback for clients not yet present in the relation, so
// nothing regresses when the join finds no row.
func (j *CheckClientIpJob) getInboundByEmail(clientEmail string) (*model.Inbound, error) {
	db := database.GetDB()
	inbound := &model.Inbound{}

	err := db.Model(&model.Inbound{}).
		Joins("JOIN client_inbounds ON client_inbounds.inbound_id = inbounds.id").
		Joins("JOIN clients ON clients.id = client_inbounds.client_id").
		Where("clients.email = ?", clientEmail).
		First(inbound).Error
	if err == nil {
		return inbound, nil
	}

	var candidates []model.Inbound
	if listErr := db.Model(&model.Inbound{}).Where("settings LIKE ?", "%"+clientEmail+"%").Find(&candidates).Error; listErr != nil {
		return nil, listErr
	}
	for i := range candidates {
		clients, jsonErr := service.ParseInboundSettingsClients(candidates[i].Settings)
		if jsonErr != nil {
			continue
		}
		for _, client := range clients {
			if client.Email == clientEmail {
				return &candidates[i], nil
			}
		}
	}

	return nil, err
}
