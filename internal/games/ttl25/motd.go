package ttl25

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

const defaultMOTD = "Welcome to Quetzal. Have fun!"

type messageOfDay struct {
	mu       sync.Mutex
	path     string
	messages map[string]string
	log      *slog.Logger
}

func loadMessageOfDay(path string, log *slog.Logger) (*messageOfDay, error) {
	m := &messageOfDay{path: path, messages: map[string]string{"default": defaultMOTD}, log: log}
	if path != "" {
		messages, err := readMessages(path)
		if err != nil {
			return nil, err
		}
		m.messages = messages
	}
	return m, nil
}

func localeKey(locale string) string {
	key := strings.ToLower(strings.TrimSpace(locale))
	first := strings.FieldsFunc(key, func(r rune) bool { return r == '-' || r == '_' })
	if len(first) > 0 {
		key = first[0]
	}
	switch key {
	case "en":
		return "english"
	case "fr":
		return "french"
	case "de":
		return "german"
	case "es":
		return "spanish"
	case "it":
		return "italian"
	case "*":
		return "default"
	}
	return key
}

func readMessages(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil {
		return nil, err
	}
	if len(b) > 16384 || !utf8.Valid(b) {
		return nil, errors.New("invalid MOTD file size or encoding")
	}
	var input map[string]string
	if err := json.Unmarshal(b, &input); err != nil {
		return nil, err
	}
	if len(input) == 0 || len(input) > 32 {
		return nil, errors.New("MOTD requires 1 to 32 localized messages")
	}
	result := make(map[string]string, len(input))
	for locale, message := range input {
		key := localeKey(locale)
		if key == "" || len(locale) > 64 || len(message) > 1024 || strings.ContainsRune(message, 0) {
			return nil, errors.New("invalid MOTD locale or message")
		}
		for _, r := range message {
			if r < 32 && r != '\n' && r != '\r' && r != '\t' {
				return nil, errors.New("invalid MOTD control character")
			}
		}
		if _, exists := result[key]; exists {
			return nil, errors.New("duplicate normalized MOTD locale")
		}
		result[key] = message
	}
	return result, nil
}

func (m *messageOfDay) message(locale string) string {
	if m == nil {
		return defaultMOTD
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.path != "" {
		if messages, err := readMessages(m.path); err == nil {
			m.messages = messages
		} else if m.log != nil {
			m.log.Warn("MOTD reload failed; retaining last valid messages", "err", err)
		}
	}
	for _, key := range []string{localeKey(locale), "default", "english"} {
		if message, ok := m.messages[key]; ok {
			return message
		}
	}
	return defaultMOTD
}
