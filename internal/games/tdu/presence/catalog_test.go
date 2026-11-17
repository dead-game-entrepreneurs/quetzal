package presence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestActivityCatalog(t *testing.T) {
	if m, e := LoadActivityCatalog(""); e != nil || m != nil {
		t.Fatalf("disabled %v %v", m, e)
	}
	file := filepath.Join(t.TempDir(), "catalog.json")
	valid := ActivityCatalog{Schema: 1, Build: "SLUS_214.90", ActivityCount: StockActivityCount, Activities: []ActivityEntry{{"AIRBALL_PS2", 210}, {"ZIGZAG_PS2", 447}}}
	put := func(v ActivityCatalog) {
		t.Helper()
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(file, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	put(valid)
	m, e := LoadActivityCatalog(file)
	if e != nil || len(m) != 2 || m["AIRBALL_PS2"] != 210 || m["ZIGZAG_PS2"] != 447 {
		t.Fatalf("catalog %v %v", m, e)
	}
	for _, entries := range [][]ActivityEntry{
		{{"AIRBALL_PS2", 209}}, {{"AIRBALL_PS2", 448}}, {{"DriveIn_PS2", 447}}, {{"AIRBALL_PS2", 210}, {"AIRBALL_PS2", 211}}, {{"AIRBALL_PS2", 210}, {"ZIGZAG_PS2", 210}}, {{"FreeRideV5_PS2", 210}}, {{"AIRBALL", 210}}, {{"bad\x00_PS2", 210}},
	} {
		v := valid
		v.Activities = entries
		put(v)
		if _, e = LoadActivityCatalog(file); e == nil {
			t.Fatalf("accepted invalid catalog %+v", entries)
		}
	}
	v := valid
	v.Build = "another build"
	put(v)
	if _, e = LoadActivityCatalog(file); e == nil {
		t.Fatal("wrong build")
	}
	put(valid)
	f, e := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteString(" {}")
	f.Close()
	if _, e = LoadActivityCatalog(file); e == nil {
		t.Fatal("trailing json")
	}
	if e = os.WriteFile(file, make([]byte, MaxActivityCatalogBytes+1), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadActivityCatalog(file); e == nil {
		t.Fatal("oversize")
	}
}

func TestActivityCatalogFilePolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	data := []byte(`{"schema":1,"build":"SLUS_214.90","activity_count":448,"activities":[{"name":"AIRBALL_PS2","index":210}]}`)
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(dir, "link")
	if e := os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadActivityCatalog(link); e == nil {
		t.Fatal("symlink accepted")
	}
	if e := os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadActivityCatalog(path); e == nil {
		t.Fatal("public mode accepted")
	}
	if _, e := LoadActivityCatalog(dir); e == nil {
		t.Fatal("directory accepted")
	}
}
func TestGeneratedPrivateCatalog(t *testing.T) {
	path := os.Getenv("TDU_ACTIVITY_CATALOG_FIXTURE")
	if path == "" {
		t.Skip("optional private retail-derived protocol metadata")
	}
	m, e := LoadActivityCatalog(path)
	if e != nil {
		t.Fatal(e)
	}
	if len(m) != MissionActivityCount || m["AIRBALL_PS2"] != 210 || m["ZIGZAG_PS2"] != 447 {
		t.Fatalf("unexpected mission catalog count=%d", len(m))
	}
}
