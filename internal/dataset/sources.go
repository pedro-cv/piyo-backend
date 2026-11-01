package dataset

import "strings"

// Upstream source families. Provider brand names live only in this file (and in
// docs/DATA_SOURCES.md); the rest of the importer speaks of layouts:
//
//   - "osm":          one polyline per variant, stops and street names (data/lines.json)
//   - "named-routes": one named route per line with ida/vuelta halves and colours,
//     no stops (data-<provider>/lines.json)
const (
	// modeNamedRoutes is the `mode` token the named-routes upstream writes on lines and variants.
	modeNamedRoutes = "microcruz"
	// namedRoutesMarker identifies the named-routes provider in a source file's source/license text.
	namedRoutesMarker = "microcruz"
	// AttributionNamedRoutes is the short credit the app shows for named-routes releases.
	AttributionNamedRoutes = "Recorridos © Microcruz"
)

// attributionFor picks the credit line from the source file's own source/license text.
func attributionFor(source, license string) string {
	if strings.Contains(strings.ToLower(source+" "+license), namedRoutesMarker) {
		return AttributionNamedRoutes
	}
	return AttributionOSM
}
