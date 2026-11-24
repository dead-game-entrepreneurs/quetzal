package snowblind

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/princebyyourlogic/quetzal/internal/protocol/quazal"
)

const (
	clanAbbreviationTaken uint32 = 0xcb
	clanNameTaken         uint32 = 0xcc
	clanAlreadyMember     uint32 = 0xcd
	clanIllegalText       uint32 = 0xd4
	maxClans                     = 4096
	maxClanStateBytes            = 1 << 20
	maxClanNotices               = 3
	maxClanNoticeBytes           = 70
)

// Clan identity and membership belong to this tenant. No client-supplied rank
// or notices confer authority; a newly created clan contains only its founder.
type clanRecord struct {
	Abbreviation string       `json:"abbreviation"`
	Name         string       `json:"name"`
	FounderPID   uint32       `json:"founder_pid"`
	Notices      []clanNotice `json:"notices,omitempty"`
	Members      []uint32     `json:"members,omitempty"`
	Officers     []uint32     `json:"officers,omitempty"`
	Unread       []uint32     `json:"unread_notices,omitempty"`
}

type clanNotice struct {
	Author string `json:"author"`
	Body   string `json:"body"`
}

type clanDisk struct {
	Version     int              `json:"version"`
	Clans       []clanRecord     `json:"clans"`
	Invitations []clanInvitation `json:"invitations,omitempty"`
	Declined    []uint32         `json:"declined_invitations,omitempty"`
}

type clanStore struct {
	mu          sync.Mutex
	path        string
	accounts    *quazal.Store
	records     []clanRecord
	online      func(uint32) bool
	invitations []clanInvitation
	declined    []uint32
}

