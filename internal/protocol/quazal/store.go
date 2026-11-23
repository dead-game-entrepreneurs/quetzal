// Adapted from sibling tdu-server; title differences are documented in docs/games/25tolife/analysis.md.
package quazal

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/princebyyourlogic/quetzal/internal/quazalcrypto"
)

const GuestPID uint32 = 100
const guestName = "guest"

// The stock SDK bootstrap value embedded in the retail client, not an operator secret.
const guestPassword = "h7fyctiuucf"

var (
	ErrAccountExists = errors.New("quazal: account already exists")
	ErrAccountName   = errors.New("quazal: invalid or reserved account name")
)

// Account stores an initial signup hash, never the raw password.
type Account struct {
	Name                    string
	PID                     uint32
	InitialKey              quazalcrypto.Key
	PublicData, PrivateData *AnyData
	Contact                 string
}

type diskAccount struct {
	Name        string   `json:"name"`
	PID         uint32   `json:"pid"`
	InitialHash string   `json:"initial_hash"`
	PublicData  *AnyData `json:"public_data,omitempty"`
	PrivateData *AnyData `json:"private_data,omitempty"`
	Contact     string   `json:"contact,omitempty"`
}
type diskStore struct {
	Version  int           `json:"version"`
	NextPID  uint32        `json:"next_pid"`
	Accounts []diskAccount `json:"accounts"`
}

// Store owns shared accounts and transient issued-ticket state for both roles.
// Use one Store instance per account file; two independent writers are unsupported.
type Store struct {
	mu                 sync.Mutex
	path               string
	nextPID            uint32
	accounts           map[string]Account
	reserved           map[uint32]bool
	tickets            map[quazalcrypto.Key]*issuedTicket
	credentialVersions map[uint32]uint64
	names              NamePolicy
}

// NamePolicy is a title's account-name rule. The zero value is the original
// rule: 3-15 printable ASCII characters without spaces.
type NamePolicy struct {
	// InnerSpaces also accepts spaces between other characters, but not at
	// the start or end. World Championship Cards documents this rule in its
	// own name-entry error text.
	InnerSpaces bool
}

func (p NamePolicy) canonical(name string) (string, error) {
	if len(name) < 3 || len(name) > 15 {
		return "", ErrAccountName
	}
	for _, b := range []byte(name) {
		if b == ' ' && p.InnerSpaces {
			continue
		}
		if b < 0x21 || b > 0x7e {
			return "", ErrAccountName
		}
	}
	if name[0] == ' ' || name[len(name)-1] == ' ' {
		return "", ErrAccountName
	}
	return strings.ToLower(name), nil
}

// canonicalName applies this store's title name policy.
func (s *Store) canonicalName(name string) (string, error) { return s.names.canonical(name) }

// OpenStore loads or creates a versioned JSON account file with mode 0600,
// using the default account-name rule.
func OpenStore(path string) (*Store, error) { return OpenStoreWithNames(path, NamePolicy{}) }

