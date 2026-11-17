// Package capture preserves complete received UDP payloads for protocol research.
package capture

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Recorder rotates an active NDJSON file and three backups, including across restarts.
type Recorder struct {
	mu          sync.Mutex
	file        *os.File
	path        string
	size, limit int64
}

type Packet struct {
	Time       time.Time `json:"time"`
	Game       string    `json:"game"`
	Transport  string    `json:"transport"`
	Direction  string    `json:"direction"`
	Local      string    `json:"local"`
	Remote     string    `json:"remote"`
	Length     int       `json:"length"`
	PayloadHex string    `json:"payload_hex"`
}

func Open(dir string, limit int64) (*Recorder, error) {
	if limit < 262144 {
		return nil, fmt.Errorf("capture file limit must be at least 262144 bytes")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	r := &Recorder{path: filepath.Join(dir, "packets.ndjson"), limit: limit}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Recorder) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.file, r.size = f, st.Size()
	return nil
}

func (r *Recorder) rotate() error {
	if err := r.file.Close(); err != nil {
		return err
	}
	r.file = nil
	for i := 3; i >= 1; i-- {
		src := r.path
		if i > 1 {
			src = fmt.Sprintf("%s.%d", r.path, i-1)
		}
		err := os.Rename(src, fmt.Sprintf("%s.%d", r.path, i))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return r.open()
}

func (r *Recorder) Record(game, local, remote string, payload []byte) error {
	return r.RecordDirection("rx", game, local, remote, payload)
}

func (r *Recorder) RecordDirection(direction, game, local, remote string, payload []byte) error {
	if direction != "rx" && direction != "tx" {
		return fmt.Errorf("invalid capture direction %q", direction)
	}
	line, err := json.Marshal(Packet{Time: time.Now().UTC(), Game: game, Transport: "udp", Direction: direction,
		Local: local, Remote: remote, Length: len(payload), PayloadHex: hex.EncodeToString(payload)})
	if err != nil {
		return err
	}
	line = append(line, '\n')
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return fmt.Errorf("capture recorder is closed")
	}
	if r.size > 0 && r.size+int64(len(line)) > r.limit {
		if err := r.rotate(); err != nil {
			return err
		}
	}
	n, err := r.file.Write(line)
	r.size += int64(n)
	return err
}

func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}
