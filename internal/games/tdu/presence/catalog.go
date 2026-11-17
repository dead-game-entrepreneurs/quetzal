package presence

import (
	"encoding/json"
	"io"
	"os"
	"strings"
)

// StockActivityCount is the active US PS2 CPSpots count: 210 non-race island
// spots plus 238 mission records. These are factual metadata counts, not a
// geographic grid or a copy of the game's spot/mission assets.
const StockActivityCount = 448

// AggregateDensityIndex is the extra aggregate index selected by the native UI
// for activity type 6, used by DriveIn_PS2. It is one past the last ordinary
// activity index; FreeRideV5 cruise groups use another namespace.
const AggregateDensityIndex = StockActivityCount
const SessionDensityCells = StockActivityCount + 1
const MissionActivityStart = 210
const MissionActivityCount = 238

// DriveInSessionName is constructed by the retail Drive-In initializer362640.
// FreeRideV5 cruise groups use a different name and do not belong in this slot.
const DriveInSessionName = "DriveIn_PS2"

const MaxActivityCatalogBytes = 64 * 1024

// ActivityCatalog is the generated operator metadata format. A partial catalog
// counts only its explicitly listed mission names; indices are never inferred at runtime.
type ActivityCatalog struct {
	Schema        int             `json:"schema"`
	Build         string          `json:"build"`
	ActivityCount int             `json:"activity_count"`
	Activities    []ActivityEntry `json:"activities"`
}
type ActivityEntry struct {
	Name  string `json:"name"`
	Index uint16 `json:"index"`
}

// LoadActivityCatalog loads optional protocol metadata from a private regular
// file (no symlink, no group/other permissions), never a game asset.
// Empty path disables ordinary activity routing. DriveIn_PS2 remains a separate
// fixed retail fact; callers must not map arbitrary or FreeRide names there.
func LoadActivityCatalog(path string) (map[string]uint16, error) {
	if path == "" {
		return nil, nil
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() > MaxActivityCatalogBytes {
		return nil, ErrMalformed
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, st) || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > MaxActivityCatalogBytes {
		return nil, ErrMalformed
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxActivityCatalogBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxActivityCatalogBytes {
		return nil, ErrMalformed
	}
	var document ActivityCatalog
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || decoder.Decode(new(any)) != io.EOF || document.Schema != 1 || document.Build != "SLUS_214.90" || document.ActivityCount != StockActivityCount || len(document.Activities) < 1 || len(document.Activities) > MissionActivityCount {
		return nil, ErrMalformed
	}
	result := make(map[string]uint16, len(document.Activities))
	used := make(map[uint16]bool, len(document.Activities))
	for _, v := range document.Activities {
		if v.Index < MissionActivityStart || v.Index >= StockActivityCount || v.Name == DriveInSessionName || !strings.HasSuffix(v.Name, "_PS2") {
			return nil, ErrMalformed
		}
		prefix := strings.TrimSuffix(v.Name, "_PS2")
		if len(prefix) < 1 || len(prefix) > 8 {
			return nil, ErrMalformed
		}
		for i := range len(prefix) {
			if prefix[i] < 33 || prefix[i] > 126 {
				return nil, ErrMalformed
			}
		}
		if _, exists := result[v.Name]; exists || used[v.Index] {
			return nil, ErrMalformed
		}
		result[v.Name] = v.Index
		used[v.Index] = true
	}
	return result, nil
}
