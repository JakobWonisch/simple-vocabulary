package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// wikdictRow is one Spanish→German line from WikDict (sqlite export or TSV/JSONL).
type wikdictRow struct {
	WrittenRep string
	TransList  string
}

func loadWikdict(path string) ([]wikdictRow, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sqlite3", ".db", ".sqlite":
		return loadWikdictSQLite(path)
	case ".jsonl":
		return loadWikdictJSONL(path)
	default:
		return loadWikdictTSV(path)
	}
}

func loadWikdictSQLite(path string) ([]wikdictRow, error) {
	sqlite3, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, fmt.Errorf("sqlite3 not found on PATH (needed to read %s); export a TSV instead", path)
	}
	cmd := exec.Command(sqlite3, path, "-separator", "\t",
		`SELECT written_rep, trans_list FROM simple_translation WHERE written_rep IS NOT NULL AND written_rep != '' AND trans_list IS NOT NULL AND trans_list != '';`)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("sqlite3 %s: %w: %s", path, err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("sqlite3 %s: %w", path, err)
	}
	return parseWikdictTSVBytes(out)
}

func loadWikdictTSV(path string) ([]wikdictRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseWikdictTSVBytes(data)
}

func parseWikdictTSVBytes(data []byte) ([]wikdictRow, error) {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var rows []wikdictRow
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Allow header.
		if lineNo == 1 && strings.HasPrefix(strings.ToLower(line), "written_rep") {
			continue
		}
		rep, trans, ok := splitTSV(line)
		if !ok {
			continue
		}
		rows = append(rows, wikdictRow{WrittenRep: rep, TransList: trans})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func splitTSV(line string) (rep, trans string, ok bool) {
	i := strings.IndexByte(line, '\t')
	if i < 0 {
		return "", "", false
	}
	rep = strings.TrimSpace(line[:i])
	trans = strings.TrimSpace(line[i+1:])
	if rep == "" || trans == "" {
		return "", "", false
	}
	return rep, trans, true
}

func loadWikdictJSONL(path string) ([]wikdictRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var rows []wikdictRow
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw struct {
			WrittenRep string `json:"written_rep"`
			TransList  string `json:"trans_list"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		raw.WrittenRep = strings.TrimSpace(raw.WrittenRep)
		raw.TransList = strings.TrimSpace(raw.TransList)
		if raw.WrittenRep == "" || raw.TransList == "" {
			continue
		}
		rows = append(rows, wikdictRow{WrittenRep: raw.WrittenRep, TransList: raw.TransList})
	}
	return rows, sc.Err()
}

func splitWikdictTranslations(list string) []string {
	parts := strings.Split(list, "|")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	return out
}

func keepWikdictLemma(rep string) bool {
	rep = strings.TrimSpace(rep)
	if rep == "" {
		return false
	}
	// Affixes and clitics in WikDict (e.g. "-aje", "-fono").
	if strings.HasPrefix(rep, "-") || strings.HasSuffix(rep, "-") {
		return false
	}
	return true
}
