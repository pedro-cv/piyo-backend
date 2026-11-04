// Package model defines the domain types shared by the store, the HTTP API
// and the exporters: line reports, NPS feedback, device blocks and the data
// manifest. It also holds the closed enums (report types, statuses, NPS
// reasons) and small helpers to validate them.
//
// Privacy note: no type here carries a raw install id, IP address or user
// agent. DeviceHash is always hex(sha256(HASH_SALT + install_id)).
package model

import (
	"encoding/json"
	"slices"
	"time"
)

// Report types a user can send from the app.
const (
	ReportTypeStop    = "stop"    // wrong stop
	ReportTypeRoute   = "route"   // wrong route / polyline
	ReportTypeGone    = "gone"    // line no longer passes here
	ReportTypeMissing = "missing" // missing line or trufi
	ReportTypeOther   = "other"
)

// ReportTypes lists every valid report type.
var ReportTypes = []string{ReportTypeStop, ReportTypeRoute, ReportTypeGone, ReportTypeMissing, ReportTypeOther}

// IsReportType reports whether s is a known report type.
func IsReportType(s string) bool { return slices.Contains(ReportTypes, s) }

// Review statuses of a report.
const (
	StatusPending  = "pending"
	StatusAccepted = "accepted"
	StatusRejected = "rejected"
)

// ReportStatuses lists every valid report status.
var ReportStatuses = []string{StatusPending, StatusAccepted, StatusRejected}

// IsReportStatus reports whether s is a known report status.
func IsReportStatus(s string) bool { return slices.Contains(ReportStatuses, s) }

// NPS reason tags the app offers after the score question.
const (
	NPSReasonFaltanLineas  = "faltan_lineas"
	NPSReasonRecorridosMal = "recorridos_mal"
	NPSReasonElMapa        = "el_mapa"
	NPSReasonEsLento       = "es_lento"
	NPSReasonMeSirvio      = "me_sirvio"
	NPSReasonOtraCosa      = "otra_cosa"
)

// NPSReasons lists every valid NPS reason tag.
var NPSReasons = []string{
	NPSReasonFaltanLineas, NPSReasonRecorridosMal, NPSReasonElMapa,
	NPSReasonEsLento, NPSReasonMeSirvio, NPSReasonOtraCosa,
}

// IsNPSReason reports whether s is a known NPS reason tag.
func IsNPSReason(s string) bool { return slices.Contains(NPSReasons, s) }

// Limits enforced on user input.
const (
	MaxReportComment = 500 // runes
	MaxNPSComment    = 140 // runes
	MaxLineID        = 32  // runes
	MaxAppVersion    = 32  // runes
	MaxDeviceClass   = 32  // runes
)