func openClans(path string, accounts *quazal.Store) (*clanStore, error) {
	if accounts == nil || path == "" {
		return nil, errors.New("Snowblind clan path and account store are required")
	}
	s := &clanStore{path: path, accounts: accounts, records: []clanRecord{}}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxClanStateBytes {
		return nil, errors.New("invalid Snowblind clan state file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var disk clanDisk
	if err := d.Decode(&disk); err != nil {
		return nil, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF || disk.Version != 1 || disk.Clans == nil || len(disk.Clans) > maxClans {
		return nil, errors.New("invalid Snowblind clan state")
	}
	abbreviations, names, founders := map[string]bool{}, map[string]bool{}, map[uint32]bool{}
	for _, clan := range disk.Clans {
		abbr, name := strings.ToLower(clan.Abbreviation), strings.ToLower(clan.Name)
		if !clanText(clan.Abbreviation, 3) || !clanText(clan.Name, 20) || clan.FounderPID < 1000 || abbreviations[abbr] || names[name] || founders[clan.FounderPID] {
			return nil, errors.New("invalid Snowblind clan record")
		}
		if _, ok := accounts.LookupPID(clan.FounderPID); !ok {
			return nil, errors.New("Snowblind clan founder account is missing")
		}
		if len(clan.Notices) > maxClanNotices {
			return nil, errors.New("invalid Snowblind clan notice count")
		}
		for _, notice := range clan.Notices {
			if !clanText(notice.Author, 15) || !clanNoticeText(notice.Body) {
				return nil, errors.New("invalid Snowblind clan notice")
			}
		}
		abbreviations[abbr], names[name], founders[clan.FounderPID] = true, true, true
	}
	s.records = disk.Clans
	s.invitations, s.declined = disk.Invitations, disk.Declined
	if err := s.validateInvitationsAndMembers(); err != nil {
		return nil, err
	}
	return s, nil
}

func clanText(text string, limit int) bool {
	if text == "" || len(text) > limit || strings.TrimSpace(text) != text {
		return false
	}
	for _, c := range []byte(text) {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func clanStatus(status uint32) ([]byte, error) {
	var w quazal.Writer
	w.U32(status)
	return w.Bytes()
}

func clanNoticeText(text string) bool {
	if len(text) == 0 || len(text) > maxClanNoticeBytes || strings.TrimSpace(text) == "" {
		return false
	}
	for _, c := range []byte(text) {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func (s *clanStore) handle(pid uint32, request quazal.Request) ([]byte, error) {
	account, ok := s.accounts.LookupPID(pid)
	if request.Protocol != 102 || !ok || pid < 1000 {
		return nil, quazal.ErrRMC
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Method >= 4 && request.Method <= 10 {
		return s.invitationRPC(pid, request)
	}
	if request.Method == 13 || request.Method == 14 {
		return s.roleRPC(pid, request)
	}
	switch request.Method {
	case 3:
		// Native 0x16f660 writes ClanDesc through 0x173950; reply is u32.
		r := quazal.NewReader(request.Args)
		abbr, name := r.String(255), r.String(255)
		r.U32() // Client rank is not authoritative.
		notices := r.U32()
		if !r.Done() || notices != 0 {
			return nil, quazal.ErrRMC
		}
		if !clanText(abbr, 3) || !clanText(name, 20) {
			return clanStatus(clanIllegalText)
		}
		for _, clan := range s.records {
			if clanHasMember(clan, pid) {
				if clan.FounderPID == pid && strings.EqualFold(clan.Abbreviation, abbr) && strings.EqualFold(clan.Name, name) {
					return clanStatus(0) // Retry of an already committed creation.
				}
				return clanStatus(clanAlreadyMember)
			}
		}
		for _, clan := range s.records {
			if strings.EqualFold(clan.Abbreviation, abbr) {
				return clanStatus(clanAbbreviationTaken)
			}
			if strings.EqualFold(clan.Name, name) {
				return clanStatus(clanNameTaken)
			}
		}
		if len(s.records) >= maxClans {
			return nil, errors.New("Snowblind clan capacity reached")
		}
		next := append(append([]clanRecord{}, s.records...), clanRecord{Abbreviation: abbr, Name: name, FounderPID: pid})
		pending := []clanInvitation{}
		for _, inv := range s.invitations {
			if inv.To != pid {
				pending = append(pending, inv)
			}
		}
		if err := saveState(s.path, ".clans-*", clanDisk{Version: 1, Clans: next, Invitations: pending, Declined: s.declined}, maxClanStateBytes); err != nil {
			return nil, err
		}
		s.records, s.invitations = next, pending
		return clanStatus(0)
	case 2:
		if len(request.Args) != 0 {
			return nil, quazal.ErrRMC
		}
		return s.fetchClan(pid)
	case 16:
		if len(request.Args) != 0 {
			return nil, quazal.ErrRMC
		}
		var w quazal.Writer
		w.U32(0)
		s.writeAlerts(&w, pid)
		return w.Bytes()
	case 17:
		// Native 0x170f50: one notice String, then a u32 application status.
		r := quazal.NewReader(request.Args)
		body := r.String(1024)
		if !r.Done() {
			return nil, quazal.ErrRMC
		}
		for i, clan := range s.records {
			if !clanCanRecruit(clan, pid) {
				if clanHasMember(clan, pid) {
					return clanStatus(clanPermission)
				}
				continue
			}
			if !clanNoticeText(body) {
				return clanStatus(clanIllegalText)
			}
			notices := append([]clanNotice{{Author: account.Name, Body: body}}, clan.Notices...)
			if len(notices) > maxClanNotices {
				notices = notices[:maxClanNotices]
			}
			next := append([]clanRecord{}, s.records...)
			next[i].Notices = notices
			next[i].Unread = nil
			for _, member := range clanMembers(clan) {
				if member != pid {
					next[i].Unread = append(next[i].Unread, member)
				}
			}
			if err := s.save(next); err != nil {
				return nil, err
			}
			s.records = next
			return clanStatus(0)
		}
		return clanStatus(clanNotMember)
	case 18:
		// Native 0x171110: MarkNoticesRead has no arguments and a u32 status.
		if len(request.Args) != 0 {
			return nil, quazal.ErrRMC
		}
		for i, clan := range s.records {
			if clanHasMember(clan, pid) {
				if containsPID(clan.Unread, pid) {
					next := append([]clanRecord{}, s.records...)
					next[i].Unread = withoutPID(clan.Unread, pid)
					if err := s.save(next); err != nil {
						return nil, err
					}
					s.records = next
				}
				return clanStatus(0)
			}
		}
		return clanStatus(clanNotMember)
	default:
		return nil, errors.New("unsupported Snowblind clan method")
	}
}

func writeClanMember(w *quazal.Writer, name string, leader, officer, accepting, online bool) {
	w.String(name)
	w.String("")
	w.Bool(leader)    // Leader: UI controls at 0x207a18/0x207b60 consume member +0x10.
	w.Bool(officer)   // Native member +0x15 -> UI +0x11; Officer role.
	w.Bool(accepting) // Member +0x16 -> UI +0x12; accepting clan invitations.
	w.U32(0)          // No typed presence data attached.
	w.Bool(online)    // Native member +0x24 -> UI +0x18; 0x2068c0 selects Online/Offline.
}

func (s *clanStore) abbreviation(pid uint32) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, clan := range s.records {
		if clanHasMember(clan, pid) {
			return clan.Abbreviation
		}
	}
	return ""
}

func (s *clanStore) save(records []clanRecord) error {
	return saveState(s.path, ".clans-*", clanDisk{Version: 1, Clans: records, Invitations: s.invitations, Declined: s.declined}, maxClanStateBytes)
}
