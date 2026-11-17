package tdu

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/princebyyourlogic/quetzal/internal/games/tdu/ednet"
	"github.com/princebyyourlogic/quetzal/internal/state"
)

func nativeResources(c Config) (map[string][]byte, error) {
	resources := make(map[string][]byte)
	if c.ResourcePort != 0 {
		resources["netconfig.xml"] = ednet.DefaultNetworkConfig()
		resources["prize.xml"] = ednet.NoAwardsConfig()
	}
	for _, source := range []struct{ name, path string }{{"netconfig.xml", c.NetConfigPath}, {"prize.xml", c.PrizePath}} {
		if source.path == "" {
			continue
		}
		info, err := os.Lstat(source.path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source.name, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("%s override must be a private regular file (0600)", source.name)
		}
		file, err := os.Open(source.path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source.name, err)
		}
		data, err := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(data) == 0 || len(data) > 4096 {
			return nil, fmt.Errorf("%s must contain 1..4096 bytes", source.name)
		}
		decoder := xml.NewDecoder(bytes.NewReader(data))
		roots, depth := 0, 0
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("%s XML: %w", source.name, err)
			}
			switch token := token.(type) {
			case xml.StartElement:
				if depth == 0 {
					roots++
				}
				depth++
			case xml.EndElement:
				depth--
			case xml.CharData:
				if depth == 0 && strings.TrimSpace(string(token)) != "" {
					return nil, fmt.Errorf("%s has text outside its XML root", source.name)
				}
			}
		}
		if roots != 1 {
			return nil, fmt.Errorf("%s must have one XML root", source.name)
		}
		resources[source.name] = data
	}
	if c.ResourcePort != 0 {
		key, err := state.LoadKey(c.StateDir, "p2p.key")
		if err != nil {
			return nil, fmt.Errorf("p2p key: %w", err)
		}
		resources["p2pkey"] = append([]byte(nil), key[:]...)
	}
	return resources, nil
}
