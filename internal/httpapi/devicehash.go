// Device identity for the anonymous API. The app sends its random install id
// in the X-Install-Id header; the server never stores it. Every lookup and
// record uses HashInstallID(salt, id) = hex(sha256(salt + id)) instead, so a
// database leak cannot be joined back to installs without HASH_SALT.
package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

// HeaderInstallID is the header carrying the anonymous install id.
const HeaderInstallID = "X-Install-Id"

// installIDRe accepts UUID-ish ids: 8..64 chars of [A-Za-z0-9-].
var installIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// deviceHashRe accepts a hex SHA-256 as produced by HashInstallID.
var deviceHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

var errInvalidInstallID = errors.New("missing or invalid " + HeaderInstallID + " header (8-64 chars, [A-Za-z0-9-])")

// HashInstallID returns hex(sha256(salt + installID)).
func HashInstallID(salt, installID string) string {
	sum := sha256.Sum256([]byte(salt + installID))
	return hex.EncodeToString(sum[:])
}

// IsDeviceHash reports whether s looks like a HashInstallID output.
func IsDeviceHash(s string) bool { return deviceHashRe.MatchString(s) }

// deviceHashFromRequest validates X-Install-Id and returns its hash.
func (s *Server) deviceHashFromRequest(r *http.Request) (string, error) {
	id := strings.TrimSpace(r.Header.Get(HeaderInstallID))
	if !installIDRe.MatchString(id) {
		return "", errInvalidInstallID
	}
	return HashInstallID(s.cfg.HashSalt, id), nil
}

// requireDevice is the common prologue of device endpoints: on failure it
// has already written the 400 and returns ok=false.
func (s *Server) requireDevice(w http.ResponseWriter, r *http.Request) (hash string, ok bool) {
	hash, err := s.deviceHashFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_install_id", err.Error(), nil)
		return "", false
	}
	return hash, true
}
