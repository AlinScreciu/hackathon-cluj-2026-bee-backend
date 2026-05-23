package services

import "math"

// Haversine returns the distance in meters between two lat/lng points.
// Uses Earth radius 6,371,000 m.
func Haversine(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadius = 6_371_000.0

	dLat := toRad(lat2 - lat1)
	dLng := toRad(lng2 - lng1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*
			math.Sin(dLng/2)*math.Sin(dLng/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadius * c
}

func toRad(deg float64) float64 {
	return deg * math.Pi / 180
}