// OpenStoreWithNames is OpenStore with a title-specific account-name rule.
// The rule also validates names already stored in the file.
func OpenStoreWithNames(path string, names NamePolicy) (*Store, error) {
	if path == "" {
		return nil, errors.New("quazal: account store path is required")
	}
	s := &Store{path: path, nextPID: 1000, accounts: make(map[string]Account), reserved: map[uint32]bool{0: true, GuestPID: true}, tickets: make(map[quazalcrypto.Key]*issuedTicket), credentialVersions: make(map[uint32]uint64), names: names}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		if err := s.persist(s.accounts, s.nextPID); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("quazal: account file must be regular and private (0600)")
	}
	if info.Size() > 16<<20 {
		return nil, errors.New("quazal: account file exceeds size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, (16<<20)+1))
	decoder.DisallowUnknownFields()
	var disk diskStore
	if err := decoder.Decode(&disk); err != nil {
		return nil, fmt.Errorf("quazal: load account store: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("quazal: trailing account store data")
	}
	if (disk.Version != 1 && disk.Version != 2) || disk.NextPID < 1000 {
		return nil, errors.New("quazal: invalid account store version or PID counter")
	}
	used := map[uint32]bool{0: true, GuestPID: true}
	for _, row := range disk.Accounts {
		name, err := s.canonicalName(row.Name)
		if err != nil || name == guestName || row.PID < 1000 || used[row.PID] || row.PID >= disk.NextPID {
			return nil, errors.New("quazal: invalid account identity in store")
		}
		if _, exists := s.accounts[name]; exists {
			return nil, ErrAccountExists
		}
		key, err := hex.DecodeString(row.InitialHash)
		if err != nil || len(key) != 16 {
			return nil, errors.New("quazal: invalid account hash in store")
		}
		var initial quazalcrypto.Key
		copy(initial[:], key)
		if row.PublicData != nil || row.PrivateData != nil {
			if !validData(row.PublicData) || !validData(row.PrivateData) {
				return nil, errors.New("quazal: invalid stored account data")
			}
		}
		if len(row.Contact) > 255 {
			return nil, errors.New("quazal: invalid stored contact")
		}
		s.accounts[name] = Account{Name: row.Name, PID: row.PID, InitialKey: initial, PublicData: cloneData(row.PublicData), PrivateData: cloneData(row.PrivateData), Contact: row.Contact}
		used[row.PID] = true
	}
	s.nextPID = disk.NextPID
	return s, nil
}

func (s *Store) persist(accounts map[string]Account, next uint32) error {
	disk := diskStore{Version: 2, NextPID: next, Accounts: make([]diskAccount, 0, len(accounts))}
	for _, a := range accounts {
		disk.Accounts = append(disk.Accounts, diskAccount{Name: a.Name, PID: a.PID, InitialHash: hex.EncodeToString(a.InitialKey[:]), PublicData: a.PublicData, PrivateData: a.PrivateData, Contact: a.Contact})
	}
	sort.Slice(disk.Accounts, func(i, j int) bool { return disk.Accounts[i].PID < disk.Accounts[j].PID })
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".accounts-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	fail := func(err error) error { _ = temporary.Close(); return err }
	if err := temporary.Chmod(0600); err != nil {
		return fail(err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fail(err)
	}
	if err := temporary.Sync(); err != nil {
		return fail(err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, s.path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (s *Store) reservePID(pid uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pid == 0 || pid == GuestPID {
		return errors.New("quazal: server PID conflicts with a reserved identity")
	}
	for _, a := range s.accounts {
		if a.PID == pid {
			return errors.New("quazal: server PID conflicts with an account")
		}
	}
	s.reserved[pid] = true
	return nil
}

func (s *Store) Lookup(name string) (Account, bool) {
	canonical, err := s.canonicalName(name)
	if err != nil {
		return Account{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[canonical]
	a.PublicData = cloneData(a.PublicData)
	a.PrivateData = cloneData(a.PrivateData)
	return a, ok
}

// LookupPID returns a detached account record for authenticated game services.
func (s *Store) LookupPID(pid uint32) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, account := range s.accounts {
		if account.PID == pid {
			account.PublicData = cloneData(account.PublicData)
			account.PrivateData = cloneData(account.PrivateData)
			return account, true
		}
	}
	return Account{}, false
}

func (s *Store) UpdateData(pid uint32, public, private AnyData) error {
	if !validData(&public) || !validData(&private) {
		return errors.New("quazal: invalid account data")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := make(map[string]Account, len(s.accounts))
	found := false
	for name, account := range s.accounts {
		if account.PID == pid {
			account.PublicData = cloneData(&public)
			account.PrivateData = cloneData(&private)
			found = true
		}
		updated[name] = account
	}
	if !found {
		return errors.New("quazal: account unavailable")
	}
	if err := s.persist(updated, s.nextPID); err != nil {
		return err
	}
	s.accounts = updated
	return nil
}

func (s *Store) Create(name string, initial quazalcrypto.Key) (Account, error) {
	return s.CreateWithContact(name, initial, "")
}

func (s *Store) UpdateContact(pid uint32, contact string) error {
	if len(contact) > 255 || strings.IndexByte(contact, 0) >= 0 {
		return errors.New("quazal: invalid contact")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := make(map[string]Account, len(s.accounts))
	found := false
	for name, account := range s.accounts {
		if account.PID == pid {
			account.Contact = contact
			found = true
		}
		updated[name] = account
	}
	if !found {
		return errors.New("quazal: account unavailable")
	}
	if err := s.persist(updated, s.nextPID); err != nil {
		return err
	}
	s.accounts = updated
	return nil
}
func (s *Store) CreateWithContact(name string, initial quazalcrypto.Key, contact string) (Account, error) {
	if len(contact) > 255 {
		return Account{}, errors.New("quazal: invalid contact")
	}
	canonical, err := s.canonicalName(name)
	if err != nil || canonical == guestName {
		return Account{}, ErrAccountName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.accounts[canonical]; exists {
		return Account{}, ErrAccountExists
	}
	if len(s.accounts) >= 100000 {
		return Account{}, errors.New("quazal: account capacity reached")
	}
	pid := s.nextPID
	for s.reserved[pid] {
		if pid == ^uint32(0) {
			return Account{}, errors.New("quazal: PID space exhausted")
		}
		pid++
	}
	if pid == ^uint32(0) {
		return Account{}, errors.New("quazal: PID space exhausted")
	}
	a := Account{Name: name, PID: pid, InitialKey: initial, Contact: contact}
	updated := make(map[string]Account, len(s.accounts)+1)
	for k, v := range s.accounts {
		updated[k] = v
	}
	updated[canonical] = a
	if err := s.persist(updated, pid+1); err != nil {
		return Account{}, err
	}
	s.accounts = updated
	s.nextPID = pid + 1
	return a, nil
}