// Report is a user-submitted correction about a line, stop or route.
type Report struct {
	ID          string     `json:"id"`
	DeviceHash  string     `json:"device_hash"`
	LineID      string     `json:"line_id,omitempty"`
	Type        string     `json:"type"`
	Lat         float64    `json:"lat"`
	Lng         float64    `json:"lng"`
	Comment     string     `json:"comment,omitempty"`
	AppVersion  string     `json:"app_version"`
	DataVersion int        `json:"data_version"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ReviewedAt  *time.Time `json:"reviewed_at,omitempty"`
}

// NPS is one Net Promoter Score answer.
type NPS struct {
	ID          string    `json:"id"`
	DeviceHash  string    `json:"device_hash"`
	Score       int       `json:"score"` // 0..10
	Reasons     []string  `json:"reasons"`
	Comment     string    `json:"comment,omitempty"`
	AppVersion  string    `json:"app_version"`
	DataVersion int       `json:"data_version"`
	DeviceClass string    `json:"device_class,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Block prevents a device from sending reports until Until.
type Block struct {
	DeviceHash string    `json:"device_hash"`
	Until      time.Time `json:"until"`
	Reason     string    `json:"reason,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Expired reports whether the block is no longer in effect at now.
func (b Block) Expired(now time.Time) bool { return !now.Before(b.Until) }

// Manifest tells the app which data bundle to download and whether the
// installed app version is still supported.
type Manifest struct {
	DataVersion   int       `json:"data_version"`
	URL           string    `json:"url"`
	Bytes         int64     `json:"bytes"`
	SHA256        string    `json:"sha256"`
	MinAppVersion string    `json:"min_app_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	FareBs        string    `json:"fare_bs"`
	Message       string    `json:"message,omitempty"`
}

// --- Suggestions (iteration 2) ---------------------------------------------

// Suggestion kinds: what the user proposes to change in the line data.
const (
	SuggestionPolyline    = "polyline"     // replace / fix part of a variant geometry
	SuggestionStopAdd     = "stop_add"     // a request stop that is missing
	SuggestionStopMove    = "stop_move"    // a stop is in the wrong place
	SuggestionStopRemove  = "stop_remove"  // a stop no longer exists
	SuggestionLineMeta    = "line_meta"    // name, number, colour, fare, mode
	SuggestionMissingLine = "missing_line" // a whole line or trufi is missing
	SuggestionOther       = "other"
)

// SuggestionKinds lists every valid suggestion kind.
var SuggestionKinds = []string{
	SuggestionPolyline, SuggestionStopAdd, SuggestionStopMove, SuggestionStopRemove,
	SuggestionLineMeta, SuggestionMissingLine, SuggestionOther,
}

// IsSuggestionKind reports whether s is a known suggestion kind.
func IsSuggestionKind(s string) bool { return slices.Contains(SuggestionKinds, s) }

// Limits on suggestion input.
const (
	MaxSuggestionPayload = 16 << 10 // bytes of JSON
	MaxSuggestionPoints  = 400      // polyline points inside a payload
	MaxVariantID         = 64       // runes
	MaxReviewNote        = 500      // runes
)

// Suggestion is a proposed change to the line data. The payload is the
// structured proposal (kind-specific JSON); Lat/Lng anchor it on the map.
// It carries the dataset version the user was looking at so a reviewer can
// tell whether the suggestion is already obsolete.
type Suggestion struct {
	ID             string          `json:"id"`
	DeviceHash     string          `json:"device_hash"`
	Kind           string          `json:"kind"`
	LineID         string          `json:"line_id,omitempty"`
	VariantID      string          `json:"variant_id,omitempty"`
	DatasetVersion int             `json:"dataset_version"`
	Lat            float64         `json:"lat"`
	Lng            float64         `json:"lng"`
	Payload        json.RawMessage `json:"payload"`
	Comment        string          `json:"comment,omitempty"`
	AppVersion     string          `json:"app_version"`
	Status         string          `json:"status"`
	ReviewNote     string          `json:"review_note,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	ReviewedAt     *time.Time      `json:"reviewed_at,omitempty"`
}

// --- Contributions (iteration 4) -------------------------------------------

// Limits on contribution input (docs/implementation/CONTRIBUTIONS_DESIGN.md §3.3).
const (
	MaxContributionRoutes  = 10        // lines per contribution
	MaxContributionPoints  = 600       // polyline points per variant
	MaxContributionPayload = 448 << 10 // bytes of JSON payload (the route body cap is 512 KB)
	MaxProviderName        = 60        // runes
	MaxContributionBody    = 512 << 10 // request body cap for POST /v1/contributions
)

// Contribution is one route (a new or updated line) a user sent from a local
// data provider for review. A request with several routes is stored as one
// row per route sharing a BatchID, so each route is reviewed, published,
// withdrawn and notified on its own; the monthly quota counts batches.
// Payload holds the route in the dataset package shapes (`lines`, `variants`,
// `edits` with the original geometry of updated variants); RouteIDs mirrors
// the line id inside it so lists and the device status endpoint do not need
// to parse the payload. Rows written before the split may hold several routes
// and have no BatchID.
//
// PublishedVersion is the dataset release that includes the route (0 while it
// is not in any release); it is set by POST /v1/admin/datasets/{v}/publish.
type Contribution struct {
	ID                 string          `json:"id"`
	BatchID            string          `json:"batch_id,omitempty"`
	DeviceHash         string          `json:"device_hash"`
	ProviderName       string          `json:"provider_name"`
	BaseDatasetVersion int             `json:"base_dataset_version"`
	RouteIDs           []string        `json:"route_ids"`
	Payload            json.RawMessage `json:"payload,omitempty"`
	Comment            string          `json:"comment,omitempty"`
	AppVersion         string          `json:"app_version"`
	Status             string          `json:"status"`
	ReviewNote         string          `json:"review_note,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	ReviewedAt         *time.Time      `json:"reviewed_at,omitempty"`
	PublishedVersion   int             `json:"published_version,omitempty"`
	PublishedAt        *time.Time      `json:"published_at,omitempty"`
}

// QuotaKey is what the monthly quota counts: the batch, or the row itself for
// rows written before batches existed.
func (c Contribution) QuotaKey() string {
	if c.BatchID != "" {
		return c.BatchID
	}
	return c.ID
}

// Summary returns a copy without the payload, for list endpoints.
func (c Contribution) Summary() Contribution {
	c.Payload = nil
	return c
}
