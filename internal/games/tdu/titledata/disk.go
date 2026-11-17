package titledata

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const diskVersion = 2

// The wire SDK strings are byte strings, not necessarily UTF-8. Keep challenge
// text out of JSON strings so encoding/json cannot replace legacy text bytes.
// Account names are separately validated and canonicalized by the account store.
type fileStatsV2 struct {
	ID          uint64    `json:"id"`
	Category    uint32    `json:"category"`
	Name        []byte    `json:"name_bytes"`
	Description []byte    `json:"description_bytes"`
	Creator     string    `json:"creator"`
	Reserved    [7]uint32 `json:"reserved"`
	Flag        byte      `json:"flag"`
}
type fileV2 struct {
	Stats fileStatsV2 `json:"stats"`
	Owner uint32      `json:"owner"`
	Data  []byte      `json:"data"`
}
type stateV2 struct {
	Version  int                   `json:"version"`
	NextFile uint32                `json:"next_file"`
	Games    map[uint32]GameStats  `json:"games"`
	Events   map[string]EventStats `json:"events"`
	Files    map[uint32]fileV2     `json:"files"`
}

func encodeState(d diskState) ([]byte, error) {
	out := stateV2{Version: diskVersion, NextFile: d.NextFile, Games: d.Games, Events: d.Events, Files: make(map[uint32]fileV2, len(d.Files))}
	for id, f := range d.Files {
		out.Files[id] = fileV2{
			Owner: f.Owner,
			Data:  f.Data,
			Stats: fileStatsV2{
				ID:          f.Stats.ID,
				Category:    f.Stats.Category,
				Name:        []byte(f.Stats.Name),
				Description: []byte(f.Stats.Description),
				Creator:     f.Stats.Creator,
				Reserved:    f.Stats.Reserved,
				Flag:        f.Stats.Flag,
			},
		}
	}
	return json.Marshal(out)
}

func decodeState(b []byte) (diskState, error) {
	var header struct{ Version int }
	if err := json.Unmarshal(b, &header); err != nil {
		return diskState{}, err
	}
	switch header.Version {
	case 1:
		// Version 1 stored SDK text as JSON strings. Read surviving bytes unchanged;
		// any replacement already written by the old encoder cannot be recovered.
		var d diskState
		if err := decodeStrict(b, &d); err != nil {
			return diskState{}, err
		}
		d.Version = diskVersion
		return d, nil
	case diskVersion:
		var in stateV2
		if err := decodeStrict(b, &in); err != nil {
			return diskState{}, err
		}
		d := diskState{Version: diskVersion, NextFile: in.NextFile, Games: in.Games, Events: in.Events}
		if in.Files != nil {
			d.Files = make(map[uint32]File, len(in.Files))
		}
		for id, f := range in.Files {
			// Missing/null byte strings are malformed. Empty descriptions are encoded
			// explicitly as an empty base64 string, and remain distinct from omission.
			if f.Stats.Name == nil || f.Stats.Description == nil {
				return diskState{}, errors.New("missing challenge text bytes")
			}
			d.Files[id] = File{
				Owner: f.Owner,
				Data:  f.Data,
				Stats: FileStats{
					ID:          f.Stats.ID,
					Category:    f.Stats.Category,
					Name:        string(f.Stats.Name),
					Description: string(f.Stats.Description),
					Creator:     f.Stats.Creator,
					Reserved:    f.Stats.Reserved,
					Flag:        f.Stats.Flag,
				},
			}
		}
		return d, nil
	default:
		return diskState{}, errors.New("unsupported title data version")
	}
}

func decodeStrict(b []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return errors.New("trailing title data")
	}
	return nil
}
