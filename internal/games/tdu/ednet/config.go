package ednet

import "strings"

// DefaultSetting is a numeric client setting. The retail client caches each
// supplied value in integer, float, u16 and byte forms; the consumer selects
// its representation. These are recovered constructor defaults, not a claim
// that SessionMaster, density or other game services have been implemented.
type DefaultSetting struct{ Name, Value string }

var defaultSettings = [...]DefaultSetting{
	{"QOS_BANDWIDTH_RETIME_MINIMAL", "1300"},
	{"QOS_BANDWIDTH_GAMEOBJECTS", "8000"},
	{"CORRECTION_ACTIVE", "3"},
	{"CORRECTION_DT", "0.220"},
	{"MAX_CORRECTION_DT", "0.5"},
	{"RETIME_DT", "50"},
	{"CORRECTION_RANGE", "0.5"},
	{"MINIMALUPDATE_DT", "0.25"},
	{"FORCEUPDATE_DT", "0.33"},
	{"ORB_REFRESH_DT", "10000"},
	{"ORB_LOST_RPC_DT", "60000"},
	{"USERMAP_REFRESH_DT", "10000"},
	{"DENSITY_MAP_DT", "5"},
	{"SESSION_DENSITY_DT", "10"},
	{"PLAYERS_NEAR_DT", "5"},
	{"P2PFR_TIMEOUT_START_SCAN", "10000"},
	{"P2PFR_TIMEOUT_ELECT", "15000"},
	{"P2PFR_TIMEOUT_INITIAL_CORJ", "5000"},
	{"P2PFR_TIMEOUT_ALONE", "15000"},
	{"P2PFR_ACTIVE", "1"},
	{"P2PFR_STRENGTH_RADIUS", "1500"},
	{"PROXY_HEARTBEAT", "8.0"},
	{"LOG_ROADS", "1"},
	{"INSTANT_CHALLENGES", "1"},
	{"ONLINE_FREERIDE", "1"},
	{"GAMESTAT_REPORT_INCREMENT", "30.0"},
	{"MAX_SESSIONS_QUERY", "10"},
	{"LOG_GAMEMODE_CHANGES", "0"},
	{"LOG_RACE_RESULTS", "1"},
	{"P2PGAMESERVICE_TIMEOUT", "11000"},
	{"P2PGAMESERVICE_INITIALTIMEOUT", "20000"},
	{"P2PGAMESERVICE_KEEPALIVE_DT", "3000"},
	{"P2PGAMESERVICE_SESSION_KEEPALIVE_DT", "3000"},
	{"P2PGAMESERVICE_CONSISTENCY_DT", "22000"},
	{"LAIR_HEALTH_CHECK_FREQ", "0.0"},
	{"EDNETACTIONCACHE_TIMEOUT", "5000"},
	{"EDNETACTION_DEFAULT_TIMEOUT", "0.250"},
	{"EDNETACTION_DEFAULT_MAX_RETRY", "20"},
	{"MAX_MASTER_REJECTIONS", "6"},
}

// DefaultSettings returns an independent copy of the 39 settings established
// by retail constructor 0x4095c0. No game assets are read or embedded.
func DefaultSettings() []DefaultSetting { return append([]DefaultSetting(nil), defaultSettings[:]...) }

// DefaultNetworkConfig explicitly retains the compiled client network policy.
// Parser 0x409300 looks up these named children and reads their value attribute.
// No service addresses, account authorization or success flags occur in this XML.
func DefaultNetworkConfig() []byte {
	var b strings.Builder
	b.WriteString("<NETCONFIG>\n")
	for _, s := range defaultSettings {
		b.WriteString("  <")
		b.WriteString(s.Name)
		b.WriteString(" value=\"")
		b.WriteString(s.Value)
		b.WriteString("\"/>\n")
	}
	b.WriteString("</NETCONFIG>\n")
	return []byte(b.String())
}

// NoAwardsConfig means the server has no pending prize grants. Parser 0x461720
// visits PRIZE children; zero children leave both prize high-water marks and
// the player's money/car inventory unchanged. This is not a fabricated grant.
func NoAwardsConfig() []byte { return []byte("<PRIZES></PRIZES>\n") }
