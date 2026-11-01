// Google polyline encoding (precision 1e-5), the same algorithm the Android
// app decodes. ~5.5 bytes per point instead of ~22 for [lat,lng] JSON.
package dataset

import (
	"math"
	"strings"
)

// Point is a WGS84 coordinate.
type Point struct{ Lat, Lng float64 }

// EncodePolyline encodes points.
func EncodePolyline(points []Point) string {
	var sb strings.Builder
	var prevLat, prevLng int64
	for _, p := range points {
		lat := int64(math.Round(p.Lat * 1e5))
		lng := int64(math.Round(p.Lng * 1e5))
		encodeValue(&sb, lat-prevLat)
		encodeValue(&sb, lng-prevLng)
		prevLat, prevLng = lat, lng
	}
	return sb.String()
}

func encodeValue(sb *strings.Builder, v int64) {
	u := v << 1
	if v < 0 {
		u = ^u
	}
	for u >= 0x20 {
		sb.WriteByte(byte((0x20 | (u & 0x1f)) + 63))
		u >>= 5
	}
	sb.WriteByte(byte(u + 63))
}

// DecodePolyline decodes an encoded string.
func DecodePolyline(s string) []Point {
	var out []Point
	var lat, lng int64
	i := 0
	for i < len(s) {
		var result, shift int64
		for {
			b := int64(s[i]) - 63
			i++
			result |= (b & 0x1f) << shift
			shift += 5
			if b < 0x20 || i >= len(s) {
				break
			}
		}
		if result&1 != 0 {
			lat += ^(result >> 1)
		} else {
			lat += result >> 1
		}
		if i >= len(s) {
			break
		}
		result, shift = 0, 0
		for {
			b := int64(s[i]) - 63
			i++
			result |= (b & 0x1f) << shift
			shift += 5
			if b < 0x20 || i >= len(s) {
				break
			}
		}
		if result&1 != 0 {
			lng += ^(result >> 1)
		} else {
			lng += result >> 1
		}
		out = append(out, Point{float64(lat) / 1e5, float64(lng) / 1e5})
	}
	return out
}

// DistanceM is the haversine distance in metres.
func DistanceM(a, b Point) float64 {
	const r = 6371000.0
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLng := (b.Lng - a.Lng) * math.Pi / 180
	la1 := a.Lat * math.Pi / 180
	la2 := b.Lat * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
}

// LengthM sums the segment lengths of a path.
func LengthM(pts []Point) float64 {
	total := 0.0
	for i := 1; i < len(pts); i++ {
		total += DistanceM(pts[i-1], pts[i])
	}
	return total
}
