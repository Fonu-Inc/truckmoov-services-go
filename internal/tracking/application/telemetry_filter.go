package application

import (
	"math"
	"time"
)

type TelemetryConfig struct {
	MaxAccuracyM float64
	MaxSpeedKPH  float64
}

type filterResult struct {
	skip   bool
	reason string
}

func applyTelemetryFilter(cfg TelemetryConfig, prev *LatestLocation, cmd IngestLocationCommand, fixTime time.Time, fixOK bool) filterResult {
	// Accuracy is time-independent, so it must run even when fix_time is unparseable.
	if cfg.MaxAccuracyM > 0 && cmd.AccuracyM != nil && *cmd.AccuracyM > cfg.MaxAccuracyM {
		return filterResult{skip: true, reason: "accuracy_exceeded"}
	}

	// The remaining checks compare against the previous fix and need a valid timestamp.
	if prev == nil || !fixOK {
		return filterResult{}
	}

	prevFix, err := time.Parse(time.RFC3339, prev.FixTimeRFC)
	if err != nil {
		return filterResult{}
	}

	if !fixTime.After(prevFix) {
		return filterResult{skip: true, reason: "out_of_order"}
	}

	if cfg.MaxSpeedKPH > 0 {
		distKM := haversineKM(prev.Latitude, prev.Longitude, cmd.Latitude, cmd.Longitude)
		elapsedH := fixTime.Sub(prevFix).Hours()
		if elapsedH > 0 && distKM/elapsedH > cfg.MaxSpeedKPH {
			return filterResult{skip: true, reason: "impossible_jump"}
		}
	}

	return filterResult{}
}

func haversineKM(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	return r * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
