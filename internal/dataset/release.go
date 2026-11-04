// Writing a release to disk and serving releases from a directory.
//
// Layout of RELEASES_DIR:
//
//	releases/
//	  latest.json              {"dataset_version": 13}
//	  v13/
//	    manifest.json
//	    dataset-v13.json.gz    the whole package, what the app downloads
//	    lines.json  stops.json  variants.json  transfers.json   (partial updates)
package dataset

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
)

// Write writes the package as a release under dir and returns the manifest.
// It overwrites an existing version (the importer's re-run with -db relies on
// that); new releases built from contributions use WriteRelease.
func Write(dir string, pkg *Package, minAppVersion string) (*Manifest, error) {
	return write(dir, pkg, minAppVersion, nil)
}

func write(dir string, pkg *Package, minAppVersion string, changes *ReleaseChanges) (*Manifest, error) {
	vdir := filepath.Join(dir, "v"+strconv.Itoa(pkg.DatasetVersion))
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		return nil, err
	}
	m := &Manifest{
		DatasetVersion: pkg.DatasetVersion,
		PackageFormat:  pkg.PackageFormat,
		DataVersion:    pkg.DatasetVersion,
		GeneratedAt:    pkg.GeneratedAt,
		MinAppVersion:  minAppVersion,
		FareBs:         pkg.FareBs,
		License:        pkg.License,
		Attribution:    pkg.Attribution,
		Source:         pkg.Source,
		BBox:           pkg.BBox,
		Counts: map[string]int{
			"lines": len(pkg.Lines), "stops": len(pkg.Stops), "variants": len(pkg.Variants), "transfers": len(pkg.Transfers),
		},
		Changes: changes,
	}
	write := func(name string, v any) (FileInfo, error) {
		data, err := json.Marshal(v)
		if err != nil {
			return FileInfo{}, err
		}
		return writeFile(vdir, name, data)
	}
	parts := []struct {
		name string
		v    any
	}{
		{"lines.json", map[string]any{"dataset_version": pkg.DatasetVersion, "lines": pkg.Lines}},
		{"stops.json", map[string]any{"dataset_version": pkg.DatasetVersion, "stops": pkg.Stops}},
		{"variants.json", map[string]any{"dataset_version": pkg.DatasetVersion, "variants": pkg.Variants}},
		{"transfers.json", map[string]any{"dataset_version": pkg.DatasetVersion, "package_format": pkg.PackageFormat, "transfers": pkg.Transfers}},
	}
	for _, p := range parts {
		fi, err := write(p.name, p.v)
		if err != nil {
			return nil, err
		}
		m.Files = append(m.Files, fi)
	}
	// Full package, gzip.
	raw, err := json.Marshal(pkg)
	if err != nil {
		return nil, err
	}
	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	pkgName := fmt.Sprintf("dataset-v%d.json.gz", pkg.DatasetVersion)
	fi, err := writeFile(vdir, pkgName, gz.Bytes())
	if err != nil {
		return nil, err
	}
	m.Package = fi
	m.Bytes, m.SHA256 = fi.Bytes, fi.SHA256
	mb, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(vdir, "manifest.json"), mb, 0o644); err != nil {
		return nil, err
	}
	// latest pointer (atomic rename)
	lb, _ := json.Marshal(map[string]int{"dataset_version": pkg.DatasetVersion})
	tmp := filepath.Join(dir, "latest.json.tmp")
	if err := os.WriteFile(tmp, lb, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "latest.json")); err != nil {
		_ = os.Remove(filepath.Join(dir, "latest.json"))
		if err := os.Rename(tmp, filepath.Join(dir, "latest.json")); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func writeFile(dir, name string, data []byte) (FileInfo, error) {
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return FileInfo{}, err
	}
	sum := sha256.Sum256(data)
	return FileInfo{Name: name, Bytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, nil
}

// LatestVersion reads latest.json; 0 when the directory has no release.
func LatestVersion(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil {
		return scanLatest(dir)
	}
	var v struct {
		DatasetVersion int `json:"dataset_version"`
	}
	if json.Unmarshal(b, &v) != nil {
		return scanLatest(dir)
	}
	return v.DatasetVersion
}

func scanLatest(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	best := 0
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > 1 && e.Name()[0] == 'v' {
			if n, err := strconv.Atoi(e.Name()[1:]); err == nil && n > best {
				best = n
			}
		}
	}
	return best
}

// Catalog serves releases from a directory with a small manifest cache.
type Catalog struct {
	Dir string
	mu  sync.RWMutex
	man map[int]*Manifest
}

// NewCatalog returns a catalog for dir (which may not exist yet).
func NewCatalog(dir string) *Catalog { return &Catalog{Dir: dir, man: map[int]*Manifest{}} }

// ErrNoRelease is returned when the directory holds no release.
var ErrNoRelease = errors.New("dataset: no release available")

// Latest returns the newest manifest.
func (c *Catalog) Latest() (*Manifest, error) {
	v := LatestVersion(c.Dir)
	if v == 0 {
		return nil, ErrNoRelease
	}
	return c.Manifest(v)
}

// Manifest returns the manifest of version v.
func (c *Catalog) Manifest(v int) (*Manifest, error) {
	c.mu.RLock()
	m, ok := c.man[v]
	c.mu.RUnlock()
	if ok {
		return m, nil
	}
	b, err := os.ReadFile(filepath.Join(c.Dir, "v"+strconv.Itoa(v), "manifest.json"))
	if err != nil {
		return nil, ErrNoRelease
	}
	var mm Manifest
	if err := json.Unmarshal(b, &mm); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.man[v] = &mm
	c.mu.Unlock()
	return &mm, nil
}

// FilePath returns the on-disk path of a published file, or "" when it is
// not part of the release (prevents path traversal: only listed names).
func (c *Catalog) FilePath(v int, name string) string {
	m, err := c.Manifest(v)
	if err != nil {
		return ""
	}
	if name == "manifest.json" || name == m.Package.Name {
		return filepath.Join(c.Dir, "v"+strconv.Itoa(v), name)
	}
	for _, f := range m.Files {
		if f.Name == name {
			return filepath.Join(c.Dir, "v"+strconv.Itoa(v), name)
		}
	}
	return ""
}

// Versions lists the available versions, newest first.
func (c *Catalog) Versions() []int {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > 1 && e.Name()[0] == 'v' {
			if n, err := strconv.Atoi(e.Name()[1:]); err == nil {
				out = append(out, n)
			}
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}
